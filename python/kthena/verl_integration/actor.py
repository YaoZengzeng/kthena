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

"""Ray actor that owns the Kthena router process."""

from __future__ import annotations

import logging

import ray

from kthena.verl_integration.router import (
    DEFAULT_BINARY,
    DEFAULT_DEBUG_PORT,
    DEFAULT_METRICS_PORT,
    DEFAULT_MODEL_NAME,
    DEFAULT_PORT,
    DEFAULT_WORK_DIR,
    KthenaRouter,
    discover_served_model,
)

logger = logging.getLogger(__name__)


@ray.remote
class KthenaRouterActor:
    """Runs the router on one node so every rollout worker shares its state.

    Prefix cache affinity and in-flight accounting are only useful when all
    rollout requests are scored against the same state, so the router must be a
    single process; the actor is pinned to the Ray head node by the caller.
    """

    def __init__(self) -> None:
        self._router: KthenaRouter | None = None

    def start(self, endpoints: list[str], custom: dict) -> str:
        """Start the router for ``endpoints`` and return its ``host:port``."""
        upstream_model = custom.get("kthena_router_upstream_model")
        if not upstream_model:
            upstream_model = discover_served_model(endpoints)
        logger.info("[KthenaRouterActor] rollout replicas serve model %s", upstream_model)

        self._router = KthenaRouter(
            binary=custom.get("kthena_router_binary", DEFAULT_BINARY),
            work_dir=custom.get("kthena_router_work_dir", DEFAULT_WORK_DIR),
            config_file=custom.get("kthena_router_config"),
            model_name=custom.get("kthena_router_model_name", DEFAULT_MODEL_NAME),
            port=int(custom.get("kthena_router_port", DEFAULT_PORT)),
            metrics_port=int(custom.get("kthena_router_metrics_port", DEFAULT_METRICS_PORT)),
            debug_port=int(custom.get("kthena_router_debug_port", DEFAULT_DEBUG_PORT)),
        )
        port = self._router.start(endpoints, upstream_model)
        return f"{ray.util.get_node_ip_address()}:{port}"

    def stop(self) -> None:
        if self._router is not None:
            self._router.stop()
            self._router = None
