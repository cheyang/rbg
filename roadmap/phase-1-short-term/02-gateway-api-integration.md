# Action 1.2: Gateway API Inference Extension 集成

> 时间线：3-6 个月 (2026 Q3-Q4)
> 优先级：P0
> 依赖：无

## 问题陈述

K8s WG-AI-Gateway 已成立（2026.3），Gateway API Inference Extension（`kubernetes-sigs/gateway-api-inference-extension`）正在快速发展为 K8s 推理网关的标准。llm-d 和 KServe 已经与之深度集成。RBG 如果不对接，将在网关层被绕过。

## 技术方案

### 1. InferencePool 适配

Gateway API Inference Extension 定义了 `InferencePool` 资源来描述推理后端池。RBG 需要自动为每个角色创建对应的 `InferencePool`：

```yaml
# RBG 控制器自动生成
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferencePool
metadata:
  name: my-rbg-decode
  labels:
    rbg.workloads.x-k8s.io/name: my-rbg
    rbg.workloads.x-k8s.io/role: decode
spec:
  targetPortNumber: 8000
  selector:
    rbg.workloads.x-k8s.io/name: my-rbg
    rbg.workloads.x-k8s.io/role: decode
```

### 2. RBG 控制器变更

在 RBG reconcile 逻辑中增加 InferencePool 管理：

- **创建**：为标记了 `gatewayIntegration: true` 的角色自动创建 InferencePool
- **更新**：角色 selector 变更时同步更新 InferencePool
- **清理**：RBG 删除时级联删除 InferencePool

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: pd-inference
spec:
  roles:
    - name: decode
      replicas: 4
      gatewayIntegration:
        enabled: true
        targetPort: 8000
      standalonePattern:
        template: ...
```

### 3. InferenceModel 关联

支持用户通过 `InferenceModel` 资源将模型名映射到 RBG 角色：

```yaml
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferenceModel
metadata:
  name: llama-70b
spec:
  modelName: meta-llama/Llama-3.1-70B
  targetRef:
    name: my-rbg-decode  # 自动生成的 InferencePool
  criticality: Critical
```

### 4. 端到端流量路径

```
Client → HTTPRoute → Gateway → InferenceModel → InferencePool → RBG Decode Pods
                                                        ↑
                                          RBG Controller 自动管理
```

## 行动清单

- [ ] 参与 WG-AI-Gateway 社区会议，了解最新 API 设计
- [ ] 在 RBG controller 中增加 InferencePool reconcile 逻辑
- [ ] 设计 `gatewayIntegration` 字段的 API（是否放在 spec 还是 annotation）
- [ ] 实现 InferencePool 的创建/更新/清理生命周期管理
- [ ] 编写 Gateway API + RBG 的端到端示例
- [ ] E2E 测试覆盖 Gateway API 集成路径
- [ ] 编写文档 `doc/features/gateway-api-integration.md`

## 成功标准

- RBG 可自动为角色创建 InferencePool
- 端到端流量通过 Gateway API → InferencePool → RBG Pod 可达
- 与 Envoy Gateway 或 Istio Gateway 验证兼容
- 提供完整示例和文档
