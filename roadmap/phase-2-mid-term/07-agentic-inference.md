# Action 2.7: Agentic 推理编排优化

> 时间线：6-12 个月 (2027 H1)
> 优先级：P2
> 依赖：Action 2.1 (自适应角色分配), Action 2.2 (KV 缓存感知路由)

## 问题陈述

Agentic 推理（Agent 工作流、多轮对话、工具调用）正在成为主流推理模式。与传统单轮推理的关键差异：
- **多轮交互**：同一会话内 3-11 轮请求，前缀高度复用
- **长上下文**：单轮 prefill 长度可达 6161 tokens
- **动态路由**：需要将同一会话的后续请求路由到同一引擎实例（KV Cache 亲和）

Dynamo v1.0 已增加 Agentic 推理支持（agent hints 元数据），RBG 需要提供相应的编排优化。

## 技术方案

### 1. 会话亲和调度

在 RBG Router 角色中支持会话亲和：

```yaml
spec:
  roles:
    - name: decode
      replicas: 8
      routing:
        sessionAffinity:
          enabled: true
          # 使用请求头中的 session ID
          headerName: "X-Session-ID"
          # 亲和保持时间
          timeout: 600s
          # 亲和失效时的回退策略
          fallback: "kv-cache-aware"  # kv-cache-aware | round-robin
```

### 2. Agentic 专用拓扑

Agent 推理场景的推荐拓扑：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: agentic-inference
spec:
  roles:
    - name: cache-manager
      replicas: 2
      dependencies: []
      standalonePattern:
        template:
          spec:
            containers:
              - name: kv-cache-store
                image: kv-cache-manager:latest

    - name: prefill
      replicas: 4
      dependencies: ["cache-manager"]

    - name: decode
      replicas: 8
      dependencies: ["prefill", "cache-manager"]

    - name: tool-executor
      replicas: 2
      dependencies: ["decode"]
      standalonePattern:
        template:
          spec:
            containers:
              - name: tool-exec
                image: tool-executor:latest
                # 不需要 GPU

    - name: router
      replicas: 2
      dependencies: ["prefill", "decode", "tool-executor"]
```

### 3. Patio Agent Hints 支持

Patio sidecar 增加 agent hints 元数据暴露：
- 当前会话上下文长度
- KV Cache 前缀状态
- 工具调用状态

## 行动清单

- [ ] 研究 Dynamo agent hints 协议
- [ ] 设计会话亲和调度 API
- [ ] 实现 Patio agent hints 暴露
- [ ] 编写 Agentic 推理拓扑示例
- [ ] 性能测试：Agentic 场景下有/无会话亲和的对比

## 成功标准

- 支持会话亲和调度
- 提供 Agentic 推理标准化拓扑示例
- 多轮推理场景下 TTFT 降低 30%+（KV Cache 复用）
