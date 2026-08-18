# Copyright The Volcano Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""verl AgentLoopManager that routes rollouts through the Kthena router.

Enable it with a single Hydra override, no verl source change needed::

    +actor_rollout_ref.rollout.agent.agent_loop_manager_class=\
kthena.verl_integration.agent_loop_manager.KthenaRouterAgentLoopManager

The router is configured through ``actor_rollout_ref.rollout.custom``:

================================= ======================= ===========================================
Key                               Default                 Meaning
================================= ======================= ===========================================
``kthena_router_address``         (unset)                 Reuse an already running router
``kthena_router_binary``          ``kthena-router``       Router binary to launch
``kthena_router_config``          (generated)             Router scheduler configuration file
``kthena_router_work_dir``        ``/tmp/kthena-router``  Where manifests, config and logs are written
``kthena_router_model_name``      ``verl-rollout``        Model name the rollout requests carry
``kthena_router_port``            ``8080``                Router listen port
``kthena_router_metrics_port``    ``9090``                Router Prometheus port
``kthena_router_debug_port``      ``15000``               Router debug port
================================= ======================= ===========================================
"""

from __future__ import annotations

import logging
import socket

import ray
from omegaconf import DictConfig, OmegaConf
from ray.util.scheduling_strategies import NodeAffinitySchedulingStrategy

from kthena.verl_integration.actor import KthenaRouterActor
from kthena.verl_integration.llm_client import KthenaLLMClient
from kthena.verl_integration.router import DEFAULT_MODEL_NAME
from verl.trainer.ppo.v1 import AgentLoopManagerTQ

logger = logging.getLogger(__name__)

# Name verl's vLLM backend registers every rollout server actor under.
_SERVER_ACTOR_NAME = "vllm_server_{replica_rank}_0"


class KthenaRouterAgentLoopManager(AgentLoopManagerTQ):
    """Starts the router once the rollout replicas are up, then rewires the client.

    Everything happens in ``__init__``, which verl runs before the agent loop
    workers are created, so every worker receives the routing client.
    """

    def __init__(self, config, llm_client, *args, **kwargs):
        super().__init__(config, llm_client, *args, **kwargs)

        rollout_name = getattr(self.rollout_config, "name", None)
        if rollout_name != "vllm":
            raise NotImplementedError(
                f"the Kthena router integration supports actor_rollout_ref.rollout.name=vllm, got {rollout_name!r}"
            )

        custom = _custom_config(self.rollout_config)
        replicas = ray.get(llm_client._load_balancer.get_all_servers.remote())
        server_handles = _server_handles(len(replicas))
        logger.info("[KthenaRouter] rollout replicas: %s", sorted(server_handles))

        router_address = custom.get("kthena_router_address")
        if router_address:
            logger.info("[KthenaRouter] using the already running router at %s", router_address)
        else:
            self._router_actor = KthenaRouterActor.options(
                scheduling_strategy=_head_node_strategy(),
            ).remote()
            router_address = ray.get(self._router_actor.start.remote(sorted(server_handles), custom))
            logger.info("[KthenaRouter] router ready at %s", router_address)

        self.llm_client = KthenaLLMClient(
            config=self.config,
            load_balancer_handle=llm_client._load_balancer,
            router_address=router_address,
            model_name=custom.get("kthena_router_model_name", DEFAULT_MODEL_NAME),
            server_handles=server_handles,
        )


def _server_handles(num_replicas: int) -> dict:
    """Map every rollout replica's ``host:port`` to its server actor handle.

    The router answers with the address it selected, which is translated back
    into the actor handle verl dispatches the generation request to.
    """
    handles = {}
    for replica_rank in range(num_replicas):
        handle = ray.get_actor(_SERVER_ACTOR_NAME.format(replica_rank=replica_rank))
        address, port = ray.get(handle.get_server_address.remote())
        handles[f"{address}:{port}"] = handle
    return handles


def _custom_config(rollout_config) -> dict:
    custom = getattr(rollout_config, "custom", None)
    if custom is None:
        return {}
    if isinstance(custom, DictConfig):
        return OmegaConf.to_container(custom, resolve=True)
    return dict(custom)


def _head_node_strategy() -> NodeAffinitySchedulingStrategy:
    """Pin the router actor to the Ray head node.

    Prefix cache affinity only pays off when every rollout request is scored
    against the same cache, so there must be exactly one router.
    """
    head_host = ray.get_runtime_context().gcs_address.split(":")[0]
    try:
        head_ip = socket.gethostbyname(head_host)
    except socket.gaierror:
        head_ip = head_host
    for node in ray.nodes():
        if node["Alive"] and node["NodeManagerAddress"] == head_ip:
            return NodeAffinitySchedulingStrategy(node_id=node["NodeID"], soft=False)
    raise RuntimeError(
        f"could not find a live Ray node with GCS address {head_ip}; "
        f"nodes: {[node['NodeManagerAddress'] for node in ray.nodes()]}"
    )
