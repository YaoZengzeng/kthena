# Tokenizer Service

The Kthena Tokenizer Service is an optional, GPU-free component that tokenizes prompts for the [kvcache-aware plugin](./kvcache-aware.md) so that the router does not need to call the backend inference engines for tokenization. It wraps [`vllm render`](https://docs.vllm.ai/en/latest/cli/launch/render/) and dynamically loads one tokenizer per model by watching ModelServer objects.

The feature is **disabled by default**. When enabled, the router sends `/tokenize` requests to the tokenizer service first and, by default, **falls back to engine-side tokenization** if the service is unavailable or has not loaded the requested model's tokenizer.

## Why use it

With `kvcache-aware` routing enabled, every scheduling decision tokenizes the prompt. Without the tokenizer service, the router sends this request to a randomly selected backend engine pod (vLLM `/tokenize`), which:

- adds load to GPU pods that are already serving inference traffic, and
- puts a busy engine HTTP frontend on the routing critical path, increasing latency.

The tokenizer service moves this work to a cheap CPU-only component that can scale independently.

## How it works

1. The service watches ModelServer objects (all namespaces by default) and reads their `spec.model` field.
2. For each distinct model, it launches a `vllm render` subprocess, which serves the vLLM-compatible `/tokenize` API using only the model's tokenizer and chat template — no model weights and no GPU.
3. The service frontend proxies each `/tokenize` request to the subprocess for the requested `model`.
4. If no tokenizer is ready for the model, the frontend returns `503` and the router falls back to engine tokenization (unless fallback is disabled).

## Deployment modes

### Sidecar mode (default)

The tokenizer runs as an extra container in the router pod. This gives the lowest latency (localhost) and scales together with the router.

```bash
helm upgrade --install kthena charts/kthena -n kthena-system \
  --set networking.kthenaRouter.tokenizerService.enabled=true \
  --set networking.kthenaRouter.tokenizerService.mode=sidecar
```

### Standalone mode

The tokenizer runs as its own Deployment and ClusterIP Service, with optional HPA-based autoscaling. Use this when you serve many models or want to scale tokenizer capacity independently of router replicas.

```bash
helm upgrade --install kthena charts/kthena -n kthena-system \
  --set networking.kthenaRouter.tokenizerService.enabled=true \
  --set networking.kthenaRouter.tokenizerService.mode=standalone \
  --set networking.kthenaRouter.tokenizerService.standalone.replicas=2 \
  --set networking.kthenaRouter.tokenizerService.standalone.autoscaling.enabled=true \
  --set networking.kthenaRouter.tokenizerService.standalone.autoscaling.minReplicas=1 \
  --set networking.kthenaRouter.tokenizerService.standalone.autoscaling.maxReplicas=5
```

## Router configuration

Enable the tokenizer service in the `kvcache-aware` plugin args of the router ConfigMap (`kthena-router-config`):

```yaml
scheduler:
  pluginConfig:
  - name: kvcache-aware
    args:
      blockSizeToHash: 16
      maxBlocksToMatch: 128
      tokenizerService:
        # Default: false. When false, the router tokenizes via the backend engines.
        enabled: true
        # Sidecar mode:
        endpoint: http://127.0.0.1:8100
        # Standalone mode (adjust the namespace):
        # endpoint: http://kthena-tokenizer.kthena-system.svc:8100
        # Default: true. Fall back to engine tokenization when the service fails.
        fallbackToEngine: true
  plugins:
    Score:
      enabled:
        - name: kvcache-aware
          weight: 1
        - name: least-request
          weight: 1
```

Restart the router after changing the ConfigMap (hot reload is not supported).

| Field                               | Default                   | Description                                                 |
| ----------------------------------- | ------------------------- | ----------------------------------------------------------- |
| `tokenizerService.enabled`          | `false`                   | Use the dedicated tokenizer service for prompt tokenization |
| `tokenizerService.endpoint`         | — (required when enabled) | Base URL of the tokenizer service                           |
| `tokenizerService.fallbackToEngine` | `true`                    | Fall back to engine `/tokenize` on any service failure      |

## Helm values reference

All values live under `networking.kthenaRouter.tokenizerService`:

| Value                                                   | Default                               | Description                                      |
| ------------------------------------------------------- | ------------------------------------- | ------------------------------------------------ |
| `enabled`                                               | `false`                               | Deploy the tokenizer service                     |
| `mode`                                                  | `sidecar`                             | `sidecar` or `standalone`                        |
| `port`                                                  | `8100`                                | Frontend listen port                             |
| `image.repository`                                      | `ghcr.io/volcano-sh/kthena-tokenizer` | Image repository                                 |
| `image.tag`                                             | `latest`                              | Image tag                                        |
| `maxTokenizers`                                         | `8`                                   | Max concurrently loaded model tokenizers         |
| `extraEnv`                                              | `[]`                                  | Extra env vars, e.g. `HF_TOKEN` for gated models |
| `resources`                                             | 500m/1Gi – 2/4Gi                      | Container resources                              |
| `standalone.replicas`                                   | `1`                                   | Replicas when autoscaling is off                 |
| `standalone.autoscaling.enabled`                        | `false`                               | Create an HPA                                    |
| `standalone.autoscaling.minReplicas`                    | `1`                                   | HPA minimum                                      |
| `standalone.autoscaling.maxReplicas`                    | `5`                                   | HPA maximum                                      |
| `standalone.autoscaling.targetCPUUtilizationPercentage` | `80`                                  | HPA CPU target                                   |

Service environment variables (advanced, set via `extraEnv`):

| Variable                           | Default    | Description                                                             |
| ---------------------------------- | ---------- | ----------------------------------------------------------------------- |
| `MAX_TOKENIZERS`                   | `8`        | Cap on concurrently loaded tokenizers                                   |
| `WATCH_NAMESPACE`                  | `""` (all) | Restrict the ModelServer watch to one namespace                         |
| `RENDERER_STARTUP_TIMEOUT_SECONDS` | `600`      | Time budget for a tokenizer to become ready                             |
| `VLLM_RENDER_EXTRA_ARGS`           | `""`       | Extra flags for every `vllm render` process, e.g. `--trust-remote-code` |
| `HF_TOKEN`                         | —          | Hugging Face token for gated models                                     |

## Verification

1. Check that the tokenizer discovered your models:

   ```bash
   # Standalone mode
   kubectl -n kthena-system port-forward svc/kthena-tokenizer 8100:8100 &
   curl -s localhost:8100/models | jq
   ```

   Expected output once the tokenizer is loaded:

   ```json
   {"models": [{"model": "Qwen/Qwen3-0.6B", "status": "ready", "port": 8200, "restarts": 0, "lastError": ""}]}
   ```

2. Tokenize directly against the service:

   ```bash
   curl -s localhost:8100/tokenize \
     -H 'Content-Type: application/json' \
     -d '{"model": "Qwen/Qwen3-0.6B", "prompt": "Hello, world!"}' | jq
   ```

3. Send an inference request through the router and confirm in the router logs (`-v=4`) that tokenization no longer targets engine pod IPs, and that `KVCacheAware.Score` reports tokens.

4. Test the fallback: scale the tokenizer to zero (or request a model it has not loaded) and confirm requests still route successfully with a router log line like `tokenizer service failed for model ..., falling back to engine`.

## Troubleshooting

- **Model stays in `loading`**: the renderer is downloading tokenizer files from the model hub. For gated models, set `HF_TOKEN` via `extraEnv`; for air-gapped clusters, mount a Hugging Face cache and set `HF_HUB_OFFLINE=1`.
- **Model shows `failed`**: check `lastError` in `/models` and the pod logs. Common causes: unsupported model name, hub unreachable, or `maxTokenizers` reached (increase it or use standalone mode).
- **Router never uses the service**: verify `tokenizerService.enabled: true` and a non-empty `endpoint` in the kvcache-aware plugin args, then restart the router.
