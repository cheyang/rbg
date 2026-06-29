# Action 1.4: llm-d 互补集成探索

> 时间线：3-6 个月 (2026 Q3-Q4)
> 优先级：P1
> 依赖：Action 1.2 (Gateway API 集成)

## 问题陈述

llm-d 拥有最广泛的产业联盟（Red Hat、Google Cloud、IBM、CoreWeave、NVIDIA），但其核心定位是**路由/缓存/FMA 层**（推理引擎之上、用户请求之下）。RBG 的定位是**工作负载编排层**（K8s 资源管理）。二者在架构上处于不同层次，存在互补可能性：

```
用户请求
   ↓
[llm-d: 路由 + KV 缓存 + 前缀匹配]  ← llm-d 的强项
   ↓
[Gateway API Inference Extension]     ← 标准化接口层
   ↓
[RBG: 工作负载编排 + 角色管理]        ← RBG 的强项
   ↓
[推理引擎 Pod (SGLang/vLLM)]
```

## 技术方案

### 1. 架构层次分析

| 层次 | 职责 | RBG 覆盖 | llm-d 覆盖 | 重叠度 |
|------|------|---------|-----------|--------|
| 请求路由 | 前缀缓存感知路由、KV 感知路由 | 无 | 核心 | 无 |
| KV 缓存管理 | 缓存卸载、分层存储 | 无 | 核心 | 无 |
| 模型启动 (FMA) | vLLM Sleep/Wake、Dual Pods | 无 | 核心 | 低 |
| 工作负载编排 | 多角色部署、协调扩缩、滚动更新 | 核心 | 无 | 无 |
| 调度 | Gang Scheduling、拓扑感知 | 核心 | 无 | 无 |
| 引擎运行时 | Sidecar 注入、LoRA 管理 | 核心 (Patio) | 无 | 无 |
| 自动扩缩 | HPA 适配 | RBGSA | 部分 | 低 |

**结论**：重叠度很低，互补性强。

### 2. 集成方案

#### 方案 A: RBG 管理 llm-d 组件的部署

RBG 将 llm-d 的路由组件作为一个角色管理：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: full-stack-inference
spec:
  roles:
    - name: llmd-router
      replicas: 2
      dependencies: ["prefill", "decode"]
      standalonePattern:
        template:
          spec:
            containers:
              - name: llmd-router
                image: llm-d/router:latest
                env:
                  - name: ROUTING_STRATEGY
                    value: "prefix-cache-aware"

    - name: prefill
      replicas: 4
      standalonePattern:
        template:
          spec:
            containers:
              - name: engine
                image: vllm:latest

    - name: decode
      replicas: 8
      dependencies: ["prefill"]
      standalonePattern:
        template:
          spec:
            containers:
              - name: engine
                image: vllm:latest
```

#### 方案 B: 通过 Gateway API 松耦合集成

RBG 管理推理引擎 Pod，llm-d 独立管理路由层，通过 Gateway API InferencePool 对接：

```
[llm-d Router] → [InferencePool (auto-created by RBG)] → [RBG Decode Pods]
```

#### 方案 C: Patio + llm-d 能力互补

Patio sidecar 专注引擎管理（LoRA、指标），llm-d 路由组件专注请求路由（前缀缓存），二者通过标准 API 通信。

### 3. 推荐路径

先实施方案 B（松耦合，低风险），同时探索方案 A（深度集成，高价值）。

## 行动清单

- [ ] 分析 llm-d 的 CRD 体系（InferenceServerConfig、LauncherConfig）
- [ ] 验证 llm-d router 可通过 InferencePool 接入 RBG 管理的 Pod
- [ ] 编写 RBG + llm-d 联合部署示例
- [ ] 联系 llm-d 社区讨论互操作性
- [ ] 评估 Patio 与 llm-d FMA 的功能边界
- [ ] 发布 "RBG + llm-d: Better Together" 技术博客

## 成功标准

- 验证 llm-d router 可通过 InferencePool 接入 RBG Pod
- 提供至少一个联合部署示例
- 与 llm-d 社区建立对话渠道
