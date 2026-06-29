# Action 1.8: 滚动更新 Pod 复用优化

> 时间线：3-6 个月 (2026 Q3-Q4)
> 优先级：P0
> 依赖：无
> KEP：待编写

## 问题陈述

推理场景下，滚动更新（镜像升级等）导致 Pod 被删除重建的代价极高：

| 操作 | 代价 |
|------|------|
| 模型权重重新加载 | 分钟级（大模型 100GB+ 权重） |
| CUDA Graph 重新捕获 | 数十秒（DeepGEMM warmup 等） |
| KV Cache 丢失 | 在线请求延迟飙升 |
| NCCL 重新初始化 | 十秒级（多节点 TP 场景） |

当前 `RecreatePod` 更新策略下，即使 Pod 已经运行了目标镜像（例如扩容时创建的新版 Pod），只要其 revision hash 与 updateRevision 不匹配，就会被判定为"旧版本"而删除重建。

**典型场景**：

```
T0: RBG 有 4 replicas，运行 image:v1
T1: 用户同时修改 image → v2 AND replicas → 6
    → controller 创建 instances 4,5 使用 image:v2（updateRevision-A）
T2: 用户修改了一个无关 annotation（比如 monitoring label）
    → 新的 updateRevision-B 生成
    → instances 4,5 的 revision-A ≠ updateRevision-B
    → 被标记为更新目标，触发删除重建
    → 但实际上 instances 4,5 的镜像已经是 v2，annotation 也可以 in-place patch
```

## 核心思路：语义等价检测

不应仅依赖 revision hash 判断是否需要更新。应该增加**语义等价检测**——比较实际运行的 Pod spec 与目标 spec 的关键字段是否一致：

```
if instance.revision != updateRevision:
    if semanticallyEqual(instance.spec, updateSpec, criticalFields):
        # 关键字段一致 → 只需更新 revision label，不删除重建
        adoptInstance(instance, updateRevision)
    else:
        # 关键字段不同 → 走正常更新路径（in-place 或 recreate）
        applyTargetUpdate(instance, ...)
```

### 关键字段 vs 非关键字段

| 字段类型 | 示例 | 变更时是否需要重建 |
|----------|------|:---:|
| **关键字段** | container image, resource limits, volumeMounts | 可能需要（取决于 InPlaceIfPossible） |
| **非关键字段** | labels, annotations, env vars (non-downward API) | 不需要重建，可 in-place patch |
| **元数据字段** | revision hash label | 绝不需要重建 |

### 优化层次

**层次 1：Revision 采纳（Adoption）**

如果实例的实际 Pod spec 与 updateRevision 的 spec 完全一致（仅 revision label 不同），直接更新 revision label，零代价：

```go
// 在 applyTargetUpdate 之前增加检查
if isSpecSemanticallyEqual(target, updateSet) {
    // 只需要更新 revision label，不需要删除或 in-place update
    return adoptInstance(target, updateRevision)
}
```

**层次 2：增量 In-Place 优先**

如果仅非关键字段变更（labels、annotations、env），强制走 in-place patch 路径，即使配置了 `RecreatePod` 策略：

```go
// 在 applyTargetUpdate 中增加判断
diff := computeSpecDiff(target, updateSet)
if diff.onlyMetadataChanges() {
    // 元数据变更不需要 recreate，强制 in-place
    return inPlacePatchMetadata(target, updateRevision)
}
```

**层次 3：热迁移（长期）**

对于镜像变更场景，探索"热迁移"能力——新容器启动后从旧容器接管 GPU 状态，而非从零初始化。这与 Warmup Phase 2 和 Checkpoint/Restore 有交叉。

## 实现要点

### 需要修改的关键函数

1. **`buildUpdateTargets()`** — 增加语义等价过滤
   - 文件：`pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_control.go:703`
   - 当前逻辑：`if getInstanceRevision(replicas[idx]) == updateRev { continue }`
   - 新增逻辑：`if isSpecSemanticallyEqual(replicas[idx], updateSet) { adoptAndContinue }`

2. **`applyTargetUpdate()`** — 增加增量 in-place 优先判断
   - 文件：`pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_control.go:735`
   - 在 `inPlaceUpdateInstance` 之前增加 metadata-only 检测

3. **新增 `isSpecSemanticallyEqual()`** — 比较两个实例的 Pod template 关键字段
   - 比较 container images、resources、volumeMounts、command、args
   - 忽略 labels、annotations、revision hash

### 与现有机制的关系

| 现有机制 | 关系 |
|----------|------|
| `InPlaceIfPossible` 更新策略 | 互补——InPlace 处理 image 变更，本优化处理"无需变更"的场景 |
| `InPlaceOnly` 更新策略 | 互补——本优化可以在 InPlaceOnly 失败前提前判断是否真的需要更新 |
| `RecreatePod` 更新策略 | 核心优化点——当前 RecreatePod 无条件删除，本优化在删除前检查是否可跳过 |
| Warmup readiness gate | 本优化减少不必要的重建，从而减少不必要的 warmup |
| CoordinatedPolicy maxSkew | 本优化减少每次更新的 Pod 变更数量，降低跨角色 skew |

## 行动清单

- [ ] 分析 revision hash 计算逻辑，确定哪些字段参与 hash
- [ ] 设计 `isSpecSemanticallyEqual()` 的比较范围
- [ ] 实现层次 1（Revision Adoption）
- [ ] 实现层次 2（Metadata-only in-place patch）
- [ ] 单元测试覆盖各种 diff 场景
- [ ] E2E 测试：镜像不变只改 annotation → 验证 Pod 不重建
- [ ] 性能基线：对比优化前后的滚动更新时间
- [ ] 编写 KEP 设计文档

## 成功标准

- 镜像不变、仅 metadata 变更时，Pod 不被删除重建
- 扩容创建的新版 Pod 在后续滚动更新中不被重建
- 滚动更新总时间降低（减少不必要的重建次数）
