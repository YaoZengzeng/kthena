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

#### 4.1.1 LoRA 亲和性调度

LoRA（低秩适应）Adapter 能够在不重新部署 Base Model 的情况下实现微调的模型行为。然而，加载和卸载 Adapter 会引入延迟。LoRA 亲和性插件通过智能路由最小化这种开销。

**工作原理**：

- 持续跟踪每个 Pod 上当前加载的 LoRA Adapter 状态
- 将需要特定 LoRA 的请求优先路由到已加载该 Adapter 的 Pod
- 如果没有 Pod 已加载所需 Adapter，则选择具有可用 Adapter 槽的 Pod
- 通过缓存命中避免 Adapter 加载/卸载操作

**优势**：

- **消除 Adapter Swap 延迟**: 将数百毫秒的 Adapter 切换延迟降至接近零
- **提高响应速度**: 避免动态加载 Adapter 带来的额外等待时间
- **支持多 LoRA 场景**: 高效处理多个 LoRA Adapter 的混合工作负载

#### 4.1.2 Least Latency 调度

Least Latency 插件根据实时延迟指标将请求路由到响应最快的 Pod，优化用户体验和整体生成速度。

**工作原理**：

- 持续监控推理引擎的延迟指标
- 综合评估 TTFT（首 token 时间）和 TPOT（每输出 token 时间）
- 将请求路由到延迟最低的 Pod
- 动态适应 Pod 性能变化

**关键指标**：

- **TTFT（首 token 时间）**: 影响流式响应和用户感知延迟
- **TPOT（每输出 token 时间）**: 决定整体生成速度和吞吐量

#### 4.1.3 Least Request 调度

Least Request 插件基于请求队列状态进行负载均衡，将请求路由到最不繁忙的 Pod，确保均匀的负载分布。

**工作原理**：

- 监控推理引擎的 `num_requests_running` 和 `num_requests_waiting` 指标
- 计算每个 Pod 的总待处理工作负载（运行中 + 等待中）
- 将新请求路由到负载最低的 Pod
- 动态平衡各 Pod 之间的工作分配

**优势**：

- **防止热点**: 避免部分 Pod 过载而其他 Pod 空闲
- **均衡负载**: 确保所有 Pod 的利用率趋于一致
- **提高吞吐量**: 通过最优负载分配最大化整体处理能力

#### 4.1.4 GPU Usage 调度

GPU Usage 插件基于 GPU 缓存使用率进行负载均衡，将请求路由到缓存压力较小的 Pod，优化 GPU 资源利用率。

**工作原理**：

- 持续监控每个 Pod 的 GPU 缓存使用率指标
- 计算可用缓存容量：`score = (1.0 - GPU缓存使用率) × 100`
- 优先将请求路由到 GPU 缓存使用率较低的 Pod
- 避免因缓存耗尽导致的性能下降和请求失败

**优势**：

- **防止缓存溢出**: 避免将请求发送到缓存已满的 Pod
- **提高稳定性**: 减少因缓存不足导致的请求失败和重试
- **负载均衡**: 确保 GPU 缓存资源在所有 Pod 间均衡使用
- **提升吞吐量**: 保持各 Pod 在最优缓存利用率区间运行

#### 4.1.5 Prefix Cache 感知调度

现代推理引擎（如 vLLM 和 SGLang）实现 Prefix Cache 机制，将常用的 Prompt 前缀缓存起来以避免冗余计算。Prefix Cache 感知插件通过智能路由策略最大化前缀缓存的命中率，显著提升推理性能。

**核心机制**：

1. **滚动哈希生成**:
   - 将 Prompt 按固定大小（默认 64 字节）划分为块
   - 使用 xxHash 算法为每个块生成滚动哈希
   - 每个哈希值结合前一个块的哈希，形成依赖链
   - 哈希匹配时保证所有前置块也匹配，实现高效前缀识别

2. **内存缓存存储**:
   - 使用三层映射结构：模型 → 哈希 → Pod
   - 采用 LRU（最近最少使用）缓存策略管理内存
   - 自动清理过期条目，保持缓存效率
   - 默认缓存容量 50,000 条，返回 Top-5 最佳匹配

3. **智能评分算法**:
   - 根据前缀匹配长度对 Pod 评分
   - 评分公式：`score = (匹配块数 / 总块数) × 100`
   - 优先选择具有最长匹配前缀的 Pod
   - 提高缓存命中率和推理效率

**适用场景**：

- 多轮对话系统，用户在同一 session 中发送多个相关请求
- 具有固定 system prompt 的应用场景
- 问答系统中频繁使用相同上下文的查询

#### 4.1.6 KV Cache Aware 调度

KV Cache Aware 插件是一个高级调度插件,通过基于token级别的块匹配来智能路由请求,最大化 KV 缓存命中率。与简单监控缓存利用率不同,该插件实现了细粒度的内容感知调度,显著提升性能。

**核心机制**：

1. **Token 块哈希**:
   - 将传入的prompt分词后按固定大小(默认128个token)划分为块
   - 使用 SHA-256 为每个块生成标准化哈希值
   - 支持可配置的最大块数(默认128块)以平衡性能和准确性

2. **分布式缓存跟踪**:
   - 使用 Redis 维护全局的块到 Pod 映射
   - Redis key 格式: `matrix:kv:block:{model}@{hash}`
   - 每个 key 存储包含该块的 Pod 列表及其缓存时间戳
   - 通过 Redis Pipeline 批量查询实现高效的分布式协调

