# Action 0.2: v1alpha2 → v1beta1 API 稳定化

> 时间线：0-3 个月 (2026 Q3)
> 优先级：P0 紧急
> 负责人：API 设计组

## 问题陈述

RBG 当前 API 为 `v1alpha2`，Grove 仍在 `v1alpha1`。API 稳定性是 RBG 当前最大的先发优势。尽快推出 `v1beta1` 可以：
1. 向用户传递"API 趋于稳定"的信号，降低采纳风险
2. 在 Grove 追赶之前建立 API 锁定效应
3. 为后续 CNCF/K8s SIG 申请提供成熟度证明

## 技术方案

### 1. API 审计与冻结

对 `workloads.x-k8s.io/v1alpha2` 中所有 CRD 进行审计：

| CRD | 当前状态 | v1beta1 准备度 |
|-----|---------|---------------|
| `RoleBasedGroup` | 核心 CRD，功能完整 | 高 — 需审计字段命名一致性 |
| `RoleBasedGroupScalingAdapter` | 扩缩适配器 | 高 — 接口简洁 |
| `CoordinatedPolicy` | v1alpha2 新增 | 中 — 需验证 maxSkew 语义 |
| `ClusterEngineRuntimeProfile` | 引擎运行时 | 中 — 需确认 updateStrategy 语义 |

### 2. 需审计的 API 设计点

- [ ] `RoleBasedGroup.spec.roles[].standalonePattern` / `leaderWorkerPattern` / `customComponentsPattern` — 模式命名是否最终确定
- [ ] `RoleBasedGroup.spec.roleTemplates` — 模板引用机制是否覆盖所有场景
- [ ] `engineRuntimes[].profileName` — 是否需要支持 namespace-scoped profile
- [ ] `rolloutStrategy.rollingUpdate.type` 中 `InPlaceIfPossible` / `InPlaceOnly` / `RecreatePod` 命名是否与 K8s 上游一致
- [ ] `scalingAdapter` 是否需要支持更多自动扩缩器（KEDA、KPA）的配置
- [ ] Annotation-based 配置（gang scheduling、exclusive topology）是否应该提升为 spec 字段
- [ ] Status 字段是否充分覆盖调试需求

### 3. 关键变更决策

**应在 v1beta1 前解决的问题**：

1. **Gang Scheduling 配置位置**：当前通过 annotation（`rbg.workloads.x-k8s.io/group-gang-scheduling`）配置。v1beta1 应考虑提升到 spec 层：
   ```yaml
   spec:
     scheduling:
       gangScheduling:
         enabled: true
         timeout: 120s
         backend: SchedulerPlugins  # or Volcano
   ```

2. **Exclusive Topology 配置位置**：同样通过 annotation 配置，应提升到 spec：
   ```yaml
   spec:
     scheduling:
       exclusiveTopology:
         topologyKey: "kubernetes.io/hostname"
   ```

3. **多引擎支持扩展点**：确保 `ClusterEngineRuntimeProfile` 不绑定特定引擎

### 4. Conversion Webhook 维护

- 保持 `v1alpha2` → `v1beta1` 的 conversion webhook
- 保持 `v1alpha1` → `v1alpha2` → `v1beta1` 的完整转换链
- 发布 v1beta1 后宣布 v1alpha1 废弃时间线

## 行动清单

- [ ] 完成所有 CRD 字段的命名和语义审计
- [ ] 决策：annotation-based 配置是否提升到 spec
- [ ] 决策：是否增加 `spec.scheduling` 顶层字段
- [ ] 编写 v1alpha2 → v1beta1 migration guide
- [ ] 实现 conversion webhook
- [ ] 编写 API 兼容性承诺文档（类似 K8s API deprecation policy）
- [ ] 发布 v1beta1 版本 + 博客公告
- [ ] 宣布 v1alpha1 废弃时间线（6 个月后移除）

## 成功标准

- v1beta1 API 发布
- Conversion webhook 覆盖 v1alpha1/v1alpha2/v1beta1 完整转换
- API 兼容性承诺文档发布
- 至少 1 篇博客文章说明稳定化进展
