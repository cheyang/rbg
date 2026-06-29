# Action 2.1: 自适应角色分配

> 时间线：6-12 个月 (2027 H1)
> 优先级：P1
> 依赖：Phase 1 完成

## 问题陈述

当前 RBG 的角色副本数是静态声明的。AMPD 论文证明，在多轮推理场景下，自适应调整 P/D 角色比例可提升 67.29% SLO 达成率。随着 Agentic 推理和多轮对话的普及，动态调整角色比例将成为必要能力。

## 技术方案

### 1. 角色比例策略

在 RBG 或 CoordinatedPolicy 中增加角色比例策略：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: CoordinatedPolicy
metadata:
  name: adaptive-pd-ratio
spec:
  policies:
    - name: pd-ratio-policy
      roles:
        - prefill
        - decode
      strategy:
        adaptiveRatio:
          enabled: true
          # 最小/最大比例约束
          minRatio:
            prefill: 1
            decode: 2
          maxRatio:
            prefill: 4
            decode: 8
          # 触发调整的指标
          metrics:
            - type: PrefillQueueDepth
              targetValue: 10
            - type: DecodeLatencyP99
              targetValue: 100ms
          # 调整间隔
          cooldownPeriod: 60s
```

### 2. 决策引擎

基于运行时指标（通过 Patio 采集）自动调整角色副本数：

```
Patio 指标 → RBG Controller 决策引擎 → 调整 role.replicas
                    ↓
        [Prefill 队列深了 → 增加 Prefill]
        [Decode 延迟高了 → 增加 Decode]
        [两者都空闲 → 按最小比例缩容]
```

### 3. 安全约束

- 总 GPU 数上限约束（不超过预算）
- 每次调整幅度限制（避免剧烈波动）
- 与 CoordinatedPolicy 的 maxSkew 联动

## 行动清单

- [ ] 研究 AMPD 论文的自适应算法
- [ ] 设计 adaptiveRatio API
- [ ] 实现基于 Patio 指标的决策引擎
- [ ] 实现角色比例调整的 reconcile 逻辑
- [ ] 与 CoordinatedPolicy 的协调更新联动
- [ ] 性能测试：固定比例 vs 自适应比例

## 成功标准

- P/D 角色比例可基于运行时指标自动调整
- 在多轮推理负载下 SLO 达成率提升 30%+
- 安全约束防止比例剧烈波动
