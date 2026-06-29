# Action 2.5: 多级自动扩缩

> 时间线：6-12 个月 (2027 H1)
> 优先级：P1
> 依赖：Action 2.1 (自适应角色分配)

## 问题陈述

当前 RBGSA 支持 per-role HPA 适配，但缺少多层级扩缩协调。Grove 提供三层扩缩（PodClique、PodCliqueScalingGroup、PodCliqueSet），RBG 需要对应的多级扩缩能力。

## 技术方案

### 1. 三级扩缩层次

| 级别 | RBG 对应 | 行为 | Grove 对标 |
|------|---------|------|-----------|
| Role 级 | 单个 role 的 replicas | 单角色独立扩缩 | PodClique |
| Instance 级 | LeaderWorkerPattern 的一组 Pod | 多节点推理实例整体扩缩 | PodCliqueScalingGroup |
| RBG 级 | 整个 RBG 所有角色 | 全服务按比例扩缩 | PodCliqueSet |

### 2. API 设计

```yaml
spec:
  roles:
    - name: prefill
      replicas: 4
      scalingAdapter:
        enable: true
        # Role 级扩缩边界
        minReplicas: 2
        maxReplicas: 16
      leaderWorkerPattern:
        size: 4  # 4 GPU per instance

  # RBG 级扩缩策略
  scaling:
    # 全局扩缩约束
    totalGPUBudget: 64
    # 角色比例约束（与 CoordinatedPolicy 联动）
    roleRatioConstraints:
      - roles: ["prefill", "decode"]
        ratio: "1:2"  # 保持 P:D = 1:2 的比例
        tolerance: "20%"
```

### 3. 与 CoordinatedPolicy 联动

扩缩时自动检查 CoordinatedPolicy 的约束：
- maxSkew 保证角色间扩缩进度不会偏离太远
- 角色比例约束保证整体比例

## 行动清单

- [ ] 设计三级扩缩 API
- [ ] 实现 Instance 级扩缩逻辑
- [ ] 实现 RBG 级按比例扩缩
- [ ] 与 CoordinatedPolicy 联动
- [ ] 与 KEDA 集成验证
- [ ] 性能测试

## 成功标准

- 支持 Role/Instance/RBG 三级扩缩
- 扩缩时自动维持角色比例约束
- 与 HPA/KEDA 集成验证通过
