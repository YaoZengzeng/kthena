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

"""Environment based configuration for the tokenizer service."""

import os
import shlex
from dataclasses import dataclass, field
from typing import List


def _env_int(name: str, default: int) -> int:
    value = os.getenv(name)
    if value is None or value == "":
        return default
    try:
        return int(value)
    except ValueError:
        return default


@dataclass
class TokenizerServiceConfig:
    # Port the tokenizer service frontend listens on.
    port: int = field(default_factory=lambda: _env_int("TOKENIZER_PORT", 8100))
    # Host the frontend binds to.
    host: str = field(default_factory=lambda: os.getenv("TOKENIZER_HOST", "0.0.0.0"))
    # First local port assigned to a `vllm render` subprocess. Each loaded
    # model gets its own consecutive port starting from this value.
    renderer_base_port: int = field(
        default_factory=lambda: _env_int("RENDERER_BASE_PORT", 8200)
    )
    # Maximum number of concurrently loaded tokenizers. Additional models are
    # reported as unavailable so that the router falls back to the engine.
    max_tokenizers: int = field(default_factory=lambda: _env_int("MAX_TOKENIZERS", 8))
    # Seconds to wait for a renderer subprocess to become healthy.
    renderer_startup_timeout: int = field(
        default_factory=lambda: _env_int("RENDERER_STARTUP_TIMEOUT_SECONDS", 600)
    )
    # Maximum consecutive restarts before a renderer is marked failed.
    renderer_max_restarts: int = field(
        default_factory=lambda: _env_int("RENDERER_MAX_RESTARTS", 3)
    )
    # Command used to launch a renderer. The model name and
    # `--host/--port` flags are appended automatically.
    renderer_command: List[str] = field(
        default_factory=lambda: shlex.split(
            os.getenv("VLLM_RENDER_COMMAND", "vllm render")
        )
    )
    # Extra arguments appended to every renderer command, e.g.
    # "--trust-remote-code".
    renderer_extra_args: List[str] = field(
        default_factory=lambda: shlex.split(os.getenv("VLLM_RENDER_EXTRA_ARGS", ""))
    )
    # Namespace to watch for ModelServer objects. Empty means all namespaces.
    watch_namespace: str = field(
        default_factory=lambda: os.getenv("WATCH_NAMESPACE", "")
    )
    # Seconds between full re-lists of ModelServer objects.
    resync_period: int = field(
        default_factory=lambda: _env_int("RESYNC_PERIOD_SECONDS", 300)
    )
    # Timeout for proxied /tokenize requests, in seconds.
    proxy_timeout: float = field(
        default_factory=lambda: float(os.getenv("PROXY_TIMEOUT_SECONDS", "5"))
    )
