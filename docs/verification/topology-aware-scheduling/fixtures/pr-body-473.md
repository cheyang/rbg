### Ⅰ. Motivation

RBG workloads are increasingly topology-sensitive:

1. **Multi-node instance packing.** A single inference instance may span several machines, and its pods must land inside one high-performance network domain (rack/block) to keep cross-machine RDMA traffic off oversubscribed spine links.
2. **Prefill–decode co-location.** PD-disaggregated serving continuously transfers KV cache between prefill and decode; those roles need to stay inside the same network block, while unrelated roles should remain outside that constraint.

Volcano, Koordinator, and KAI all model this, but each uses a different topology dialect. RBG should let users declare topology intent once and have it compiled faithfully by the configured scheduler.

### Ⅱ. Modifications

Adds `keps/473-topology-aware-scheduling/`:

- **English KEP**: `README.md`
- **Metadata**: `kep.yaml`

Main design points:

1. **Public API**
   - `RoleSpec.InstanceTopologyConstraint`
     - group = one `RoleInstance`
     - each instance independently selects a topology domain
     - pattern-independent, applies to `LeaderWorkerPattern` and `CustomComponentsPattern`
   - `CoordinatedPolicy.spec.policies[].strategy.scheduling.topologyConstraint`
     - group = all pods of the rule’s `roles`
   - `TopologyConstraint.topologyName`
     - selects the scheduler topology resource where needed, currently KAI’s `Topology` CR
   - `pack.required` / `pack.preferred`
     - hard and soft gather semantics

2. **Scheduler-independent placement planning**
   - Gang and topology no longer independently partition PodGroups.
   - The planner resolves all placement intents into one logical `PlacementPlan` / `PlacementGroup` tree.
   - Gang and topology are attributes of the same logical group.
   - Scopes are compared as `(Roles, PartitionBy)`:
     - equal scopes merge
     - disjoint scopes remain independent
     - contained scopes form a parent/child tree
     - partially overlapping scopes are rejected
   - The scheduler compiler is the only component that renders physical PodGroups and pod bindings.

3. **Scheduler compilation**
   - Volcano, Koordinator, and KAI each compile the same logical plan into their own dialect.
   - Volcano uses `networkTopology` and `subGroupPolicy[].networkTopology`.
   - Koordinator uses `network-topology-spec` gather strategies.
   - KAI uses nested subGroups and `topologyConstraint`.
   - Topology-only groups do not create gang semantics:
     - Volcano: `minMember: 0`
     - KAI: `minSubGroup: 0` / `minMember: 0`

4. **KAI topology identity**
   - KAI levels are `nodeLabel` keys, not aliases.
   - `TopologyConstraint.topologyName` selects the KAI `Topology` CR.
   - The controller default can be overridden by the API field.
   - Missing or inconsistent topology names are reported as `TopologyResourceUnresolved` / `IncompatibleTopologyNames`.

5. **Mutability**
   - Topology declarations are immutable for the RBG and CoordinatedPolicy lifecycle.
   - Admission rejects in-place additions, removals, and changes.
   - To change topology, delete and recreate the affected workload and, when needed, its topology-bearing CoordinatedPolicy.
   - An active topology-bearing CoordinatedPolicy is protected by a controller-managed finalizer until its matching RBG is deleted, preventing delete/recreate from bypassing immutability.
   - Rolling updates that change topology constraints are out of scope.

6. **Explicit failure semantics**
   - Reconcile validates:
     - level existence
     - parent/child level ordering
     - preferred level not broader than required
     - topology resource existence
     - scheduler capability
   - Failures surface as conditions and events:
     - `PlacementPlanReady=False`
     - `TopologyTranslated=False`
     - `PreferredAbsorbed=True`
     - `TopologyConstraintActive=True`
   - Role create/update is gated when the plan or translation is invalid.

7. **KEP-430 compatibility**
   - `ResolveGangStrategy` semantics are unchanged.
   - Gang-only workloads must produce the same PodGroup rendering as KEP-430.
   - Gang and topology can compose on the same logical scope.
   - A pod still has exactly one PodGroup membership.
   - The scheduler compiler owns physical PodGroups and pod bindings, avoiding order-dependent translation.

### Ⅲ. Does this pull request fix one issue?

ref #473

This PR adds the KEP and does not close the tracking issue. Implementation PRs will follow the phase plan.

### Ⅳ. List the added test cases (unit test/integration test) if any, please explain if no tests are needed.

No runtime code is added in this PR.

The KEP includes a test plan for:

- placement planning
- KEP-430 equivalence
- topology-only rendering
- topology translation
- mutability
- scheduler capability detection
- admission and reconcile validation
- integration and e2e coverage

### Ⅴ. Describe how to verify it

Review:

- `keps/473-topology-aware-scheduling/README.md`
- `keps/473-topology-aware-scheduling/kep.yaml`

Key design decisions worth reviewing:

1. Placement planning is scheduler-independent.
2. Gang and topology are attributes of the same logical placement group.
3. PodGroup membership has one owner: the scheduler compiler.
4. Role-level constraints are named `InstanceTopologyConstraint`.
5. Cross-role constraints live on CoordinatedPolicy rules.
6. KAI topology identity is explicit via `topologyName`.
7. Topology constraints are immutable for the workload lifecycle.
8. Unsupported scheduler capabilities are reported, not approximated.

### Ⅵ. Special notes for reviews

- This KEP intentionally narrows alpha topology rules to whole-role-scope co-location.
- Segment-level grouping and spread are not part of this KEP.
- Native kube-scheduler translation is explicitly unsupported.
- The Chinese translation is kept in-tree with the English KEP.

## Checklist

- [ ] Format your code `make fmt`.
- [ ] Add unit tests or integration tests.
- [ ] Update the documentation related to the change.


