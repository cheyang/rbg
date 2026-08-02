# Cross-file composition sweep

## Why this exists

Two of the most serious findings on this branch (**F8** and **P1**) were not found by reading the
diff. Both have the same shape:

> Several individually reasonable decisions, in different files, composing into a breaking outcome
> that no single-file review would catch.

F8: a CRD default (`types.go`) → an unconditional copy (`conversion.go`) → a validator
(`validation.go`). P1: two reconcilers requesting different injector lists → two different pod
environments → a broken contract with user application code.

Both surfaced only after being pushed on. That is evidence the class was not exhausted, so this
sweep hunts the *shape* rather than re-reading the diff. Negative results are recorded on purpose:
they are what makes the coverage claim meaningful.

## Result: one systemic instance found, plus two extensions

| Probe | Question | Outcome |
|---|---|---|
| **A / G** | Who writes the resources whose webhook rules this PR changed? | ❌ **F9 + F10 — the main find** |
| **C** | Other CRD-default → conversion → validation chains? | ❌ **F8b** (F8 extends to RBGSet); no *new* chains |
| **B** | Conversion-set annotations read elsewhere as user intent? | ✅ clean (bounded, see below) |
| **E** | Can conversion emit a v1alpha2 object the CRD schema rejects? | ✅ clean |
| **F** | Divergent pod contracts across reconciler paths (beyond env)? | ⚠️ minor — label counterpart of P1 |
| **H** | Can `RoleTemplate` / `templateRef` bypass the new validation? | ✅ clean |

---

## F9 + F10 — the new UPDATE rule was added without auditing who writes the resource

This PR sets `verbs=create;update` on the `rolebasedgroups` validating webhook. **Three separate
controllers write that resource**, and all three are denied for a legacy-typed RBG once
compatibility is disabled. None sets a condition; all three retry forever.

| # | Call site | What it writes | Consequence |
|---|---|---|---|
| F2a | `rolebasedgroup_controller.go:362` | `ensureDiscoveryConfigMode` patches an annotation | the RBG can never be reconciled at all |
| **F9** | `rolebasedgroupset_controller.go:465` | RBGSet copies `groupTemplate.spec.roles` onto a child and `Update`s it | a **pre-existing** RBGSet with a legacy template error-loops forever |
| **F10** | `rolebasedgroupscalingadapter_controller.go:511` | `updateRoleReplicas` sets `role.Replicas` and `Update`s — **the HPA / scale path** | HPA-driven autoscaling of a legacy RBG fails |

Proven by `TestVerifyPR416_F9F10_ThreeControllersAreDeniedByTheNewUpdateRule`
(`api/workloads/v1alpha2/pr416_writers_sweep_test.go`). Each subtest reproduces the exact mutation
the production writer performs and has a control asserting the identical write is accepted with
compatibility enabled — so each denial is attributable to the flag, not to an invalid mutation.

Two things worth drawing out:

* **F9 is PR #414's F7 recurring.** #414's F7 was "RBGSet error-loops forever for a config error."
  This PR fixes that for *new* RBGSets by adding `RoleBasedGroupSetValidator` — but objects already
  in the cluster are never re-validated, and the child-sync path is exactly where the loop returns.
* **F10 lands on the autoscaling path**, and `retry.RetryOnConflict` only retries `Conflict`, so an
  admission `Forbidden` propagates straight out. This also reframes F2c: its real consequence is
  not "users cannot scale" but "HPA cannot scale."

## F8b — the defaulting chain also reaches RoleBasedGroupSet

The v1alpha1 `rolebasedgroupsets` schema carries the same
`+kubebuilder:default={apiVersion:"apps/v1", kind:"StatefulSet"}` on
`spec.template.roles[].workload`
(`config/crd/bases/workloads.x-k8s.io_rolebasedgroupsets.yaml:16672-16681`), and
`rolebasedgroupset_conversion.go:37` routes through the **same** `convertRoleV1alpha1ToV2`. So
**every v1alpha1 RoleBasedGroupSet is rejected wholesale too**, including ones whose template never
mentioned a workload type.

Consequence for the fix: **a remedy confined to the RBG validator would leave RBGSet broken.**
Proven by `TestVerifyPR416_F8b_V1alpha1RBGSetIsAlsoRejectedWholesale`, with a passing control.

---

## Negative results (coverage record)

### Probe C — no other complete defaulting chains

