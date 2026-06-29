# Action 2.6: 跨角色协调滚动更新增强

> 时间线：6-12 个月 (2027 H1)
> 优先级：P1
> 依赖：CoordinatedPolicy (已有)

## 问题陈述

当前 CoordinatedPolicy 支持基础的 maxSkew 控制，但缺少以下能力：
- 角色更新顺序声明（先更新 Router，再更新 Prefill，最后更新 Decode）
- 金丝雀发布（先更新 1 个实例验证，再全量）
- 蓝绿切换（新旧版本共存，流量切换）

Grove 的 2026 路线图包含"资源优化的滚动更新"，RBG 需要领先。

## 技术方案

### 1. 更新顺序声明

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: CoordinatedPolicy
metadata:
  name: ordered-rollout
spec:
  policies:
    - name: sequential-update
      roles:
        - router    # 第 1 批更新
        - prefill   # 第 2 批更新
        - decode    # 第 3 批更新
      strategy:
        rollingUpdate:
          maxSkew: "1%"
          progression: Sequential  # Sequential | Parallel | Custom
          # Sequential: 前一角色更新完成后才开始下一角色
```

### 2. 金丝雀发布

```yaml
strategy:
  rollingUpdate:
    canary:
      enabled: true
      steps:
        - replicas: 1       # 先更新 1 个实例
          pause: {}          # 等待手动确认
        - replicas: "25%"   # 更新 25%
          pause:
            duration: 300s   # 等 5 分钟观察
        - replicas: "100%"  # 全量更新
```

### 3. 更新过程中的请求无损

- 利用 Pod Readiness Gates 确保新 Pod warmup 完成后才接收流量
- 旧 Pod 在优雅关闭期间完成 in-flight 请求
- 结合 Gateway API InferencePool 的权重调整

## 行动清单

- [ ] 设计更新顺序声明 API
- [ ] 实现 Sequential progression 逻辑
- [ ] 设计金丝雀发布 API
- [ ] 实现金丝雀分步更新逻辑
- [ ] 实现请求无损的优雅切换
- [ ] 编写文档和示例

## 成功标准

- 支持声明式更新顺序
- 金丝雀发布可分步执行
- 滚动更新期间请求成功率 > 99.9%
