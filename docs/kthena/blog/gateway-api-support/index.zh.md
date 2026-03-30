---
slug: gateway-api-support
title: Kthena Router 支持 Gateway API 与Inference Extension
authors: [YaoZengzeng]
tags: []
---

# Kthena Router 支持 Gateway API 与Inference Extension

## 简介

随着 Kubernetes 成为部署 AI/ML 工作负载的事实标准，对标准化、可互操作流量管理 API 的需求日益迫切。[Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/) 相较于传统的 Ingress API 是一次重大演进，它提供了更具表达力、面向角色且高度可扩展的模型，用于管理 Kubernetes 集群中的南北向流量。

在 Gateway API 的基础上，[Gateway API Inference Extension](https://gateway-api-inference-extension.sigs.k8s.io/) 引入了专为 AI/ML 推理工作负载设计的资源和能力。该扩展标准化了推理服务通过网关实现暴露和路由的方式，实现了不同网关提供商之间的无缝集成。

Kthena Router 现已同时支持 Gateway API 和 Gateway API Inference Extension，在保持与行业标准兼容的同时，为用户提供灵活的路由选项。本文将介绍这些 API 的重要性、如何启用它们，并提供实际使用示例。

<!-- truncate -->

## 什么是 Gateway API 和 Gateway API Inference Extension？

Gateway API 是 Kubernetes 项目中用于管理服务网络的标准化、面向角色的 API。它将关注点分离为不同的角色（基础设施提供商、集群运营者和应用开发者），并支持跨命名空间路由、多协议和流量拆分等高级路由能力。Gateway API Inference Extension在 Gateway API 基础上，为 AI/ML 工作负载提供推理专属能力。它引入了 InferencePool 和 InferenceObjective 等专用资源，支持模型感知路由和 OpenAI API 兼容性，从而实现推理服务的标准化暴露与路由。

## 为什么要支持 Gateway API 和Inference Extension？

Kthena Router 支持这些 API 有以下几个重要原因：

### 1. 解决全局 ModelName 冲突

在传统路由配置中，`ModelRoute` 资源中的 `modelName` 字段是全局的。当多个 `ModelRoute` 资源使用相同的 `modelName` 时，会发生冲突，导致未定义的路由行为。这一限制在多租户环境中尤为突出，不同团队或应用可能希望将相同的模型名称用于不同目的。

Gateway API 通过引入 **Gateway** 资源的概念解决了这一问题，每个 Gateway 定义了独立的路由空间。每个 Gateway 可以监听不同端口，绑定到不同 Gateway 的 ModelRoute 完全隔离，即使它们共享相同的 `modelName`。这一机制支持：

- **多租户隔离**：不同团队可以使用相同的模型名称而不产生冲突
- **环境隔离**：为开发、测试和生产环境分别配置独立的路由
- **基于端口的路由**：不同应用可以通过不同端口访问不同后端

### 2. 行业标准兼容性

Gateway API 正在成为 Kubernetes 服务网络的行业标准。通过支持 Gateway API，Kthena Router：

- **提升互操作性**：与其他 Gateway API 兼容的工具和基础设施无缝协作
- **降低厂商锁定**：用户可以更轻松地在不同网关实现之间迁移
- **利用生态系统**：从更广泛的 Gateway API 社区和工具链中受益

### 3. 支持 Gateway API Inference Extension

Gateway API Inference Extension提供了暴露 AI/ML 推理服务的标准化方式。通过支持该扩展，Kthena Router：

- **支持标准化推理路由**：与 InferencePool 和 InferenceObjective 资源协同工作
- **促进多网关部署**：可与使用相同 API 的其他网关实现并行工作

### 4. 灵活的部署选项

借助 Gateway API 支持，用户可以在以下方案中选择：

- **原生 ModelRoute/ModelServer**：Kthena 的自定义 CRD，提供 PD 拆分、加权路由和复杂调度算法等高级特性
- **Gateway API + Inference Extension**：标准 Kubernetes API，提供与其他网关实现的互操作性和兼容性

这种灵活性使用户能够根据自身需求和基础设施约束选择最合适的方案。

## 启用 Gateway API 支持

### 前提条件

在启用 Gateway API 支持之前，请确保：

- 已安装 Kthena 的 Kubernetes 集群（参见[安装指南](/docs/getting-started/installation)）
- 对 Kubernetes Gateway API 概念有基本了解
- 已配置 `kubectl` 以访问集群

### 配置

在部署 Kthena Router 时，通过设置 `--enable-gateway-api=true` 参数来启用 Gateway API 支持：

```bash
# 在 Helm 安装时配置
helm install kthena \
  --set networking.kthenaRouter.gatewayAPI.enabled=true \
  --version v0.2.0 \
  oci://ghcr.io/volcano-sh/charts/kthena
```

或在已部署的 Kthena Router 中修改配置：

```bash
kubectl edit deployment kthena-router -n kthena-system
```

确保容器参数中包含 `--enable-gateway-api=true`。

### 默认 Gateway

启用 Gateway API 支持后，Kthena Router 会自动创建一个具有以下特性的默认 Gateway：

- **名称**：`default`
- **命名空间**：与 Kthena Router 所在命名空间相同（通常为 `kthena-system`）
- **GatewayClass**：`kthena-router`
- **监听端口**：Kthena Router 的默认服务端口（默认为 8080）
- **协议**：HTTP

查看默认 Gateway：

```bash
kubectl get gateway

# 示例输出：
# NAME      CLASS           ADDRESS   PROGRAMMED   AGE
# default   kthena-router             True         5m
```

## 将 Gateway API 与原生 ModelRoute/ModelServer 结合使用

本示例演示如何将 Gateway API 与 Kthena 原生的 `ModelRoute` 和 `ModelServer` CRD 结合使用，从而解决 modelName 冲突问题。

### 步骤 1：部署模拟模型服务器

部署模拟的 LLM 服务及其对应的 ModelServer 资源：

```bash
# 部署 DeepSeek 1.5B 模拟服务
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/main/examples/kthena-router/LLM-Mock-ds1.5b.yaml

# 部署 DeepSeek 7B 模拟服务
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/main/examples/kthena-router/LLM-Mock-ds7b.yaml

# 为 DeepSeek 1.5B 创建 ModelServer
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/main/examples/kthena-router/ModelServer-ds1.5b.yaml

# 为 DeepSeek 7B 创建 ModelServer
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/main/examples/kthena-router/ModelServer-ds7b.yaml
```

等待 Pod 就绪：

```bash
kubectl wait --for=condition=ready pod -l app=deepseek-r1-1-5b --timeout=300s
kubectl wait --for=condition=ready pod -l app=deepseek-r1-7b --timeout=300s
```

### 步骤 2：创建新的 Gateway

创建并应用一个监听不同端口的新 Gateway：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: kthena-gateway-8081
  namespace: default
spec:
  gatewayClassName: kthena-router
  listeners:
  - name: http
    port: 8081  # 使用不同端口
    protocol: HTTP
EOF

# 验证 Gateway 状态
kubectl get gateway kthena-gateway-8081 -n default
```

**重要说明**：新创建的 Gateway 监听端口 8081，但需要手动配置 Kthena Router 的 Service 以暴露该端口：

```bash
# 编辑 kthena-router Service
kubectl edit service kthena-router -n kthena-system
```

在 `spec.ports` 中添加新端口：

```yaml
spec:
  ports:
  - name: http
    port: 80
    targetPort: 8080
    protocol: TCP
  - name: http-81  # 添加新端口
    port: 81
    targetPort: 8081
    protocol: TCP
```

### 步骤 3：创建绑定到不同 Gateway 的 ModelRoute

创建并应用绑定到默认 Gateway 的 ModelRoute：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: networking.serving.volcano.sh/v1alpha1
kind: ModelRoute
metadata:
  name: deepseek-default-route
  namespace: default
spec:
  modelName: "deepseek-r1"
  parentRefs:
  - name: "default"      # 绑定到默认 Gateway
    namespace: "kthena-system"
    kind: "Gateway"
  rules:
  - name: "default"
    targetModels:
    - modelServerName: "deepseek-r1-1-5b"  # 后端 ModelServer
EOF
```

创建并应用另一个使用**相同 modelName** 但绑定到新 Gateway 的 ModelRoute：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: networking.serving.volcano.sh/v1alpha1
kind: ModelRoute
metadata:
  name: deepseek-route-8081
  namespace: default
spec:
  modelName: "deepseek-r1"  # 与默认 Gateway 的 ModelRoute 使用相同 modelName
  parentRefs:
  - name: "kthena-gateway-8081"  # 绑定到新 Gateway
    namespace: "default"
    kind: "Gateway"
  rules:
  - name: "default"
    targetModels:
    - modelServerName: "deepseek-r1-7b"  # 使用不同后端
EOF
```

**注意**：启用 Gateway API 后，`parentRefs` 字段为必填项。未设置 `parentRefs` 的 ModelRoute 将被忽略，不会路由任何流量。

### 步骤 4：验证配置

现在您拥有两个独立的路由配置：

1. **默认 Gateway（端口 8080）**
   - ModelRoute：`deepseek-default-route`
   - ModelName：`deepseek-r1`
   - 后端：`deepseek-r1-1-5b`（DeepSeek-R1-Distill-Qwen-1.5B）

2. **新 Gateway（端口 8081）**
   - ModelRoute：`deepseek-route-8081`
   - ModelName：`deepseek-r1`（相同 modelName）
   - 后端：`deepseek-r1-7b`（DeepSeek-R1-Distill-Qwen-7B）

测试默认 Gateway（端口 8080）：

```bash
# 获取 kthena-router 的 IP 或主机名
ROUTER_IP=$(kubectl get service kthena-router -n kthena-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# 如果 LoadBalancer 不可用，使用 NodePort 或端口转发
# kubectl port-forward -n kthena-system service/kthena-router 80:80 81:81

# 测试默认端口
curl http://${ROUTER_IP}:80/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-r1",
    "prompt": "What is Kubernetes?",
    "max_tokens": 100,
    "temperature": 0
  }'

# 预期来自 deepseek-r1-1-5b 的输出：
# {"choices":[{"finish_reason":"length","index":0,"logprobs":null,"text":"This is simulated message from deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B!"}],...}
```

测试新 Gateway（端口 8081）：

```bash
# 测试端口 81
curl http://${ROUTER_IP}:81/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-r1",
    "prompt": "What is Kubernetes?",
    "max_tokens": 100,
    "temperature": 0
  }'

