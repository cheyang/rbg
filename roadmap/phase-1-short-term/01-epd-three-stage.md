# Action 1.1: E/P/D 三阶段原生支持

> 时间线：3-6 个月 (2026 Q3-Q4)
> 优先级：P0
> 依赖：无

## 问题陈述

多模态推理（视觉、音频等）需要在传统 P/D 分离基础上增加 Encode 阶段，形成 E/P/D 三阶段拓扑。EPD 论文（ICML 2025）证实三阶段分离可实现 15x 峰值内存降低和 71% TTFT 降低。Dynamo v1.0 已支持 E/P/D，RBG 的多角色模型天然适合但需要原生化支持。

## 技术方案

### 1. 新增 Encode 角色模板

在 `spec.roleTemplates` 中提供标准化的 Encode 角色模板：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: multimodal-epd-inference
spec:
  roleTemplates:
    - name: inference-base
      template:
        spec:
          containers:
            - name: engine
              image: sglang:latest

  roles:
    - name: encode
      replicas: 2
      dependencies: []
      standalonePattern:
        template:
          spec:
            containers:
              - name: engine
                env:
                  - name: ROLE
                    value: "encode"
                resources:
                  limits:
                    nvidia.com/gpu: "1"  # Encode 通常需要较少 GPU

    - name: prefill
      replicas: 4
      dependencies: ["encode"]
      standalonePattern:
        template:
          spec:
            containers:
              - name: engine
                env:
                  - name: ROLE
                    value: "prefill"
                resources:
                  limits:
                    nvidia.com/gpu: "2"

    - name: decode
      replicas: 8
      dependencies: ["prefill"]
      standalonePattern:
        template:
          spec:
            containers:
              - name: engine
                env:
                  - name: ROLE
                    value: "decode"
                resources:
                  limits:
                    nvidia.com/gpu: "2"

    - name: router
      replicas: 2
      dependencies: ["encode", "prefill", "decode"]
      standalonePattern:
        template:
          spec:
            containers:
              - name: router
                image: inference-router:latest
```

### 2. 预定义示例库

在 `examples/` 下提供标准化的推理拓扑示例：

```
examples/
├── inference-topologies/
│   ├── aggregated/          # 聚合式（单角色）
│   ├── pd-disaggregated/    # P/D 分离（两角色）
│   ├── epd-multimodal/      # E/P/D 分离（三角色）
│   ├── agentic/             # Agentic 拓扑（Router + Cache + Engine）
│   └── custom/              # 自定义多角色
```

### 3. CoordinatedPolicy 扩展

确保 `CoordinatedPolicy` 支持 3+ 角色的协调：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: CoordinatedPolicy
metadata:
  name: epd-rollout-policy
spec:
  policies:
    - name: epd-coordination
      roles:
        - encode
        - prefill
        - decode
      strategy:
        rollingUpdate:
          maxSkew: "1%"
```

### 4. Patio Sidecar 扩展

在 Patio 中增加 Encode 角色的指标采集和拓扑管理：
- 新增 `encode_engine.py` 后端适配
- 统一指标：`patio:encode_latency`、`patio:encode_throughput`
- Encode → Prefill 的数据传输监控

## 行动清单

- [ ] 设计 E/P/D 拓扑的标准化 YAML 结构
- [ ] 编写 `examples/inference-topologies/epd-multimodal/` 示例
- [ ] 验证 3 角色依赖链的启动顺序正确性
- [ ] 验证 CoordinatedPolicy 对 3+ 角色的支持
- [ ] Patio 增加 Encode 角色支持
- [ ] 编写文档 `doc/features/epd-disaggregated.md`
- [ ] E2E 测试覆盖 E/P/D 拓扑

## 成功标准

- E/P/D 三角色拓扑可通过单个 RBG YAML 声明式部署
- 启动依赖链（Encode → Prefill → Decode → Router）正确执行
- CoordinatedPolicy 可协调 3 角色的滚动更新
- 提供完整的 E/P/D 示例和文档
