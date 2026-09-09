# PR 463 review findings — heal legacy update strategy types via mutating webhook

PR: https://github.com/sgl-project/rbg/pull/463
Base: main (07fc643)  Head: origin/pr/463 (diw-zw:fix/pr-436-review-findings)

## Problem being solved (claim P0 — verify against BASE first)

- **Claimed symptom**: after upgrading from a pre-enum release, any spec write to an
  object carrying a legacy `""` or v1alpha1 `Recreate` update-strategy value fails with
  HTTP 422; on k8s >= 1.31 validation ratcheting lets the invalid value persist only
  until the next write that touches it.
- **Claimed mechanism**: PR #436 added CRD enums that reject `""`/`Recreate`; the
  v1alpha1 conversion webhook did not map `Recreate` -> `RecreatePod`, so writing through
  v1alpha1 kept producing the invalid value.
- **Claimed affected component**: `RoleInstanceSet.updateStrategy.type` enum, with the
  legacy value originating in `RoleBasedGroup.spec.roles[].rolloutStrategy.rollingUpdate.type`
  and copied through by the reconciler.
- **No closing issue**; `#436` is a bare reference (follow-up), not `fixes`. PR body states
  the problem itself, so review against the body.

### Premise refinement (a finding, not a refutation)
The PR body says #436 added enums to BOTH the RBG `rolloutStrategy.rollingUpdate.type`
AND the RIS `updateStrategy.type`. The generated CRDs (base AND PR, identical here) show
the enum ONLY on the RoleInstanceSet field (`config/crd/bases/...roleinstancesets.yaml`
~line 263: `enum: [RecreatePod, InPlaceIfPossible, ...]`). The RBG/RBGS
`rollingUpdate.type` has no enum — only `type: string` with a description.

Implication: a spec write to the **RBG** carrying `""`/`Recreate` does NOT itself 422
(no enum there). The 422 surfaces on the **RoleInstanceSet** when the reconciler copies
the legacy value through (`constructRoleInstanceSetApplyConfiguration`). The fix is still
correct — normalizing at the RBG layer prevents the invalid value reaching the RIS — but
the documented symptom location is imprecise. **Live verify where the 422 actually occurs.**

## Findings

### F1 — Premise: enum rejects legacy values; self-heal on write (claim P0)
- severity: blocker (unverified behavioral assumption — must prove on live cluster)
- The whole fix rests on: (a) the RIS CRD enum rejects `Recreate`/`""` on write, and
  (b) the mutating webhook + reconciler normalization heal it. Prove both against base
  and against PR on the ACK cluster.

### F2 — failurePolicy: Fail on the new RoleInstanceSet mutating webhook
- severity: major (availability, unverified)
- file: config/webhook/manifests.yaml; cmd/rbgs/main.go
- RoleInstanceSet previously had NO admission webhook. This PR adds a mutating webhook
  with failurePolicy=Fail. RIS is controller-created via SSA, so the controller's own
  apply calls its own webhook. Confirm the controller bootstraps and reconciles without
  deadlock when the mutating webhook is registered, and that RIS creation still succeeds
  once caBundle is injected.

### F3 — RIS defaulter vs reconciler normalization redundancy
- severity: nit
- file: api/workloads/v1alpha2/roleinstanceset_default.go; pkg/reconciler/roleinstanceset_reconciler.go
- The reconciler already calls NormalizeUpdateStrategyType before SSA-apply; the RIS
  mutating defaulter is redundant on the controller path but useful for direct user
  writes. Harmless. Worth a one-line comment cross-referencing the two.

### F4 — snapshot objectBumps accounts only for the Recreate fixture's RIS repair
- severity: minor (test-internal consistency, upgrade-suite only)
- file: test/e2e/upgrade/snapshot.go
- objectBumps records a one-off gen bump for `RoleInstanceSet/<legacyStrategyRISName()>`
  (the `Recreate` fixture) but NOT for the `fxLegacyStrategyEmpty` fixture's RIS. This is
  only correct if v0.7.0's controller already stored `InPlaceIfPossible` for the empty
  case (so no repair bump). Cannot fully confirm on a fresh cluster without the v0.7.0
  upgrade fixtures; flag for the upgrade e2e lane.

## Coverage assessment (positive)
- Unit: NormalizeUpdateStrategyType, all three defaulters, conversion round-trip
  (empty preserved, Recreate<->RecreatePod), certmanager mutate patch/not-found.
- Reconciler: legacy/empty normalization on construct.
- Envtest: all 3 valid types accepted + update-path (flip paused) on a valid object.
- E2e upgrade: phase 1 stores legacy values (incl. explicit `type:""` via unstructured),
  phase 3 asserts they survive untouched, phase 4 asserts the mutating webhook heals them
  on a metadata-only write. CI bootstrap gate now waits for caBundle on the mutating config.
- RBAC: mutatingwebhookconfigurations get/list/watch/patch — same scope/pattern as the
  existing validating rule (cluster-scoped resource; least-privilege within that constraint).
- Mutating webhook config name `rbgs-mutating-webhook-configuration` is consistent across
  kustomize (namePrefix `rbgs-`), helm, and kubectl — matches the cert-manager const.