3. **智能评分算法**:
   - 查找每个token块在哪些 Pod 上已缓存
   - 从第一个块开始计算连续匹配长度
   - 优先选择具有最长连续匹配前缀的 Pod
   - 评分公式: `score = (匹配块数 / 总块数) × 100`

4. **Tokenizer 集成**:
   - 支持多种tokenizer backend(本地和远程 vLLM)
   - 自动处理不同模型的分词差异
   - 确保一致的token到块的映射

**优势**：

- **精确匹配**: 不仅考虑缓存容量,还考虑实际缓存的内容
- **避免冷启动**: 将请求路由到已经处理过相似prompt的 Pod
- **提升吞吐量**: 减少重复计算,在long system prompt场景下可实现 2.73 倍吞吐量提升
- **降低延迟**: TTFT 可降低 73.5%,显著改善用户体验

更多技术细节请参考 [KV Cache Aware 插件设计文档](https://github.com/volcano-sh/kthena/blob/main/docs/proposal/kvcache-aware-plugin-design.md)。

#### 4.1.7 插件配置

这些插件通过调度器框架协同工作。您可以通过Router配置来配置启用哪些插件及其相对权重。

调度器框架按顺序运行启用的插件：

1. **Filter**：插件过滤不合适的 Pod（例如，缓存不足、错误的 LoRA）
2. **Score**：插件根据其条件对剩余 Pod 评分
3. **Select**：为请求选择得分最高的 Pod

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

### 4.3 PD分离支持

对于高级部署模式，Kthena Router 原生支持PD分离（xPyD），其中计算密集型的Prefill阶段与token生成Decode阶段分离。

**工作原理**：

- 从 ModelServer CRD 识别 PD 组配置
- 将Prefill请求路由到Prefill优化的 Pod
- 通过可配置的连接器（HTTP、Nixl 等）传输 KV 缓存状态
- 将Decode请求路由到Decode优化的 Pod
- 对客户端透明地协调两阶段过程

**优势**：

- 通过将工作负载特征与硬件匹配来优化硬件利用率
- 通过为每个阶段使用专用硬件来减少延迟
- 通过更好的资源分配提高成本效率

### 4.4 基于token的RateLimit

Kthena Router 提供全面的RateLimit功能，以保护您的推理基础设施免受过载，并确保跨用户的公平资源分配。

- **Input token限制**：控制每个用户或 API 密钥的传入prompt token速率
- **Output token限制**：限制生成的token以管理计算成本
- **Local RateLimit**：在每个Router实例基础上实施限制。
- **Global RateLimit**：使用 Redis 等中央存储在所有Router实例之间实施共享限制。

### 4.5 可观测性

Kthena Router 提供为生产 LLM 服务设计的全面可观测性功能：

- **指标**：在 `/metrics` endpoint公开详细指标，包括请求延迟、token消耗、调度器插件性能和RateLimit统计信息
- **结构化访问日志**：以 JSON 或文本格式记录完整的请求生命周期，包括路由决策、用时分布和token跟踪
- **Debug endpoint**：提供 `/debug/config_dump/*` API 来检查内部状态、ModelRoute/ModelServer 配置和实时 Pod 等指标
- **标准集成**：与 Prometheus、Grafana、ELK 等可观测性堆栈无缝协作，用于监控、告警和故障排除

## 5. 性能

**Kthena Router** 中的 **ScorePlugin** 模块利用可配置的可插拔架构来实现推理请求的多维评分和智能路由。为了演示智能调度的影响，我们基于 **DeepSeek-R1-Distill-Qwen-7B** 模型构建了一个标准化的基准测试环境，以评估不同调度策略在长短system prompt场景下的性能。

实验结果表明，在**long system prompt场景**中，**KV Cache Aware Plugin + Least Request Plugin** 组合实现了 **2.73 倍的吞吐量**，并将 **TTFT 延迟降低了 73.5%**，显著优化了整体推理服务性能，验证了Cache感知调度对大规模模型推理的核心价值。

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

### 5.2 Long System Prompt Scenario（4096 token）

**表 2：性能指标 - Long System Prompt**

| 插件配置                     | 运行次数 | 成功率 (%) | 吞吐量 (req/s) | 延迟 (s) | TTFT (s) |
| :--------------------------- | :------: | :--------: | :------------: | :------: | :------: |
| Least Request + KV Cache Aware |    3     |   100.0    |   **32.22**    | **9.22** | **0.57** |
| Least Request + Prefix Cache |    3     |   100.0    |     23.87      |  12.47   |   0.83   |
| Random                       |    3     |   100.0    |     11.81      |  25.23   |   2.15   |
| Least Request                |    3     |   100.0    |      9.86      |  30.13   |  12.46   |
| GPU Usage                    |    3     |   100.0    |      9.56      |  30.92   |  13.14   |
| Least Latency                |    3     |   100.0    |      9.47      |  31.44   |  11.07   |

## 6. 结论

Kthena Router 代表了 LLM 服务基础设施的重大飞跃。通过超越简单的负载均衡，实现模型感知、指标驱动的路由，它释放了以前无法实现的显著性能改进和成本节省。

它是开源的，现在就可以使用。[文档](https://volcano-sh.github.io/kthena/)提供了安装、配置和部署的全面指南。[示例目录](https://github.com/volcano-sh/kthena/tree/main/examples/kthena-router)包含用于常见场景的即用型配置。

无论您是运行单个模型还是管理复杂的多租户 LLM 平台，Kthena Router 都提供了最大化性能、最小化成本和提供卓越用户体验所需的智能路由功能。
