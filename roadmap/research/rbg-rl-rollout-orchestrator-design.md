# 选项 C 详细设计：RBG 作为 RL Rollout 编排器

> 106 个研究智能体，24 个信源，115 条声明提取，23 条对抗验证确认
>
> 日期：2026-06-30

## 一、设计定位

RBG 管理 RL 训练中的**推理侧角色**（Rollout/Reference/Reward），不介入训练侧编排。训练框架（Miles/veRL/OpenRLHF）通过标准化 API 与 RBG 管理的推理引擎交互。

```
┌─────────────────────────────────────┐   ┌────────────────────────────────────────┐
│  训练侧（框架自行管理）               │   │  推理侧（RBG 管理）                      │
│                                      │   │                                        │
│  Actor (训练)     Critic (训练)       │   │  Rollout (SGLang/vLLM)    ← Patio     │
│       │                │              │   │  Reference (推理, 冻结)    ← Patio     │
│       └───── 权重同步 ──┼─────────────┼──→│  Reward (推理)            ← Patio     │
│                        │              │   │                                        │
│  PyTorchJob / Volcano / Ray          │   │  RoleBasedGroup CRD                    │
└─────────────────────────────────────┘   └────────────────────────────────────────┘
```

**核心价值**：RBG 从推理侧出发天然匹配——Rollout/Reference/Reward 本质上就是推理服务，只是生命周期受训练循环驱动。

## 二、关键发现：各框架的权重同步机制

### veRL

- **默认模式（协同）**：Actor/Rollout/Ref 在同一 Worker 进程内，权重通过 `get_per_tensor_param()` 直接内存传递，不经网络
- **分离模式**：通过可插拔 `CheckpointEngine`（NCCL/Mooncake/NIXL 后端）实现跨进程权重同步
- **GPU 内存管理**：`resume(tags=['weights'])` → `update_weights()` → `release()` 二态模型
- **RBG 价值点**：主要在**分离式部署**场景——Actor 和 Rollout 在不同节点时

