# Action 2.4: 冷启动加速集成

> 时间线：6-12 个月 (2027 H1)
> 优先级：P2
> 依赖：Action 1.5 (Warmup Phase 2)

## 问题陈述

推理服务冷启动是影响弹性伸缩效率的关键瓶颈。当前主流方案：
- 路线 B: vLLM Sleep/Wake（~3s 唤醒，需引擎支持）
- 路线 C: 引擎层优化（CUDA Graph 缓存等，已在 Warmup Phase 2 部分覆盖）

RBG 应在工作负载编排层集成这些冷启动优化能力。

## 技术方案

### 1. vLLM Sleep 模式集成

在 RBG 角色定义中支持 Sleep/Wake 生命周期管理：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: sleepable-inference
spec:
  roles:
    - name: decode
      replicas: 4
      lifecycle:
        sleepMode:
          enabled: true
          # 空闲多久后进入睡眠
          idleTimeout: 300s
          # 睡眠时释放 GPU 内存但保持进程
          strategy: "OffloadWeights"  # OffloadWeights | FullSleep
      scalingAdapter:
        enable: true
        # 缩容到 0 时保留睡眠实例
        scaleToZero:
          keepSleepingInstances: 2
```

### 2. 启动路径自动选择

RBG 控制器根据当前集群状态自动选择最优启动路径：

```
新 Pod 需要启动:
  ├── 节点上有睡眠中的引擎实例？ → Wake (~3s)
  ├── 有 warmup 缓存可用？ → 冷启动 + 缓存命中 (减少 50%+ warmup)
  └── 全新冷启动 → 完整启动流程 + 生成 warmup 缓存
```

### 3. 与自动扩缩的联动

扩容时优先唤醒睡眠实例，而非创建新 Pod：

```
HPA 触发扩容 → RBGSA 接收 → RBG Controller:
  1. 查找可用的睡眠实例 → 唤醒
  2. 无睡眠实例 → 查找 warmup 缓存 → 创建新 Pod 并挂载缓存
  3. 无缓存 → 创建新 Pod（完整冷启动）
```

## 行动清单

- [ ] 设计 `lifecycle.sleepMode` API 字段
- [ ] 实现 Patio 对 vLLM Sleep/Wake API 的调用
- [ ] 实现启动路径自动选择逻辑
- [ ] 与 RBGSA 自动扩缩联动
- [ ] 性能测试：唤醒 vs 冷启动 vs 缓存冷启动

## 成功标准

- 睡眠实例唤醒时间 < 5s
- 扩容时优先唤醒睡眠实例
- 端到端弹性响应时间从分钟级降至秒级
