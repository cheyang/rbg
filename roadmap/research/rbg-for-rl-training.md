# RBG 支持 RL 训练可行性研究报告

> 99 个研究智能体，17 个信源，75 条声明提取，19 条对抗验证确认
>
> 日期：2026-06-30

## 一、核心结论

**RBG 的多角色编排模型与 RL 训练在架构上天然匹配，且 K8s 生态存在明确空白。**

| 维度 | 判断 |
|:---:|:---|
| 结构匹配度 | **高** — RL 训练的 4-5 角色拓扑与 RBG 的 RoleSpec 完全对应 |
| 生态空白 | **确认** — 所有主流 RL 框架（Miles/veRL/OpenRLHF/DeepSpeed-Chat）均依赖 Ray，无 K8s Operator |
| 技术可行性 | **可行但有核心差距** — 需新增权重同步生命周期、周期性循环编排、设备放置策略 |
| 战略价值 | **高** — RL 训练+推理融合是趋势，RBG 从推理侧向训练侧延伸有先发优势 |

## 二、RL 训练的多角色拓扑

RL 训练（RLHF/PPO/GRPO）需要同时协调 4-5 个异构角色：

```
┌──────────┐     权重同步      ┌──────────┐
│  Actor   │ ───────────────→ │ Rollout  │  ← SGLang/vLLM 推理引擎
│ (训练)   │ ←── 生成数据 ──── │ (推理)   │
└────┬─────┘                  └──────────┘
     │ 梯度
┌────▼─────┐  ┌──────────┐  ┌──────────┐
│  Critic  │  │ Reference│  │  Reward  │
│ (训练)   │  │ (推理)   │  │ (推理)   │
└──────────┘  └──────────┘  └──────────┘
```

**与 RBG RoleSpec 的映射：**

```yaml
# 概念示意 — RBG 管理 RL 训练拓扑
spec:
  roles:
    - name: actor        # Actor/Policy 模型（训练）
      replicas: 1
      leaderWorkerPattern:
        size: 8           # 8-GPU TP/PP
    - name: critic       # Critic/Value 模型（训练）
      replicas: 1
      leaderWorkerPattern:
        size: 4
    - name: reference    # Reference 模型（推理，冻结）
      replicas: 1
      dependencies: ["actor"]
    - name: reward       # Reward 模型（推理）
      replicas: 1
    - name: rollout      # Rollout 引擎（SGLang 推理）
      replicas: 2
      dependencies: ["actor"]
      engineRuntimes:
        - profileName: sglang-runtime
```

结构上完全可以表达。**但表达拓扑只是第一步**。

## 三、为什么当前 RBG 还不能直接支持 RL

### 差距 1：权重同步生命周期

RL 训练的核心循环是 **Train → Sync Weights → Generate → Train → ...**。每个训练步结束后，Actor 权重必须同步到 Rollout 引擎：