来源：[veRL engine_workers.py](https://github.com/volcengine/verl)、[HybridFlow (EuroSys '25)](https://arxiv.org/abs/2409.19256)

### OpenRLHF

- **NCCL 直连**：通过 vLLM 的 `StatelessProcessGroup` 建立独立于 `torch.distributed` 全局进程组的 NCCL 通道
- **控制平面**：TCP 引导（master_address/port）
- **数据平面**：NCCL GPU-to-GPU 广播
- **partial rollout**：`pause_generation(mode='keep')` / `resume_generation` 允许训练和生成重叠
- **RBG 价值点**：管理 vLLM Pod 生命周期 + 提供 NCCL bootstrap 信息（master_address/port）给训练侧

来源：[OpenRLHF distributed_util.py](https://github.com/OpenRLHF/OpenRLHF)、[OpenRLHF 论文](https://arxiv.org/abs/2405.11143)

### vLLM 原生 RLHF API

vLLM 已提供**完整的 RLHF API 路由**（实验性，`dev/` 路径下）：

| 端点 | 方法 | 功能 |
|:---|:---:|:---|
| `/pause` | POST | 暂停推理（mode: abort/wait/keep） |
| `/resume` | POST | 恢复推理 |
| `/is_paused` | GET | 查询暂停状态 |
| `/init_weight_transfer_engine` | POST | 初始化权重传输引擎 |
| `/start_weight_update` | POST | 开始权重更新事务 |
| `/update_weights` | POST | 传输权重（可多次调用） |
| `/finish_weight_update` | POST | 完成权重更新事务 |
| `/get_world_size` | GET | 查询分布式 world size |
| `/sleep` | POST | GPU 内存卸载（level 参数） |
| `/wake_up` | POST | GPU 内存恢复（tags 参数） |

来源：[vLLM rlhf/api_router.py](https://github.com/vllm-project/vllm)、[vLLM sleep/api_router.py](https://github.com/vllm-project/vllm)

### SGLang RL API（wiki vault）

- `pause_and_register_engine` / `continue_generation` — 暂停/恢复
- `get_remote_instance_transfer_engine_info` — Rfork 权重注册信息
- `get_parallelism_info` — TP/EP 并行度
- `load_weight(huggingface_tensor)` — 标准权重加载
- P2P RDMA via Mooncake TransferEngine

## 三、Patio 权重生命周期 API 设计

### 统一抽象层

在 SGLang 和 vLLM 的异构 API 之上，Patio 提供统一的权重生命周期管理：

```python
# patio/engine/base.py 新增抽象方法

class BaseEngine:
    # 已有
    async def health_check(self) -> bool: ...
    async def get_metrics(self) -> dict: ...
    async def load_lora(self, name, path): ...

    # 新增：权重生命周期管理
    async def pause(self, mode: str = "wait") -> bool:
        """暂停推理引擎。mode: abort|wait|keep"""

    async def resume(self) -> bool:
        """恢复推理引擎"""

    async def is_paused(self) -> bool:
        """查询暂停状态"""

    async def get_weight_sync_info(self) -> WeightSyncInfo:
        """获取权重同步所需的连接信息（NCCL bootstrap、RDMA endpoint 等）"""

    async def get_weight_version(self) -> int:
        """获取当前加载的权重版本号"""

    async def sleep(self, level: int = 1) -> bool:
        """GPU 内存卸载"""

    async def wake_up(self, tags: list[str] = None) -> bool:
        """GPU 内存恢复"""
```

### 引擎适配映射

| Patio 统一 API | vLLM 映射 | SGLang 映射 |
|:---|:---|:---|
| `pause(mode)` | `POST /pause {mode}` | `pause_and_register_engine` |
| `resume()` | `POST /resume` | `continue_generation` |
| `is_paused()` | `GET /is_paused` | 内部状态查询 |
| `get_weight_sync_info()` | `GET /get_world_size` + Pod IP/Port | `get_remote_instance_transfer_engine_info` + `get_parallelism_info` |
| `get_weight_version()` | 内部版本计数器 | 内部版本计数器 |
| `sleep(level)` | `POST /sleep {level}` | sleep mode API |
| `wake_up(tags)` | `POST /wake_up {tags}` | wake mode API |

### Patio HTTP API 端点

```
# 权重生命周期管理
POST   /v1/weights/pause          {mode: "wait"}
POST   /v1/weights/resume
GET    /v1/weights/status          → {paused: bool, version: int, engine: "sglang"|"vllm"}
GET    /v1/weights/sync-info       → {nccl_endpoint: "host:port", world_size: 4, ...}

# GPU 内存管理
POST   /v1/memory/sleep            {level: 1}
POST   /v1/memory/wake-up          {tags: ["weights", "kv_cache"]}
```

### WeightSyncInfo 结构

训练框架需要知道 Rollout 引擎的连接信息来建立权重同步通道：

```json
{
  "engine_type": "sglang",
  "nccl_endpoint": {
    "host": "10.0.1.5",
    "port": 29500,
    "world_size": 4,
    "rank_offset": 0
  },
  "rdma_endpoint": {
    "host": "10.0.1.5",
    "transfer_engine_port": 12345,
    "registered_tensors": ["model.layers.0.self_attn.qkv_proj.weight", ...]
  },
  "parallelism": {
    "tp": 4,
    "ep": 1,
    "pp": 1
  },
  "weight_version": 0
}
```

## 四、ConfigMap 服务发现扩展

扩展 KEP-133 格式，增加 RL 训练所需的服务发现信息：

```yaml
namespace: rl-training
group: llama-70b-ppo
roles:
  rollout:
    replicas: 4
    size: 2                      # 每实例 2 GPU (TP=2)
    portTemplates:
      grpc:
        base: 30100
        stride: 2
      patio:                     # Patio sidecar 端口
        base: 9091
        stride: 1
    # 新增：RL 集成字段
    weightSync:
      engineType: sglang         # sglang | vllm
      mode: p2p                  # p2p | nccl | disk
      patioEndpoint: "/v1/weights/sync-info"
  reference:
    replicas: 2
    size: 2
  reward:
    replicas: 1
    size: 1
```

训练框架通过读取 ConfigMap 获取：
1. Rollout 实例的地址（通过 FQDN 模板推导）
2. Rollout 实例的端口（通过 portTemplates 推导）
3. 权重同步模式和连接方式
4. Patio 端口（用于调用 `/v1/weights/sync-info` 获取 NCCL/RDMA endpoint）

## 五、RBG YAML 设计

### 生产级 RL Rollout 编排示例

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: llama-70b-ppo-inference
  namespace: rl-training
  annotations:
    # 跨角色拓扑亲和：rollout 和 reference 优先同 zone
    rbg.workloads.x-k8s.io/group-inter-role-topology: "topology.kubernetes.io/zone"
    rbg.workloads.x-k8s.io/group-inter-role-topology-roles: "rollout,reference"
    # Gang scheduling：所有角色一起调度
    rbg.workloads.x-k8s.io/group-gang-scheduling: "true"
spec:
  roleTemplates:
    - name: inference-base
      template:
        spec:
          hostNetwork: true
          dnsPolicy: ClusterFirstWithHostNet
          containers:
            - name: engine
              resources:
                limits:
                  nvidia.com/gpu: "1"
                  rdma/rdma_shared_device_a: "1"

  roles:
    # Rollout 引擎：生成 rollout 数据，接收训练框架的权重更新
    - name: rollout
      replicas: 4
      annotations:
        # 端口分配（hostNetwork RDMA）
        rolebasedgroup.workloads.x-k8s.io/port-allocator: |
          {
            "allocations": [
              {"name": "grpc", "env": "SGLANG_PORT", "scope": "PodScoped"},
              {"name": "nccl", "env": "NCCL_PORT", "scope": "PodScoped"},
              {"name": "rdma-transfer", "env": "TRANSFER_ENGINE_PORT", "scope": "PodScoped"}
            ]
          }
        # intra-role 拓扑：rollout pod 优先同 NVSwitch 域
        rbg.workloads.x-k8s.io/role-intra-topology: "nvidia.com/nvswitch-domain"
      scalingAdapter:
        enable: true
      leaderWorkerPattern:
        size: 2    # TP=2
        templateRef:
          name: inference-base
          patch:
            spec:
              containers:
                - name: engine
                  image: lmsys/sglang:latest
                  command: ["python", "-m", "sglang.launch_server"]
                  args:
                    - "--model-path=/models/llama-70b"
                    - "--tp=2"
                    - "--port=$(SGLANG_PORT)"
      engineRuntimes:
        - profileName: sglang-rl-runtime

    # Reference 模型：冻结权重，只读推理
    - name: reference
      replicas: 2
      dependencies: []
      leaderWorkerPattern:
        size: 2
        templateRef:
          name: inference-base
          patch:
            spec:
              containers:
                - name: engine
                  image: lmsys/sglang:latest
                  args:
                    - "--model-path=/models/llama-70b"
                    - "--tp=2"
      engineRuntimes:
        - profileName: sglang-runtime

    # Reward 模型：评分推理
    - name: reward
      replicas: 1
      dependencies: []
      standalonePattern:
        templateRef:
          name: inference-base
          patch:
            spec:
              containers:
                - name: engine
                  image: lmsys/sglang:latest
                  args:
                    - "--model-path=/models/reward-model"

---
# 引擎运行时：Patio sidecar（含权重管理 API）
apiVersion: workloads.x-k8s.io/v1alpha2
kind: ClusterEngineRuntimeProfile
metadata:
  name: sglang-rl-runtime
spec:
  containers:
    - name: patio
      image: patio:latest
      env:
        - name: ENGINE_TYPE
          value: "sglang"
        - name: WEIGHT_MANAGEMENT_ENABLED
          value: "true"
        - name: WEIGHT_SYNC_MODE
          value: "p2p"
      ports:
        - containerPort: 9091
          name: patio
      resources:
        requests:
          cpu: "200m"
          memory: "256Mi"
```

### 训练侧使用方式

训练框架（Miles/veRL/OpenRLHF）从 RBG ConfigMap 读取 rollout 服务发现信息：

```python
# 训练框架侧伪代码
import yaml, requests

# 1. 读取 RBG ConfigMap
with open("/etc/rbg/config.yaml") as f:
    config = yaml.safe_load(f)

# 2. 获取 rollout 引擎信息
rollout = config["roles"]["rollout"]
for i in range(rollout["replicas"]):
    # 地址通过 FQDN 模板推导
    host = f"llama-70b-ppo-inference-rollout-{i}-leader-0.s-llama-70b-ppo-inference-rollout.rl-training.svc"
    # 端口通过 portTemplate 推导
    grpc_port = rollout["portTemplates"]["leader.grpc"]["base"] + i * rollout["portTemplates"]["leader.grpc"]["stride"]
    patio_port = 9091  # Patio 固定端口

    # 3. 从 Patio 获取权重同步信息
    sync_info = requests.get(f"http://{host}:{patio_port}/v1/weights/sync-info").json()

    # 4. 建立 NCCL/RDMA 连接
    setup_weight_sync(sync_info["nccl_endpoint"], sync_info["rdma_endpoint"])

# 5. 训练循环
for step in range(num_steps):
    # 暂停 rollout → 同步权重 → 恢复 rollout → 生成数据
    for endpoint in rollout_endpoints:
        requests.post(f"{endpoint}/v1/weights/pause", json={"mode": "wait"})
    sync_weights_to_rollout()  # NCCL/RDMA
    for endpoint in rollout_endpoints:
        requests.post(f"{endpoint}/v1/weights/resume")
    rollout_data = generate_rollout()
    train_step(rollout_data)
```

## 六、Reference/Reward 模型部署策略

| 场景 | 部署方式 | RBG 建模 |
|:---:|:---|:---|
| **单训练任务** | 同一 RBG 内作为角色 | `roles: [rollout, reference, reward]` |
| **多训练任务共享** | 独立 RBG 部署 | 两个 RBG：`llama-70b-inference`（ref+reward）+ `llama-70b-ppo-rollout`（rollout） |
| **小集群（<64 GPU）** | 协同部署（同节点） | 拓扑亲和 + exclusive-topology |
| **大集群（>96 GPU）** | 分离部署（独占节点） | 各角色独立节点组 |

veRL 的 HybridFlow 论文验证：最优策略随规模变化（16-64 GPU 协同，96-128 GPU 分离），RBG 通过拓扑感知调度注解灵活支持两种模式。

## 七、实现路线

| 阶段 | 工作项 | 依赖 |
|:---:|:---|:---|
| **Phase 1** | Patio 增加 `pause/resume/is_paused` API（vLLM + SGLang） | 无 |
| **Phase 1** | Patio 增加 `get_weight_sync_info` API | 无 |
| **Phase 1** | 编写 RL Rollout 示例 YAML + 文档 | 拓扑感知 + 端口模板 |
| **Phase 2** | Patio 增加 `sleep/wake_up` API | 无 |
| **Phase 2** | ConfigMap 增加 `weightSync` 字段 | KEP-133 |
| **Phase 2** | 与 Miles 联合验证 RBG + SGLang RL 场景 | Phase 1 |
| **Phase 3** | RBGSA 支持训练驱动的弹性伸缩 | Phase 2 |
| **Phase 3** | 权重版本跟踪（Pod annotation / CRD status） | Phase 2 |

## 八、注意事项

1. **veRL 协同模式下 RBG 价值有限**：veRL 默认 Actor/Rollout 同进程，权重直接内存传递。RBG 价值主要在分离式部署（不同节点）场景。
2. **vLLM RLHF API 仍为实验性**（`dev/` 路径），API 可能变更。Patio 应做好适配层隔离。
3. **OpenRLHF 对 vLLM 内部 API 有硬依赖**（`StatelessProcessGroup`），Patio 不应尝试替代此数据平面，而是管理 Pod 生命周期并暴露 NCCL bootstrap 信息。
4. **SGLang RL API 需补充验证**——本次研究主要覆盖 veRL、OpenRLHF 和 vLLM，SGLang 侧 API 来自 wiki vault 而非源码验证。

## 参考来源

- [veRL - engine_workers.py](https://github.com/volcengine/verl)
- [veRL - rollout/base.py](https://github.com/volcengine/verl)
- [OpenRLHF - distributed_util.py](https://github.com/OpenRLHF/OpenRLHF)
- [OpenRLHF 论文](https://arxiv.org/abs/2405.11143)
- [HybridFlow/veRL 论文 (EuroSys '25)](https://arxiv.org/abs/2409.19256)
- [vLLM - rlhf/api_router.py](https://github.com/vllm-project/vllm)
- [vLLM - sleep/api_router.py](https://github.com/vllm-project/vllm)
- [SGLang RL Docs](https://docs.sglang.io/docs/advanced_features/sglang_for_rl)
- [LMSYS P2P Weight Transfer](https://www.lmsys.org/blog/2026-04-29-p2p-update/)
- Wiki vault: Miles, P2PWeightTransfer, SGLang, Patio
