# Action 3.6: 多模型调度

> 时间线：12-24 个月 (2027 H2 - 2028 H1)
> 优先级：P2
> 依赖：Action 2.4 (冷启动加速), LoRA 热加载 (Patio 已有)

## 问题陈述

GPU 利用率普遍仅 10-20%。通过在同一组 GPU 上调度多个模型，可以显著提升 GPU 利用率。当前场景：
- 不同时段服务不同模型（白天用 Chat 模型，夜间用 Code 模型）
- 同一 GPU 上运行基座模型 + 多个 LoRA 适配器
- 小模型和大模型混部

## 技术方案

### 1. 模型切换（Model Swapping）

与 llm-d FMA Milestone 3 的 Launcher 模型切换对齐：

```yaml
spec:
  roles:
    - name: decode
      replicas: 4
      modelManagement:
        # 支持的模型列表
        models:
          - name: "llama-3.1-70b"
            priority: 1
          - name: "qwen-3-32b"
            priority: 2
        # 切换策略
        swapPolicy:
          type: "Schedule"  # Schedule | OnDemand | LoadBased
          schedule:
            - model: "llama-3.1-70b"
              cron: "0 8 * * *"   # 白天
            - model: "qwen-3-32b"
              cron: "0 22 * * *"  # 夜间
```

### 2. LoRA 多模型共享

利用 Patio 已有的 LoRA 热加载能力，在同一基座模型上动态管理多个 LoRA 适配器：

```yaml
spec:
  roles:
    - name: decode
      replicas: 4
      modelManagement:
        baseModel: "llama-3.1-70b"
        loraAdapters:
          - name: "customer-a-adapter"
            path: "s3://models/lora/customer-a"
            maxConcurrent: 2
          - name: "customer-b-adapter"
            path: "s3://models/lora/customer-b"
            maxConcurrent: 2
        loraPolicy:
          maxLoadedAdapters: 4
          evictionPolicy: "LRU"
```

### 3. GPU 分时复用

在闲时释放 GPU 内存（vLLM Sleep），为其他模型或工作负载腾出空间：

```
高峰时段: [模型 A 运行中, 全量 GPU 使用]
低谷时段: [模型 A 睡眠, GPU 释放] → [模型 B 启动, 使用释放的 GPU]
恢复时段: [模型 B 睡眠] → [模型 A 唤醒]
```

## 行动清单

- [ ] 设计 `modelManagement` API
- [ ] 实现基于调度的模型切换
- [ ] 扩展 Patio LoRA 管理为多模型调度
- [ ] 实现 GPU 分时复用编排
- [ ] 性能测试：多模型调度 vs 单模型部署的 GPU 利用率

## 成功标准

- 支持声明式多模型调度
- GPU 利用率从 10-20% 提升至 40%+
- 模型切换时间 < 30s（Sleep/Wake 模式）
