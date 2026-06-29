# Action 3.2: 跨集群推理编排

> 时间线：12-24 个月 (2027 H2 - 2028 H1)
> 优先级：P2
> 依赖：Phase 2 完成

## 问题陈述

跨云弹性推理是真实需求（wiki vault 数据），但面临数据迁移、三角权衡（低价/稳定/弹性）和存储性能挑战。大型推理服务可能需要跨多个集群部署角色：
- Prefill 集群（高计算密度）
- Decode 集群（高内存带宽）
- Router 集群（轻量级，多可用区）

## 技术方案

### 1. 联邦 RBG

引入 `FederatedRoleBasedGroup` 或在现有 RBG 中增加集群声明：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: cross-cluster-inference
spec:
  roles:
    - name: prefill
      replicas: 8
      placement:
        clusters:
          - name: gpu-cluster-a
            replicas: 4
          - name: gpu-cluster-b
            replicas: 4
        strategy: "Spread"  # Spread | Consolidated

    - name: decode
      replicas: 16
      placement:
        clusters:
          - name: gpu-cluster-a
            replicas: 8
          - name: gpu-cluster-b
            replicas: 8

    - name: router
      replicas: 4
      placement:
        clusters:
          - name: edge-cluster
            replicas: 4
```

### 2. 跨集群 KV Cache 传输

- 与 Mooncake 远程 KV Cache 传输集成
- 跨集群 RDMA/网络优化

### 3. 模型权重跨集群分发

- 与 Fluid/JuiceFS 集成实现跨集群模型缓存
- P2P 权重传输跨集群扩展

## 行动清单

- [ ] 调研 K8s 多集群方案（KubeFed、Karmada、OCM）
- [ ] 设计 `placement` API
- [ ] 实现跨集群角色部署
- [ ] 验证跨集群 PD 分离场景

## 成功标准

- 支持跨 2+ 集群部署推理角色
- 跨集群 KV Cache 传输延迟可接受