# 预期来自 deepseek-r1-7b 的输出：
# {"choices":[{"finish_reason":"length","index":0,"logprobs":null,"text":"This is simulated message from deepseek-ai/DeepSeek-R1-Distill-Qwen-7B!"}],...}
```

尽管两个请求使用相同的 `modelName`（`deepseek-r1`），但由于通过不同端口（对应不同 Gateway）访问，它们被路由到不同的后端模型服务。这展示了 Gateway API 如何解决全局 modelName 冲突问题。

## 将 Gateway API 与Inference Extension结合使用

本示例演示如何将 Gateway API Inference Extension与 Kthena Router 结合使用，以提供标准化的推理服务暴露和路由方式。

### 步骤 1：安装Inference Extension CRD

在集群中安装 Gateway API Inference Extension CRD：

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/releases/latest/download/manifests.yaml
```

### 步骤 2：部署示例模型服务器

部署一个模型作为 Gateway Inference Extension的后端。请参考[快速开始](/docs/getting-started/quick-start)指南，在 `default` 命名空间中部署模型，并确保其处于 `Active` 状态。

部署完成后，查看模型 Pod 的标签：

```bash
# 获取模型 Pod 及其标签
kubectl get pods -l workload.serving.volcano.sh/managed-by=workload.serving.volcano.sh --show-labels

# 示例输出中包含以下标签：
# modelserving.volcano.sh/name=demo-backend1
# modelserving.volcano.sh/group-name=demo-backend1-0
# modelserving.volcano.sh/role=leader
# workload.serving.volcano.sh/model-name=demo
# workload.serving.volcano.sh/backend-name=backend1
# workload.serving.volcano.sh/managed-by=workload.serving.volcano.sh
```

