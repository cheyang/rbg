# Action 0.1: 推进社区治理中立化

> 时间线：0-3 个月 (2026 Q3)
> 优先级：P0 紧急
> 负责人：项目核心维护者

## 问题陈述

RBG 当前托管在 `sgl-project`（SGLang 的 GitHub 组织）下，这造成两个问题：
1. **引擎绑定印象**：vLLM、TensorRT-LLM 社区会认为 RBG 是 SGLang 专属工具，降低采纳意愿
2. **治理竞争力不足**：llm-d 已加入 CNCF Sandbox（2026.3），KServe 是 CNCF Incubating，RBG 在基金会层面缺乏背书

## 可选路径

### 路径 A：申请 CNCF Sandbox（推荐）

**优势**：
- 获得中立基金会背书，与 llm-d/KServe 同等治理级别
- CNCF 项目可获得 KubeCon 演讲机会、CI 基础设施、法律支持
- 降低潜在合作伙伴的担忧

**具体步骤**：
1. 准备 CNCF Sandbox 申请材料
   - 项目概述文档（已有 README 基础上增加治理章节）
   - 多样化贡献者证明（至少 3 个不同组织的贡献者）
   - 采纳案例列表
   - 安全审计计划
2. 建立 GOVERNANCE.md，定义：
   - Maintainer 准入/退出机制
   - 决策流程（lazy consensus + voting for disputes）
   - 子项目（Patio、CLI 等）独立维护者路径
3. 建立 OWNERS 文件体系（参考 Kubernetes sig-apps 模式）
4. 向 CNCF TOC 提交申请（需要至少 2 个 TOC sponsor）

**前置条件**：
- 至少 3 个不同组织的活跃贡献者
- Apache 2.0 许可证（已满足）
- CLA/DCO 签署流程（已有）

### 路径 B：加入 Kubernetes SIG（备选）

**优势**：
- 直接进入 K8s 生态核心，API group `workloads.x-k8s.io` 已暗示此路径
- 与 LeaderWorkerSet、JobSet 等项目同级

**具体步骤**：
1. 联系 SIG-Apps chairs，提议 RBG 作为 SIG-Apps 子项目
2. 将仓库迁移到 `kubernetes-sigs/rbg`
3. 遵循 K8s 贡献者流程

**风险**：K8s SIG 流程较慢，审批可能需要 6+ 个月

### 路径 C：建立独立中立组织（折中）

**具体步骤**：
1. 创建 `rolebasedgroup` GitHub 组织（已有 `rolebasedgroup.github.io`）
2. 将 `sgl-project/rbg` → `rolebasedgroup/rbg` 迁移
3. 邀请多引擎社区成员加入组织
4. 后续再申请 CNCF

## 行动清单

- [ ] 评估三条路径，选定方向
- [ ] 起草 GOVERNANCE.md
- [ ] 建立 OWNERS 文件体系
- [ ] 联系至少 2 个外部组织贡献者
- [ ] 如选路径 A：准备 CNCF Sandbox 申请材料
- [ ] 如选路径 B：联系 SIG-Apps chairs
- [ ] 如选路径 C：创建中立组织并迁移仓库
- [ ] 更新 README、官网，去除 SGLang 独占感知

## 成功标准

- 至少有 3 个不同组织的活跃贡献者
- 治理文档就位（GOVERNANCE.md、OWNERS）
- 申请或迁移流程启动
