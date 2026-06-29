# Action 3.3: Checkpoint/Restore 系统集成

> 时间线：12-24 个月 (2027 H2 - 2028 H1)
> 优先级：P2
> 依赖：Action 2.4 (冷启动加速)

## 问题陈述

系统层 C/R（Checkpoint/Restore）可实现 21x-40x 的冷启动加速（Dynamo Snapshot 21x、Modal 40x），远超应用层 Sleep 模式。RBG 作为工作负载控制器，应该在编排层集成 C/R 能力，让用户通过声明式 API 即可享受 C/R 加速。

## 技术方案

### 1. Checkpoint 生命周期管理

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: cr-enabled-inference
spec:
  roles:
    - name: decode
      replicas: 8
      lifecycle:
        checkpoint:
          enabled: true
          # 首次启动完成后自动创建 checkpoint
          autoCheckpoint: true
          # checkpoint 存储位置
          storage:
            persistentVolumeClaim:
              claimName: checkpoint-pvc
          # 恢复策略
          restorePolicy: "PreferCheckpoint"  # PreferCheckpoint | AlwaysColdStart
```

### 2. 与外部 C/R 方案集成

| C/R 方案 | 集成方式 | 适用场景 |
|----------|---------|---------|
| DynamoSnapshot | 通过 CRD 或 API 触发 | 已使用 Dynamo 的用户 |
| CRIU + cuda-checkpoint | 通过 containerd API | 通用 Linux 环境 |
| vLLM Sleep/Wake | 通过 Patio API（已有） | 应用层轻量方案 |

### 3. 启动路径决策树

```
RBG Controller 创建新 Pod:
  1. 检查 checkpoint 存储 → 有可用 checkpoint → 触发 restore
  2. 检查睡眠实例 → 有可用 → 触发 wake
  3. 检查 warmup 缓存 → 有可用 → 冷启动 + 挂载缓存
  4. 全新冷启动
```

## 行动清单

- [ ] 设计 `lifecycle.checkpoint` API
- [ ] 实现 checkpoint 存储管理（PVC 生命周期）
- [ ] 集成 DynamoSnapshot API
- [ ] 集成 CRIU 容器 checkpoint API
- [ ] 实现启动路径自动选择
- [ ] 性能基线：restore vs cold start

## 成功标准

- 支持声明式 checkpoint 管理
- 集成至少一个 C/R 方案
- Restore 启动时间 < 冷启动的 20%
