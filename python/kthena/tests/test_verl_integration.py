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

import pytest

from kthena.verl_integration.router import build_resources


def _by_kind(resources):
    return {resource["kind"]: resource for resource in resources}


def test_build_resources_declares_every_replica_as_an_endpoint():
    resources = _by_kind(build_resources(["10.0.0.1:8000", "10.0.0.2:8001"], "verl-rollout", "Qwen/Qwen3-0.6B"))

    model_server = resources["ModelServer"]
    assert model_server["spec"]["model"] == "Qwen/Qwen3-0.6B"
    assert model_server["spec"]["endpoints"] == [
        {"name": "rollout-0", "address": "10.0.0.1", "port": 8000},
        {"name": "rollout-1", "address": "10.0.0.2", "port": 8001},
    ]

    model_route = resources["ModelRoute"]
    assert model_route["spec"]["modelName"] == "verl-rollout"
    assert model_route["spec"]["rules"][0]["targetModels"] == [{"modelServerName": "verl-rollout"}]


def test_build_resources_rejects_an_empty_replica_list():
    with pytest.raises(ValueError):
        build_resources([], "verl-rollout", "Qwen/Qwen3-0.6B")


@pytest.mark.parametrize("endpoint", ["10.0.0.1", "10.0.0.1:", "10.0.0.1:http"])
def test_build_resources_rejects_malformed_endpoints(endpoint):
    with pytest.raises(ValueError):
        build_resources([endpoint], "verl-rollout", "Qwen/Qwen3-0.6B")
