# Action 2.3: 高级拓扑感知调度

> 时间线：6-12 个月 (2027 H1)
> 优先级：P1
> 依赖：Action 1.3 (基础拓扑感知)

## 问题陈述

Phase 1 实现了基础拓扑感知（Pod Affinity/Anti-Affinity），但不支持自动拓扑检测和 NVLink/NVSwitch 域感知。Grove 的 2026 路线图包含自动拓扑检测和拓扑扩展约束。RBG 需要在这些方面跟进。

## 技术方案

### 1. GPU 互联拓扑自动检测

通过 DRA 或节点标签自动获取 GPU 互联拓扑：

```yaml
# RBG 控制器读取节点标签
# nvidia.com/gpu.topology.nvlink-domain: "domain-0"
# nvidia.com/gpu.topology.nvswitch-fabric: "fabric-a"

spec:
  scheduling:
    topologyPolicy:
      autoDetect: true  # 自动读取节点拓扑标签
      intraRole:
        topologyKey: "nvidia.com/gpu.topology.nvlink-domain"
        policy: "Required"
```

### 2. 多维拓扑约束

支持多个拓扑维度的组合约束：

```yaml
spec:
  scheduling:
    topologyPolicy:
      constraints:
        - topologyKey: "nvidia.com/gpu.topology.nvswitch-fabric"
          scope: "IntraInstance"    # 同实例内 Pod
          policy: "Required"
        - topologyKey: "topology.kubernetes.io/zone"
          scope: "InterRole"       # 跨角色
          roles: ["prefill", "decode"]
          policy: "Preferred"
        - topologyKey: "topology.kubernetes.io/rack"
          scope: "IntraRole"       # 同角色
          policy: "Preferred"
```

### 3. NUMA 感知集成

结合 K8s v1.36 的 Pod 级资源管理器，实现 NUMA 感知放置：
- 利用 topology manager 确保 GPU 和 CPU 在同一 NUMA 节点
- 通过 DRA 获取 GPU NUMA 亲和信息

## 行动清单

- [ ] 调研 DRA v1.36 拓扑信息 API
- [ ] 实现 GPU 互联拓扑自动检测
- [ ] 设计多维拓扑约束 API
- [ ] 与 NUMA 感知调度集成
- [ ] 在 NVLink/NVSwitch 集群验证

## 成功标准

- 自动检测 GPU 互联拓扑（NVLink/NVSwitch）
- 多维拓扑约束可声明式配置
- 在 NVSwitch 集群上多节点推理吞吐提升 15%+
