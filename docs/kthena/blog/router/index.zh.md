---
slug: router-blog-post
title: 深入解析 Kthena Router
authors: [YaoZengzeng]
tags: []
---

# 深入解析 Kthena Router

## 1. 简介

随着大语言模型（LLM）日益成为现代应用的核心，支持它们的基础设施必须不断演进，以满足苛刻的性能、可扩展性和成本要求。在生产环境中部署 LLM 面临着独特的挑战：模型需要大量资源，推理工作负载变化显著，用户期望低延迟和高吞吐量。传统的负载均衡器和 API 网关虽然在传统 Web 服务中表现出色，但缺乏智能路由 AI 推理流量所需的感知能力。

**Kthena Router** 正面应对这些挑战。它是一个 Kubernetes 原生的独立推理Router，专为 LLM 服务工作负载而设计。与通用代理或负载均衡器不同，Kthena Router 具有模型感知能力，根据推理引擎的实时指标做出智能路由决策。这使得其能够实现复杂的流量管理策略，显著提高吞吐量、降低延迟并降低运营成本。

Kthena Router 能够与现有 API 网关基础设施无缝集成，同时提供专为 AI 工作负载设计的高级功能：

- **模型感知路由**：利用推理引擎（vLLM、SGLang、TGI）的实时指标做出智能路由决策
- **LoRA 感知负载均衡**：智能路由到已加载所需 LoRA Adapter 的 Pod，将数百毫秒的 Adapter Swap 延迟降低到接近零
- **高级调度算法**：包括Prefix Cache感知、KV Cache感知和公平性调度等
- **PD分离**：原生支持 xPyD（x-prefill/y-decode）部署模式

Kthena Router 作为独立二进制文件部署，极简依赖，确保轻量级运行和简单部署。它持续监控推理引擎指标，以获取有关模型状态的实时信息，包括当前加载的 LoRA Adapter、KV Cache 利用率、请求队列长度和延迟指标（TTFT/TPOT）。这种实时感知能力使 Router 能够做出传统负载均衡器根本无法实现的最优路由决策。

<!-- truncate -->

## 2. 架构

Kthena Router 实现了一个清晰的模块化架构，专为性能和可扩展性而设计。该系统由几个核心组件协同工作，提供智能请求路由。

![Kthena Router 架构](../../docs/assets/diagrams/kthena-router-arch.svg)

### 2.1 核心组件概述

**Router**：负责接收、处理和转发请求的核心执行框架。它协调所有其他组件之间的交互，并维护从初始接收到最终响应的请求生命周期。

**Listener**：管理 HTTP/HTTPS 监听器并处理指定端口上的传入流量。它为不同协议提供灵活的配置，并可以绑定到多个地址以服务各种类型的请求。监听器确保高效的连接处理，并支持流式和非流式请求模式。

**Controller**：一个 Kubernetes 原生组件，用于同步和处理 Pod 和自定义资源（CR），例如 `ModelRoute` 和 `ModelServer`。Controller监视集群中的变化，并相应地更新Router的内部状态，确保路由决策始终基于当前的集群拓扑。

**Filters**：包含两个关键子模块，在请求到达后端之前处理请求：

- **Auth**：处理流量认证和授权，支持 API Key、JWT
- **RateLimit**：管理全面的速率限制策略，包括输入token和输出token限制

**Backend**：提供访问各种推理引擎的抽象层。它掩盖了不同框架（如 vLLM、SGLang 和 TGI）之间Metrics接口访问方法和Metrics命名约定的差异，向调度器提供统一的接口。

**Metrics Fetcher**：持续从模型 Pod 上运行的推理引擎端点收集实时Metrics。它收集关键性能数据，包括：

- KV Cache利用率
- 当前加载的 LoRA Adapter
- 请求队列长度
- 延迟指标（TTFT，TPOT）

**Datastore**：一个统一的数据存储层，提供对 ModelServer 到 Pod 关联、Base Model/LoRA 配置和运行时Metrics的高效访问。它作为所有路由相关信息的中央存储库，并支持实时更新的回调。

**Scheduler**：Router的大脑，实现复杂的流量调度算法。它由一个调度框架和各种可插拔的调度算法插件组成。该框架集成并运行不同的调度插件，以过滤和评分与 `ModelServer` 对应的 Pod 集合，选择全局最优的 Pod 作为最终访问目标。

