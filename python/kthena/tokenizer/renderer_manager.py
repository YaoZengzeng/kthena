# Copyright The Volcano Authors.
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

"""Manage one `vllm render` subprocess per model.

`vllm render` is vLLM's lightweight, GPU-free frontend that serves the
OpenAI-compatible /tokenize and /detokenize endpoints without loading model
weights or an engine. The manager reconciles the set of running renderers
against the set of models declared by ModelServer objects.
"""

import asyncio
import logging
import time
from dataclasses import dataclass, field
from enum import Enum
from typing import Dict, List, Optional, Set

import httpx

from kthena.tokenizer.config import TokenizerServiceConfig

logger = logging.getLogger(__name__)

HEALTH_POLL_INTERVAL = 2.0
MONITOR_INTERVAL = 5.0


class RendererStatus(str, Enum):
    LOADING = "loading"
    READY = "ready"
    FAILED = "failed"


@dataclass
class Renderer:
    model: str
    port: int
    status: RendererStatus = RendererStatus.LOADING
    process: Optional[asyncio.subprocess.Process] = None
    restarts: int = 0
    last_error: str = ""
    started_at: float = field(default_factory=time.time)

    @property
    def endpoint(self) -> str:
        return f"http://127.0.0.1:{self.port}"


class RendererManager:
    """Reconciles desired models against running renderer subprocesses."""

    def __init__(self, config: TokenizerServiceConfig):
        self._config = config
        self._renderers: Dict[str, Renderer] = {}
        self._desired: Set[str] = set()
        self._lock = asyncio.Lock()
        self._monitor_task: Optional[asyncio.Task] = None
        self._client = httpx.AsyncClient(timeout=httpx.Timeout(5.0))
        self._closed = False

    async def start(self) -> None:
        self._monitor_task = asyncio.create_task(self._monitor_loop())

    async def shutdown(self) -> None:
        self._closed = True
        if self._monitor_task:
            self._monitor_task.cancel()
        async with self._lock:
            for renderer in self._renderers.values():
                await self._stop_process(renderer)
            self._renderers.clear()
        await self._client.aclose()

    async def set_models(self, models: Set[str]) -> None:
        """Reconcile the desired model set (called by the ModelServer watcher)."""
        async with self._lock:
            self._desired = set(models)
            await self._reconcile_locked()

    def endpoint_for(self, model: str) -> Optional[str]:
        """Return the renderer endpoint for a model if it is ready."""
        renderer = self._renderers.get(model)
        if renderer is not None and renderer.status == RendererStatus.READY:
            return renderer.endpoint
        return None

    def snapshot(self) -> List[dict]:
        return [
            {
                "model": r.model,
                "status": r.status.value,
                "port": r.port,
                "restarts": r.restarts,
                "lastError": r.last_error,
            }
            for r in self._renderers.values()
        ]

    def is_ready(self) -> bool:
        return not self._closed

    # -- internal ---------------------------------------------------------

    async def _reconcile_locked(self) -> None:
        # Stop renderers whose model is no longer desired.
        for model in list(self._renderers):
            if model not in self._desired:
                logger.info("Unloading tokenizer for model %s", model)
                await self._stop_process(self._renderers.pop(model))

        # Start renderers for newly desired models.
        for model in sorted(self._desired):
            if model in self._renderers:
                continue
            if len(self._renderers) >= self._config.max_tokenizers:
                logger.warning(
                    "Max tokenizers (%d) reached, not loading model %s",
                    self._config.max_tokenizers,
                    model,
                )
                continue
            renderer = Renderer(model=model, port=self._allocate_port())
            self._renderers[model] = renderer
            asyncio.create_task(self._launch(renderer))

    def _allocate_port(self) -> int:
        used = {r.port for r in self._renderers.values()}
        port = self._config.renderer_base_port
        while port in used:
            port += 1
        return port

    async def _launch(self, renderer: Renderer) -> None:
        cmd = (
            self._config.renderer_command
            + [renderer.model]
            + ["--host", "127.0.0.1", "--port", str(renderer.port)]
            + self._config.renderer_extra_args
        )
        logger.info("Starting renderer for model %s: %s", renderer.model, " ".join(cmd))
        renderer.status = RendererStatus.LOADING
        renderer.started_at = time.time()
        try:
            renderer.process = await asyncio.create_subprocess_exec(*cmd)
        except (OSError, ValueError) as e:
            renderer.status = RendererStatus.FAILED
            renderer.last_error = f"failed to spawn renderer: {e}"
            logger.error(
                "Failed to start renderer for model %s: %s", renderer.model, e
            )
            return

        await self._wait_ready(renderer)

    async def _wait_ready(self, renderer: Renderer) -> None:
        deadline = time.time() + self._config.renderer_startup_timeout
        url = f"{renderer.endpoint}/health"
        while time.time() < deadline and not self._closed:
            if renderer.process is not None and renderer.process.returncode is not None:
                renderer.status = RendererStatus.FAILED
                renderer.last_error = (
                    f"renderer exited during startup "
                    f"(code {renderer.process.returncode})"
                )
                logger.error(
                    "Renderer for model %s exited during startup with code %s",
                    renderer.model,
                    renderer.process.returncode,
                )
                return
            try:
                resp = await self._client.get(url)
                if resp.status_code == 200:
                    renderer.status = RendererStatus.READY
                    renderer.last_error = ""
                    logger.info(
                        "Tokenizer for model %s ready at %s",
                        renderer.model,
                        renderer.endpoint,
                    )
                    return
            except httpx.HTTPError:
                pass
            await asyncio.sleep(HEALTH_POLL_INTERVAL)

        renderer.status = RendererStatus.FAILED
        renderer.last_error = "renderer did not become healthy before timeout"
        logger.error(
            "Renderer for model %s did not become healthy within %ds",
            renderer.model,
            self._config.renderer_startup_timeout,
        )
        await self._stop_process(renderer)

    async def _monitor_loop(self) -> None:
        """Restart renderers that exited unexpectedly, with a restart budget."""
        while not self._closed:
            await asyncio.sleep(MONITOR_INTERVAL)
            async with self._lock:
                for renderer in self._renderers.values():
                    process = renderer.process
                    if (
                        renderer.status != RendererStatus.READY
                        or process is None
                        or process.returncode is None
                    ):
                        continue
                    logger.warning(
                        "Renderer for model %s exited with code %s",
                        renderer.model,
                        process.returncode,
                    )
                    if renderer.restarts >= self._config.renderer_max_restarts:
                        renderer.status = RendererStatus.FAILED
                        renderer.last_error = "restart budget exhausted"
                        continue
                    renderer.restarts += 1
                    asyncio.create_task(self._launch(renderer))

    @staticmethod
    async def _stop_process(renderer: Renderer) -> None:
        process = renderer.process
        renderer.process = None
        if process is None or process.returncode is not None:
            return
        process.terminate()
        try:
            await asyncio.wait_for(process.wait(), timeout=10)
        except asyncio.TimeoutError:
            process.kill()
            await process.wait()
