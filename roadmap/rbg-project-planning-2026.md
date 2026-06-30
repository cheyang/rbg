# RBG (RoleBasedGroup) 项目规划报告

> 基于 110 个研究智能体、27 个信源、131 条声明提取、19 条对抗验证确认的深度研究。
>
> 生成日期：2026-06-29 | 最后更新：2026-06-29

## 进度总览

| 状态 | 数量 | 说明 |
|:---:|:---:|:---|
| KEP + PoC 完成 | 2 | 拓扑感知调度、端口模板化服务发现 |
| Action 已定义，待 KEP | 1 | 滚动更新 Pod 复用优化 |
| 待规划 | 22 | 其余 action 待 KEP 设计 |

> **开发流程**：每个 action 遵循 KEP → 评审 → 正式实现 → E2E 测试 → 文档 的流程。
> PoC 代码仅作为设计验证参考，正式实现需根据 KEP 评审反馈调整。

## 详细行动计划索引

### Phase 0: 紧急行动（0-3 个月）
- [ ] [Action 0.1: 推进社区治理中立化](phase-0-emergency/00-governance-neutrality.md)
- [ ] [Action 0.2: v1alpha2 → v1beta1 API 稳定化](phase-0-emergency/01-api-v1beta1.md)
- [ ] [Action 0.3: 建立产业联盟](phase-0-emergency/02-industry-alliance.md)
- [ ] [Action 0.4: 发布 Benchmark 和对比报告](phase-0-emergency/03-benchmark-report.md)

### Phase 1: 短期（3-6 个月）
- [ ] [Action 1.1: E/P/D 三阶段原生支持](phase-1-short-term/01-epd-three-stage.md)
- [ ] [Action 1.2: Gateway API Inference Extension 集成](phase-1-short-term/02-gateway-api-integration.md)
- [x] [Action 1.3: 拓扑感知调度（基础版）](phase-1-short-term/03-topology-aware-scheduling.md) — KEP + PoC 完成，待评审
- [ ] [Action 1.4: llm-d 互补集成探索](phase-1-short-term/04-llmd-integration.md)
- [ ] [Action 1.5: Warmup Phase 2](phase-1-short-term/05-warmup-phase2.md)
- [ ] [Action 1.6: TensorRT-LLM 引擎支持](phase-1-short-term/06-trtllm-support.md)
- [x] [Action 1.7: 端口模板化服务发现](phase-1-short-term/07-port-template-discovery.md) — KEP + PoC 完成，待评审
- [ ] [Action 1.8: 滚动更新 Pod 复用优化](phase-1-short-term/08-rolling-update-pod-reuse.md) — 语义等价检测避免不必要的删除重建

### Phase 2: 中期（6-12 个月）
- [ ] [Action 2.1: 自适应角色分配](phase-2-mid-term/01-adaptive-role-allocation.md)
- [ ] [Action 2.2: KV 缓存感知路由](phase-2-mid-term/02-kv-cache-aware-routing.md)
- [ ] [Action 2.3: 高级拓扑感知调度](phase-2-mid-term/03-advanced-topology.md)
- [ ] [Action 2.4: 冷启动加速集成](phase-2-mid-term/04-cold-start-acceleration.md)
- [ ] [Action 2.5: 多级自动扩缩](phase-2-mid-term/05-multi-level-autoscaling.md)
- [ ] [Action 2.6: 跨角色协调滚动更新增强](phase-2-mid-term/06-coordinated-rolling-update.md)
- [ ] [Action 2.7: Agentic 推理编排优化](phase-2-mid-term/07-agentic-inference.md)

### Phase 3: 长期（12-24 个月）
- [ ] [Action 3.1: 声明式推理服务规范标准化](phase-3-long-term/01-inference-service-spec.md)
- [ ] [Action 3.2: 跨集群推理编排](phase-3-long-term/02-cross-cluster.md)
- [ ] [Action 3.3: Checkpoint/Restore 系统集成](phase-3-long-term/03-checkpoint-restore.md)
- [ ] [Action 3.4: 模型权重 P2P 加速](phase-3-long-term/04-weight-p2p.md)
- [ ] [Action 3.5: AI 推理可观测性平台](phase-3-long-term/05-observability.md)
- [ ] [Action 3.6: 多模型调度](phase-3-long-term/06-multi-model.md)

