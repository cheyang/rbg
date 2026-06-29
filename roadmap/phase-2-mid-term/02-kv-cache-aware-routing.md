# Action 2.2: KV 缓存感知路由

> 时间线：6-12 个月 (2027 H1)
> 优先级：P1
> 依赖：Action 1.2 (Gateway API), Action 1.4 (llm-d 集成探索)

## 问题陈述

llm-d 的前缀缓存感知路由在 Tesla 生产环境实现了 3x 吞吐提升。Dynamo 的 KV-aware Router 是其核心差异化能力。RBG 当前不提供路由层能力，需要决定是自建还是集成。

## 技术方案

### 路径 A: 集成 llm-d 路由组件（推荐）

通过 Gateway API InferencePool 集成 llm-d 的路由能力，避免重复建设：

```
请求 → llm-d Prefix-Cache Router → InferencePool → RBG Decode Pods
```

RBG 职责：管理推理引擎 Pod 生命周期，通过 Patio 暴露 KV Cache 状态指标。

### 路径 B: Patio 内置路由能力

在 Patio sidecar 中增加轻量路由能力：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: ClusterEngineRuntimeProfile
metadata:
  name: kv-aware-routing-profile
spec:
  containers:
    - name: patio
      image: patio:latest
      env:
        - name: ROUTING_MODE
          value: "kv-cache-aware"
        - name: KV_CACHE_REPORT_INTERVAL
          value: "5s"
```

Patio 向路由层暴露每个引擎实例的 KV Cache 状态：
- 缓存前缀列表
- 缓存命中率
- 当前队列深度
- 可用 KV Cache 容量

### 路径 C: 独立路由组件

开发独立的 RBG Router 组件作为一个角色部署。

## 推荐决策

先走路径 A（集成 llm-d），Patio 提供 KV Cache 状态暴露接口（路径 B 的一部分）。如果 llm-d 集成不可行，再回退到路径 C。

## 行动清单

- [ ] 在 Patio 中增加 KV Cache 状态暴露 API
- [ ] 验证 llm-d router 消费 Patio KV Cache 状态的可行性
- [ ] 实现端到端 KV 感知路由 demo
- [ ] 性能基准对比

## 成功标准

- 推理请求可基于 KV Cache 命中率路由到最优实例
- 在前缀复用场景下吞吐提升 50%+
