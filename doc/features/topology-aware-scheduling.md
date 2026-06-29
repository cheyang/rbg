# Topology-Aware Scheduling

Topology-aware scheduling allows you to express scheduling preferences based on infrastructure topology (e.g., NVSwitch domains, availability zones, racks). This is critical for distributed inference workloads where cross-role and cross-pod communication performance depends on physical placement.

RBG supports two levels of topology-aware scheduling:

1. **Intra-role topology affinity**: Pods of the same role prefer (or require) landing on the same topology domain.
2. **Inter-role topology affinity**: Pods of different roles prefer (or require) landing on the same topology domain.

These complement the existing [exclusive topology](exclusive-topology.md) feature, which ensures 1:1 exclusive ownership of topology domains.

## Intra-Role Topology Affinity

Use case: Multi-GPU inference workers of the same role should land on the same NVSwitch domain for fast GPU interconnect.

### Annotations

Set on the role's `annotations` field:

| Annotation | Description | Default |
|-----------|-------------|---------|
| `rbg.workloads.x-k8s.io/role-intra-topology` | Topology key (e.g., `nvidia.com/nvswitch-domain`) | Not set |
| `rbg.workloads.x-k8s.io/role-intra-topology-policy` | `Preferred` or `Required` | `Preferred` |

### Example

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: inference-cluster
spec:
  roles:
    - name: decode
      replicas: 4
      annotations:
        rbg.workloads.x-k8s.io/role-intra-topology: "nvidia.com/nvswitch-domain"
        rbg.workloads.x-k8s.io/role-intra-topology-policy: "Preferred"
      standalonePattern:
        template:
          spec:
            containers:
              - name: engine
                image: sglang:latest
```

### How It Works

The controller injects a PodAffinity rule into each Pod:
- **Preferred mode** (default): `preferredDuringSchedulingIgnoredDuringExecution` with weight 80
- **Required mode**: `requiredDuringSchedulingIgnoredDuringExecution`

The label selector matches Pods with the same `rbg.workloads.x-k8s.io/group-name` and `rbg.workloads.x-k8s.io/role-name`.

## Inter-Role Topology Affinity

Use case: Prefill and Decode roles should prefer the same availability zone to minimize KV Cache transfer latency across roles.

### Annotations

Set on the RBG's `metadata.annotations`:

| Annotation | Description | Default |
|-----------|-------------|---------|
| `rbg.workloads.x-k8s.io/group-inter-role-topology` | Topology key (e.g., `topology.kubernetes.io/zone`) | Not set |
| `rbg.workloads.x-k8s.io/group-inter-role-topology-policy` | `Preferred` or `Required` | `Preferred` |
| `rbg.workloads.x-k8s.io/group-inter-role-topology-roles` | Comma-separated list of participating roles | All roles |

### Example

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: pd-inference
  annotations:
    rbg.workloads.x-k8s.io/group-inter-role-topology: "topology.kubernetes.io/zone"
    rbg.workloads.x-k8s.io/group-inter-role-topology-roles: "prefill,decode"
spec:
  roles:
    - name: prefill
      replicas: 2
      ...
    - name: decode
      replicas: 4
      ...
    - name: router
      replicas: 1
      # Router is NOT listed in inter-role-topology-roles, so it has no zone preference
      ...
```

### How It Works

The controller injects a PodAffinity rule into each participating role's Pods:
- **Preferred mode** (default): `preferredDuringSchedulingIgnoredDuringExecution` with weight 60
- **Required mode**: `requiredDuringSchedulingIgnoredDuringExecution`

The label selector matches Pods with the same `rbg.workloads.x-k8s.io/group-name` (any role within the same RBG).

## Interaction with Exclusive Topology

If both exclusive topology and intra-role topology are set with the **same** topology key, the intra-role affinity injection is skipped because the exclusive topology already implies a stronger constraint (all Pods of the RBG land on the same domain).

## Interaction with In-Place Scheduling

Topology-aware scheduling and [in-place scheduling](exclusive-topology.md) can be used together. In-place scheduling adds node-level affinity (hostname), while topology-aware scheduling adds domain-level affinity (zone, NVSwitch domain, etc.).

## Full Example

See [`examples/basic/rbg/scheduling/topology-aware.yaml`](../../examples/basic/rbg/scheduling/topology-aware.yaml) for a complete PD-disaggregated inference example combining intra-role and inter-role topology affinity.
