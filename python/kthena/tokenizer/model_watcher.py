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

"""Watch ModelServer objects and keep the renderer manager in sync.

The watcher runs in a background thread (the official kubernetes client is
synchronous) and pushes the desired model set into the asyncio event loop
that owns the RendererManager.
"""

import asyncio
import logging
import threading
from typing import Dict, Set

from kubernetes import client, config, watch

from kthena.tokenizer.config import TokenizerServiceConfig
from kthena.tokenizer.renderer_manager import RendererManager

logger = logging.getLogger(__name__)

GROUP = "networking.serving.volcano.sh"
VERSION = "v1alpha1"
PLURAL = "modelservers"

WATCH_TIMEOUT_SECONDS = 300
RETRY_BACKOFF_SECONDS = 5


def _extract_model(obj: dict) -> str:
    return (obj.get("spec") or {}).get("model") or ""


class ModelServerWatcher:
    """Lists and watches ModelServer objects; tracks uid -> model."""

    def __init__(
        self,
        cfg: TokenizerServiceConfig,
        manager: RendererManager,
        loop: asyncio.AbstractEventLoop,
    ):
        self._cfg = cfg
        self._manager = manager
        self._loop = loop
        self._models_by_uid: Dict[str, str] = {}
        self._stop = threading.Event()
        self._thread = threading.Thread(
            target=self._run, name="modelserver-watcher", daemon=True
        )
        self._synced = threading.Event()

    def start(self) -> None:
        try:
            config.load_incluster_config()
        except config.ConfigException:
            config.load_kube_config()
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()

    def has_synced(self) -> bool:
        return self._synced.is_set()

    # -- internal ---------------------------------------------------------

    def _run(self) -> None:
        api = client.CustomObjectsApi()
        while not self._stop.is_set():
            try:
                self._list_and_watch(api)
            except Exception as e:  # noqa: BLE001 - keep the watcher alive
                logger.warning("ModelServer watch failed, retrying: %s", e)
                self._stop.wait(RETRY_BACKOFF_SECONDS)

    def _list(self, api: client.CustomObjectsApi, **kwargs):
        if self._cfg.watch_namespace:
            return api.list_namespaced_custom_object(
                GROUP, VERSION, self._cfg.watch_namespace, PLURAL, **kwargs
            )
        return api.list_cluster_custom_object(GROUP, VERSION, PLURAL, **kwargs)

    def _list_and_watch(self, api: client.CustomObjectsApi) -> None:
        resp = self._list(api)
        self._models_by_uid = {
            item["metadata"]["uid"]: _extract_model(item)
            for item in resp.get("items", [])
            if _extract_model(item)
        }
        self._push_desired()
        self._synced.set()

        resource_version = resp.get("metadata", {}).get("resourceVersion")
        w = watch.Watch()
        stream = w.stream(
            self._list,
            api,
            resource_version=resource_version,
            timeout_seconds=min(WATCH_TIMEOUT_SECONDS, self._cfg.resync_period),
        )
        for event in stream:
            if self._stop.is_set():
                w.stop()
                return
            obj = event.get("object") or {}
            uid = (obj.get("metadata") or {}).get("uid")
            if not uid:
                continue
            event_type = event.get("type")
            model = _extract_model(obj)
            if event_type == "DELETED":
                self._models_by_uid.pop(uid, None)
            elif model:
                self._models_by_uid[uid] = model
            else:
                self._models_by_uid.pop(uid, None)
            self._push_desired()

    def _push_desired(self) -> None:
        desired: Set[str] = set(self._models_by_uid.values())
        asyncio.run_coroutine_threadsafe(
            self._manager.set_models(desired), self._loop
        )
