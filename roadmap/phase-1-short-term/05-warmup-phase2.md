# Action 1.5: Warmup Phase 2

> 时间线：3-6 个月 (2026 Q3-Q4)
> 优先级：P1
> 依赖：Warmup Phase 1 (已完成, #335)

## 问题陈述

RBG Warmup Phase 1 已合并（PR #335），但推理引擎的冷启动仍包含多个耗时阶段：CUDA Graph 捕获、DeepGEMM JIT 编译、torch.compile 等。Phase 2 需要将 warmup 缓存持久化和跨 Pod 共享，进一步降低启动时间。

## 技术方案

### 1. Warmup 缓存持久化

在 RBG 角色模板中支持 warmup 缓存卷声明：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: cached-warmup-inference
spec:
  roles:
    - name: decode
      replicas: 4
      warmup:
        # Phase 1: 已有的 warmup 探针
        readinessGates:
          - conditionType: "rbg.workloads.x-k8s.io/warmup-complete"
        # Phase 2: 缓存持久化
        cache:
          enabled: true
          persistentVolumeClaim:
            claimName: warmup-cache-pvc
          cachePaths:
            - /root/.cache/torch/  # torch.compile 缓存
            - /root/.cache/sglang/ # CUDA Graph 缓存
            - /root/.cache/deepgemm/ # DeepGEMM JIT 缓存
      standalonePattern:
        template:
          spec:
            containers:
              - name: engine
                image: sglang:latest
                env:
                  - name: SGLANG_JIT_DEEPGEMM_FAST_WARMUP
                    value: "1"
                  - name: TORCH_COMPILE_CACHE_DIR
                    value: "/root/.cache/torch/"
```

### 2. 缓存共享策略

| 策略 | 说明 | 适用场景 |
|------|------|---------|
| **PVC 共享** | 同一 PVC 挂载到同角色所有 Pod | 同型号 GPU、同模型的 Pod |
| **Init Container 预加载** | Init container 从对象存储下载缓存 | 跨节点缓存分发 |
| **Patio Sidecar 管理** | Patio 管理缓存的生成、上传和分发 | 统一缓存生命周期 |

### 3. 引擎侧优化配置

通过 `ClusterEngineRuntimeProfile` 注入引擎优化配置：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: ClusterEngineRuntimeProfile
metadata:
  name: fast-warmup-profile
spec:
  containers:
    - name: patio
      image: patio:latest
      env:
        - name: WARMUP_CACHE_ENABLED
          value: "true"
        - name: WARMUP_CACHE_UPLOAD_ON_COMPLETE
          value: "true"
  initContainers:
    - name: cache-loader
      image: cache-loader:latest
      command: ["download-warmup-cache"]
      volumeMounts:
        - name: warmup-cache
          mountPath: /root/.cache/
  volumes:
    - name: warmup-cache
      persistentVolumeClaim:
        claimName: warmup-cache-pvc
```

### 4. 预期效果

基于 wiki vault 数据（Qwen3.5-397B 实验）：
- DeepGEMM warmup：93s → 44s（`FAST_WARMUP=1`）→ 跳过（缓存命中）
- CUDA Graph 捕获：可通过缓存跳过
- torch.compile：编译产物持久化后跳过

## 行动清单

- [ ] 设计 `warmup.cache` API 字段
- [ ] 实现 PVC-based 缓存持久化
- [ ] 实现 Init Container 缓存预加载
- [ ] Patio 增加缓存上传/下载管理
- [ ] 与 SGLang/vLLM 验证缓存兼容性（跨版本、跨 GPU 型号）
- [ ] 性能基线测试（有缓存 vs 无缓存启动时间对比）
- [ ] 编写文档 `doc/features/warmup-cache.md`

## 成功标准

- 第二次启动时 warmup 时间降低 50%+ （缓存命中）
- 同角色新 Pod 扩容时可复用已有缓存
- 提供端到端示例和性能数据
