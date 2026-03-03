# Kthena 部署 DeepSeek V4 Demo 脚本

## Demo 设计目标

通过 Kthena 的 ModelServing 和 Router 能力，展示云原生 AI 推理基础设施的核心优势：利用 ModelServing 实现 DeepSeek V4 的 PD 分离部署与多级弹性伸缩，并通过 Kthena Router 的智能调度插件框架，显著提升模型访问的吞吐量与降低访问时延。

---

## 阶段一：ModelServing PD 分离部署 DeepSeek V4

### 步骤 1：了解 ModelServing 三层架构

- **操作：**
  1. 打开 Kthena 文档，介绍 ModelServing 的三层架构：`ModelServing → ServingGroup → Role`。
  2. 说明各层的职责：
     - `ModelServing`：统一管理推理工作负载生命周期的核心组件；
     - `ServingGroup`：一组 Role 的集合，每个 ServingGroup 完整承载一次推理服务实例；
     - `Role`：ServingGroup 内的实际 Pod 工作负载组，在 PD 分离场景中分别承担 Prefill 和 Decode 角色。

- **可视化：**
  - 展示 ModelServing 架构图，标注三层层次关系及各层与 PD 分离部署的对应关系。

- **画外音：**
  > "传统 Kubernetes 的两层架构（如 Deployment、StatefulSet）难以表达多 Pod 协同完成一次推理任务的复杂场景。Kthena ModelServing 引入三层架构，天然支持 PD 分离、张量并行、流水线并行等多种推理部署模式。以 DeepSeek V4 为例，我们可以在同一个 ModelServing 对象中，分别定义 Prefill Role 和 Decode Role，将计算密集的 Prefill 阶段与逐 Token 生成的 Decode 阶段分离部署。"

---

### 步骤 2：部署 PD 分离的 DeepSeek V4

- **操作：**
  1. 展示以下 ModelServing YAML 配置并创建资源：

     ```yaml
     apiVersion: workload.serving.volcano.sh/v1alpha1
     kind: ModelServing
     metadata:
       name: deepseek-v4-pd
       namespace: default
     spec:
       schedulerName: volcano
       replicas: 2
       template:
         restartGracePeriodSeconds: 60
         gangPolicy:
           minRoleReplicas:
             prefill: 1
             decode: 1
         roles:
           - name: prefill
             replicas: 2
             # Prefill Role 配置（GPU/镜像等）
           - name: decode
             replicas: 4
             # Decode Role 配置（GPU/镜像等）
     ```

  2. 执行 `kubectl apply -f deepseek-v4-pd.yaml`。
  3. 执行 `kubectl get modelserving deepseek-v4-pd -n default` 观察状态。
  4. 执行 `kubectl get pods -n default -l modelserving=deepseek-v4-pd` 查看 Pod 分布。
  5. 展示自动创建的 PodGroup，说明 Gang 调度的工作方式。

     ```bash
     kubectl get podgroup -n default
     ```

- **可视化：**
  - 展示 Prefill Pod 和 Decode Pod 的实际分布情况。
  - 展示 PodGroup 中 `minTaskMember` 字段（包含 `prefill-0`、`decode-0` 等），说明 Gang 调度确保 "All or nothing" 的资源分配语义。

- **画外音：**
  > "通过一个简洁的 ModelServing YAML，Kthena 自动管理了 PD 分离部署所需的全部资源：Prefill Pod 专注于处理输入 Prompt 的计算密集任务，Decode Pod 专注于逐步生成输出 Token。同时，Kthena 基于 ModelServing 自动创建 PodGroup，借助 Volcano 的 Gang 调度策略，确保 Prefill 和 Decode Pod 满足最小数量要求后才会被调度，从而避免资源碎片化。"

---

## 阶段二：ModelServing 多级 Scale 功能

### 步骤 3：ServingGroup 级别缩放

