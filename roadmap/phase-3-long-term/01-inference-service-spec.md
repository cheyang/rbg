# Action 3.1: 声明式推理服务规范标准化

> 时间线：12-24 个月 (2027 H2 - 2028 H1)
> 优先级：P1
> 依赖：CNCF/K8s SIG 治理 (Action 0.1), v1beta1 API (Action 0.2)

## 问题陈述

K8s 生态缺乏统一的推理服务规范。当前各项目各自定义 CRD：
- RBG: `RoleBasedGroup`
- Grove: `PodCliqueSet`
- KServe: `InferenceService` / `LLMInferenceService`
- llm-d: `InferenceServerConfig`

类似于 Gateway API 统一了网关规范，推理服务也需要标准化。RBG 的多角色声明式模型是最通用的抽象，有潜力成为标准。

## 目标

推动 RBG 的 API 成为 K8s 推理工作负载的标准规范，或至少成为标准规范的参考实现。

## 技术方案

### 1. API 稳定化路径

```
v1alpha2 (当前) → v1beta1 (Phase 0) → v1beta2 → v1 (stable)
```

### 2. 标准化推理工作负载原语

提炼出推理工作负载的通用原语：

| 原语 | 说明 | RBG 对应 |
|------|------|---------|
| **InferenceRole** | 推理拓扑中的一个角色 | `spec.roles[]` |
| **InferenceTopology** | 角色间的依赖和通信拓扑 | `dependencies` + 拓扑约束 |
| **InferenceScaling** | 角色级和服务级扩缩策略 | `scalingAdapter` + `CoordinatedPolicy` |
| **InferenceRuntime** | 引擎运行时配置注入 | `ClusterEngineRuntimeProfile` |

### 3. 与 K8s WG 协作

- 参与 WG-Device-Management：推理工作负载的设备需求表达
- 参与 WG-AI-Gateway：推理网关标准化
- 提议成立推理工作负载 subproject 或 WG

## 行动清单

- [ ] 发布 v1 stable API
- [ ] 撰写推理工作负载标准化 KEP
- [ ] 向 SIG-Apps 提议讨论推理工作负载标准
- [ ] 与 Grove/KServe/llm-d 社区协商共同标准
- [ ] 在 KubeCon 演讲推广标准化提案

## 成功标准

- v1 stable API 发布
- 推理工作负载标准化提案被 K8s 社区讨论
- 至少 1 个竞品表达采纳意愿
