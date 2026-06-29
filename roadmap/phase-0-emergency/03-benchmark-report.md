# Action 0.4: 发布 Benchmark 和对比报告

> 时间线：0-3 个月 (2026 Q3)
> 优先级：P0 紧急
> 负责人：性能工程 + 技术写作

## 问题陈述

RBG 缺乏公开的性能基准和竞品对比数据。用户在选型时无法量化 RBG 相对于 Grove/KServe/原生 K8s 的优势。竞品的性能声明（Dynamo 7x 吞吐、llm-d 3x 路由性能）部分已被对抗验证否决或降级，RBG 有机会通过严谨的第三方可复现基准建立公信力。

## Benchmark 设计

### 1. 功能对比矩阵

| 能力 | RBG v0.7.0 | Grove v0.1.0-alpha.9 | KServe + llmisvc | llm-d FMA |
|------|-----------|---------------------|-----------------|-----------|
| 多角色声明式定义 | 待填写 | 待填写 | 待填写 | 待填写 |
| 角色间启动依赖 | 待填写 | 待填写 | 待填写 | 待填写 |
| 协调滚动更新 | 待填写 | 待填写 | 待填写 | 待填写 |
| Gang Scheduling | 待填写 | 待填写 | 待填写 | 待填写 |
| 引擎运行时注入 | 待填写 | 待填写 | 待填写 | 待填写 |
| In-Place Update | 待填写 | 待填写 | 待填写 | 待填写 |
| 扩缩容协调 | 待填写 | 待填写 | 待填写 | 待填写 |
| API 稳定性 | 待填写 | 待填写 | 待填写 | 待填写 |

### 2. 性能基准场景

#### 场景 A: PD 分离部署（核心场景）

- **模型**：Llama 3.1 70B（通用可复现）
- **硬件**：8x H100 或 8x A100（标注具体型号）
- **拓扑**：2 Prefill + 4 Decode + 1 Router
- **测量指标**：
  - 部署时间（从 apply YAML 到所有 Pod Ready）
  - TTFT (Time-To-First-Token) P50/P90/P99
  - TPS (Tokens Per Second) 吞吐
  - 扩容时间（从 2P+4D 扩到 4P+8D）
  - 滚动更新时间 + 期间请求成功率

#### 场景 B: 多角色编排（差异化场景）

- **拓扑**：Router + Prefill + Decode + LoRA Adapter Manager
- **测量**：4 角色联合部署 vs 手动 4 个 Deployment 的运维复杂度对比
- **故障注入**：单角色 Pod 失败时的恢复时间

#### 场景 C: 规模测试

- **规模**：50/100/200 个 RBG 实例
- **测量**：Controller 内存/CPU、reconcile 延迟、API Server 压力

### 3. 可复现性要求

- 所有测试脚本开源到 `benchmarks/` 目录
- 提供 Makefile 一键运行
- 结果以 JSON + Grafana Dashboard 形式发布
- 标注硬件环境、K8s 版本、引擎版本

## 行动清单

- [ ] 设计 benchmark 场景和指标
- [ ] 搭建测试环境（至少 H100 集群）
- [ ] 实现自动化测试脚本（`benchmarks/` 目录）
- [ ] 运行 RBG 基准测试
- [ ] 运行 Grove 对比测试（如可部署）
- [ ] 运行原生 K8s Deployment 基线对比
- [ ] 编写 benchmark 报告（中英文）
- [ ] 发布到官网和技术博客
- [ ] 在 GitHub README 中增加性能数据摘要

## 成功标准

- 发布可复现的 benchmark 套件
- 至少覆盖 PD 分离部署和规模测试两个场景
- 对比报告以博客形式发布
- 数据被至少 1 篇外部技术文章引用