| 方案 | 耗时 (1T 参数) | 来源 |
|:---:|:---:|:---|
| NCCL Broadcast | 53s | Miles/P2P (wiki vault) |
| P2P RDMA | 7.2s | [Miles/SGLang (LMSYS)](https://www.lmsys.org/blog/2026-04-29-p2p-update/) |
| 3D-HybridEngine | 减少 89.1% 转换开销 | [HybridFlow (EuroSys '25)](https://arxiv.org/abs/2409.19256) |

**RBG 当前 API 没有表达这个生命周期事件的能力。** `Dependencies` 只表达启动顺序（"B 在 A ready 后启动"），无法表达"每次训练步结束后触发权重同步到 Rollout"。

### 差距 2：周期性 Train-Generate 循环

RL 训练不是"启动后持续运行"（推理的模式），而是**周期性循环**：

```
Step 1: Rollout 生成 → Actor/Critic 训练 → 权重同步 → Step 2: Rollout 生成 → ...
```

RBG 的 `Dependencies` 是**单向 DAG**（A → B → C），无法表达循环依赖。推理场景不需要这个能力（Prefill/Decode 启动后持续服务），但 RL 训练是本质上的循环工作流。

### 差距 3：可变设备放置策略

HybridFlow 论文（EuroSys '25）实验证明：
- **16-64 GPU**：所有角色共置于同一组 GPU 性能最优
- **96-128 GPU**：每个角色独占 GPU 池性能最优

RBG 当前每个角色有独立的 `replicas` 和 Pod 模板，但**不能声明"Actor 和 Rollout 共享同一组 GPU"**（共置模式）。需要新增设备放置策略。

## 四、生态空白验证

### 所有主流 RL 框架均无 K8s 原生支持

| 框架 | 编排层 | K8s 支持 | 来源 |
|:---:|:---:|:---:|:---|
| **Miles** (Radixark) | Ray | 无（路线图中列为"弹性资源调度"待实现） | [GitHub](https://github.com/radixark/miles) |
| **veRL** (ByteDance) | Ray | 无（可通过 SkyPilot 间接部署，非原生） | [GitHub](https://github.com/volcengine/verl) |
| **OpenRLHF** | Ray | 无（ray start/ray job submit） | [GitHub](https://github.com/OpenRLHF/OpenRLHF) |
| **DeepSpeed-Chat** | PDSH/MPI/Slurm | 无（无 K8s Runner） | [GitHub](https://github.com/microsoft/DeepSpeed) |

### K8s 现有方案均不覆盖 RL 特有需求

| K8s 方案 | 多角色 | Gang 调度 | 权重同步 | 循环编排 | RL 支持 |
|:---:|:---:|:---:|:---:|:---:|:---:|
| **Volcano** | VolcanoJob 多任务组 | minAvailable | 无 | 无 | 无 |
| **JobSet** | 多 Job 组 | 有 | 无 | 无（Job=运行至完成） | 无 |
| **Kubeflow Training** | 多角色 | 有 | 无 | 无 | 无 |
| **KubeRay** | RayCluster | Ray 内部 | Ray 内部 | Ray 内部 | 间接（运行 RL 框架） |
| **RBG** | **RoleSpec 多角色** | **Volcano/SP** | **无（需新增）** | **无（需新增）** | **无（需扩展）** |

## 五、RBG 扩展 RL 的策略选择

### 选项 A：底层 Pod 编排器（与 Ray 互补）

RBG 管理多角色 Pod 拓扑（RayCluster 的声明式替代），RL 框架在 Pod 内运行（仍用 Ray/原生脚本）。

```
[RL 框架 (Miles/veRL)] → 运行在 → [RBG 管理的 Pod 拓扑] → 调度到 → [K8s 集群]
```

- **优势**：集成成本低，复用 RBG 现有能力（gang scheduling、拓扑感知、端口分配）
- **劣势**：差异化有限——KubeRay 也能做到类似的事

### 选项 B：RL 感知编排器（替代 Ray 层）

RBG 直接理解 RL 训练语义，增加权重同步、循环编排等原生能力。

```
[用户 YAML: 声明 Actor/Critic/Rollout 角色 + 权重同步策略]
  → [RBG Controller: 管理 Pod 生命周期 + 权重同步触发 + 循环调度]
    → [K8s 集群]
```

- **优势**：强差异化，真正的 K8s-native RL 编排
- **劣势**：需重新实现大量框架级功能，开发量大

### 选项 C（推荐）：推理侧 RL 辅助

RBG 专注管理 **Rollout/Reference/Reward** 这些本质上是推理的角色，与训练框架通过标准接口（权重同步 API、gRPC）交互。

```
[训练框架 (Miles/veRL)]  ← 权重同步 API →  [RBG 管理的 Rollout/Reference/Reward]
   ↑ 用户自行管理                              ↑ RBG 管理
   (PyTorchJob/VolcanoJob)                     (RoleBasedGroup)
```

- **优势**：
  - 完全发挥 RBG 推理编排优势（Patio sidecar、端口模板、拓扑感知）
  - 不需要理解训练语义，只需暴露权重更新接口
  - 与 Miles/SGLang 生态天然契合（Rfork API、P2P 权重传输）
  - 训练侧可自由选择 PyTorchJob/Volcano/原生脚本
- **劣势**：不覆盖训练侧编排

## 六、选项 C 的具体设计思路

### 1. Patio 增加权重更新接口

Patio sidecar 已有 LoRA 热加载能力。扩展为权重更新管理器：

```python
# Patio 新增 API
POST /v1/weights/pause       # 暂停推理引擎
POST /v1/weights/update      # 触发权重更新（P2P/NCCL/磁盘）
POST /v1/weights/resume      # 恢复推理引擎
GET  /v1/weights/version     # 当前权重版本号
```

### 2. RBG 增加 weightSync 角色依赖类型

```yaml
spec:
  roles:
    - name: rollout
      replicas: 4
      weightSync:
        source: "actor"              # 权重来源角色
        mode: "p2p"                  # p2p | nccl | disk
        triggerEndpoint: "/v1/weights/update"  # Patio API
      engineRuntimes:
        - profileName: sglang-runtime
```

### 3. ConfigMap 暴露权重版本

扩展 KEP-133 ConfigMap 格式，增加 `weightVersion` 字段：

```yaml
roles:
  rollout:
    replicas: 4
    weightVersion: 42          # 训练框架更新后递增
    portTemplates:
      grpc: {base: 30100, stride: 1}
```

训练框架通过 K8s API 更新 ConfigMap 的 weightVersion → Patio 监听 → 触发权重同步。

## 七、建议的行动

| 优先级 | 行动 | 时间线 |
|:---:|:---|:---:|
| **P0** | 编写 KEP "RBG for RL Rollout Orchestration"（选项 C） | v0.9.0 |
| **P1** | Patio 增加权重更新 API（pause/update/resume） | v0.9.0 |
| **P1** | 与 Miles 团队联合验证 RBG + Miles RL 训练场景 | v0.9.0 |
| **P2** | ConfigMap 增加 weightVersion 字段 | v1.0 |
| **P2** | RBG spec 增加 weightSync 角色配置 | v1.0 |

## 八、待解决的开放问题

1. **RBG 应在哪个抽象层次介入 RL 训练编排**——是作为底层 Pod 编排器与 Ray 互补（管理 RayCluster 的多角色拓扑），还是替代 Ray 直接编排训练和推理进程？前者集成成本低但差异化价值有限，后者差异化明显但需重新实现大量框架级功能。

2. **权重同步事件应如何在 RBG 的 CRD 中建模**——是作为角色间的新型依赖类型（如 weightSyncDependency）、作为独立的生命周期钩子（如 postTrainingStep webhook）、还是通过 Sidecar/Init Container 模式在 Pod 层面处理？

3. **RL 训练的周期性 train-generate 循环与 RBG 当前的单向 Dependencies 模型存在根本性不匹配**，是否需要引入状态机或工作流引擎概念来表达循环依赖？这是否会使 RBG 的 API 复杂度超出合理范围？

4. **市场定位问题**：Miles（RadixArk/SGLang 生态）已经与 SGLang 深度集成且计划弹性调度，veRL（字节跳动）有强大的工程资源——RBG 作为 K8s-native 方案的独特价值主张是什么？是否应聚焦于成为 RL 框架的「K8s 基础设施层」而非竞争完整的 RL 编排？

## 九、研究注意事项

1. **时效性风险**：RL 训练框架发展极快，Miles/veRL/OpenRLHF 均处于活跃开发中，数月内可能出现重大变化（如某框架推出原生 K8s Operator）。
2. **Ray+KubeRay 的桥接作用被低估**：虽然 RL 框架本身不提供 K8s Operator，但 KubeRay 已为 Ray 提供了 K8s 原生支持（RayCluster/RayJob/RayService CRDs），这在一定程度上填补了编排空白，RBG 的差异化价值需定位在「RL 训练拓扑感知编排」而非仅仅「K8s 上运行 RL」。
3. **权重同步机制的多样性**：各框架采用截然不同的权重同步方案（进程内切换、NCCL 广播、P2P RDMA、零冗余重分片），RBG 若要提供统一抽象需谨慎设计以避免过度耦合具体实现。
4. **共置 vs 分离的设备放置策略依赖模型大小和集群规模**（HybridFlow 论文数据），RBG 不能假设单一最优拓扑。

## 参考来源

- [Miles GitHub](https://github.com/radixark/miles)
- [OpenRLHF GitHub](https://github.com/OpenRLHF/OpenRLHF)
- [veRL GitHub](https://github.com/volcengine/verl)
- [DeepSpeed-Chat GitHub](https://github.com/microsoft/DeepSpeed/tree/master/blogs/deepspeed-chat)
- [HybridFlow Paper (EuroSys '25)](https://arxiv.org/abs/2409.19256)
- [LMSYS P2P Weight Transfer Blog](https://www.lmsys.org/blog/2026-04-29-p2p-update/)
- [SGLang RL Docs](https://docs.sglang.io/docs/advanced_features/sglang_for_rl)
- [Volcano GitHub](https://github.com/volcano-sh/volcano)
- [JobSet GitHub](https://github.com/kubernetes-sigs/jobset)
- [Kubeflow Training Operator GitHub](https://github.com/kubeflow/training-operator)
- Wiki vault: Miles, P2PWeightTransfer, SGLang, Mooncake, KimiK2
