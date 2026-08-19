# verl Integration (RL Rollout Routing)

Kthena Router can serve the rollout traffic of a reinforcement learning run.
This guide shows how to route [verl](https://github.com/volcengine/verl) RL
training rollouts through the router so that the prompts of a rollout group are
prefilled once instead of once per replica.

## Overview

During each training step verl generates completions from a pool of vLLM
replicas. Its built-in client picks a replica by round-robin over the in-flight
request counts, which ignores what each replica already has in its KV cache. In
a GRPO step the same prompt is sent `rollout.n` times, and multi-turn rollouts
re-send the whole conversation on every turn, so a cache-aware router removes a
large amount of redundant prefill.

The integration replaces verl's replica selection with the router and needs no
verl source changes: it is one Hydra override. The router runs as a standalone
binary with [file-based resources](standalone-router.md), so it needs no
controller and no CRDs installed in the cluster the training job runs on.

The router only selects the replica; verl sends the request itself over its own
Ray call. Generation therefore keeps every rollout feature that depends on the
rollout server, such as the weight version reported with each response, while
the scheduling decision moves to the router.

```
                        ┌──────────────────────────────────┐
                        │ kthena-router                    │
   AgentLoopWorker ──1──▶│  --resource-source=file          │
     (verl rollout)      │  prefix-cache + least-request    │
          │              └──────────────────────────────────┘
          │                     │ 2. "use replica 1"
          │◀────────────────────┘
          │
          └──3. generate ──▶ vLLM replica 1     vLLM replica 0
```

## Why the router does not proxy the rollout traffic

For serving traffic the router is a proxy, and routing rollouts the same way
looks simpler: point verl at the router and let it forward to a replica. That
does work for synchronous on-policy training, but it loses information that only
the rollout server actor has, so this integration keeps generation on verl's own
dispatch path.

**The weight version of the reply.** Every response carries
`extra_fields["global_steps"]`, the version of the weights the replica served it
with. It lives in the rollout server actor and is refreshed by a `set_global_steps`
call during weight sync; the inference engine's OpenAI API does not return it.
verl's v1 trainer, which is the default (`trainer.use_v1: true`), turns it into
the `min_global_steps` and `max_global_steps` of a trajectory and derives
`trajectory_staleness` from them. A proxy cannot supply the field, and a client
that filled in the trainer's current step instead would report a staleness of
zero for every trajectory, which is exactly the quantity asynchronous RL is
measuring.

**Fields the HTTP API does not carry.** `routed_experts` for MoE expert-routing
statistics, `prompt_logprobs`, `num_preempted`, and the `stop_reason="aborted"`
reply that a paused replica returns are all produced by the Ray path. Rollout
also relies on engine-level behaviour, such as request priority for preemption
and the LoRA adapter loaded into the running engine, which the proxy would have
to reproduce.

So the router does the one thing the client cannot do for itself. Prefix cache
hashes, in-flight counters and scraped engine metrics all live in the router
process, so it answers which replica to use, and verl sends the request there.
This is the mode that
[llm-d-rl](https://github.com/llm-d-incubation/llm-d-rl/tree/main/integrations/verl)
recommends for the same reasons.

The cost is the [endpoint picker API](#endpoint-picker-api) described below. It
is off by default and adds no work to the proxy path.

## Enabling it

Put the integration on the Python path of the training container, for example
by adding a `.pth` file pointing at `python/` of a Kthena checkout, then add the
override:

```bash
python3 -m verl.trainer.main_ppo \
  ... \
  +actor_rollout_ref.rollout.agent.agent_loop_manager_class=kthena.verl_integration.agent_loop_manager.KthenaRouterAgentLoopManager \
  +actor_rollout_ref.rollout.custom.kthena_router_binary=/path/to/kthena-router \
  +actor_rollout_ref.rollout.custom.kthena_router_config=/path/to/routerConfiguration.yaml
```

`KthenaRouterAgentLoopManager` runs before verl spawns its agent loop workers
and

1. reads the rollout replica addresses from verl's load balancer,
2. starts a Ray actor pinned to the head node that writes a ModelServer with one
   static endpoint per replica plus a ModelRoute, launches `kthena-router`
   against that directory and waits for `/readyz`,
3. replaces verl's `LLMServerClient` with a client that asks the router which
   replica to use.

For every generation the client posts the prompt token ids to the router's
`/v1/schedule`, dispatches to the replica the router names, and reports
completion to `/v1/schedule/release`. Nothing is re-tokenized on the way: the
router hashes the token ids directly.

## Configuration

Every option lives under `actor_rollout_ref.rollout.custom`:

| Key                            | Default              | Description                                                       |
| ------------------------------ | -------------------- | ----------------------------------------------------------------- |
| `kthena_router_address`        | unset                | Reuse an already running router instead of starting one.          |
| `kthena_router_binary`         | `kthena-router`      | Router binary to launch.                                          |
| `kthena_router_config`         | generated            | Router scheduler configuration file.                              |
| `kthena_router_work_dir`       | `/tmp/kthena-router` | Where manifests, generated configuration and the log are written. |
| `kthena_router_model_name`     | `verl-rollout`       | Model name the rollout requests carry, matched by the ModelRoute. |
| `kthena_router_upstream_model` | discovered           | Model name the replicas serve; read from `/v1/models` when unset. |
| `kthena_router_port`           | `8080`               | Router listen port.                                               |
| `kthena_router_metrics_port`   | `9090`               | Router Prometheus port.                                           |
| `kthena_router_debug_port`     | `15000`              | Router debug port.                                                |

When `kthena_router_config` is not set, the integration generates a
configuration that scores replicas by prefix cache affinity first and by
in-flight requests second:

```yaml
scheduler:
  pluginConfig:
    - name: prefix-cache
      args:
        blockSizeToHash: 64
        maxBlocksToMatch: 256
        maxHashCacheSize: 500000
        topKMatches: 5
  plugins:
    Score:
      enabled:
        - name: prefix-cache
          weight: 2
        - name: least-request
          weight: 1
```

Prompt token ids are hashed as four bytes each, so the default 64 byte block
matches vLLM's 16 token KV cache block.

Set `actor_rollout_ref.rollout.disable_log_stats=False` so the replicas expose
`/metrics`; without it the router has no engine metrics and load based plugins
such as `least-request` or `gpu-usage` score every replica the same.

## Endpoint picker API

The integration starts the router with `ENABLE_ENDPOINT_PICKER=true`, which adds
two endpoints used by clients that dispatch requests themselves. They are
disabled by default because the response reveals the addresses of the serving
instances. On Kubernetes set `networking.kthenaRouter.endpointPicker.enabled=true`
in the Helm values instead.

`POST /v1/schedule` takes the body of a normal completion request and runs the
configured scheduler over it, but returns the instance it picked instead of
forwarding. `prompt` may be a string or a list of token ids:

```json
{"model": "verl-rollout", "prompt": [151644, 872, 198]}
```

The response names the selected instance:

```json
{
  "model": "upstream-model",
  "instances": [
    {"namespace": "default", "name": "rollout-1", "address": "10.0.0.5", "port": 8000}
  ]
}
```

`model` is the name the instances serve, so it can be sent upstream unchanged.
With prefill/decode disaggregation the response holds two instances, each with a
`role` of `prefill` or `decode`.

Scheduling counts the request as in-flight on the instance it picked, so the
caller must report completion:

```json
POST /v1/schedule/release
{"instances": [{"namespace": "default", "name": "rollout-1"}]}
```

The router answers `204 No Content` even for instances it no longer knows, which
keeps the release path safe to fire and forget.

## Limitations

- The rollout backend must be vLLM. The client resolves the replica the router
  named through the Ray actor names verl's vLLM backend registers.
- Prefill/decode disaggregated rollouts are dispatched to the decode replica,
  which is what the rollout engine expects; the router does not drive the
  prefill/decode handover itself in this mode.

## Example

[examples/kthena-router/verl](https://github.com/volcano-sh/kthena/tree/main/examples/kthena-router/verl)
contains a one-command demo that runs GRPO on GSM8K with Qwen3-0.6B in a single
pod on two 20GB GPUs.
