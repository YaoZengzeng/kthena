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

"""Tokenizer service frontend.

Exposes a vLLM-compatible /tokenize (and /detokenize) API and proxies each
request to the `vllm render` subprocess that serves the requested model.
Requests for models without a ready tokenizer are rejected with 503 so that
the router can fall back to engine-side tokenization.
"""

import asyncio
import json
import logging
from contextlib import asynccontextmanager
from typing import Optional

import httpx
import uvicorn
from fastapi import FastAPI, Request
from starlette.responses import JSONResponse, Response

from kthena.tokenizer.config import TokenizerServiceConfig
from kthena.tokenizer.model_watcher import ModelServerWatcher
from kthena.tokenizer.renderer_manager import RendererManager

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
logger = logging.getLogger(__name__)


class AppState:
    def __init__(self):
        self.config = TokenizerServiceConfig()
        self.manager: Optional[RendererManager] = None
        self.watcher: Optional[ModelServerWatcher] = None
        self.client: Optional[httpx.AsyncClient] = None


state = AppState()


@asynccontextmanager
async def lifespan(app: FastAPI):
    cfg = state.config
    state.client = httpx.AsyncClient(
        timeout=httpx.Timeout(cfg.proxy_timeout),
        limits=httpx.Limits(max_keepalive_connections=100, max_connections=200),
    )
    state.manager = RendererManager(cfg)
    await state.manager.start()
    state.watcher = ModelServerWatcher(cfg, state.manager, asyncio.get_running_loop())
    state.watcher.start()
    logger.info("Tokenizer service started on port %d", cfg.port)
    yield
    state.watcher.stop()
    await state.manager.shutdown()
    await state.client.aclose()


app = FastAPI(lifespan=lifespan)


async def _proxy(request: Request, path: str) -> Response:
    body = await request.body()
    try:
        payload = json.loads(body)
    except json.JSONDecodeError:
        return JSONResponse(status_code=400, content={"error": "invalid JSON body"})

    model = payload.get("model", "")
    if not model:
        return JSONResponse(
            status_code=400, content={"error": "missing 'model' field"}
        )

    endpoint = state.manager.endpoint_for(model)
    if endpoint is None:
        return JSONResponse(
            status_code=503,
            content={"error": f"no ready tokenizer for model {model!r}"},
        )

    try:
        resp = await state.client.post(
            f"{endpoint}{path}",
            content=body,
            headers={"Content-Type": "application/json"},
        )
    except httpx.HTTPError as e:
        logger.warning("Proxy to renderer for model %s failed: %s", model, e)
        return JSONResponse(
            status_code=502,
            content={"error": f"tokenizer backend for model {model!r} unreachable"},
        )

    return Response(
        content=resp.content,
        status_code=resp.status_code,
        media_type=resp.headers.get("content-type", "application/json"),
    )


@app.post("/tokenize")
async def tokenize(request: Request) -> Response:
    return await _proxy(request, "/tokenize")


@app.post("/detokenize")
async def detokenize(request: Request) -> Response:
    return await _proxy(request, "/detokenize")


@app.get("/models")
async def models() -> JSONResponse:
    return JSONResponse(content={"models": state.manager.snapshot()})


@app.get("/healthz")
async def healthz() -> JSONResponse:
    return JSONResponse(content={"status": "ok"})


@app.get("/readyz")
async def readyz() -> JSONResponse:
    if (
        state.manager is not None
        and state.manager.is_ready()
        and state.watcher is not None
        and state.watcher.has_synced()
    ):
        return JSONResponse(content={"status": "ready"})
    return JSONResponse(status_code=503, content={"status": "not ready"})


def main() -> None:
    cfg = state.config
    uvicorn.run(app, host=cfg.host, port=cfg.port, log_level="info")


if __name__ == "__main__":
    main()
