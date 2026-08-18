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

"""Standalone Kthena router process for verl rollouts.

The router is started with ``--resource-source=file``, so it needs no
Kubernetes API server: the rollout replicas are declared as static endpoints of
a ModelServer manifest written next to the process.
"""

from __future__ import annotations

import ctypes
import ctypes.util
import json
import logging
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request

import yaml

logger = logging.getLogger(__name__)

DEFAULT_BINARY = "kthena-router"
DEFAULT_PORT = 8080
DEFAULT_METRICS_PORT = 9090
DEFAULT_DEBUG_PORT = 15000
DEFAULT_WORK_DIR = "/tmp/kthena-router"
DEFAULT_MODEL_NAME = "verl-rollout"

# The router polls the resource directory instead of watching it, so a short
# period keeps startup fast; the manifests are written once and never change.
RESOURCE_SYNC_PERIOD = "2s"

_API_VERSION = "networking.serving.volcano.sh/v1alpha1"
_NAMESPACE = "default"
_PR_SET_PDEATHSIG = 1


def default_router_config() -> dict:
    """Scheduler configuration used when the caller does not provide one.

    Rollout batches of an RL step share long prompt prefixes (a GRPO group
    repeats the same prompt ``n`` times), so prefix cache affinity is weighted
    above the in-flight request count, which spreads prompts that no replica
    has cached yet.
    """
    return {
        "scheduler": {
            "pluginConfig": [
                {
                    "name": "prefix-cache",
                    # Prompt token ids are hashed as four bytes each, so a
                    # 64 byte block matches vLLM's 16 token KV cache block.
                    "args": {
                        "blockSizeToHash": 64,
                        "maxBlocksToMatch": 256,
                        "maxHashCacheSize": 500000,
                        "topKMatches": 5,
                    },
                },
            ],
            "plugins": {
                "Score": {
                    "enabled": [
                        {"name": "prefix-cache", "weight": 2},
                        {"name": "least-request", "weight": 1},
                    ],
                },
            },
        },
    }


def build_resources(endpoints: list[str], model_name: str, upstream_model: str) -> list[dict]:
    """Build the ModelServer and ModelRoute manifests for the rollout replicas.

    Args:
        endpoints: rollout replica addresses as ``host:port``.
        model_name: model name the rollout clients send in the request body.
        upstream_model: model name the replicas serve, which the router
            substitutes before forwarding.
    """
    if not endpoints:
        raise ValueError("no rollout endpoints to route to")

    parsed = [_split_address(endpoint) for endpoint in endpoints]
    model_server = {
        "apiVersion": _API_VERSION,
        "kind": "ModelServer",
        "metadata": {"name": model_name, "namespace": _NAMESPACE},
        "spec": {
            "model": upstream_model,
            "inferenceEngine": "vLLM",
            "workloadPort": {"port": parsed[0][1]},
            "endpoints": [
                {"name": f"rollout-{rank}", "address": host, "port": port} for rank, (host, port) in enumerate(parsed)
            ],
        },
    }
    model_route = {
        "apiVersion": _API_VERSION,
        "kind": "ModelRoute",
        "metadata": {"name": model_name, "namespace": _NAMESPACE},
        "spec": {
            "modelName": model_name,
            "rules": [
                {
                    "name": "rollout",
                    "targetModels": [{"modelServerName": model_name}],
                },
            ],
        },
    }
    return [model_server, model_route]


def discover_served_model(endpoints: list[str], timeout: float = 30.0) -> str:
    """Return the model id the rollout replicas serve, read from ``/v1/models``."""
    deadline = time.monotonic() + timeout
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        for endpoint in endpoints:
            try:
                with urllib.request.urlopen(f"http://{endpoint}/v1/models", timeout=5) as response:
                    models = json.load(response).get("data") or []
                if models:
                    return models[0]["id"]
            except (urllib.error.URLError, OSError, ValueError, KeyError) as exc:
                last_error = exc
        time.sleep(1)
    raise RuntimeError(f"could not read the served model from {endpoints}: {last_error}")