### KEP 索引

| KEP | Action | 状态 | 文档 |
|:---|:---|:---:|:---|
| topology-aware-scheduling | 1.3 | KEP + PoC 完成 | [KEP](../keps/topology-aware-scheduling/README.md) |
| port-template-discovery | 1.7 | KEP + PoC 完成 | [KEP](../keps/port-template-discovery/README.md) |
| rolling-update-pod-reuse | 1.8 | 待 KEP | [Action](phase-1-short-term/08-rolling-update-pod-reuse.md) |

### 研究报告

| 主题 | 结论 | 文档 |
|:---|:---|:---|
| RBG 支持 RL 训练 | 结构匹配、生态空白确认；推荐选项 C（推理侧 RL 辅助）| [研究报告](research/rbg-for-rl-training.md) |

---

## 一、竞争格局分析

### 1.1 核心竞品矩阵

| 维度 | **RBG** | **Grove (NVIDIA)** | **llm-d (Red Hat)** | **KServe (CNCF)** |
|:---:|:---:|:---:|:---:|:---:|
| **成熟度** | v0.7.0 GA API (v1alpha2) | v0.1.0-alpha.9 | CNCF Sandbox (2026.3) | CNCF Incubating, 5600+ stars |
| **Stars** | ~中等 | 228 | 增长中 | 5600+ |
| **核心能力** | 多角色声明式编排 | 拓扑感知层级调度 | 路由 + KV 缓存 + FMA | 标准化模型服务 + LLM 扩展 |
| **产业联盟** | SGLang 生态 | NVIDIA Dynamo 生态 (20+ 生产部署) | Red Hat + Google Cloud + IBM + CoreWeave + NVIDIA | Kubeflow 生态, 694 依赖仓库 |
| **PD 分离** | 原生多角色支持 | PodClique 层级 | 路由层 P/D | 通过 llmisvc-controller |
| **引擎支持** | SGLang + vLLM (via Patio) | SGLang + TRT-LLM + vLLM | vLLM 为主 | 多引擎 (ServingRuntime) |
| **调度集成** | Volcano PodGroup | KAI Scheduler Gang | K8s 原生 | K8s 原生 |

### 1.2 竞争态势关键发现

#### NVIDIA Dynamo/Grove — 最大威胁但窗口存在

