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

"""verl rollout client that asks the Kthena router which replica to generate on.

The router only selects the replica; the request itself travels verl's own
dispatch path, so everything that depends on the rollout server actor, such as
the weight version reported with each response, keeps working unchanged.
"""

from __future__ import annotations

import asyncio
import contextvars
import logging
from typing import Any

import aiohttp
import ray

from verl.workers.rollout.llm_server import LLMServerClient

logger = logging.getLogger(__name__)

SCHEDULE_PATH = "/v1/schedule"
RELEASE_PATH = "/v1/schedule/release"

# Selecting a replica is a fast, in-memory operation in the router, so a short
# timeout surfaces a wedged router instead of stalling the whole rollout.
_REQUEST_TIMEOUT = aiohttp.ClientTimeout(total=30)

# Prompt of the request currently being scheduled. verl's
# ``LLMServerClient.generate`` calls ``_acquire_server(request_id)`` without the
# prompt, so it is handed over through the task-local context rather than by
# reimplementing generate().
_current_prompt_ids: contextvars.ContextVar = contextvars.ContextVar("kthena_current_prompt_ids", default=None)


class KthenaLLMClient(LLMServerClient):
    """Replaces verl's replica selection with the Kthena router's scheduler.

    The router scores every replica on prefix cache overlap, in-flight requests
    and engine metrics, which steers the requests of a rollout group to the
    replica that already holds their shared prompt prefix.

    Args:
        config: verl DictConfig.
        load_balancer_handle: verl's load balancer, kept so the base class stays
            usable; replica selection happens in the router.
        router_address: router endpoint as ``host:port``.
        model_name: model name the router routes on.
        server_handles: rollout server actor handle per replica ``host:port``.
    """

    def __init__(
        self,
        config,
        load_balancer_handle=None,
        *,
        router_address: str,
        model_name: str,
        server_handles: dict[str, Any],
        **kwargs,
    ):
        super().__init__(config=config, load_balancer_handle=load_balancer_handle, **kwargs)
        self._schedule_url = f"http://{router_address}{SCHEDULE_PATH}"
        self._release_url = f"http://{router_address}{RELEASE_PATH}"
        self._model_name = model_name
        self._server_handles = dict(server_handles)
        self._instances: dict[str, dict[str, str]] = {}
        self._session: aiohttp.ClientSession | None = None
        self._release_tasks: set[asyncio.Task] = set()

    def __getstate__(self) -> dict:
        # The client is shipped to the AgentLoopWorker actors, where each one
        # opens its own connection pool and tracks its own requests.
        state = self.__dict__.copy()
        state["_session"] = None
        state["_release_tasks"] = set()
        return state

    async def generate(self, request_id, *, prompt_ids: list[int], **kwargs: Any):
        token = _current_prompt_ids.set(prompt_ids)
        try:
            return await super().generate(request_id, prompt_ids=prompt_ids, **kwargs)
        finally:
            _current_prompt_ids.reset(token)

    async def _acquire_server(self, request_id: str) -> tuple[str, ray.actor.ActorHandle]:
        """Ask the router which replica should serve the current prompt."""
        prompt_ids = _current_prompt_ids.get() or []
        session = await self._get_session()
        async with session.post(self._schedule_url, json={"model": self._model_name, "prompt": prompt_ids}) as response:
            if response.status != 200:
                detail = await response.text()
                raise RuntimeError(f"kthena router returned HTTP {response.status} for {self._schedule_url}: {detail}")
            payload = await response.json()

        instances = payload.get("instances") or []
        if not instances:
            raise RuntimeError("kthena router selected no serving instance")
        # Prefill/decode disaggregation is handled by the rollout engine itself,
        # so requests always go to the decode instance.
        instance = next((entry for entry in instances if entry.get("role") != "prefill"), instances[0])

        address = f"{instance['address']}:{instance['port']}"
        handle = self._server_handles.get(address)
        if handle is None:
            raise RuntimeError(
                f"kthena router selected {address}, which is not a known rollout replica "
                f"(known: {sorted(self._server_handles)})"
            )
        self._instances[address] = {"namespace": instance.get("namespace", ""), "name": instance["name"]}
        return address, handle

    def _release_server(self, server_id: str) -> None:
        """Tell the router that the replica finished serving a request."""
        instance = self._instances.get(server_id)
        session = self._session
        if instance is None or session is None or session.closed:
            return
        # Fire and forget: the in-flight count is a scheduling hint, so waiting
        # for the router to acknowledge it would only delay the caller.
        task = asyncio.create_task(self._release(session, instance))
        self._release_tasks.add(task)
        task.add_done_callback(self._release_tasks.discard)

    async def _release(self, session: aiohttp.ClientSession, instance: dict[str, str]) -> None:
        try:
            async with session.post(self._release_url, json={"instances": [instance]}):
                pass
        except (aiohttp.ClientError, asyncio.TimeoutError) as exc:
            # A lost release only makes the router believe a replica is busier
            # than it is, which the next metrics scrape corrects.
            logger.warning("[KthenaRouter] releasing %s failed: %s", instance, exc)

    async def _get_session(self) -> aiohttp.ClientSession:
        if self._session is None or self._session.closed:
            self._session = aiohttp.ClientSession(timeout=_REQUEST_TIMEOUT)
        return self._session
