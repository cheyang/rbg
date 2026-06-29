# Action 1.7: 端口模板化服务发现

> 时间线：3-6 个月 (2026 Q3-Q4)
> 优先级：P0
> 依赖：KEP-171 (Pod Port Allocation, 已完成)
> KEP：[keps/port-template-discovery](../../keps/port-template-discovery/README.md)
> PoC：已完成（`plan` 分支，commit `7c4d3d09`）

## 问题陈述

hostNetwork + PD 分离场景下，每个 Pod 需要动态分配独占端口。当前 KEP-171 通过 RandomAllocator 为每个 Pod 独立随机分配端口，导致：
1. 端口不可推导——Decode 角色无法知道 Prefill 角色实例的端口
2. ConfigMap（KEP-133）无法记录端口信息（否则回到 O(N) 大小）
3. DNS/EndpointSlice 不承载动态端口

这是 hostNetwork 推理部署中跨角色通信（如 KV Cache 传输）的硬伤。

## 技术方案

两层端口分配：
- **Role 级**：RandomAllocator 分配 1 个 base 端口（处理跨 RBG 冲突）
- **Pod 级**：`port = base + instanceIndex × stride + podIndex`（确定性递增）

ConfigMap 新增 `portTemplates` 字段：
```yaml
roles:
  prefill:
    replicas: 2
    size: 2
    portTemplates:
      leader.grpc:
        base: 30100
        stride: 2
```

引擎端可推导任意实例的端口，ConfigMap 保持 O(1) 大小。

## 当前状态

| 阶段 | 状态 | 产出 |
|------|------|------|
| KEP 设计文档 | ✅ 完成 | `keps/port-template-discovery/README.md` |
| PoC 实现 | ✅ 完成 | `pkg/port-allocator/manager.go`（AllocateBasePort、DerivePortsForInstance）、ConfigMap portTemplates 字段 |
| 单元测试 | ✅ 完成 | 10 个测试通过 |
| KEP 评审 | ❌ 待评审 | — |
| 正式实现 | ❌ 待 KEP 批准后开始 | — |
| E2E 测试 | ❌ 待实现 | — |
| 文档 | ❌ 待实现 | — |

## 下一步

- [ ] KEP 评审和批准
- [ ] 基于评审反馈调整设计
- [ ] 正式实现（PoC 代码需要根据评审反馈重构）
- [ ] E2E 测试：hostNetwork PD 分离 + 端口模板
- [ ] 文档：`doc/features/port-template.md`
- [ ] Patio 集成：Patio 读取 ConfigMap portTemplates 并更新引擎配置（后续 KEP）

## 成功标准

- Decode Pod 可通过 ConfigMap portTemplate 推算 Prefill 端口
- ConfigMap 大小不随 replica 数增长
- 向后兼容：无 portTemplate annotation 的 RBG 行为不变