class KthenaRouter:
    """Owns a ``kthena-router`` process configured for a set of rollout replicas."""

    def __init__(
        self,
        *,
        binary: str = DEFAULT_BINARY,
        work_dir: str = DEFAULT_WORK_DIR,
        config_file: str | None = None,
        model_name: str = DEFAULT_MODEL_NAME,
        port: int = DEFAULT_PORT,
        metrics_port: int = DEFAULT_METRICS_PORT,
        debug_port: int = DEFAULT_DEBUG_PORT,
    ):
        self._binary = binary
        self._work_dir = work_dir
        self._config_file = config_file
        self._model_name = model_name
        self._port = port
        self._metrics_port = metrics_port
        self._debug_port = debug_port
        self._process: subprocess.Popen | None = None
        self._log_file = None

    @property
    def model_name(self) -> str:
        return self._model_name

    def start(self, endpoints: list[str], upstream_model: str) -> int:
        """Write the manifests, start the router and wait until it is ready.

        Returns the port the router listens on.
        """
        binary = shutil.which(self._binary) or self._binary
        if not os.path.isfile(binary):
            raise RuntimeError(
                f"kthena-router binary not found at {self._binary!r}; build it with "
                f"`make build` and point rollout.custom.kthena_router_binary at bin/kthena-router"
            )

        resource_dir = os.path.join(self._work_dir, "resources")
        os.makedirs(resource_dir, exist_ok=True)
        resources = build_resources(endpoints, self._model_name, upstream_model)
        with open(os.path.join(resource_dir, "rollout.yaml"), "w") as f:
            yaml.safe_dump_all(resources, f, sort_keys=False)

        config_file = self._config_file
        if not config_file:
            config_file = os.path.join(self._work_dir, "routerConfiguration.yaml")
            with open(config_file, "w") as f:
                yaml.safe_dump(default_router_config(), f, sort_keys=False)

        log_path = os.path.join(self._work_dir, "router.log")
        self._log_file = open(log_path, "w")
        command = [
            binary,
            "--resource-source=file",
            f"--resource-dir={resource_dir}",
            f"--resource-sync-period={RESOURCE_SYNC_PERIOD}",
            f"--router-config={config_file}",
            f"--port={self._port}",
            f"--metrics-port={self._metrics_port}",
            f"--debug-port={self._debug_port}",
        ]
        logger.info("[KthenaRouter] starting %s (log: %s)", " ".join(command), log_path)
        self._process = subprocess.Popen(
            command,
            # The endpoint picker API is opt-in because it exposes the serving
            # instance addresses; the rollout clients need it to dispatch.
            env={**os.environ, "ENABLE_ENDPOINT_PICKER": "true"},
            stdout=self._log_file,
            stderr=subprocess.STDOUT,
            preexec_fn=_die_with_parent if sys.platform == "linux" else None,
        )
        self._wait_ready(log_path)
        return self._port

    def stop(self) -> None:
        if self._process is not None and self._process.poll() is None:
            self._process.send_signal(signal.SIGTERM)
            try:
                self._process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                self._process.kill()
        self._process = None
        if self._log_file is not None:
            self._log_file.close()
            self._log_file = None

    def _wait_ready(self, log_path: str, timeout: float = 120.0) -> None:
        deadline = time.monotonic() + timeout
        url = f"http://127.0.0.1:{self._port}/readyz"
        while time.monotonic() < deadline:
            if self._process.poll() is not None:
                raise RuntimeError(f"kthena-router exited with {self._process.returncode}; see {log_path}")
            try:
                with urllib.request.urlopen(url, timeout=2) as response:
                    if response.status == 200:
                        logger.info("[KthenaRouter] ready on :%d", self._port)
                        return
            except (urllib.error.URLError, OSError):
                pass
            time.sleep(0.5)
        self.stop()
        raise RuntimeError(f"kthena-router did not become ready within {timeout}s; see {log_path}")


def _split_address(endpoint: str) -> tuple[str, int]:
    host, separator, port = endpoint.rpartition(":")
    if not separator or not port.isdigit():
        raise ValueError(f"rollout endpoint {endpoint!r} is not in host:port form")
    return host, int(port)


def _die_with_parent() -> None:
    """Ask the kernel to signal the router when the launching process dies."""
    libc = ctypes.CDLL(ctypes.util.find_library("c"), use_errno=True)
    libc.prctl(_PR_SET_PDEATHSIG, signal.SIGTERM, 0, 0, 0)