- Dynamo 增长迅猛：v1.0 → v1.2.1，37 个版本，7.4k stars，1.3k forks，70+ 贡献者 [[来源: GitHub]](https://github.com/ai-dynamo/dynamo)
- 但 Grove（K8s 操作层）仍处于 **alpha 阶段**（v0.1.0-alpha.9），API 为 v1alpha1
- RBG 的 v1alpha2 + conversion webhooks **比 Grove 更成熟**，这是重要时间窗口
- **被否决的声明**：Dynamo 宣称"完全 K8s 原生"和"基于 CRD 的服务发现"被 3 票否决，说明其 K8s 集成深度可能被夸大

#### llm-d — 最广泛联盟，但定位互补

- 产业联盟最广：Red Hat、Google Cloud、IBM Research、CoreWeave、NVIDIA 联合创立，AMD、Cisco、Hugging Face、Intel 等支持 [[来源: GitHub]](https://github.com/llm-d/llm-d)
- 宣称性能：前缀缓存感知路由 3x 吞吐（Tesla 生产数据）、P/D 分离 70% tokens/sec 提升（AWS/B200）
- **关键洞察**：llm-d 聚焦路由/缓存/FMA 层，RBG 聚焦工作负载编排层，**二者存在互补集成可能性而非纯竞争**

#### KServe — 生态威胁最大

- 已通过专用 `LLMInferenceService` 控制器（llmisvc-controller）进入分离式推理编排 [[来源: GitHub]](https://github.com/kserve/kserve)
- CRD 包含一级 `Prefill` 字段和分离式推理逻辑
- 深度集成 llm-d，形成 KServe + llm-d 的强力组合
- 694 个依赖仓库 = 最大的既有用户基础

### 1.3 学术验证：RBG 方向正确

研究证实多角色编排是正确方向：

- **Mooncake** (FAST 2025 最佳论文)：P/D 分离生产提升 ~75% [[arxiv]](https://arxiv.org/abs/2407.00079)
- **EPD** (ICML 2025)：编码/预填充/解码三阶段分离，15x 峰值内存降低，71% TTFT 降低 [[arxiv]](https://arxiv.org/abs/2501.05460)
- **AMPD**：自适应 P/D 分配比静态分离提升 67.29% SLO 达成率 [[arxiv]](https://arxiv.org/abs/2602.14516)

**核心结论**：未来推理编排需支持 **三个或更多角色**（E/P/D），而非仅两角色 P/D。RBG 的声明式多角色模型天然契合这一趋势。

---

## 二、市场分析

### 2.1 市场规模

- 生成式 AI 服务器市场：2025 年 **1039 亿美元** → 2030 年 **4486 亿美元**（CAGR 34%）[[来源: MarketsandMarkets]](https://www.marketsandmarkets.com/Market-Reports/generative-ai-server-market-68416882.html)
- GPU 利用率普遍仅 10-20%，驱动 Serverless/Scale-to-zero 模式
- **推理占比正在超过训练**：a16z 数据显示推理支出已成为 AI 基础设施的主要部分 [[来源: a16z]](https://a16z.com/navigating-the-high-cost-of-ai-compute/)

### 2.2 技术趋势

1. **K8s 上游 AI 转型加速**
   - K8s v1.36：DRA 增强、Pod 级资源管理器（alpha）、工作负载感知调度增强 [[来源: K8s blog]](https://kubernetes.io/blog/2026/05/13/kubernetes-v1-36-advancing-workload-aware-scheduling/)
   - WG-Device-Management 成立，统一设备管理方向 [[来源: K8s blog]](https://kubernetes.io/blog/2026/06/24/wg-device-management-spotlight-2026/)
   - WG-AI-Gateway 成立，标准化 AI 推理网关 [[来源: K8s blog]](https://kubernetes.io/blog/2026/03/09/announcing-ai-gateway-wg/)
   - Gateway API Inference Extension 快速发展 [[来源: GitHub]](https://github.com/kubernetes-sigs/gateway-api-inference-extension)

2. **多模态推理 E/P/D 三阶段分离** 成为新前沿（Dynamo v1.0 已支持）

3. **Agentic 推理** 带来新的编排挑战：多轮对话、工具调用、动态路由

4. **冷启动加速** 成为关键差异化能力（Modal 40x、Dynamo Snapshot 21x、vLLM Sleep ~3s）

---

## 三、RBG 差异化定位

### 3.1 核心竞争优势

| 优势 | 说明 | 持续性 |
|:---:|:---|:---:|
| **声明式多角色编排** | 唯一将推理服务抽象为 N 个角色的控制器，天然支持 E/P/D 和更多角色拓扑 | 高 |
| **API 成熟度** | v1alpha2 + conversion webhooks，比 Grove (v1alpha1 alpha) 领先 | 中（窗口有限） |
| **引擎运行时抽象** | Patio sidecar 统一 SGLang/vLLM 指标、LoRA 管理、拓扑 | 高 |
| **Coordinated Scaling** | 跨角色协调扩缩容，确保角色比例一致 | 高 |
| **Stateful InstanceSet** | 原生有状态实例管理 + in-place updates | 高 |

### 3.2 需要弥补的短板

| 短板 | 竞品对标 | 优先级 | 状态 |
|:---:|:---|:---:|:---:|
| **社区治理和中立性** | llm-d (CNCF)、KServe (CNCF) | P0 | 待启动 |
| **产业联盟** | llm-d (Red Hat+Google+IBM+NVIDIA) | P0 | 待启动 |
| **拓扑感知调度** | Grove (NVLink/NVSwitch 感知) | P1 | KEP + PoC 完成 |
| **跨角色服务发现（hostNetwork 端口）** | hostNetwork PD 分离刚需 | P1 | KEP + PoC 完成 |
| **路由和 KV 缓存管理** | llm-d (前缀缓存路由、KV 卸载) | P1 | 待规划 |
| **冷启动加速** | Dynamo Snapshot (21x)、vLLM Sleep | P2 | 待规划 |

---

## 四、项目规划：短期 → 长期

### Phase 0: 紧急行动（0-3 个月, 2026 Q3）

**主题：建立生态位，抢占窗口**

| # | 行动 | 目标 | 理由 |
|:---:|:---|:---|:---|
| 0.1 | **推进 CNCF 或 K8s SIG 治理** | 从 sgl-project 迁移到更中立的组织治理，或申请 CNCF Sandbox | llm-d 已进 CNCF，KServe 是 CNCF Incubating。RBG 托管在 sgl-project 下会限制 vLLM/TRT-LLM 社区的接受度 |
| 0.2 | **v1alpha2 → v1beta1 API 稳定化** | 发布 v1beta1 API，建立 API 稳定性承诺 | Grove 仍在 v1alpha1 alpha，API 稳定性是 RBG 当前最大的先发优势 |
| 0.3 | **建立产业联盟** | 至少引入 2-3 个产业合作伙伴（云厂商、推理引擎团队） | llm-d 联盟已经很广，RBG 必须加速 |
| 0.4 | **发布 Benchmark 和对比报告** | 标准化场景下与 Grove/KServe 的功能和性能对比 | 建立市场认知，用数据说话 |

### Phase 1: 短期（3-6 个月, 2026 Q3-Q4）

**主题：补齐核心短板，巩固多角色编排优势**

| # | 特性 | 说明 | 竞品对标 |
|:---:|:---|:---|:---|
| # | 特性 | 说明 | 竞品对标 | 状态 |
|:---:|:---|:---|:---|:---:|
| 1.1 | **E/P/D 三阶段原生支持** | 在角色模型中内置 Encode 角色模板，支持多模态推理的三阶段分离拓扑 | Dynamo v1.0 E/P/D, EPD 论文 | 待规划 |
| 1.2 | **Gateway API Inference Extension 集成** | 与 K8s WG-AI-Gateway 对接，提供标准化推理网关集成 | llm-d 路由层, KServe | 待规划 |
| 1.3 | **拓扑感知调度（基础版）** | intra-role / inter-role 拓扑亲和，通过标准 Pod Affinity 实现 | Grove 拓扑感知 | **KEP + PoC 完成** |
| 1.4 | **llm-d 互补集成探索** | 探索 RBG（工作负载编排） + llm-d（路由/缓存）的集成路径 | 变竞争为互补 | 待规划 |
| 1.5 | **Warmup Phase 2** | 完善 RBG Warmup，支持 CUDA Graph 缓存共享、DeepGEMM 预编译 | 引擎层优化（路线 C） | 待规划 |
| 1.6 | **TRT-LLM 引擎支持** | Patio 增加 TRT-LLM 后端支持 | Grove/Dynamo 原生支持 | 待规划 |
| 1.7 | **端口模板化服务发现** | Role 级随机 base + Pod 级顺序递增，ConfigMap portTemplates 跨角色端口推导 | hostNetwork PD 分离刚需 | **KEP + PoC 完成** |
| 1.8 | **滚动更新 Pod 复用** | 语义等价检测：revision hash 不同但 spec 实质相同的 Pod 不删除重建，直接 adopt | 推理场景重建代价极高（分钟级权重加载） | 待 KEP |

### Phase 2: 中期（6-12 个月, 2027 H1）

**主题：深度优化，建立技术壁垒**

| # | 特性 | 说明 | 竞品对标 |
|:---:|:---|:---|:---|
| 2.1 | **自适应角色分配** | 基于 AMPD 论文思路，实现运行时动态调整 P/D 角色比例 | AMPD: 67% SLO 提升 |
| 2.2 | **KV 缓存感知路由** | 在 Patio 或独立组件中实现前缀缓存感知路由 | Dynamo KV-aware Router, llm-d |
| 2.3 | **高级拓扑感知** | NVLink/NVSwitch 域感知，跨 NUMA 节点优化放置 | Grove 拓扑感知, NUMA 感知 |
| 2.4 | **冷启动加速集成** | 集成 vLLM Sleep 模式 + 引擎层优化（CUDA Graph 部分/延迟捕获） | 路线 B+C |
| 2.5 | **多级自动扩缩** | Role 级、InstanceSet 级、RBG 级三层 HPA/VPA | Grove PodClique/ScalingGroup/CliqueSet |
| 2.6 | **Coordinated Rolling Update** | 跨角色协调滚动更新，保证服务连续性 | Grove 2026 路线图 |
| 2.7 | **Agentic 推理优化** | 支持多轮对话工作负载的编排优化（上下文预加载、会话亲和） | Dynamo Agentic 支持 |

### Phase 3: 长期（12-24 个月, 2027 H2 - 2028 H1）

**主题：平台级能力，生态领导力**

| # | 特性 | 说明 | 战略意义 |
|:---:|:---|:---|:---|
| 3.1 | **声明式推理服务规范** | 推进 RBG API 成为 K8s 推理工作负载的标准规范（类似 Gateway API 之于网关） | 生态锁定 |
| 3.2 | **跨集群推理编排** | 支持跨云/跨集群的推理角色调度 | 跨云弹性推理需求 |
| 3.3 | **Checkpoint/Restore 集成** | 系统层 C/R 支持（对接 CRIU/DynamoSnapshot） | 系统层突破 |
| 3.4 | **模型权重 P2P 加速** | 集成 ModelExpress/NIXL 或类似方案实现 GPU-to-GPU 权重传输 | Dynamo ModelExpress 7x |
| 3.5 | **AI 可观测性平台** | 基于 Patio 指标构建推理服务全链路可观测性 | 差异化运维能力 |
| 3.6 | **多模型调度** | 同一 RBG 实例支持多模型共享 GPU，基于流量动态切换 | 模型热切换 |

---

## 五、生态策略建议

### 5.1 社区建设优先级

```
P0 紧急：
  ├── 组织治理中立化（CNCF/K8s SIG）
  ├── 产业合作伙伴引入（至少 2-3 家）
  └── 贡献者指南 + Good First Issues

P1 重要：
  ├── 与 llm-d 探索互补集成
  ├── 与 Gateway API Inference Extension 对接
  └── 定期 Community Meeting + 公开路线图

P2 持续：
  ├── 技术博客 + 会议演讲 (KubeCon, AI Infra Summit)
  ├── 用户案例收集和发布
  └── 教程和 QuickStart 持续优化
```

### 5.2 关键集成优先级

| 集成方向 | 优先级 | 理由 |
|:---:|:---:|:---|
| SGLang (已有) | P0 | 核心引擎，持续深化 |
| vLLM | P0 | 最大用户基础，必须一级支持 |
| Gateway API Inference Extension | P0 | K8s 上游标准化方向 |
| Kueue/Volcano | P1 | Gang 调度生态 |
| TensorRT-LLM | P1 | NVIDIA 生态覆盖 |
| llm-d (路由/KV 层) | P1 | 互补集成，扩大生态 |
| DRA (K8s upstream) | P2 | 设备管理标准化 |

---

## 六、风险与应对

| 风险 | 可能性 | 影响 | 应对 |
|:---:|:---:|:---:|:---|
| Grove 快速成熟 | 高 | 高 | 加速 v1beta1 稳定化，建立 API 先发优势 |
| KServe + llm-d 组合垄断 | 高 | 高 | 探索 llm-d 互补集成，避免正面竞争 |
| K8s 上游标准化替代 RBG | 中 | 高 | 主动参与上游（WG-Device-Management、WG-AI-Gateway），让 RBG 成为参考实现 |
| SGLang 绑定限制社区接受度 | 高 | 中 | 组织治理中立化 + 增强 vLLM/TRT-LLM 支持 |
| 市场窗口关闭 | 中 | 高 | Phase 0 紧急行动在 3 个月内完成 |

---

## 七、核心叙事

> **RBG 是 Kubernetes 上唯一的声明式多角色推理编排控制器**。当 Dynamo/Grove 聚焦于 NVIDIA 硬件生态、llm-d 聚焦于路由和缓存优化、KServe 从传统 ML Serving 向 LLM 扩展时，RBG 将推理服务抽象为 **N 个角色的拓扑化协作体**——这是 EPD 三阶段分离、Agentic 推理、多模态推理等趋势的正确抽象层次。

**一句话定位**：RBG = Kubernetes 推理服务的 "Deployment for AI"——声明式、多角色、引擎无关、生态开放。

---

## 八、待解决的开放问题

1. **RBG 应该寻求加入 CNCF 还是 Kubernetes SIG 以获得社区中立性和更广泛的治理？** 考虑到 llm-d 已经是 CNCF Sandbox，KServe 是 CNCF Incubating，RBG 在基金会层面的定位策略是什么？

2. **RBG 与 llm-d 是否存在互补集成的可能性（RBG 提供工作负载编排，llm-d 提供路由和 KV 缓存管理）？** 如果是，技术集成的可行路径是什么？

3. **随着 Kubernetes 上游正在推进 DRA GA、原生 Gang 调度和拓扑感知调度，RBG 应如何调整其调度策略以利用这些上游能力而非重新实现？**

4. **RBG 托管在 sgl-project（SGLang 的 GitHub 组织）下，这在多大程度上限制了其被 vLLM、TensorRT-LLM 等其他推理引擎社区接受？** 是否需要迁移到更中立的组织？

---

## 九、研究注意事项

1. **时效性高度敏感**：所有 GitHub 指标（stars、forks、版本号）截至 2026 年 6 月底，AI 推理领域发展极快，3-6 个月内竞争格局可能显著变化。
2. **性能基准数据普遍来自项目自报或合作伙伴营销材料**（Dynamo 的 7x 吞吐、llm-d 的 3x/13.9x 提升、Mooncake 的 525%），均为最佳情况下的上限值，实际生产环境性能通常显著低于宣称值（如 Mooncake 生产仅 75% vs 模拟 525%）。
3. **市场规模数据来自 MarketsandMarkets 单一来源**，该机构方法论不透明，不同分析机构对同一市场的估值差异可达 2-3 倍。
4. **已被否决的声明中包含 Dynamo 的 K8s 原生性和 CRD-based 服务发现等声明**，说明 Dynamo 的部分自我宣传可能夸大了其 Kubernetes 集成深度。
5. **AMPD 论文为未经同行评审的 arxiv 预印本**，其对 Dynamo 基线的性能对比结论需谨慎引用。

---

## 参考来源

- [NVIDIA Dynamo GitHub](https://github.com/ai-dynamo/dynamo)
- [Grove GitHub](https://github.com/ai-dynamo/grove)
- [llm-d GitHub](https://github.com/llm-d/llm-d)
- [KServe GitHub](https://github.com/kserve/kserve)
- [Mooncake Paper (FAST 2025)](https://arxiv.org/abs/2407.00079)
- [EPD Paper (ICML 2025)](https://arxiv.org/abs/2501.05460)
- [AMPD Paper](https://arxiv.org/abs/2602.14516)
- [MarketsandMarkets Gen AI Server Market](https://www.marketsandmarkets.com/Market-Reports/generative-ai-server-market-68416882.html)
- [a16z AI Compute](https://a16z.com/navigating-the-high-cost-of-ai-compute/)
- [K8s v1.36 Workload-Aware Scheduling](https://kubernetes.io/blog/2026/05/13/kubernetes-v1-36-advancing-workload-aware-scheduling/)
- [K8s WG-AI-Gateway](https://kubernetes.io/blog/2026/03/09/announcing-ai-gateway-wg/)
- [K8s WG-Device-Management](https://kubernetes.io/blog/2026/06/24/wg-device-management-spotlight-2026/)
- [Gateway API Inference Extension](https://github.com/kubernetes-sigs/gateway-api-inference-extension)