![Kthena Router 组件](../../docs/assets/diagrams/kthena-router-components.svg)

## 3. Router API

Kthena Router 的路由行为由两个关键的自定义资源定义（CRD）控制：**ModelServer** 和 **ModelRoute**。这些声明式 API 允许您使用熟悉的 Kubernetes 模式定义复杂的路由策略。

### 3.1 ModelRoute

**ModelRoute** 根据请求特征定义流量路由规则。它根据模型名称、LoRA Adapter、HTTP Header和其他条件确定哪些 ModelServer 应该处理请求。

关键字段包括：

- **ModelName**：要在传入请求中匹配的模型名称
- **LoRAAdapters**：此路由支持的 LoRA 适配器名称列表
- **Rules**：有序的路由规则列表，每个规则包含：
  - **ModelMatch**：匹配请求的条件（Header、URI 等）
  - **TargetModels**：要路由到的 ModelServer 列表，可选权重
- **RateLimit**：基于token的速率限制配置

有关 `ModelRoute` 的更多详细信息，请参阅 [定义](https://github.com/volcano-sh/kthena/blob/main/charts/kthena/charts/networking/crds/networking.serving.volcano.sh_modelroutes.yaml)。

### 3.2 ModelServer

**ModelServer** 定义推理服务实例及其访问策略。它标识运行模型的 Pod，指定正在使用的推理框架，并定义如何处理流量。

关键字段包括：

- **WorkloadSelector**：通过标签标识 Pod，并支持 PD（prefill-decode）组规范
- **Model**：指定服务器托管的Base Model名称
- **InferenceFramework**：指示推理引擎（vLLM、SGLang、TGI 等）
- **WorkloadPort**：定义推理服务监听的端口
- **TrafficPolicy**：配置超时、重试策略和其他流量处理行为
- **KVConnector**：为 PD 分离部署指定 KV Connector类型（HTTP、Nixl、LMCache、Mooncake）

有关 `ModelServer` 的更多详细信息，请参阅 [定义](https://github.com/volcano-sh/kthena/blob/main/charts/kthena/charts/networking/crds/networking.serving.volcano.sh_modelservers.yaml)。

### 3.3 示例：基于HTTP Header的多模型路由

对于分层服务产品，根据头将用户路由到不同大小的模型：

```yaml
apiVersion: networking.serving.volcano.sh/v1alpha1
kind: ModelRoute
metadata:
  name: deepseek-multi-models
  namespace: default
spec:
  modelName: "deepseek-multi-models"
  rules:
  - name: "premium"
    modelMatch:
      headers:
        user-type:
          exact: premium
    targetModels:
    - modelServerName: "deepseek-r1-7b"
  - name: "default"
    targetModels:
    - modelServerName: "deepseek-r1-1-5b"
---
apiVersion: networking.serving.volcano.sh/v1alpha1
kind: ModelServer
metadata:
  name: deepseek-r1-7b
  namespace: default
spec:
  workloadSelector:
    matchLabels:
      app: deepseek-r1-7b
  workloadPort:
    port: 8000
  model: "deepseek-ai/DeepSeek-R1-Distill-Qwen-7B"
  inferenceEngine: "vLLM"
  trafficPolicy:
    timeout: 10s
---
apiVersion: networking.serving.volcano.sh/v1alpha1
kind: ModelServer
metadata:
  name: deepseek-r1-1-5b
  namespace: default
spec:
  workloadSelector:
    matchLabels:
      app: deepseek-r1-1-5b
  workloadPort:
    port: 8000
  model: "deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B"
  inferenceEngine: "vLLM"
  trafficPolicy:
    timeout: 10s
```

**流量处理过程**：

1. 请求到达，模型名称为 "deepseek-multi-models"
2. Router检查HTTP Header中是否存在 `user-type: premium`
3. 高级用户 → 路由到更大的 7B 模型以获得更好的质量
4. 普通用户 → 路由到更小的 1.5B 模型以提高成本效率

测试高级路由：

```bash
curl http://$ROUTER_IP/v1/completions \
    -H "Content-Type: application/json" \
    -H "user-type: premium" \
    -d '{"model": "deepseek-multi-models", "prompt": "Explain quantum computing"}'
```

这个示例演示了 `ModelRoute` 和 `ModelServer` CRD 如何通过标准 Kubernetes API 提供对复杂路由策略的灵活声明式控制。

## 4. 核心功能

### 4.1 智能调度插件

真正将 Kthena Router 与传统负载均衡器区分开的是其一套模型感知调度插件。这些插件利用实时推理引擎指标做出智能路由决策，显著提高性能。

#### 4.1.1 前缀缓存感知调度

现代推理引擎（如 vLLM 和 SGLang）实现前缀缓存，其中常用的提示前缀被缓存以避免冗余计算。前缀缓存感知插件通过将具有相似前缀的请求路由到相同的 Pod 来最大化缓存命中率。

**工作原理**：

- 从传入请求中提取提示前缀
- 维护前缀到已处理它们的 Pod 的映射
- 将具有匹配前缀的新请求路由到可能已缓存 KV 状态的 Pod
- 显著减少重复或相似提示的首token时间（TTFT）

#### 4.1.2 KV 缓存感知调度

KV 缓存感知插件监控每个 Pod 的 KV 缓存利用率，并将请求路由到具有可用缓存容量的 Pod。这可以防止缓存抖动并提高整体吞吐量。

**工作原理**：

- Metrics Fetcher 持续轮询推理引擎 Pod 上的 `/metrics` 端点
- 提取 KV 缓存使用百分比（例如，来自 vLLM 的 `vllm:kv_cache_usage_perc` 指标）
- 根据可用缓存容量对 Pod 评分
- 将新请求路由到具有足够可用缓存空间的 Pod

#### 4.1.3 LoRA 亲和性调度

LoRA（低秩适应）适配器能够在不重新部署基础模型的情况下实现微调的模型行为。然而，加载和卸载适配器会引入延迟。LoRA 亲和性插件最小化了这种开销。

**工作原理**：

- 跟踪每个 Pod 上当前加载的 LoRA 适配器
- 将需要特定 LoRA 的请求路由到已加载它的 Pod
- 如果没有 Pod 已缓存适配器，则回退到具有可用适配器槽的 Pod
- 将适配器交换延迟从数百毫秒减少到接近零

#### 4.1.4 最小延迟调度

最小延迟插件根据实时延迟指标将请求路由到最快的可用 Pod。

**考虑的指标**：

- **TTFT（首token时间）**：对流式响应和用户感知延迟很重要
- **TPOT（每输出token时间）**：对整体生成速度至关重要

#### 4.1.5 最少请求调度

最少请求插件考虑等待处理的请求数和正在运行的请求数，以路由到最不繁忙的 Pod。

**工作原理**：

- 监控推理引擎的 `num_requests_running` 和 `num_requests_waiting` 指标
- 计算每个 Pod 的总待处理工作
- 将新请求路由到最不繁忙的 Pod
- 防止热点并确保均匀的负载分布

#### 4.1.6 插件配置

这些插件通过调度器框架协同工作。您可以通过Router配置来配置启用哪些插件及其相对权重。

调度器框架按顺序运行启用的插件：

1. **过滤**：插件消除不合适的 Pod（例如，缓存不足、错误的 LoRA）
2. **评分**：插件根据其条件对剩余 Pod 评分
3. **选择**：为请求选择得分最高的 Pod

这种可组合的架构允许您根据特定的工作负载需求定制路由行为。

### 4.2 公平性调度

公平性调度确保基于token消耗历史在用户之间公平分配资源。

**工作原理**：

- 跟踪每个用户每个模型的累积token使用量（输入 + 输出）
- 分配与历史使用量成反比的请求优先级
- 将请求排队并按优先级顺序处理
- 防止任何单个用户垄断资源

**用例**：

- 具有共享基础设施的多租户平台
- 具有公平共享策略的研究集群
- 需要基于使用量的节流的 SLA 驱动系统

### 4.3 预填充-解码分离支持

对于高级部署模式，Kthena Router 原生支持预填充-解码分离（xPyD），其中计算密集型的预填充阶段与token生成解码阶段分离。

**工作原理**：

- 从 ModelServer CRD 识别 PD 组配置
- 将预填充请求路由到预填充优化的 Pod
- 通过可配置的连接器（HTTP、Redis 等）传输 KV 缓存状态
- 将解码请求路由到解码优化的 Pod
- 对客户端透明地协调两阶段过程

**优势**：

- 通过将工作负载特征与硬件匹配来优化硬件利用率
- 通过为每个阶段使用专用硬件来减少延迟
- 通过更好的资源分配提高成本效率

### 4.4 基于token的速率限制

Kthena Router 提供全面的速率限制功能，以保护您的推理基础设施免受过载，并确保跨用户的公平资源分配。

- **输入token限制**：控制每个用户或 API 密钥的传入提示token速率
- **输出token限制**：限制生成的token以管理计算成本
- **本地速率限制**：在每个Router实例基础上实施限制。
- **全局速率限制**：使用 Redis 等中央存储在所有Router实例之间实施共享限制。

### 4.5 可观测性

Kthena Router 提供为生产 LLM 服务设计的全面可观测性功能：

- **指标**：在 `/metrics` 端点公开详细指标，包括请求延迟、token消耗、调度器插件性能和速率限制统计信息
- **结构化访问日志**：以 JSON 或文本格式记录完整的请求生命周期，包括路由决策、时间分解和token跟踪
- **调试端点**：提供 `/debug/config_dump/*` API 来检查内部状态、ModelRoute/ModelServer 配置和实时 Pod 指标
- **标准集成**：与 Prometheus、Grafana、ELK 和其他可观测性堆栈无缝协作，用于监控、告警和故障排除

## 5. 性能

**Kthena Router** 中的 **ScorePlugin** 模块利用可配置的可插拔架构来实现推理请求的多维评分和智能路由。为了演示智能调度的影响，我们基于 **DeepSeek-R1-Distill-Qwen-7B** 模型构建了一个标准化的基准测试环境，以评估不同调度策略在长短系统提示场景下的性能。

实验结果表明，在**长系统提示场景**中，**KVCacheAware Plugin + Least Request Plugin** 组合实现了 **2.73 倍的吞吐量**，并将 **TTFT 延迟降低了 73.5%**，显著优化了整体推理服务性能，验证了缓存感知调度对大规模模型推理的核心价值。

### 5.1 实验设置

使用 **DeepSeek-R1-Distill-Qwen-7B** 模型构建了标准化的基准测试环境，以评估不同调度策略的性能。

**表 1：实验环境配置**

| 参数                   | 值                                      |
| :--------------------- | :-------------------------------------- |
| 模型                   | deepseek-ai/DeepSeek-R1-Distill-Qwen-7B |
| 块大小                 | 128                                     |
| 最大模型长度           | 32,768                                  |
| 最大批次token数         | 65,536                                  |
| 副本数                 | 3                                       |
| GPU 内存利用率         | 0.9                                     |
| 最大序列数             | 256                                     |
| 数据集                 | generated-shared-prefix                 |
| 请求组                 | 256                                     |
| 每组请求数             | 32                                      |
| 请求速率               | 800 req/s                               |
| 最大并发               | 300                                     |

### 5.2 长系统提示场景（4096 token）

**表 2：性能指标 - 长提示**

| 插件配置                     | 运行次数 | 成功率 (%) | 吞吐量 (req/s) | 延迟 (s) | TTFT (s) |
| :--------------------------- | :------: | :--------: | :------------: | :------: | :------: |
| Least Request + KVCacheAware |    3     |   100.0    |   **32.22**    | **9.22** | **0.57** |
| Least Request + Prefix Cache |    3     |   100.0    |     23.87      |  12.47   |   0.83   |
| Random                       |    3     |   100.0    |     11.81      |  25.23   |   2.15   |
| Least Request                |    3     |   100.0    |      9.86      |  30.13   |  12.46   |
| GPU Usage                    |    3     |   100.0    |      9.56      |  30.92   |  13.14   |
| Least Latency                |    3     |   100.0    |      9.47      |  31.44   |  11.07   |

## 6. 结论

Kthena Router 代表了 LLM 服务基础设施的重大飞跃。通过超越简单的负载均衡，实现模型感知、指标驱动的路由，它释放了以前无法实现的显著性能改进和成本节省。

它是开源的，现在就可以使用。[文档](https://volcano-sh.github.io/kthena/)提供了安装、配置和部署的全面指南。[示例目录](https://github.com/volcano-sh/kthena/tree/main/examples/kthena-router)包含用于常见场景的即用型配置。

无论您是运行单个模型还是管理复杂的多租户 LLM 平台，Kthena Router 都提供了最大化性能、最小化成本和提供卓越用户体验所需的智能路由功能。
