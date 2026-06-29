# KEP: Topology-Aware Scheduling

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
  - [Competitive Landscape](#competitive-landscape)
  - [Academic Validation](#academic-validation)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
    - [Story 1: NVSwitch Domain Affinity for Multi-GPU Inference](#story-1-nvswitch-domain-affinity-for-multi-gpu-inference)
    - [Story 2: Cross-Role Zone Affinity for PD Disaggregated Inference](#story-2-cross-role-zone-affinity-for-pd-disaggregated-inference)
    - [Story 3: E/P/D Multimodal Inference Placement](#story-3-epd-multimodal-inference-placement)
- [Design Details](#design-details)
  - [API: Annotation-Based Configuration](#api-annotation-based-configuration)
  - [Intra-Role Topology Affinity](#intra-role-topology-affinity)
  - [Inter-Role Topology Affinity](#inter-role-topology-affinity)
  - [Interaction with Existing Features](#interaction-with-existing-features)
  - [Implementation](#implementation)
  - [Future: Promotion to Spec Fields](#future-promotion-to-spec-fields)
  - [Test Plan](#test-plan)
  - [Graduation Criteria](#graduation-criteria)
- [Alternatives Considered](#alternatives-considered)
<!-- /toc -->

## Summary

This KEP introduces topology-aware scheduling for RBG, enabling users to express placement preferences based on infrastructure topology (NVSwitch domains, availability zones, racks, etc.). Two complementary capabilities are proposed:

1. **Intra-role topology affinity**: Pods of the same role prefer (or require) the same topology domain.
2. **Inter-role topology affinity**: Pods of different roles prefer (or require) the same topology domain.

These complement the existing exclusive-topology feature (which ensures 1:1 exclusive ownership of topology domains) and address a critical gap in distributed inference orchestration.

## Motivation

Distributed inference workloads (PD disaggregated, E/P/D multimodal, multi-node tensor parallel) are highly sensitive to physical placement. Performance can vary 2-7x depending on whether communicating pods share a GPU interconnect domain, zone, or rack.

### Current Gap

RBG supports exclusive-topology (all pods of an RBG/RBGS land on the same topology domain), but lacks:
- **Same-role clustering**: Multiple Decode workers should prefer the same NVSwitch domain for NCCL communication.
- **Cross-role proximity**: Prefill and Decode should prefer the same zone to minimize KV Cache transfer latency.

### Goals

1. Allow users to declare intra-role topology preferences per role (e.g., "Decode pods prefer same NVSwitch domain").
2. Allow users to declare inter-role topology preferences at the group level (e.g., "Prefill and Decode prefer same zone").
3. Support both `Preferred` (soft) and `Required` (hard) scheduling modes.
4. Reuse Kubernetes-native PodAffinity mechanism — no custom scheduler required.
5. Coexist cleanly with exclusive-topology and in-place scheduling features.

### Non-Goals

1. Automatic topology detection (e.g., NVLink domain auto-discovery from DRA). This is a Phase 2 capability.
2. Topology-aware autoscaling decisions. This KEP addresses only initial placement.
3. Replacing or modifying the scheduler. All topology hints are expressed via standard Pod Affinity/Anti-Affinity.

## Background

### Competitive Landscape

| Project | Topology-Aware Scheduling | Mechanism |
|---------|---------------------------|-----------|
| **Grove (NVIDIA)** | NVLink/NVSwitch domain-aware, rack/zone-aware | Custom scheduler integration (KAI Scheduler) |
| **llm-d** | None (routing-focused) | N/A |
| **KServe** | None (serving-focused) | N/A |
| **RBG (this KEP)** | Zone/NVSwitch domain-aware | Standard K8s Pod Affinity |

RBG's approach is differentiated: **no custom scheduler dependency**. By expressing topology hints as standard Pod Affinity rules, RBG works with any K8s scheduler (default, Volcano, Scheduler Plugins, etc.).

### Academic Validation

- **EPD (ICML 2025)**: Encode/Prefill/Decode disaggregation shows co-locating P and D in the same zone reduces KV Cache transfer latency by 71%.
- **Mooncake (FAST 2025)**: Production PD disaggregation at Moonshot AI shows 75% throughput improvement, but only when P/D instances share low-latency interconnect.
- **ZCube (SIGCOMM 2025)**: Network topology significantly impacts PD-disaggregated inference; locality-aware placement reduces tail latency.

## Proposal

### User Stories

#### Story 1: NVSwitch Domain Affinity for Multi-GPU Inference

As an inference operator, I deploy a Decode role with 8 replicas across a cluster with multiple NVSwitch domains. I want Decode pods to prefer the same NVSwitch domain for fast all-reduce communication during tensor parallelism.

```yaml
roles:
  - name: decode
    replicas: 8
    annotations:
      rbg.workloads.x-k8s.io/role-intra-topology: "nvidia.com/nvswitch-domain"
      rbg.workloads.x-k8s.io/role-intra-topology-policy: "Preferred"
```

#### Story 2: Cross-Role Zone Affinity for PD Disaggregated Inference

As an inference operator, I deploy a PD-disaggregated inference service where Prefill produces KV Cache that Decode consumes. I want Prefill and Decode pods in the same availability zone to minimize cross-zone KV Cache transfer latency, but the Router role can be anywhere.

```yaml
metadata:
  annotations:
    rbg.workloads.x-k8s.io/group-inter-role-topology: "topology.kubernetes.io/zone"
    rbg.workloads.x-k8s.io/group-inter-role-topology-roles: "prefill,decode"
```

#### Story 3: E/P/D Multimodal Inference Placement

As an inference operator, I deploy an E/P/D multimodal service. I want:
- Encode pods clustered on the same node (NVSwitch domain) for GPU-to-GPU image embedding transfer.
- Encode, Prefill, and Decode all in the same zone.

```yaml
metadata:
  annotations:
    rbg.workloads.x-k8s.io/group-inter-role-topology: "topology.kubernetes.io/zone"
    rbg.workloads.x-k8s.io/group-inter-role-topology-roles: "encode,prefill,decode"
roles:
  - name: encode
    annotations:
      rbg.workloads.x-k8s.io/role-intra-topology: "nvidia.com/nvswitch-domain"
      rbg.workloads.x-k8s.io/role-intra-topology-policy: "Required"
```

## Design Details

### API: Annotation-Based Configuration

Following the existing pattern of exclusive-topology and gang-scheduling, topology-aware scheduling is configured via annotations. This avoids API changes in the alpha stage and allows promotion to spec fields in beta based on usage feedback.

### Intra-Role Topology Affinity

**Annotations (role-level, set in `role.annotations`):**

| Annotation | Description | Default |
|-----------|-------------|---------|
| `rbg.workloads.x-k8s.io/role-intra-topology` | Topology key (e.g., `nvidia.com/nvswitch-domain`) | Not set |
| `rbg.workloads.x-k8s.io/role-intra-topology-policy` | `Preferred` or `Required` | `Preferred` |

**Behavior:**

The controller injects a PodAffinity rule matching pods with the same `rbg.workloads.x-k8s.io/group-name` AND `rbg.workloads.x-k8s.io/role-name` labels:

- **Preferred**: `preferredDuringSchedulingIgnoredDuringExecution` with weight 80.
- **Required**: `requiredDuringSchedulingIgnoredDuringExecution`.

Weight 80 is chosen to be lower than exclusive-topology (which uses `required`) but higher than inter-role affinity (weight 60), establishing a priority: exclusive > intra-role > inter-role.

### Inter-Role Topology Affinity

**Annotations (group-level, set on RBG `metadata.annotations`):**

| Annotation | Description | Default |
|-----------|-------------|---------|
| `rbg.workloads.x-k8s.io/group-inter-role-topology` | Topology key (e.g., `topology.kubernetes.io/zone`) | Not set |
| `rbg.workloads.x-k8s.io/group-inter-role-topology-policy` | `Preferred` or `Required` | `Preferred` |
| `rbg.workloads.x-k8s.io/group-inter-role-topology-roles` | Comma-separated participating roles | All roles |

**Behavior:**

The controller injects a PodAffinity rule matching pods with the same `rbg.workloads.x-k8s.io/group-name` label (any role):

- **Preferred**: `preferredDuringSchedulingIgnoredDuringExecution` with weight 60.
- **Required**: `requiredDuringSchedulingIgnoredDuringExecution`.

Only pods belonging to roles listed in `group-inter-role-topology-roles` receive the affinity injection. If this annotation is absent, all roles participate.

### Interaction with Existing Features

**Exclusive Topology**: If exclusive-topology and intra-role topology use the **same** topology key, the intra-role injection is skipped (exclusive already implies a stronger co-location constraint). Different keys work independently.

**In-Place Scheduling**: Topology-aware scheduling and in-place scheduling operate at different levels (topology domain vs. specific node) and compose naturally without conflict.

**Gang Scheduling**: Topology affinity is injected before PodGroup labels. The scheduler evaluates both gang and topology constraints simultaneously.

### Implementation

**Injection point**: `pkg/reconciler/pod_reconciler.go` → `ConstructPodTemplateSpecApplyConfiguration()`, after exclusive-topology injection.

**New functions**:
- `setIntraRoleTopologyAffinity(pod, rbg, role, topologyKey, policy)`
- `setInterRoleTopologyAffinity(pod, rbg, topologyKey, policy)`
- `shouldApplyInterRoleTopology(roleName, rolesCSV) bool`

**New constants** in `api/workloads/constants/annotation.go`:
- `IntraRoleTopologyKey`, `IntraRoleTopologyPolicyKey`
- `InterRoleTopologyKey`, `InterRoleTopologyPolicyKey`, `InterRoleTopologyRolesKey`
- `TopologyPolicyPreferred`, `TopologyPolicyRequired`

### Future: Promotion to Spec Fields

In v1beta1, topology annotations should be promoted to spec fields:

```yaml
spec:
  scheduling:
    topologyPolicy:
      intraRole:
        topologyKey: "nvidia.com/nvswitch-domain"
        policy: "Preferred"
      interRole:
        topologyKey: "topology.kubernetes.io/zone"
        policy: "Preferred"
        roles: ["prefill", "decode"]
```

This is deferred to align with the API stabilization timeline (Phase 0.2 in the project roadmap).

### Test Plan

#### Unit Tests
- Intra-role preferred/required affinity injection
- Inter-role preferred/required affinity injection
- Role filtering via `shouldApplyInterRoleTopology`
- Interaction with exclusive-topology (same key → skip)
- No injection when annotation absent

#### E2E Tests
- Deploy RBG with intra-role topology annotation → verify Pod Affinity rules on created Pods
- Deploy RBG with inter-role topology annotation → verify Pod Affinity on participating roles only
- Deploy RBG with both exclusive and intra-role topology (same key) → verify no duplicate injection

### Graduation Criteria

**Alpha → Beta:**
- At least 2 production users validating the feature
- Promotion from annotations to spec fields
- Integration tests with Volcano and Scheduler Plugins

**Beta → GA:**
- Topology auto-detection (Phase 2)
- Performance benchmarks showing throughput improvement with topology-aware placement

## Alternatives Considered

1. **Custom scheduler plugin**: Rejected. Adding a scheduler dependency limits RBG's portability. Standard Pod Affinity works with any scheduler.

2. **Topology constraints in CRD spec from day 1**: Deferred. Annotation-based approach allows experimentation without API commitment. Will promote to spec in v1beta1 based on usage patterns.

3. **Node-level affinity instead of Pod affinity**: Rejected. Node affinity would require pre-labeling nodes with topology keys. Pod affinity is self-describing and works with dynamic topologies.