- **操作：**
  1. 将 `deepseek-v4-pd` 的 `spec.replicas` 从 2 扩容至 4：

     ```bash
     kubectl patch modelserving deepseek-v4-pd -n default \
       --type=merge -p '{"spec":{"replicas":4}}'
     ```

  2. 观察 ServingGroup 实例数从 2 增加到 4 的过程：

     ```bash
     kubectl get modelserving deepseek-v4-pd -n default -w
     ```

  3. 说明扩缩容的顺序：按副本编号从大到小依次处理。

- **可视化：**
  - 展示 ServingGroup 实例从 2 增长至 4 的状态变化（类似 R-0 到 R-3 的阶段表格）。

- **画外音：**
  > "ModelServing 支持 ServingGroup 级别的水平伸缩。扩容时，Kthena 按照副本编号从大到小依次创建新的 ServingGroup 实例，每个实例的 Prefill 和 Decode Pod 都会经过 Gang 调度保障后才对外提供服务，确保扩容过程中的服务质量。"

---

### 步骤 4：Role 级别精细化缩放（PD 比例调整）

- **操作：**
  1. 当前场景：业务进入**长 Prompt、短输出**阶段，Prefill 计算压力增大。
  2. 将 Prefill Role 的副本数从 2 扩容至 4：

     ```bash
     kubectl patch modelserving deepseek-v4-pd -n default --type=json \
       -p '[{"op":"replace","path":"/spec/template/roles/0/replicas","value":4}]'
     ```

  3. 观察 Prefill Pod 数量增加，Decode Pod 数量不变：

     ```bash
     kubectl get pods -n default -l modelserving=deepseek-v4-pd
     ```

  4. 模拟**短 Prompt、长输出**场景，将 Decode Role 副本数从 4 扩容至 8：

     ```bash
     kubectl patch modelserving deepseek-v4-pd -n default --type=json \
       -p '[{"op":"replace","path":"/spec/template/roles/1/replicas","value":8}]'
     ```

- **可视化：**
  - 展示 Role 级别 Scale 流程图：从最高编号 ServingGroup 开始，依次调整每个 ServingGroup 内 Role 的 Pod 数量。

- **画外音：**
  > "在 PD 分离部署中，Prefill 和 Decode 的计算负载往往随着业务场景动态变化。Kthena ModelServing 支持 Role 级别的独立伸缩，用户可以根据实际负载模式灵活调整 Prefill 和 Decode 的比例——长 Prompt 场景下扩充 Prefill 副本，长输出场景下扩充 Decode 副本，实现资源的精准分配与高效利用。"

---

## 阶段三：Kthena Router 智能路由与调度插件框架

### 步骤 5：配置 ModelServer 和 ModelRoute

- **操作：**
  1. 创建 ModelServer，指向 DeepSeek V4 的 Decode Pod（PD 分离部署中，Router 对接 Decode 侧）：

     ```yaml
     apiVersion: networking.serving.volcano.sh/v1alpha1
     kind: ModelServer
     metadata:
       name: deepseek-v4
       namespace: default
     spec:
       workloadSelector:
         matchLabels:
           role: decode
           modelserving: deepseek-v4-pd
       workloadPort:
         port: 8000
       model: "deepseek-ai/DeepSeek-V4"
       inferenceEngine: "vLLM"
       kvConnector: "Nixl"
       trafficPolicy:
         timeout: 120s
     ```

  2. 创建 ModelRoute，将模型请求路由到上述 ModelServer：

     ```yaml
     apiVersion: networking.serving.volcano.sh/v1alpha1
     kind: ModelRoute
     metadata:
       name: deepseek-v4-route
       namespace: default
     spec:
       modelName: "deepseek-ai/DeepSeek-V4"
       rules:
       - name: "default"
         targetModels:
         - modelServerName: "deepseek-v4"
     ```

  3. 执行 `kubectl apply -f modelserver.yaml -f modelroute.yaml`。
  4. 通过 Router 发送推理请求验证连通性：

     ```bash
     curl http://$ROUTER_IP/v1/chat/completions \
       -H "Content-Type: application/json" \
       -d '{"model": "deepseek-ai/DeepSeek-V4", "messages": [{"role": "user", "content": "你好，请介绍一下自己"}]}'
     ```

