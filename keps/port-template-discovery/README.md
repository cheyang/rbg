# KEP: Port Template for Deterministic Port Allocation and Cross-Role Discovery

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [The hostNetwork Port Discovery Problem](#the-hostnetwork-port-discovery-problem)
  - [Why Existing Mechanisms Fail](#why-existing-mechanisms-fail)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
    - [Story 1: Cross-Role Port Discovery in PD Disaggregated Inference](#story-1-cross-role-port-discovery-in-pd-disaggregated-inference)
    - [Story 2: Scale-Out Without Port Re-Negotiation](#story-2-scale-out-without-port-re-negotiation)
    - [Story 3: Engine-Side Port Derivation](#story-3-engine-side-port-derivation)
- [Design Details](#design-details)
  - [Two-Level Port Allocation](#two-level-port-allocation)
  - [Port Derivation Formula](#port-derivation-formula)
  - [ConfigMap Format Extension](#configmap-format-extension)
  - [Data Flow](#data-flow)
  - [Annotation Storage](#annotation-storage)
  - [Backward Compatibility](#backward-compatibility)
  - [Implementation](#implementation)
  - [Test Plan](#test-plan)
  - [Graduation Criteria](#graduation-criteria)
- [Relationship to KEP-133 and KEP-171](#relationship-to-kep-133-and-kep-171)
- [Alternatives Considered](#alternatives-considered)
<!-- /toc -->

## Summary

This KEP extends KEP-171 (Pod Port Allocation) with a **deterministic port allocation scheme** that enables **cross-role port discovery** via the ConfigMap (KEP-133). Instead of randomly allocating independent ports for each pod, each role gets one randomly allocated base port, and individual pod ports are derived via a formula: `port = base + instanceIndex * stride + podIndex`. The ConfigMap only stores the compact `portTemplate{base, stride}` per portName, keeping O(1) size while enabling any pod to compute any other pod's port.

## Motivation

### The hostNetwork Port Discovery Problem

When deploying LLM inference with PD disaggregation on RDMA networks, pods use `hostNetwork: true`. In this mode, each pod needs a unique port to avoid conflicts when multiple replicas share a node. KEP-171 solved the port allocation problem (random per-pod ports), but introduced a new one: **how does a Decode pod discover the dynamically allocated port of a Prefill pod?**

### Why Existing Mechanisms Fail

| Mechanism | Can Provide Addresses? | Can Provide Dynamic Ports? | Cross-Role? |
|-----------|:---:|:---:|:---:|
| K8s DNS (Headless Service) | Yes | No — only static `Service.spec.ports` | Yes |
| EndpointSlice | Yes | No — ports from Service spec, not dynamic | Yes |
| KEP-171 `references` | N/A | Yes, but same-RoleInstance only | **No** |
| ConfigMap (KEP-133) | Yes (template) | **No — no port info** | Yes |
| Pod annotations | N/A | Yes | **No** — only visible to own instance |

**The gap**: No mechanism today enables Pod A in Role X to discover the dynamically allocated port of Pod B in Role Y.

### Goals

1. Enable cross-role port discovery in hostNetwork scenarios.
2. Keep ConfigMap size at O(1) — no per-instance port enumeration.
3. Make port allocation deterministic so pods can derive any other pod's port from a compact template.
4. Maintain backward compatibility with existing random allocation (KEP-171).
5. Require zero changes to Pod-level port injection (`InjectPortsIntoPod` unchanged).

### Non-Goals

1. Replacing random allocation globally. Random allocation remains the default for backward compatibility.
2. Cross-RBG port isolation guarantees. The random base port provides best-effort isolation (same as KEP-171).
3. Runtime port migration or re-allocation. Ports are assigned at RoleInstanceSet creation time and remain stable.
4. Patio/engine-side ConfigMap consumption logic (future KEP).

## Proposal

### User Stories

#### Story 1: Cross-Role Port Discovery in PD Disaggregated Inference

As an inference operator, I deploy Prefill and Decode with `hostNetwork: true`. Decode pods need to connect to Prefill pods via RDMA. Today, Decode has no way to know Prefill's dynamically allocated gRPC port.

With port templates, Decode reads the ConfigMap:
```yaml
roles:
  prefill:
    replicas: 4
    size: 2
    portTemplates:
      leader.grpc:
        base: 30142
        stride: 2
```
Decode computes: Prefill instance 2, leader pod → `30142 + 2 * 2 + 0 = 30146`.

#### Story 2: Scale-Out Without Port Re-Negotiation

I scale Decode from 4 to 8 replicas. New Decode pods get deterministic ports (`base + 4*stride` through `base + 7*stride`). The ConfigMap only updates `replicas: 8` — the portTemplate is unchanged. Existing pods don't need to re-read port information.

#### Story 3: Engine-Side Port Derivation

As a Patio/engine developer, I need to know addresses and ports of all Prefill instances to set up KV Cache transfer channels. I read `/etc/rbg/config.yaml` once and compute:

```python
for i in range(config['roles']['prefill']['replicas']):
    host = f"rbg-prefill-{i}-leader-0.s-rbg-prefill.ns.svc.cluster.local"
    port = config['roles']['prefill']['portTemplates']['leader.grpc']['base'] + i * config['roles']['prefill']['portTemplates']['leader.grpc']['stride']
    connect(host, port)
```

No per-instance lookup needed. No controller API calls. Pure local computation.

## Design Details

### Two-Level Port Allocation

```
Level 1 (Role level):   RandomAllocator.AllocateBatch(1) → one random base port
                         Handles cross-RBG collision avoidance (same as KEP-171)

Level 2 (Pod level):    base + instanceIndex * stride + podIndex
                         Deterministic, derivable, no allocator call needed
```

The key insight: **cross-RBG isolation requires randomness (Level 1), but within a role, determinism is strictly better (Level 2)**.

### Port Derivation Formula

```
port(role, portName, instanceIndex, podIndex) = portTemplate[portName].base + instanceIndex * stride + podIndex
```

Where:
- `base`: randomly allocated at RoleInstanceSet creation time (one per portName per component)
- `stride`: number of pods per instance (`LeaderWorkerPattern.size` or 1 for standalone)
- `instanceIndex`: the RoleInstance's ordinal index (from `role-instance-index` label)
- `podIndex`: the pod's ordinal within its instance (0 for standalone, 0..size-1 for leader-worker)

**Example**: Role `prefill`, 3 replicas, size=2 (leader + 1 worker), base=30100:

| Instance | Pod | Formula | Port |
|:---:|:---:|:---|:---:|
| prefill-0 | leader (pod 0) | 30100 + 0×2 + 0 | 30100 |
| prefill-0 | worker (pod 1) | 30100 + 0×2 + 1 | 30101 |
| prefill-1 | leader (pod 0) | 30100 + 1×2 + 0 | 30102 |
| prefill-1 | worker (pod 1) | 30100 + 1×2 + 1 | 30103 |
| prefill-2 | leader (pod 0) | 30100 + 2×2 + 0 | 30104 |
| prefill-2 | worker (pod 1) | 30100 + 2×2 + 1 | 30105 |

**Port range consumption**: A role with R replicas and size S consumes R×S contiguous ports starting from base. The total range consumed across all roles of one RBG is predictable: `Σ(replicas_i × size_i)`.

### ConfigMap Format Extension

Extends KEP-133's refined format with a `portTemplates` field:

```yaml
namespace: sgl-workspace
group: epd-test
roles:
  prefill:
    replicas: 2
    size: 2
    portTemplates:            # NEW — only present when port allocator is enabled
      leader.grpc:            # key format: <componentName>.<portName>
        base: 30100
        stride: 2
      leader.nccl:
        base: 31200
        stride: 2
  decode:
    replicas: 4
    size: 1
    portTemplates:
      worker.grpc:
        base: 30500
        stride: 1
```

**Size impact**: O(number of portNames × number of roles) — typically 2-3 portNames × 3-4 roles = 6-12 lines. ConfigMap stays compact regardless of replica count.

### Data Flow

```
┌──────────────────────────────────────────────────────────────────────┐
│ RoleInstanceSet Creation (once per role)                             │
│                                                                      │
│   For each PodScoped portName in port-allocator annotation:          │
│     RandomAllocator.AllocateBatch(1) → base port                     │
│     stride = component.size                                          │
│     Write to RoleInstanceSet annotation:                             │
│       portTemplate.<component>.<portName>.base = <base>              │
│       portTemplate.<component>.<portName>.stride = <stride>          │
└──────────────────────┬───────────────────────────────────────────────┘
                       │
┌──────────────────────▼───────────────────────────────────────────────┐
│ RoleInstance Creation (per instance)                                  │
│                                                                      │
│   Read portTemplate from RoleInstanceSet annotations                 │
│   For each pod in instance:                                          │
│     port = base + instanceIndex × stride + podIndex                  │
│     Write to RoleInstance annotation:                                │
│       <podName>.<portName> = <port>      (existing format, compat)   │
└──────────────────────┬───────────────────────────────────────────────┘
                       │
┌──────────────────────▼───────────────────────────────────────────────┐
│ Pod Creation (per pod) — UNCHANGED                                   │
│                                                                      │
│   Read port from RoleInstance annotation                             │
│   Inject as env var and pod annotation                               │
│   (InjectPortsIntoPod — no changes needed)                           │
└──────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────────┐
│ ConfigMap Reconcile (every RBG reconcile loop)                       │
│                                                                      │
│   For each role:                                                     │
│     Read portTemplate annotations from RoleInstanceSet               │
│     Write to ConfigMap: roles.<role>.portTemplates = {base, stride}  │
└──────────────────────────────────────────────────────────────────────┘
```

### Annotation Storage

portTemplate information is stored at the RoleInstanceSet level:

| Key Format | Example | Stored On |
|-----------|---------|-----------|
| `portTemplate.<component>.<portName>.base` | `portTemplate.leader.grpc.base: "30100"` | RoleInstanceSet annotation |
| `portTemplate.<component>.<portName>.stride` | `portTemplate.leader.grpc.stride: "2"` | RoleInstanceSet annotation |

Derived per-pod ports are stored in existing format on RoleInstance annotations (backward compatible):

| Key Format | Example | Stored On |
|-----------|---------|-----------|
| `<podName>.<portName>` | `rbg-prefill-0-leader-0.grpc: "30100"` | RoleInstance annotation |

### Backward Compatibility

- **Existing RBGs**: No portTemplate annotations → fall back to KEP-171 random allocation. Zero behavior change.
- **New RBGs with port allocator enabled**: Automatically use portTemplate mode. Per-pod annotations are still written (derived from template), so `InjectPortsIntoPod` works unchanged.
- **ConfigMap consumers**: `portTemplates` is an additive field. Existing consumers that don't read it are unaffected. New consumers can opt in.
- **Port allocator disabled**: No portTemplate annotations written, no ConfigMap portTemplates field. Feature is invisible.

### Implementation

**Modified files:**

| File | Change |
|------|--------|
| `pkg/port-allocator/type.go` | Add `PortTemplateInfo{Base, Stride}` struct |
| `pkg/port-allocator/manager.go` | Add `AllocateBasePort()`, `DerivePortsForInstance()`, `CollectPortTemplates()`, `HasPortTemplate()`, `IsPortTemplateKey()` |
| `pkg/reconciler/roleinstanceset_reconciler.go` | In `allocateRoleScopedPortAnnotations`: allocate base port + write portTemplate annotations on creation, copy on update |
| `pkg/discovery/config_builder.go` | Add `PortTemplates` to `RoleInstances` struct, populate from RoleInstanceSet annotations |
| `internal/controller/workloads/rolebasedgroup_controller.go` | In `reconcileRefinedDiscoveryConfigMap`: look up RoleInstanceSet annotations, pass to ConfigBuilder |

**Unchanged files:**

| File | Why Unchanged |
|------|---------------|
| `pkg/port-allocator/random.go` | RandomAllocator still used for base port allocation |
| `pkg/port-allocator/parser.go` | Port config parsing unchanged |
| `InjectPortsIntoPod()` | Reads from RoleInstance annotation (per-pod format), which is populated regardless of allocation strategy |

### Test Plan

#### Unit Tests
- `AllocateBasePort`: returns 1 base port + correct stride per PodScoped portName
- `DerivePortsForInstance`: correct port derivation for instanceIndex=0,1,2 with various strides
- `CollectPortTemplates`: extracts PortTemplateInfo from annotations
- `HasPortTemplate` / `IsPortTemplateKey`: key detection
- ConfigBuilder outputs portTemplates in ConfigMap when RoleInstanceSet annotations are provided

#### Integration Tests
- Create RBG with port-allocator → verify RoleInstanceSet has portTemplate annotations
- Verify RoleInstance per-pod annotations match `base + index * stride + podIndex`
- Verify ConfigMap `config.yaml` contains `portTemplates` field
- Scale up → verify new instances get correct derived ports, ConfigMap portTemplates unchanged

#### E2E Tests
- hostNetwork PD deployment with port-allocator → verify cross-role port computation matches actual pod ports

### Graduation Criteria

**Alpha → Beta:**
- Production validation in hostNetwork PD-disaggregated inference (at least 1 user)
- Patio integration for dynamic ConfigMap consumption (separate KEP)
- Cross-role `references` support in KEP-171 using portTemplate

**Beta → GA:**
- Performance benchmarks: scale-up latency comparison (random vs template allocation)
- Multi-RBG collision rate analysis

## Relationship to KEP-133 and KEP-171

This KEP sits at the intersection of KEP-133 (ConfigMap refine) and KEP-171 (port allocation):

```
KEP-171 (Port Allocation)          KEP-133 (ConfigMap Refine)
    ┌────────────┐                     ┌────────────┐
    │ Random     │                     │ Compact    │
    │ per-pod    │                     │ topology   │
    │ allocation │                     │ format     │
    └──────┬─────┘                     └──────┬─────┘
           │                                  │
           │   This KEP bridges the gap       │
           │                                  │
    ┌──────▼──────────────────────────────────▼──────┐
    │  Port Template:                                │
    │  - Random base (from KEP-171 RandomAllocator)  │
    │  - Deterministic derivation (new)              │
    │  - ConfigMap portTemplates field (extends 133) │
    │  - Cross-role discovery (new capability)       │
    └────────────────────────────────────────────────┘
```

## Alternatives Considered

1. **Enumerate all ports in ConfigMap**: Rejected. ConfigMap grows to O(N) with replica count, negating KEP-133's size optimization.

2. **SequentialAllocator (global counter)**: Considered. A global `nextPort` counter guarantees no collision but requires state recovery on controller restart (scan all existing annotations). The two-level scheme (random base + sequential derivation) avoids this complexity while providing equivalent determinism within a role.

3. **Patio-based registry**: Deferred. Patio aggregating ports via a central service would provide real-time discovery but adds a dependency and latency. Port templates via ConfigMap are simpler and sufficient for the initial use case. Patio integration is planned as a follow-up.

4. **EndpointSlice with dynamic ports**: Not feasible. EndpointSlice ports are derived from `Service.spec.ports`, which are static. Dynamic per-pod ports cannot be reflected in EndpointSlice without significant K8s upstream changes.
