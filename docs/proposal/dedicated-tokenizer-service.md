---
title: Dedicated Tokenizer Service for KV-Cache-Aware Routing
authors:
- TBD
reviewers:
- TBD
approvers:
- TBD

creation-date: 2026-09-01

---

## Dedicated Tokenizer Service for KV-Cache-Aware Routing

### Summary

Introduce an optional, dedicated tokenizer service that the Kthena router uses to
tokenize prompts for the `kvcache-aware` scheduling plugin, instead of sending
`/tokenize` requests to the backend inference engines. The service wraps
[`vllm launch render`](https://docs.vllm.ai/en/latest/cli/launch/render/) — vLLM's
lightweight, GPU-free frontend — and dynamically loads one tokenizer per model by
watching ModelServer objects. It can be deployed either as a router sidecar or as a
standalone, autoscalable service. The feature is disabled by default, and the router
falls back to engine-side tokenization when the service cannot serve a model.

### Motivation

The `kvcache-aware` score plugin must convert the request prompt into token IDs to
compute token-block hashes and query the KV-block index. Today the router picks a
random backend pod and calls the engine's `/tokenize` endpoint (vLLM or SGLang).
This has two problems:

1. **Backend pressure**: every scheduling decision issues an extra HTTP request to a
   GPU-serving engine pod. The tokenize API shares the engine's HTTP frontend with
   inference traffic, so heavy routing traffic steals CPU cycles from the serving
   path and adds load to pods that are already busy.
2. **Latency**: the tokenize round trip is on the critical path of request routing.
   When engine pods are loaded, their HTTP frontends respond slowly, inflating
   time-to-first-token for all requests.

A dedicated CPU-only tokenizer service removes this work from the GPU fleet and
gives it an independent scaling axis.

#### Goals

1. Provide a tokenizer service that exposes the vLLM-compatible `/tokenize` API
   without loading model weights or requiring GPUs.
2. Keep loaded tokenizers in sync with the cluster: watch ModelServer objects and
   dynamically load/unload the tokenizer for each distinct `spec.model`.
3. Support two deployment modes, selectable by the user:
   - **sidecar** of the router pod (lowest latency, scales with the router), and
   - **standalone** Deployment + Service with a configurable replica count.
4. Fall back transparently to engine-side tokenization when the service is
   unavailable or has not (yet) loaded the requested model's tokenizer. Fallback is
   configurable and enabled by default.
5. The whole feature is opt-in and disabled by default; existing behavior is
   unchanged when it is off.

#### Non-goals

1. Replacing engine-side tokenization entirely; the engine path remains the default
   and the fallback.
2. Serving chat-template rendering for request preprocessing outside the scheduler
   (may be a follow-up; the service already proxies `/detokenize` and can be
   extended).
3. Exact token parity guarantees between the service and every engine version.
   Both use the model's Hugging Face tokenizer; block hashing tolerates the rare
   divergence the same way it tolerates today's cross-pod differences.
4. A Kubernetes CRD for the tokenizer service; configuration stays in Helm values
   and router plugin args.
5. Autoscaling of the standalone Deployment. Users can attach their own HPA to the
   `kthena-tokenizer` Deployment if needed; a built-in HPA may be a follow-up.

### Proposal

#### User stories

1. **Platform operator running kvcache-aware routing at scale**: tokenize traffic
   noticeably loads vLLM frontends. The operator enables the tokenizer service as a
   router sidecar; scheduling no longer touches engine pods for tokenization.
2. **Operator with many models and high QPS**: a single sidecar cannot hold
   tokenizers for dozens of models. The operator deploys the standalone mode with
   multiple replicas so tokenizer capacity scales independently of router
   replicas.
3. **Operator rolling out a new model**: until the tokenizer for the new model is
   downloaded and ready, the router silently falls back to engine tokenization —
   no requests fail and no scheduling quality is lost.

#### Architecture

```
                 ┌────────────────────────────────────────────┐
                 │                kube-apiserver               │
                 └───────▲────────────────────────▲───────────┘
                         │ watch ModelServer      │ watch ModelServer
        ┌────────────────┴───────────┐     ┌──────┴──────────────────────┐
        │        kthena-router       │     │   kthena-tokenizer (svc)    │
        │  ┌──────────────────────┐  │     │  ┌───────────────────────┐  │
        │  │ kvcache-aware plugin │──┼──┬──┼─▶│ FastAPI frontend      │  │
        │  └──────────┬───────────┘  │  │  │  │  POST /tokenize       │  │
        │             │ fallback     │  │  │  └──────────┬────────────┘  │
        └─────────────┼──────────────┘  │  │             │ proxy by model│
                      │                 │  │  ┌──────────▼────────────┐  │
                      ▼                 │  │  │ renderer (model A)    │  │
        ┌──────────────────────────┐    │  │  │ renderer (model B)    │  │
        │  vLLM / SGLang pods      │    │  │  │ ... one per model     │  │
        │  POST /tokenize (GPU)    │    │  │  └───────────────────────┘  │
        └──────────────────────────┘    │  └─────────────────────────────┘
                                        └── sidecar mode: same pod,
                                            endpoint http://127.0.0.1:8100
```

The tokenizer service has three parts:

1. **Frontend** (FastAPI): serves `POST /tokenize` / `POST /detokenize` with the
   same request/response shapes as vLLM, plus `/models`, `/healthz`, `/readyz`.
   It inspects the `model` field of each request and proxies it to the renderer
   subprocess that owns that model. If no renderer is ready for the model, it
   returns `503`, which triggers the router-side fallback.
2. **Renderer manager**: launches and supervises one `vllm launch render <model>`
   subprocess per model on consecutive local ports. `vllm launch render` serves the
   OpenAI-compatible tokenization endpoints using only the model's tokenizer and
   chat template — no weights, no GPU. Crashed renderers are restarted with a
   bounded budget; the number of concurrently loaded tokenizers is capped by
   `MAX_TOKENIZERS`.
3. **ModelServer watcher**: lists and watches ModelServer objects (optionally
   namespace-scoped) and reconciles the desired model set from each object's
   `spec.model`. Creating a ModelServer pre-warms its tokenizer; deleting the last
   ModelServer that references a model unloads it.

#### Router integration

The `kvcache-aware` plugin gains an optional `tokenizerService` argument:

```yaml
- name: kvcache-aware
  args:
    blockSizeToHash: 16
    maxBlocksToMatch: 128
    tokenizerService:
      enabled: true                        # default: false
      endpoint: http://127.0.0.1:8100      # sidecar; use the Service DNS for standalone
      fallbackToEngine: true               # default: true
```

`TokenizerManager.TokenizePrompt` tries the service first when enabled. On any
failure (connection error, non-2xx, unknown model) it falls back to the existing
engine-pod path unless `fallbackToEngine: false`. Because the service speaks the
vLLM `/tokenize` protocol, the router reuses the existing vLLM adapter and the
shared retryable HTTP client.

#### Deployment modes

Helm values (`networking.kthenaRouter.tokenizerService`):

| Key                   | Default   | Meaning                            |
| --------------------- | --------- | ---------------------------------- |
| `enabled`             | `false`   | Deploy the tokenizer service       |
| `mode`                | `sidecar` | `sidecar` or `standalone`          |
| `port`                | `8100`    | Frontend listen port               |
| `maxTokenizers`       | `8`       | Max concurrently loaded tokenizers |
| `standalone.replicas` | `1`       | Replicas in standalone mode        |

- **Sidecar**: the container is added to the router pod; the plugin endpoint is
  `http://127.0.0.1:8100`. It reuses the router ServiceAccount, which already has
  `get/list/watch` on ModelServers.
- **Standalone**: a Deployment, ClusterIP Service, and a dedicated ServiceAccount
  with a read-only ClusterRole on ModelServers. The
  plugin endpoint is `http://kthena-tokenizer.<namespace>.svc:8100`.

#### Notes / constraints

- The tokenizer image is based on the official vLLM CPU build (`vllm/vllm-openai-cpu`)
  so `vllm launch render` runs on GPU-less nodes; the CUDA image fails platform
  detection on hosts without a GPU driver. Gated models need `HF_TOKEN` via `extraEnv`.
- Tokenizer loading downloads tokenizer/config files from the model hub (or a
  mounted cache); `readyz` only reflects watcher sync, so a not-yet-ready model
  simply falls back rather than failing readiness.
- The service keys renderers by ModelServer `spec.model`, which is the same model
  name the router passes to `/tokenize` today.

#### Risks and mitigations

| Risk                                       | Mitigation                                                                                            |
| ------------------------------------------ | ----------------------------------------------------------------------------------------------------- |
| Tokenizer service down or model not loaded | Router falls back to engine tokenization (default on); scheduling quality unchanged                   |
| Renderer subprocess crash loops            | Restart budget (`RENDERER_MAX_RESTARTS`), after which the model is marked failed and fallback applies |
| Memory growth with many models             | `maxTokenizers` cap; standalone mode with more replicas for horizontal scale                          |
| Token mismatch vs engine                   | Both sides use the model's HF tokenizer; block hashing already tolerates cold misses                  |
| Extra hop in standalone mode               | Sidecar mode offers a localhost path; both are still far cheaper than a GPU pod round trip            |

### Design details

- Router: `pkg/kthena-router/scheduler/plugins/tokenization` gains
  `TokenizerServiceConfig{Enabled, Endpoint, FallbackToEngine}` on
  `TokenizerManagerConfig`; `kvcache_aware.go` parses and validates the new
  `tokenizerService` plugin args (empty endpoint disables the feature with a
  warning; fallback defaults to true).
- Service: `python/kthena/tokenizer/` (`app.py`, `renderer_manager.py`,
  `model_watcher.py`, `config.py`), image built by
  `make docker-build-tokenizer` from `python/Dockerfile.tokenizer`.
- Chart: sidecar injection in the router Deployment; standalone
  Deployment/Service/RBAC under
  `charts/kthena/charts/networking/templates/kthena-tokenizer/`.
- Observability (follow-up): Prometheus metrics for per-model tokenize latency,
  proxy errors, and fallback counts on both the service and the router.

### Implementation History

- 2026-09-01: Initial proposal and demo implementation (service, router fallback,
  Helm chart, docs).