### 步骤 3：部署 InferencePool

Kthena Router 原生支持 Gateway Inference Extension，无需 Endpoint Picker 扩展。创建一个选取 Kthena 模型端点的 InferencePool 资源：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: inference.networking.k8s.io/v1
kind: InferencePool
metadata:
  name: kthena-demo
spec:
  targetPorts:
    - number: 8000  # 根据您的模型服务端口进行调整
  selector:
    matchLabels:
      workload.serving.volcano.sh/model-name: demo
  # Kthena Router 原生支持 Gateway Inference Extension，无需 Endpoint Picker 扩展。
  # 此处仅作 API 校验的占位符。
  endpointPickerRef:
    name: kthena-demo
    port:
      number: 8000
EOF
```

### 步骤 4：在 Kthena Router 中启用 Gateway API Inference Extension

在 Kthena Router 部署中启用 Gateway API Inference Extension标志：

```bash
kubectl patch deployment kthena-router -n kthena-system --type='json' -p='[
  {
    "op": "add",
    "path": "/spec/template/spec/containers/0/args/-",
    "value": "--enable-gateway-api=true"
  },
  {
    "op": "add",
    "path": "/spec/template/spec/containers/0/args/-",
    "value": "--enable-gateway-api-inference-extension=true"
  }
]'
```

等待部署完成滚动更新：

```bash
kubectl rollout status deployment/kthena-router -n kthena-system
```

### 步骤 5：部署 Gateway 和 HTTPRoute

创建使用 `kthena-router` GatewayClass 的 Gateway 资源：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: inference-gateway
spec:
  gatewayClassName: kthena-router
  listeners:
  - name: http
    port: 8080
    protocol: HTTP
EOF
```

