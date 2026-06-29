# Action 3.4: 模型权重 P2P 加速

> 时间线：12-24 个月 (2027 H2 - 2028 H1)
> 优先级：P2
> 依赖：Phase 2 拓扑感知

## 问题陈述

大模型权重加载是冷启动的主要瓶颈之一。NVIDIA ModelExpress 通过 NIXL GPUDirect RDMA 实现 GPU-to-GPU 权重传输，MoE 模型加载加速 7x。RBG 应在编排层支持 P2P 权重传输，使新扩容的 Pod 可以从已有 Pod 直接接收权重。

## 技术方案

### 1. 种子实例模式

```yaml
spec:
  roles:
    - name: decode
      replicas: 8
      weightDistribution:
        mode: "P2P"  # P2P | ObjectStorage | PVC
        p2p:
          # 已运行的 Pod 作为权重种子
          seedStrategy: "FirstReady"
          # 使用 RDMA 传输
          transport: "RDMA"  # RDMA | TCP
```

### 2. 与 ModelExpress/NIXL 集成

- Patio sidecar 暴露权重注册 API
- 新 Pod 启动时查询 RBG 中已有 Pod 的权重位置
- 通过 NIXL 或 Mooncake 直接传输

### 3. 与扩缩容联动

扩容新 Pod 时自动从同角色的 seed Pod 传输权重。

## 行动清单

- [ ] 研究 ModelExpress/NIXL API
- [ ] 设计 `weightDistribution` API
- [ ] Patio 增加权重注册和种子发现 API
- [ ] 实现 P2P 权重传输编排
- [ ] 性能基线：P2P vs 对象存储加载

## 成功标准

- 权重 P2P 传输加速 3x+
- 新 Pod 扩容时自动从 seed Pod 获取权重
