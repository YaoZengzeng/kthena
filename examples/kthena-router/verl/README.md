# verl + Kthena router

Route [verl](https://github.com/volcengine/verl) RL training rollouts through the
Kthena router. During each training step verl generates completions from a pool
of vLLM replicas; this integration replaces verl's built-in round-robin replica
selection with the router, which steers every request to the replica whose KV
cache already holds the longest prefix of the prompt. A prompt shared across a
GRPO group is then prefilled once instead of once per replica.

No verl source changes are required and no Kubernetes cluster is involved: the
router runs as a plain binary with `--resource-source=file`, so the whole
integration is one Hydra override.

## How it works

```
                        ┌──────────────────────────────────┐
                        │ kthena-router (head node)        │
   AgentLoopWorker ──1──▶│  --resource-source=file          │
     (verl rollout)      │  prefix-cache + least-request    │
          │              └──────────────────────────────────┘
          │                     │ 2. "use replica 1"
          │◀────────────────────┘
          │
          └──3. generate ──▶ vLLM replica 1     vLLM replica 0
```

The router picks the replica, verl dispatches to it. Keeping generation on
verl's own path means every rollout feature that depends on the rollout server
actor, such as the weight version reported with each response, keeps working.
See [why the router does not proxy the rollout traffic](../../../docs/kthena/docs/user-guide/verl-integration.md#why-the-router-does-not-proxy-the-rollout-traffic).

`KthenaRouterAgentLoopManager` runs before verl spawns its agent loop workers:

1. It reads the rollout replica addresses from verl's load balancer.
2. A Ray actor pinned to the head node writes a `ModelServer` (one static
   endpoint per replica) plus a `ModelRoute`, starts `kthena-router` against
   that directory, and waits for `/readyz`.
3. It replaces verl's `LLMServerClient` with `KthenaLLMClient`, which asks the
   router's `/v1/schedule` for a replica, generates on it through verl's usual
   Ray call, and reports completion to `/v1/schedule/release` so the router
   keeps counting in-flight requests correctly.

The integration lives in [python/kthena/verl_integration](../../../python/kthena/verl_integration);
the router scheduler configuration used here is [router-config.yaml](router-config.yaml).

## Run it (one command)

Prerequisites: Docker with the NVIDIA container runtime, two GPUs of roughly
20GB (the demo is tuned for RTX 4000 Ada), and Go to build the router binary.

```bash
bash examples/kthena-router/verl/run-demo.sh            # rollouts routed by Kthena
bash examples/kthena-router/verl/run-demo.sh native     # baseline: verl round-robin
bash examples/kthena-router/verl/run-demo.sh teardown   # remove the container
```

The script builds `bin/kthena-router`, starts one verl container with both GPUs,
installs verl and the integration, downloads GSM8K and Qwen3-0.6B, and runs GRPO.
Everything downloaded is cached in `$DEMO_DIR` (default `/root/kthena-verl-demo`),
so re-runs skip straight to training.

## Why two GPUs and this model

verl colocates rollout with FSDP training on the same GPUs, so the number of
vLLM replicas the router can choose between is `GPUs / tensor_parallel_size`.
With `TP=1`, two GPUs give two replicas — the minimum for routing to mean
anything, since one replica makes any router a no-op.

Qwen3-4B (the upstream default of the example script this demo reuses) does not
fit colocated on a 20GB card, so the demo uses Qwen3-0.6B. Model size is
irrelevant for demonstrating routing.

## Time budget

Once the verl image is pulled and `$DEMO_DIR` is warm, a run finishes in a few
minutes: Qwen3-0.6B, `STEPS=3`, batch 32, 512 token prompts and responses, and
validation disabled (`val_before_train=False`, `test_freq=-1`). The one-time
costs are the image pull and the first setup (verl install, model and dataset
download).

## Tuning

| Variable       | Default                    | Meaning                                   |
| -------------- | -------------------------- | ----------------------------------------- |
| `IMAGE`        | `verlai/verl:vllm024.dev2` | verl environment image                    |
| `DEMO_DIR`     | `/root/kthena-verl-demo`   | Host cache for verl, model, data and logs |
| `VERL_COMMIT`  | pinned commit              | verl revision installed in the container  |
| `MODEL`        | `Qwen/Qwen3-0.6B`          | Policy model                              |
| `STEPS`        | `3`                        | Training steps                            |
| `N`            | `8`                        | GRPO rollout group size                   |
| `GPU_MEM_UTIL` | `0.5`                      | vLLM memory fraction, lower it on OOM     |
| `SKIP_SETUP=1` | unset                      | Reuse an already prepared container       |

## Inspecting the routing

The router writes its manifests, configuration and log below
`$DEMO_DIR/router`. While a run is in progress:

```bash
# the replicas the router load balances over
cat "$DEMO_DIR/router/resources/rollout.yaml"

# prefix cache entries and scheduling metrics
docker exec kthena-verl-demo curl -s localhost:9090/metrics | grep kthena_router

# the serving instances the router knows about, with their scraped engine metrics
docker exec kthena-verl-demo curl -s localhost:15000/debug/config_dump/pods
```

## Using an already running router

Point the integration at an external router instead of letting it start one:

```bash
+actor_rollout_ref.rollout.custom.kthena_router_address=10.0.0.5:8080
```

The router must run with `ENABLE_ENDPOINT_PICKER=true`, and it must have a
`ModelRoute` whose `modelName` matches `kthena_router_model_name` (default
`verl-rollout`) and a `ModelServer` listing the rollout replicas.