- **可视化：**
  - 展示 Kthena Router 架构图，标注 Controller 监听 ModelRoute/ModelServer CRD 并动态更新路由表的流程。

- **画外音：**
  > "Kthena Router 通过 ModelServer 和 ModelRoute 两个 CRD 实现声明式的路由配置。ModelServer 描述推理服务实例，包括 Pod 选择器、推理框架类型与 KV 连接器；ModelRoute 定义请求匹配规则与路由目标。Router Controller 实时监听集群状态变化，确保路由决策始终基于最新的集群拓扑。"

---

### 步骤 6：调度插件框架与性能对比

- **操作：**
  1. **基准对比——随机调度**：
     - 配置 Router 使用 Random 调度插件，发起压力测试（800 req/s，长 Prompt 场景 4096 tokens）：

       ```bash
       # 运行基准测试（随机调度）
       python benchmark.py --scheduler random --prompt-len 4096 --rate 800
       ```

     - 记录结果：吞吐量 **~11.81 req/s**，TTFT **~2.15 s**。

  2. **优化配置——KV Cache Aware + Least Request 插件**：
     - 切换为 `KVCacheAware + LeastRequest` 组合插件配置，重新发起相同压力测试：

       ```bash
       # 运行优化测试（KV Cache Aware + Least Request）
       python benchmark.py --scheduler kvcache-leastrequest --prompt-len 4096 --rate 800
       ```

     - 记录结果：吞吐量 **~32.22 req/s**，TTFT **~0.57 s**。

  3. 展示对比数据表格：

     | 插件配置 | 吞吐量 (req/s) | 延迟 (s) | TTFT (s) |
     |---|---|---|---|
     | Least Request + KV Cache Aware | **32.22** | **9.22** | **0.57** |
     | Least Request + Prefix Cache Aware | 23.87 | 12.47 | 0.83 |
     | 随机调度 (Random) | 11.81 | 25.23 | 2.15 |
     | Least Request | 9.86 | 30.13 | 12.46 |

- **可视化：**
  - 展示 Kthena Router 调度框架图（Filter → Score → Select 三阶段流水线）。
  - 展示柱状图对比随机调度与智能调度的吞吐量和 TTFT 差异。

- **画外音：**
  > "Kthena Router 的核心竞争力在于其可插拔的智能调度框架。调度框架分为三个阶段：Filter 阶段过滤不合适的 Pod，Score 阶段对候选 Pod 进行多维评分，Select 阶段选择得分最高的目标 Pod。
  >
  > 在长 Prompt 场景（4096 tokens）下，Kthena Router 实测结果显示：与随机调度相比，启用 KV Cache Aware 与 Least Request 组合插件后，吞吐量提升 **2.73 倍**，TTFT 降低 **73.5%**。
  >
  > 这一显著提升来自于 KV Cache Aware 插件对推理引擎 KV 缓存利用率的实时感知——它持续拉取每个 Pod 的 `/metrics` 指标，将请求路由至缓存空间充足的 Pod，有效避免缓存抖动；而 Least Request 插件则确保请求均衡分发到负载最轻的 Pod，防止热点集中。两者协同作用，大幅提升了整体推理服务的性能表现。"

---

## Demo 总结

| 演示内容 | 核心能力 | 关键价值 |
|---|---|---|
| ModelServing PD 分离部署 | 三层架构 + Gang 调度 | 简化复杂推理部署，资源分配有保障 |
| ServingGroup 级别 Scale | 整体副本水平伸缩 | 快速响应容量变化，保障服务可用性 |
| Role 级别精细化 Scale | 独立调整 PD 比例 | 针对业务负载特征精准分配资源，降低成本 |
| Kthena Router 路由配置 | ModelServer + ModelRoute CRD | 声明式路由，动态感知集群拓扑变化 |
| 智能调度插件框架 | KV Cache Aware + Least Request | 吞吐量提升 2.73×，TTFT 降低 73.5% |
