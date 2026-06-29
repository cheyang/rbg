# Action 1.6: TensorRT-LLM 引擎支持

> 时间线：3-6 个月 (2026 Q3-Q4)
> 优先级：P1
> 依赖：无

## 问题陈述

RBG 通过 Patio sidecar 当前支持 SGLang 和 vLLM 两种引擎后端。NVIDIA TensorRT-LLM 是第三大主流推理引擎，尤其在 NVIDIA 硬件优化场景有广泛使用。Dynamo/Grove 原生支持 TRT-LLM，RBG 缺少此支持会限制 NVIDIA 生态用户的采纳。

## 技术方案

### 1. Patio 引擎抽象层扩展

Patio 已有 `engine/base.py` 定义统一接口，`sglang_engine.py` 和 `vllm_engine.py` 分别实现。新增 `trtllm_engine.py`：

```python
# patio/engine/trtllm_engine.py

class TRTLLMEngine(BaseEngine):
    """TensorRT-LLM engine backend for Patio."""

    def health_check(self) -> bool:
        # TRT-LLM Triton Server health endpoint
        ...

    def get_metrics(self) -> dict:
        # 映射 TRT-LLM metrics 到 patio: 前缀
        # trtllm:inflight_batcher_request_count → patio:num_requests_running
        # trtllm:prompt_tokens_total → patio:input_tokens_total
        ...

    def load_lora(self, adapter_name: str, adapter_path: str):
        # TRT-LLM LoRA adapter 加载 API
        ...

    def unload_lora(self, adapter_name: str):
        ...

    def register_worker(self, worker_info: dict):
        # 分布式拓扑注册
        ...
```

### 2. 指标映射

| TRT-LLM 原始指标 | Patio 统一指标 |
|-----------------|---------------|
| `trtllm:inflight_batcher_request_count` | `patio:num_requests_running` |
| `trtllm:request_count_total` | `patio:num_requests_total` |
| `trtllm:prompt_tokens_total` | `patio:input_tokens_total` |
| `trtllm:generation_tokens_total` | `patio:output_tokens_total` |

### 3. ClusterEngineRuntimeProfile 示例

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: ClusterEngineRuntimeProfile
metadata:
  name: trtllm-runtime
spec:
  containers:
    - name: patio
      image: patio:latest
      env:
        - name: ENGINE_TYPE
          value: "trtllm"
        - name: ENGINE_PORT
          value: "8000"
      ports:
        - containerPort: 9091
          name: patio
      resources:
        requests:
          cpu: "100m"
          memory: "128Mi"
```

## 行动清单

- [ ] 实现 `patio/engine/trtllm_engine.py`
- [ ] 实现 TRT-LLM 指标到 Patio 统一指标的映射
- [ ] 实现 TRT-LLM LoRA 加载/卸载 API 适配
- [ ] 编写 TRT-LLM + RBG 部署示例
- [ ] 测试 TRT-LLM PD 分离场景
- [ ] 编写文档更新 `doc/features/engine-runtime.md`

## 成功标准

- Patio 支持 TRT-LLM 作为第三引擎后端
- TRT-LLM 指标可通过 Patio 统一格式采集
- 提供 TRT-LLM 多角色部署示例