All **16** `+kubebuilder:default` markers in `api/workloads/v1alpha1/` were enumerated and traced
through conversion to any validator. Only the `Workload` chain is a genuine defaulting artifact.
Notably ruled out:

* `role.replicas` (default `1`) → `validation.go:133-140` (the `scalingAdapter.enable` guard) is a
  *technically complete* chain, but **v1alpha2 carries the identical default**, so a native
  v1alpha2 user hits exactly the same thing. No version asymmetry, no dead guard — not the same
  class, deliberately not reported as a finding.
* `Partition=0`, `MaxUnavailable=1`, `MaxSurge=0`, `RolloutStrategy.Type`, `MinReadySeconds=0`,
  `ScalingAdapter.Enable=false`, `LeaderWorkerSet.Size=1`, `ScheduleTimeoutSeconds=60`,
  `Progression=OrderScheduled`, RBGSet `Replicas=1` — each either isn't propagated, isn't read by
  any validator, or lands on a predicate the default value cannot satisfy.
* `clusterengineruntimeprofile` / `instance` defaults — those kinds implement no conversion at all.

### Probe E — conversion cannot produce a schema-invalid v1alpha2 object

Only one CEL rule reads the conversion-set annotation (`rolebasedgroup_types.go:199`), and it
*additionally* requires `leaderWorkerPattern.sharedServiceSelection == 'LeaderOnly'` — a field that
**does not exist in v1alpha1 and is never set by conversion**. The premise is unconstructible.
The remaining CEL rules (`:198`, `:313`, and the warmup rules) constrain v1alpha2-only fields that
conversion sets via mutually-exclusive `switch` branches, so it cannot emit both sides of a
mutual-exclusion rule.

### Probe H — no bypass via `templateRef`

`RoleTemplate` is **not** a separate CRD; templates are inline in `spec.roleTemplates` of the same
RBG, and a role's workload type still comes from its own annotation, which
`validateNoLegacyWorkloads` does inspect. `roletemplate_validation.go:112-129` reads the same
annotation but only rejects `InstanceSet` / `LeaderWorkerSet`, neither of which the v1alpha1 default
can produce.

Incidentally, the comment at `roletemplate_validation.go:107-111` confirms the codebase knows users
can hand-set the annotation on a native v1alpha2 object — supporting the design point that the
annotation is an undocumented back door.

### Probe F — minor: the label counterpart of P1

`LwsWorkerIndexLabelKey` (`leaderworkerset.sigs.k8s.io/worker-index`) is declared in **both**
`api/workloads/constants/external.go:28` and `api/workloads/v1alpha1/constant.go:153` and
referenced **nowhere else** — another dead constant, like the deprecated `LWS_*` env vars. LWS set
that label itself; the RoleInstanceSet path does not, using RBG's own `ComponentIndexLabelKey`
instead.

Same species as P1 (a pod-level contract silently renamed), but lower stakes: a label is less
likely to be load-bearing than an env var read by the application. Folded in as a secondary item of
P1 rather than filed separately. Anything selecting on `leaderworkerset.sigs.k8s.io/*` — a Service
selector, a PodMonitor, user tooling — would still stop matching after migration.

### Also checked, nothing found

* **No finalizers** on RoleBasedGroup or RoleBasedGroupSet, so the "object stuck Terminating because
  the webhook denies finalizer removal" scenario does **not** exist. (I had hypothesised it earlier;
  it is ruled out.)
* No writes to the RoleBasedGroupSet *main* resource — its controller only uses
  `Status().Update` (`rolebasedgroupset_controller.go:332`), which is a subresource and therefore
  exempt from the webhook rule.
* `rolebasedgroup_controller.go:923` updates a `RoleBasedGroupScalingAdapter`, which has no
  validating webhook — not affected.

---

## Honest limits of this sweep

* It targets **one shape** (cross-file composition). It is not a general audit.
* Probe E's dedicated agent stalled; the question was closed by hand using the CEL-rule enumeration
  above. The conclusion rests on the unconstructibility argument, not on an executed test.
* **P3 (LWS startup ordering) remains open** and needs a live run — see `LWS-PARITY.md`.
* No live/mutating arm was run for F9 or F10. Both rest on the unit layer asserting the real
  validator against the exact mutation each production writer performs. Driving the actual
  ScalingAdapter and RBGSet controllers against a cluster would be a stronger proof and is the
  obvious next step if the finding is disputed.
