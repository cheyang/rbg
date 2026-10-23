### Ⅰ. Motivation

Multi-node inference instances need rack/block locality, and prefill/decode roles need a common network domain for KV-cache transfer. This KEP defines portable gather-only topology intent while preserving KEP-430 gang semantics and one physical placement membership per Pod. Spread and native kube-scheduler translation are out of scope.

### Ⅱ. Modifications

Adds the English KEP and metadata under `keps/473-topology-aware-scheduling/`. The proposal covers:

1. **Portable topology vocabulary.** A cluster-scoped `topology.x-k8s.io/ClusterNetworkTopology` supports Managed materialization and Imported read-only binding. Its scheduler, ordered levels, and binding are immutable. Workload `topologyName` references this RBG CR, not a native KAI Topology or a controller-default override; `pack.required` and `pack.preferred` reference `status.levels`.
2. **Attachment points.** `RoleSpec.InstanceTopologyConstraint` packs each RoleInstance independently and requires RoleInstanceSet backing. CoordinatedPolicy topology rules gather exactly their explicit role sets; the policy binds by the RBG's name and namespace.
3. **One placement planner/compiler.** Gang and topology intents compose by `(Roles, PartitionBy)`: equal scopes merge, disjoint scopes remain independent, containment requires backend support, and partial overlap is rejected. Only the scheduler compiler renders PodGroups and Pod bindings, before Role creation. Gang-only rendering remains equivalent to KEP-430.
4. **Backend capability boundaries.** Phase 1 targets Volcano Managed topology with a restricted scope matrix, effective subgroup inheritance, restart recovery, resource-request checks, and startup-dependency validation. `PreferredAbsorbed` reports unanchored preferences; `ImplicitInstanceGang` reports instance-local `subGroupSize` thresholds. Topology-only does not mean universally gang-free dispatch. The inspected Koordinator baseline lacks continuous cross-batch placement, and unpatched KAI v0.18.6 has ambiguous domain-path encoding: both remain unsupported for workload topology until verified backend fixes and scheduler-running tests pass. Vocabulary Ready alone does not authorize placement. Koordinator Phase 2 additionally requires fail-closed native semantic immutability and accepted mapping checks for layer names, label keys, and parent links.
5. **Accepted contracts and recovery.** First acceptance reads the policy through uncached `apiReader`; immutable `topologyPolicyRequired` provides deterministic waiting for order-independent GitOps creation. Accepted declarations and physical layouts are frozen; late policies or incompatible changes do not overwrite bindings. KAI additionally snapshots the ordered alias/nodeLabel/depth mapping and renders stable nodeLabels. Explicit instance bindings use actual RI UIDs and an acknowledgement handshake. Legacy recordless RBGs conservatively bootstrap to none, including zero-replica/template-only cases and policies arriving before first reconciliation. Epoch-scoped CREATE provenance and a coordinated upgrade handover distinguish fresh lifecycles; reupgrade after an older controller rotates the epoch.
6. **Retention and deletion.** Managed native resources use topology-UID labels and explicit cleanup, without ownerReferences. Consumer reservations serialize acceptance against topology deletion. RBG, RI, PodGroup, and policy protection drains creators, Pods, and groups before release. Cleanup remains active after feature disablement or scheduler mismatch. Downgrade to an older controller requires draining and removing topology-bearing lifecycles first.
7. **Failure visibility and PRR.** Conditions and events report unresolved topology/levels, unsupported capabilities, mapping/layout changes, and lifecycle failures. Alpha uses the default-off `RBGTopologyAwareScheduling` gate, with enablement, disablement, rollback, observability, and metadata documented. An explicit RBAC matrix distinguishes Managed writes, Imported read-only access, and cross-namespace retention/cleanup permissions.

### Ⅲ. Scope

This is a design proposal; topology runtime implementation will follow in separate phase PRs.

The published branch contains only the two KEP files. The duplicate RoleInstance revision-selector diff already merged in #495 has been removed by publishing the rebased history.

### Ⅳ. Tests

The KEP specifies unit, integration, and scheduler-running e2e requirements for admission, naming, KEP-430 equivalence, capability gates, contract/layout freeze, binding ordering, multi-writer status, consumer retention, real GC, restart, legacy upgrade/downgrade bootstrap, native semantic guards, and rollback. These are implementation acceptance requirements, not topology tests implemented or executed by this documentation PR.

### Ⅴ. Verification

Review `README.md` and `kep.yaml` in the KEP directory. Run Markdown validation with:

```sh
npx --yes markdownlint-cli2 --config .github/.markdownlint.json keps/473-topology-aware-scheduling/README.md
git diff --check
```

### Ⅵ. Review notes

- Unsupported plans must fail explicitly, without dropping hard topology or declared gang minima.
- Upper-level failure gating is not a global pause of downstream Pod recovery.
- Koordinator and KAI examples are gated future targets, not certification of the inspected baselines.
- HyperNode readable prefixes sanitize dots before truncation while retaining full identity hash inputs. Naming validation is scoped to topology compilation without tightening topology-free KEP-430 admission.
- Rejected unselected late policies acquire no topology protection; removing them can resume an existing none/role-only lifecycle. Applying their topology requires a new lifecycle.

