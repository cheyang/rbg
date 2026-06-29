# Action 3.5: AI 推理可观测性平台

> 时间线：12-24 个月 (2027 H2 - 2028 H1)
> 优先级：P2
> 依赖：Patio 指标体系成熟

## 问题陈述

当前 RBG 的监控依赖用户手动部署 PodMonitor + Grafana。缺少：
- 跨角色的全链路追踪（请求从 Router → Prefill → Decode 的完整路径）
- 推理服务级别的 SLO 监控
- 角色间通信质量（KV Cache 传输延迟、带宽）
- 异常检测和自动告警

## 技术方案

### 1. 推理全链路追踪

基于 OpenTelemetry 实现跨角色追踪：

```
Request → Router (span) → Prefill (span) → KV Transfer (span) → Decode (span) → Response
              |                  |                |                   |
          route_time         prefill_time    kv_transfer_time     decode_time
```

### 2. RBG 级 SLO 监控

```yaml
spec:
  observability:
    slo:
      - name: "ttft-p99"
        metric: "patio:ttft_seconds"
        percentile: 99
        target: 2s
      - name: "throughput"
        metric: "patio:output_tokens_per_second"
        target: 100
    alerts:
      - name: "slo-breach"
        condition: "ttft-p99 > target for 5m"
        severity: "warning"
```

### 3. Grafana Dashboard 标准化

提供开箱即用的 RBG Grafana Dashboard：
- 每角色指标面板
- 跨角色通信质量面板
- SLO 达成率面板
- 扩缩事件时间线

## 行动清单

- [ ] Patio 增加 OpenTelemetry trace 支持
- [ ] 设计 `observability` API 字段
- [ ] 开发标准化 Grafana Dashboard
- [ ] 实现 SLO 监控和告警
- [ ] 发布可观测性最佳实践文档

## 成功标准

- 提供开箱即用的 Grafana Dashboard
- 支持跨角色全链路追踪
- SLO 监控可声明式配置
