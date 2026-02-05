---
slug: release-v0.3.0-zh
title: "Kthena v0.3.0 Release：生产级推理编排"
authors: [hzxuzhonghu, LiZhenCheng9527, YaoZengzeng]
tags: [release]
date: 2026-01-31
---

# Kthena v0.3.0 Release：Production-Ready的推理编排

Release日期：2026-01-31

## 摘要

v0.3.0 版本的Release标志着 Kthena 已经成为一个更加健壮且具有可扩展性的 AI 推理平台。此版本在 ModelServing、Router 和 ModelBooster 方面引入了重大增强。核心亮点包括：与 **LeaderWorkerSet** 的无缝集成、针对 PD 分离（Prefill-Decode Disaggregation）的高级**网络拓扑感知调度**，以及完善的 **Router 可观测性**框架。此外，本版本还带来了原生的 **ModelServing 版本控制**、对 **vLLM 数据并行部署**的支持，以及完整的 Router E2E测试套件，确保了生产环境的高稳定性和可靠性。

<!-- truncate -->

## 新功能

### 关键特性概览

- **LeaderWorkerSet 支持**：集成 **LeaderWorkerSet (LWS)** API，实现对分布式推理工作负载的高级管理。
- **角色级 Gang 调度与拓扑感知**：利用 Volcano 的 `subGroupPolicy` 新特性，实现精细化的**角色级 Gang 调度**和**网络拓扑感知**。
- **ModelServing 分区版本控制**：引入了基于 Revision 的原生 ModelServing 版本控制系统。
- **Router 可观测性与调试**：提供完善的 Router 可观测性文档和框架，并新增专用调试端口。
- **增强的滚动更新**：支持 `maxUnavailable` 参数，可调节更新速度以实现更快地发布。
- **插件支持**：为 ModelServing 提供灵活的插件架构，用于注入自定义配置逻辑。

### ModelServing 角色支持 LeaderWorkerSet

**背景与动机**：
分布式推理工作负载通常需要复杂的拓扑结构，其中一个 Leader Pod 管理多个 Worker Pod。手动配置这些关系容易出错。通过集成 Kubernetes LeaderWorkerSet (LWS) API，Kthena 简化了这些工作负载的部署和管理。

**核心能力**：

- **直接集成**：ModelServing 角色（Role）现在可以利用 LWS 自动管理 Leader-Worker 组。
- **简化拓扑**：降低了定义需要严格协调的分布式推理服务的复杂性。

**相关内容**：

