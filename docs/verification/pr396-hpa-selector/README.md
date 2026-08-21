# Verification — PR #396 (`Enable HPA scaling for RoleBasedGroupSet`)

Reviewer-private verification harness for
<https://github.com/sgl-project/rbg/pull/396>.

| | |
|---|---|
| Reviewed head | `6f14e37a0296ccd74ec697627695377e03414993` |
| Merge base | `b2b92ba5` |
| Author | @LoadingZhang, head branch `keda-rbgs-scale-selector` |
| Size | +118 / −18, 13 files |
| Round | 1 |
| PR state when reviewed | `open`, `mergeable_state=dirty` (conflicts with `main`), already `CHANGES_REQUESTED` |

**Production code on this branch is untouched.** The branch is the PR head plus two
harness test files and this `docs/verification/` tree.

---

## Observed vs. expected

| ID | Severity | Claim | Verdict | Evidence |
|----|----------|-------|---------|----------|
| **F1** | **blocker** | The scale subresource's three paths disagree on a unit: `spec/status.replicas` count **groups** but `status.selector` matches **Pods**, and HPA derives the replica count it writes from the **Pod** count | **Reproduced** | [`units-mismatch.txt`](results/units-mismatch.txt) |
| **F2** | major | On upgrade the selector is published before any Pod carries the new label, so HPA fails outright until every Pod in the set has been re-created | **Reproduced** | ditto |
| F3 | non-blocking | The `maps.Clone(matchLabels)` guards are **load-bearing**: the PR's own new code mutates the caller's map, and that map also feeds the RoleInstanceSet's **immutable** selector | **Reproduced** (with control) | [`nilmap-falsepositive.txt`](results/nilmap-falsepositive.txt) |
| D1 | — | Copilot ×4: "`maps.Clone` returns nil, writing to it panics" | **Disproved** (unreachable) | ditto |
| D2 | — | My own round-1 hypothesis that the PR fixes leader/worker label cross-contamination | **Disproved** (no contamination on base) | ditto |

---

## F1 — the blocker, in one paragraph

The PR declares
`+kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas,selectorpath=.status.selector`
and sets `status.selector = groupset-name=<name>`, propagating that label down onto Pod
templates so the selector matches something. But `status.replicas` is
`int32(len(rbglist.Items))` — a count of **child RoleBasedGroups** — while the selector
matches **every Pod of every role of every child**. Those are different units, and the HPA
controller does not treat the selector as merely informational: from Kubernetes
`pkg/controller/podautoscaler/replica_calculator.go` (release-1.32, `GetResourceReplicas`,
archived verbatim at [`k8s-replica_calculator-1.32.go`](results/k8s-replica_calculator-1.32.go)):

```go
podList, err := c.podLister.Pods(namespace).List(selector)   // selector selects PODS
...
if len(podList) == 0 {
    return 0, 0, 0, time.Time{}, fmt.Errorf("no pods returned by selector while calculating replica count")
}
readyPodCount, unreadyPods, missingPods, ignoredPods := groupPods(podList, ...)
...
if math.Abs(1.0-usageRatio) <= c.tolerance {
    return currentReplicas, ...                              // inside tolerance: no change
}
return int32(math.Ceil(usageRatio * float64(readyPodCount))), ...   // <-- POD count
```

HPA computes `ceil(usageRatio × readyPodCount)` and writes it to `spec.replicas`, which this
CRD defines as a **group** count. So every scale decision is off by the pods-per-group
factor. Measured, driving the real `updateStatus`:

```
groups=2 podsPerGroup=2 -> status.replicas=2, selector matched 4 pods
  at usageRatio=1.50: HPA writes spec.replicas=6 (groups); correct would be 3   -> 2.0x
groups=2 podsPerGroup=5 -> status.replicas=2, selector matched 10 pods
  at usageRatio=1.50: HPA writes spec.replicas=15 (groups); correct would be 3  -> 5.0x
groups=3 podsPerGroup=1 -> 5 vs 5                                               -> PASS (control)
```

The control matters: a group containing exactly one Pod is the only shape where the two
units coincide, and it passes. Every shape the PR description motivates itself with
("1P1D", "2P3D") has more than one Pod per group.

The closed-loop consequence is worse than a single bad decision, because the loop has a
**wrong fixed point** rather than an overshoot it later corrects:

```
load needs 8 pods = 4 groups; starting from 2 groups
iter 0: groups=2 pods=4  usageRatio=2.000 -> HPA wants 8 groups
iter 1: groups=8 pods=16 usageRatio=0.500 -> HPA wants 8 groups   <-- settles here
```

It stops at 8 groups when 4 is correct and never scales back down, because
`ceil(0.5 × 16) = 8`. Utilization settles at roughly `1/podsPerGroup` of target, i.e. the
set is permanently over-provisioned by the pods-per-group factor.

This is not fixable by changing the selector alone: making the selector match *groups*
instead of Pods yields zero Pods and the hard error on line 11. The scale subresource needs
`status.replicas` and the selector to agree on one unit — or the feature needs `Object` /
`External` metrics instead of `Resource` metrics.

## F2 — the upgrade path

`status.selector` is published the moment the new controller reconciles, but the
`groupset-name` label reaches Pods only when their templates are re-rendered. Until then the
selector matches nothing and `GetResourceReplicas` returns
`no pods returned by selector while calculating replica count` — HPA cannot scale the set in
either direction. Proved with a control (a Pod built by the new code path *does* match, so
the selector itself is not simply wrong). Nothing in the PR sequences this, warns about it,
or documents that adopting the feature triggers a full re-roll of every
RoleBasedGroupSet-owned Pod.

