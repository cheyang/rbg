# Action 1.3: 拓扑感知调度（基础版）

> 时间线：3-6 个月 (2026 Q3-Q4)
> 优先级：P1
> 依赖：无
> KEP：[keps/topology-aware-scheduling](../../keps/topology-aware-scheduling/README.md)
> PoC：已完成（`plan` 分支，commit `ca43813d`）

## 当前状态

| 阶段 | 状态 | 产出 |
|------|------|------|
| KEP 设计文档 | ✅ 完成 | `keps/topology-aware-scheduling/README.md` |
| PoC 实现 | ✅ 完成 | `pkg/reconciler/pod_reconciler.go`（intra/inter-role affinity 注入）|
| 单元测试 | ✅ 完成 | 8 个测试通过 |
| KEP 评审 | ❌ 待评审 | — |
| 正式实现 | ❌ 待 KEP 批准后开始 | — |
| E2E 测试 | ❌ 待实现 | — |
| 文档 | ✅ 草稿 | `doc/features/topology-aware-scheduling.md` |

## 问题陈述

RBG 当前支持 `exclusive-topology`（通过 annotation），但不支持 GPU 互联拓扑感知（NVLink/NVSwitch 域）。Grove 的核心优势之一就是拓扑感知调度。K8s v1.35/v1.36 引入了工作负载感知调度（Workload-Aware Scheduling）和 DRA 增强，RBG 应利用这些上游能力而非重新实现。

## 技术方案

### 1. 利用 K8s 上游能力

K8s v1.35 引入的工作负载感知调度包括：
- **PodGroup-level scheduling hints**：调度器可感知 PodGroup 的拓扑需求
- **DRA (Dynamic Resource Allocation)**：v1.36 增强设备拓扑信息

RBG 应通过标准化接口向调度器传递拓扑需求，而非直接实现调度逻辑。

### 2. 拓扑需求声明

在 RBG spec 中增加调度拓扑声明（v1beta1 考虑纳入 spec）：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: topology-aware-inference
  annotations:
    rbg.workloads.x-k8s.io/group-exclusive-topology: "topology.kubernetes.io/zone"
spec:
  scheduling:
    # 未来 v1beta1 字段
    topologyPolicy:
      # 同角色 Pod 的拓扑约束
      intraRole:
        topologyKey: "nvidia.com/nvswitch-domain"
        policy: "Preferred"  # Required | Preferred
      # 跨角色 Pod 的拓扑约束
      interRole:
        - roles: ["prefill", "decode"]
          topologyKey: "topology.kubernetes.io/zone"
          policy: "Preferred"  # P/D 优先同 zone 以减少 KV Cache 传输延迟

  roles:
    - name: prefill
      replicas: 4
      leaderWorkerPattern:
        size: 2  # 2 GPU per instance
        template:
          spec:
            containers:
              - name: engine
                resources:
                  limits:
                    nvidia.com/gpu: "1"

    - name: decode
      replicas: 8
      standalonePattern:
        template:
          spec:
            containers:
              - name: engine
                resources:
                  limits:
                    nvidia.com/gpu: "1"
```

### 3. 与调度器集成

#### Scheduler Plugins 路径
- 扩展现有 PodGroup 生成逻辑，增加拓扑 hints
- 利用 Coscheduling 插件的拓扑感知能力

#### Volcano 路径
- 利用 Volcano 的 nodeorder/taskorder 插件
- 通过 PodGroup annotation 传递拓扑需求

#### Kueue 路径（新增）
- 探索与 Kueue 的集成，利用其资源配额和公平调度能力

### 4. 基础实现范围

Phase 1 只实现基础版：
- [x] 已有：exclusive-topology（同 zone/同 node）
- [ ] 新增：intra-role 拓扑亲和（同角色 Pod 优先同 NVSwitch 域）
- [ ] 新增：inter-role 拓扑偏好（P/D 优先同 zone）
- [ ] 不做：自动拓扑检测（Phase 2）
- [ ] 不做：NVLink 域自动发现（Phase 2）

## 行动清单

- [ ] 调研 K8s v1.35/v1.36 工作负载感知调度 API
- [ ] 设计 `scheduling.topologyPolicy` 字段（考虑 v1beta1）
- [ ] 实现 intra-role 拓扑亲和：生成 Pod Affinity 规则
- [ ] 实现 inter-role 拓扑偏好：生成 Pod Anti-Affinity/Affinity 规则
- [ ] 与 Scheduler Plugins / Volcano 验证集成
- [ ] 编写示例和文档
- [ ] E2E 测试覆盖拓扑调度场景

## 成功标准

- 同角色 Pod 可声明拓扑亲和策略
- 跨角色 Pod 可声明拓扑偏好策略
- 与至少一个调度器（Scheduler Plugins 或 Volcano）集成验证
- 提供 PD 分离场景的拓扑调度示例