创建并应用将 Gateway 连接到 InferencePool 的 HTTPRoute 配置：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: kthena-demo-route
spec:
  parentRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    name: inference-gateway
  rules:
  - backendRefs:
    - group: inference.networking.k8s.io
      kind: InferencePool
      name: kthena-demo
    matches:
    - path:
        type: PathPrefix
        value: /
    timeouts:
      request: 300s
EOF
```

### 步骤 6：验证与测试

确认 Gateway 已分配 IP 地址并报告 `Programmed=True` 状态：

```bash
kubectl get gateway inference-gateway

# 预期输出：
# NAME                CLASS           ADDRESS         PROGRAMMED   AGE
# inference-gateway   kthena-router   <GATEWAY_IP>    True         30s
```

验证所有组件是否正确配置：

```bash
# 检查 Gateway 状态
kubectl get gateway inference-gateway -o yaml

# 检查 HTTPRoute 状态 - 应显示 Accepted=True 和 ResolvedRefs=True
kubectl get httproute kthena-demo-route -o yaml

# 检查 InferencePool 状态
kubectl get inferencepool kthena-demo -o yaml
```

通过网关测试推理：

```bash
# 获取 kthena-router 的 IP 或主机名
ROUTER_IP=$(kubectl get service kthena-router -n kthena-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
# 如果 LoadBalancer 不可用，使用 NodePort 或端口转发
# kubectl port-forward -n kthena-system service/kthena-router 80:80

# 测试 completions 端点
curl http://${ROUTER_IP}:80/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen2.5-0.5B-Instruct",
    "prompt": "Write as if you were a critic: San Francisco",
    "max_tokens": 100,
    "temperature": 0
  }'
```

## 原生 ModelRoute/ModelServer：高级特性

Gateway API 和 Gateway API Inference Extension提供了标准化、可互操作的路由能力，而 Kthena 原生的 `ModelRoute` 和 `ModelServer` CRD 则提供了更多专为 AI/ML 推理工作负载设计的实验性高级特性：

### Prefill-Decode（PD）拆分

原生 ModelRoute/ModelServer 支持 PD 拆分，将计算密集型的预填充阶段与 token 生成的解码阶段分离。这一特性支持：

- **硬件优化**：为每个阶段使用专用硬件
- **更好的资源利用率**：将工作负载特性与硬件能力相匹配
- **降低延迟**：对每个阶段独立优化

### 基于权重的路由

原生 ModelRoute 支持跨多个 ModelServer 的复杂加权路由，实现：

- **流量拆分**：基于权重在后端之间分配流量
- **A/B 测试**：在不同模型版本之间逐步切换流量
- **基于容量的路由**：根据后端容量和可用性进行路由

这些高级特性使原生 ModelRoute/ModelServer 非常适合需要复杂流量管理和优化策略的生产环境。而 Gateway API 和 Gateway API Inference Extension则提供了更好的互操作性和与其他网关实现的兼容性，适用于多网关部署和标准化基础设施。

## 总结

Kthena Router 对 Gateway API 和 Gateway API Inference Extension的支持，为用户提供了在标准化与高级能力之间平衡的灵活路由选项。Gateway API 解决了 modelName 冲突问题并支持多租户隔离，而 Gateway API Inference Extension则提供了标准化的推理路由能力。

用户可以在以下方案中选择：

- **Gateway API + Inference Extension**：适用于跨不同网关实现的标准化、可互操作路由
- **原生 ModelRoute/ModelServer**：适用于需要 PD 拆分、加权路由和复杂调度算法等高级特性的场景

两种方案均完全受支持，并可在同一集群中同时使用，为不同用例和需求提供最大灵活性。

更多信息，请参阅 [Gateway API 支持指南](/docs/user-guide/gateway-api-support) 和 [Gateway Inference Extension支持指南](/docs/user-guide/gateway-inference-extension-support)。本文引用的所有示例文件均可在 [kthena/examples/kthena-router](https://github.com/volcano-sh/kthena/tree/main/examples/kthena-router) 目录中找到。