## F3 — the clones are load-bearing, and that is fragile

The PR adds to `pkg/reconciler/pod_reconciler.go`:

```go
groupSetLabels := rbg.GetGroupSetLabels()
if len(groupSetLabels) > 0 && podLabels == nil { podLabels = make(...) }
for k, v := range groupSetLabels { podLabels[k] = v }   // writes the CALLER's map
```

`constructRoleInstanceSetApplyConfiguration` reuses the same `matchLabels` for
`spec.selector.matchLabels`, and a RoleInstanceSet selector is **immutable**. Proved:

* **Control A** — calling the real function with an *uncloned* map: the map gains
  `groupset-name`/`groupset-index` (3 keys → 5). The mutation is real.
* **B** — through the production path the selector stays exactly 3 keys, while the Pod
  template does get the groupset labels. Protected today, only because every call site
  wraps the argument in `maps.Clone`.

So the clones are not a readability refactor; they are the only thing preventing groupset
labels from leaking into an immutable selector, which would require deleting and recreating
the workload to repair. A future caller that forgets one reintroduces that. Making
`ConstructPodTemplateSpecApplyConfiguration` copy its argument instead of mutating it would
make the invariant local rather than a contract with all eight call sites.

---

## Disproved

Recorded so later rounds do not re-litigate them.

**D1 — Copilot's four nil-map panic comments are FALSE POSITIVES, on reachability
grounds.** The mechanism is real and is proved separately (`maps.Clone(nil)` returns nil;
writing to it panics `assignment to entry in nil map`), so the finding must not be waved
away as "`maps.Clone` is safe". It is unreachable: `.Labels` is populated by
`WithLabels(podLabels)` at the end of `ConstructPodTemplateSpecApplyConfiguration`, the
generated `WithLabels` allocates whenever `len(entries) > 0`, and all eight production call
sites pass `rbg.GetCommonLabelsFromRole(role)` — a three-key map *literal* that is never nil
or empty, verified even for a zero-valued RoleBasedGroup and a zero-valued RoleSpec. A
control reproduces the panic with `podLabels == nil`, so the harness is sensitive enough to
have caught the bug had any caller reached it.

**D2 — the PR does NOT fix leader/worker label cross-contamination.** This was my own
round-1 hypothesis and it is wrong. Running the same assertions at base `b2b92ba5` and head
`6f14e37a` produced byte-identical leader/worker label maps, and the aliasing sentinel check
passes on base too. The generated `WithLabels` *copies* entries into a freshly allocated map
rather than aliasing the argument, so each apply-config already had its own map before this
PR. The real hazard the clones address is F3, which the PR itself introduces.

---

## Layers

| Layer | What it proves | How to run |
|-------|----------------|------------|
| **unit** (controller) | F1, F2 | `go test ./internal/controller/workloads/ -run TestVerifyPR396 -v -count=1` |
| **unit** (reconciler) | F3, D1, D2 | `go test ./pkg/reconciler/ -run TestPR396 -v -count=1` |

Environment: go 1.24.1. No cluster required — F1's arithmetic is the HPA controller's own
formula applied to two values taken from production code, and F2 is a label-selector match.

### Why the harness is trustworthy

* **F1's two failing cases sit next to a passing control** (one Pod per group), so the
  finding is attributable to the pods-per-group factor and not to a broken selector or a
  mis-parsed status.
* **Both numbers in F1 come from production code**: `status.replicas` and `status.selector`
  are read from the real `updateStatus`, and the Pod label sets from the real
  `GetGroupSetLabels`. Only the HPA formula is transcribed, and it is archived verbatim
  alongside the results.
* **F2 and F3 each have a control** proving the negative result is not vacuous.
* **D1's dismissal is backed by a positive reproduction** of the panic in the unreachable
  case.

### Harness defect found and fixed

The first version of `TestVerifyPR396_EquilibriumIsOffByPodsPerGroup` **passed vacuously**.
It started the control loop at `usageRatio = 1.0`, which is inside HPA's default tolerance
band (0.1), so `GetResourceReplicas` returns `currentReplicas` unchanged and the loop broke
on iteration 0 without ever exercising the arithmetic. A green result there meant nothing.
Rewritten to start from a perturbed state (a load needing twice the current capacity), it
reproduces the wrong fixed point. Worth remembering for any future test of a controller
with a tolerance band: starting at equilibrium tests nothing.

---

## Continuing

```bash
git fetch origin && git checkout verify/pr396-hpa-selector
go test ./internal/controller/workloads/ -run TestVerifyPR396 -v -count=1
go test ./pkg/reconciler/ -run TestPR396 -v -count=1
```

**Polarity:** F1, F2 and F3's protective assertion are **contract** tests — they turn green
when fixed. D1/D2 are settled negatives and should stay green.

Per finding, after a fix lands:

* **F1** → `TestVerifyPR396_ScaleSubresourceUnitsDisagree` and
  `..._EquilibriumIsOffByPodsPerGroup` pass, i.e. `status.replicas` and `status.selector`
  agree on a unit.
* **F2** → the selector is not published until the Pods it selects exist, or the PR
  documents the re-roll.
* **F3** → `TestPR396_MatchLabelsNotMutatedByPodReconciler/A_control...` flips (the function
  no longer mutates its argument); that subtest is a **canary** and must then be inverted.

Note the PR is `dirty` against `main` and must be rebased before any of this can merge; the
existing `CHANGES_REQUESTED` (review `4893426270`) asks for the rebase plus test coverage,
including "an e2e case that actually scales an RBGS through the scale subresource" — that
e2e is precisely what will surface F1.
