# Action 0.3: 建立产业联盟

> 时间线：0-3 个月 (2026 Q3)
> 优先级：P0 紧急
> 负责人：项目核心维护者 + 社区经理

## 问题陈述

llm-d 已拥有最广泛的产业联盟：Red Hat、Google Cloud、IBM Research、CoreWeave、NVIDIA 联合创立，AMD、Cisco、Hugging Face、Intel 等支持。RBG 必须加速建立自己的合作伙伴网络，否则将在生态竞争中被边缘化。

## 目标合作伙伴优先级

### Tier 1: 核心合作（必须）

| 合作伙伴 | 合作方向 | 接入点 |
|----------|---------|--------|
| **阿里云 ACK** | RBG 作为 ACK 推理编排的开源基础 | 已有 RapidLLMServe 经验，双方技术栈契合 |
| **SGLang / LMSYS** | 深化推理引擎集成 | 已有 Patio sidecar 集成，需更紧密的上游协作 |
| **vLLM 社区** | 确保 vLLM 一级支持 | Patio 已支持 vLLM 后端，需参与 vLLM 社区治理讨论 |

### Tier 2: 扩展合作（重要）

| 合作伙伴 | 合作方向 | 合作形式 |
|----------|---------|---------|
| **NVIDIA** | Dynamo 集成验证 | 已有 Dynamo 集成文档（`doc/features/ecosystem-integration.md`），需正式合作关系 |
| **Volcano 社区** | Gang Scheduling 深化 | 已有 Volcano PodGroup 支持，联合优化 |
| **其他云厂商** | 多云部署验证 | 提供多云 QuickStart 指南 |

### Tier 3: 生态联动（持续）

| 合作伙伴 | 合作方向 |
|----------|---------|
| Gateway API Inference Extension WG | 标准化推理网关集成 |
| Kueue 社区 | 队列管理集成 |
| Mooncake 社区 | KV Cache 分发集成 |

## 具体行动

### 1. 贡献者招募

- [ ] 在 README 中增加 "Contributing Organizations" 章节
- [ ] 发布 "Call for Contributors" 博客
- [ ] 在 SGLang、vLLM、K8s 社区 Slack 中宣传
- [ ] 创建 `good-first-issue` 标签的 issues（目标 10+）
- [ ] 建立 bi-weekly community meeting（录制并公开）

### 2. 合作伙伴接触

- [ ] 联系阿里云 ACK 团队，讨论正式合作
- [ ] 联系 vLLM 核心维护者，确认 Patio vLLM 后端的上游反馈
- [ ] 联系 NVIDIA Grove 团队，探讨与 Dynamo 集成的深化
- [ ] 联系 Volcano 社区，讨论 Gang Scheduling 联合优化
- [ ] 参与 Gateway API Inference Extension WG 会议

### 3. 内容营销

- [ ] 撰写 "RBG vs Grove vs llm-d: 选择指南" 技术博客
- [ ] 提交 KubeCon NA 2026 / KubeCon EU 2027 演讲提案
- [ ] 录制 RBG demo 视频（PD 分离部署端到端）
- [ ] 在 CNCF 和 K8s 社区渠道发布项目介绍

## 成功标准

- 至少 2 个外部组织成为正式合作伙伴
- 至少 3 个外部组织有活跃贡献者
- 建立定期 community meeting
- 至少 1 次外部技术会议演讲