- PR: [#609](https://github.com/volcano-sh/kthena/pull/609), [#683](https://github.com/volcano-sh/kthena/pull/683)
- 贡献者: [@zhiweideren](https://github.com/zhiweideren)

### 角色级 Gang 调度与拓扑感知

**背景与动机**：
在 Prefill-Decode (PD) 分离场景中，Prefill 实例和 Decode 实例之间的通信开销至关重要。确保这些实例在物理位置上更接近（例如在同一个交换机或机架下）可以显著提升性能。Kthena 现在通过 Volcano 的 `subGroupPolicy` 实现了对 Gang 调度和网络拓扑感知的**精细化、角色级控制**。

**核心能力**：

- **声明式拓扑策略**：直接在 `ModelServing` 规范中为整个 `ServingGroup`（`groupPolicy`）以及单个角色（`rolePolicy`）配置不同的网络拓扑限制。
- **自动 Pod 分组**：Controller自动为 Pod 打上 `modelserving.volcano.sh/role` 和 `modelserving.volcano.sh/role-id` 标签，使 Volcano 能够形成子组（subGroup）以进行精确的拓扑感知放置。
- **性能优化**：通过将相关任务部署在网络邻近的节点上，最小化角色间通信延迟并最大化带宽利用率，适用于高强度分布式推理任务。
- **角色级 Gang 调度**：`subGroupPolicy` 还强制执行**角色级 Gang 调度**，确保属于特定角色（例如所有 `prefill-0` Pod）的所有 Pod 作为一个原子单元共同调度。这保证了角色不会出现部分部署的情况，这对于分布式推理工作负载的正确性至关重要。

**注意**：此特性需要针对 `subGroupPolicy` 支持的 Volcano v1.14+ 版本。

**相关内容**：

- 提案: [Network Topology](https://github.com/volcano-sh/kthena/blob/main/docs/proposal/network-topology.md)
- PR: [#587](https://github.com/volcano-sh/kthena/pull/587)
- 贡献者: [@LiZhenCheng9527](https://github.com/LiZhenCheng9527)

### ModelServing 分区版本控制

**背景与动机**：
Kthena ModelServing 中的 `partition` 字段定义了滚动更新的边界，允许你对更新过程进行分区，以便在更新一部分 ServingGroup 的同时，让其他部分保持在旧版本。这主要用于金丝雀部署、分阶段发布以及在需要严格控制更新顺序的有状态应用中进行灰度更新。

**核心能力**：

- **Revision 追踪**：自动追踪 ModelServing 配置的变更。
- **分区保护**：支持基于分区的更新，确保发布过程中的服务连续性。
- **回滚**：轻松回退到之前稳定的 Revision。

**相关内容**：

- PR: [#590](https://github.com/volcano-sh/kthena/pull/590), [#653](https://github.com/volcano-sh/kthena/pull/653), [#671](https://github.com/volcano-sh/kthena/pull/671)
- 贡献者: [@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU), [@LiZhenCheng9527](https://github.com/LiZhenCheng9527)

### Router 可观测性与调试

**背景与动机**：
对推理 Router 的深度洞察对于诊断延迟问题和确保 SLA 合规性至关重要。新的可观测性框架和调试端口为运维人员提供了必要的工具。

**核心能力**：

- **调试端口**：专用端口（默认 `15000`），用于实时查看路由表和上游健康状况。
- **综合指标**：提供详细的文档和配置，用于监控请求延迟、吞吐量和错误率。
- **端到端测试**：鲁棒的 E2E 测试框架涵盖了大多数路由场景，确保了可靠性。

**相关内容**：

- PR & Issue: [#599](https://github.com/volcano-sh/kthena/pull/599), [#622](https://github.com/volcano-sh/kthena/pull/622), [#556](https://github.com/volcano-sh/kthena/issues/556)
- 贡献者: [@yashisrani](https://github.com/yashisrani), [@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU), [@YaoZengzeng](https://github.com/YaoZengzeng), [@katara-Jayprakash](https://github.com/katara-Jayprakash)

## 其他显著变化

### 功能与改进

- **[ModelServing]** 支持 ModelServing 滚动更新中的 `maxUnavailable` 参数 [#640](https://github.com/volcano-sh/kthena/pull/640) ([@LiZhenCheng9527](https://github.com/LiZhenCheng9527))
- **[ModelServing]** 实现扩展插件框架 [#588](https://github.com/volcano-sh/kthena/pull/588) ([@hzxuzhonghu](https://github.com/hzxuzhonghu))
- **[ModelServing]** 支持 vLLM 数据并行部署和专家并行（Expert Parallel）模式
- **[CLI]** 为 PD 分离使用场景添加模板 [#571](https://github.com/volcano-sh/kthena/issues/571) ([@huntersman](https://github.com/huntersman))
- **[Client]** 使客户端 QPS 和 Burst 可配置 [#686](https://github.com/volcano-sh/kthena/pull/686) ([@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU))
- **[Webhooks]** 在 Helm chart 中默认启用 ModelServing Webhook [#694](https://github.com/volcano-sh/kthena/pull/694) ([@VanderChen](https://github.com/VanderChen))
- **[Infra]** 通过 `hack/local-up-kthena.sh` 实现源码一键部署 [#613](https://github.com/volcano-sh/kthena/pull/613) ([@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU))

### Bug Fixes

- **[Router]** 修复 LeastRequest 评分中的除零错误 [#723](https://github.com/volcano-sh/kthena/pull/723) ([@WHOIM1205](https://github.com/WHOIM1205))
- **[Controller]** 修复角色状态转换为 Running 以恢复缩容保护 [#706](https://github.com/volcano-sh/kthena/pull/706) ([@WHOIM1205](https://github.com/WHOIM1205))
- **[Controller]** 修复当没有 Prefill Pod 可用时 PD 调度器发生的 panic [#714](https://github.com/volcano-sh/kthena/pull/714) ([@WHOIM1205](https://github.com/WHOIM1205))
- **[Controller]** 修复 ModelServing Controller重启后失败 Pod 的静默恢复问题 [#697](https://github.com/volcano-sh/kthena/pull/697) ([@WHOIM1205](https://github.com/WHOIM1205))
- **[Controller]** 修复 Headless Service 被删除后的恢复问题 [#598](https://github.com/volcano-sh/kthena/pull/598) ([@LiZhenCheng9527](https://github.com/LiZhenCheng9527))
- **[Controller]** 修复对 gangpolicy `minRoleReplicas` 的验证 [#699](https://github.com/volcano-sh/kthena/pull/699) ([@VanderChen](https://github.com/VanderChen))
- **[Controller]** 修复 ControllerRevision 数据扭曲问题 [#698](https://github.com/volcano-sh/kthena/pull/698) ([@VanderChen](https://github.com/VanderChen))
- **[Controller]** 修复 ModelServing Controller panic [#688](https://github.com/volcano-sh/kthena/pull/688) ([@LiZhenCheng9527](https://github.com/LiZhenCheng9527))
- **[Controller]** 修复 ModelServing 创建期间重启导致的 Pod 数量不匹配 [#689](https://github.com/volcano-sh/kthena/pull/689) ([@hzxuzhonghu](https://github.com/hzxuzhonghu))
- **[Controller]** 在 ModelServing 验证器中检查 `role.Name` [#684](https://github.com/volcano-sh/kthena/pull/684) ([@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU))
- **[Controller]** 修复角色删除未触发重建的 Bug [#629](https://github.com/volcano-sh/kthena/pull/629) ([@LiZhenCheng9527](https://github.com/LiZhenCheng9527))
- **[Router]** 保护由 ModelServing 创建的 Headless Service [#598](https://github.com/volcano-sh/kthena/pull/598) ([@LiZhenCheng9527](https://github.com/LiZhenCheng9527))

## 贡献者

感谢所有为此次Release做出贡献的伙伴：

[@hzxuzhonghu](https://github.com/hzxuzhonghu), [@LiZhenCheng9527](https://github.com/LiZhenCheng9527), [@YaoZengzeng](https://github.com/YaoZengzeng), [@git-malu](https://github.com/git-malu), [@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU), [@katara-Jayprakash](https://github.com/katara-Jayprakash), [@zhiweideren](https://github.com/zhiweideren), [@aaradhychinche-alt](https://github.com/aaradhychinche-alt), [@WHOIM1205](https://github.com/WHOIM1205), [@yashisrani](https://github.com/yashisrani), [@huntersman](https://github.com/huntersman), [@VanderChen](https://github.com/VanderChen)
