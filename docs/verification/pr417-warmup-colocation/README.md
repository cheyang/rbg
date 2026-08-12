# Verification — sgl-project/rbg#417 (warmup multi-role co-location)

Reviewer-private harness for [PR #417](https://github.com/sgl-project/rbg/pull/417).
**Production code is untouched**; this branch adds only a Go test file and this directory.

- Status: stage ③ published. `CHANGES_REQUESTED` submitted on `7b544482` (2026-08-12), one
  inline `major` on the taint filter (F1) plus one `nit` on `go.mod` (F3). Awaiting the author's
  fix; next round is `scripts/re-verify.sh`.
- PR head verified: `7b544482` ("update go.mod")
- Base: `upstream/main` @ `cb14eee3`, merge-base `8a54787d`
- Live environment: 3-node ACK cluster (`cn-hongkong.10.167.14.221`, `10.39.55.150`,
  `10.39.55.151`) — all Ready, uncordoned, no taints. Controller
  `rolebasedgroup/rbgs-controller:v0.8.0-0c00546d` in `rbg-system`.

## Premise (P0) — CONFIRMED

> The e2e case `warmup / should complete warmup job with targetRoleBasedGroup mode and merge
> multi-role actions` fails on any cluster with more than one schedulable node.

The claimed component and the patched component are the same file, so there is no component
mismatch, and there is no linked issue to auto-close.

Running the single spec on the base branch against the 3-node cluster reproduces the exact
failure signature quoted in the PR body:

```
[FAILED] in [It] - warmup.go:215
Warmup debug info {"name":"warmup-targetrbg-test","phase":"Completed","desired":2,"succeeded":2,
  "conditions":"[... Reason:WarmupCompleted Message:2/2 nodes warmed up successfully]"}
Warmup pod {"name":"warmup-targetrbg-test-5htcc","node":"cn-hongkong.10.39.55.151"}
Warmup pod {"name":"warmup-targetrbg-test-thmpt","node":"cn-hongkong.10.39.55.150"}
Ran 1 of 106 Specs — FAIL! 0 Passed | 1 Failed
```

The same spec on the PR head passes (`Ran 1 of 105 Specs — SUCCESS! 1 Passed`), so the fix is
effective, not just plausible. The warmup controller itself behaved correctly in the failing
run (`2/2 nodes warmed up successfully`) — only the case's unstated single-node premise broke,
which matches the author's own reading.

Note that CI cannot decide this either way: `.github/workflows/e2e-test.yml` creates the kind
cluster with no config, so it is single-node, and the case passes there for the wrong reason.

- `results/L3-p0-base-FAIL.log`
- `results/L3-p0-head-PASS.log`

## Findings

| id | claim | layer | polarity | verdict | evidence |
| --- | --- | --- | --- | --- | --- |
| P0 | base-branch case fails on a multi-node cluster (roles not co-located) | live | contract | **Confirmed** | `results/L3-p0-*.log` |
| F1 | `isNodeAvailable` treats a `PreferNoSchedule` taint as a hard block, unlike kube-scheduler | unit | contract | **Confirmed** (minor) | `results/L1-unit-on-pr-head.log` |
| F2 | `defaultPodTolerations` cannot change any verdict once the Ready gate exists | unit | contract | **Confirmed** (nit) | `results/L1-unit-on-pr-head.log` |
| F3 | `component-helpers` promoted to a direct require but left at `v0.33.3` | static | — | **Confirmed** (nit) | `results/static-F3-gomod-version-skew.txt` |
| D1 | *reviewer hypothesis:* the new import is not vendored, so `-mod=vendor` breaks | static | — | **Disproved** | `results/static-F3-gomod-version-skew.txt` |

### F1 — `PreferNoSchedule` is treated as a hard block

`isNodeAvailable` (`test/e2e/testcase/v1alpha2/warmup.go:500`) calls

```go
corev1helper.FindMatchingUntoleratedTaint(node.Spec.Taints, defaultPodTolerations, nil)
```

`getFilteredTaints` in the vendored helper returns *all* taints when `inclusionFilter == nil`,
so effect is never consulted. kube-scheduler's `TaintToleration` plugin filters on
`Effect == NoSchedule || Effect == NoExecute` precisely because `PreferNoSchedule` is a soft
preference that never makes a node unschedulable.

Impact is on node *selection*, not on the merge assertions: on a cluster whose nodes carry a
`PreferNoSchedule` taint, both loops in `getFirstAvailableNodeName` fall through and all five
warmup cases die in `ginkgo.Fail("no Ready and schedulable nodes found in the cluster")` even
though the pods would have scheduled fine. On base the same cluster works, because the old
helper returned `Items[0]` — so this is a narrow regression, gated on a taint effect that is
uncommon in practice (kind and the ACK cluster used here carry none). Hence `minor`, not a
blocker.

One-line fix, and the harness confirms it bites:

```go
FindMatchingUntoleratedTaint(node.Spec.Taints, defaultPodTolerations, func(t *corev1.Taint) bool {
    return t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute
})
```

`TestVerifyF1_BaselineRejectionsStillHold` is the guard: cordoned, NotReady, Ready=Unknown, no
Ready condition, `NoSchedule` and `NoExecute` nodes all stay rejected with the filter in place,
so the fix does not loosen anything the helper is meant to catch.

### F2 — `defaultPodTolerations` is a no-op

`node.kubernetes.io/not-ready` and `node.kubernetes.io/unreachable` are added by the node
lifecycle controller exactly when `Ready` is `False`/`Unknown`, which the Ready gate above
already rejects. `TestVerifyF2_DefaultPodTolerationsAreANoOp` sets the package var to `nil` and
shows every realistic node shape produces an identical verdict — while deliberately asserting
that the one contrived shape the tolerations *could* affect (`Ready=True` carrying the
`not-ready` taint) does differ, so the test discriminates instead of passing vacuously. Harmless
as written; worth knowing that the filter argument is the part doing the work.

### F3 — version skew on a newly direct dependency

Moving `k8s.io/component-helpers` out of the indirect block is correct once the code imports it,
but it stays at `v0.33.3` while `k8s.io/api`, `apimachinery`, `client-go` and friends are all
`v0.34.1`. It is now the only direct `k8s.io` require off that line. Nothing breaks today —
`go build -mod=vendor ./...` and `go vet ./test/e2e/...` are clean, and `vendor/modules.txt`
already carried `scheduling/corev1` — but a direct dependency is the one worth keeping aligned.

### D1 — disproved, recorded so it is not re-raised

The reviewer's first hypothesis was that `vendor/modules.txt` had not been regenerated and the
new import would break `-mod=vendor` builds. It already lists all four `component-helpers`
packages at PR head, and build + vet are clean. Not a finding.

## Also checked, nothing found

- The terminating-pod filter genuinely mirrors the controller: `pod.Spec.NodeName == "" ||
  pod.DeletionTimestamp != nil` matches `getDesiredNodesToWarmup`
  (`internal/controller/workloads/rolebasedgroupwarmup_controller.go:466`) exactly.
- `HaveLen(2)` is right: `RoleSpec.Replicas` carries `+kubebuilder:default=1`, so two roles
  produce two pods.
- Warmup Pods only ever get `warmup.Spec.Tolerations`
  (`rolebasedgroupwarmup_controller.go:624`), which these cases never set — so modelling node
  availability against a tolerationless pod is the right model for all five call sites, not
  only the new one.
- Blast radius of the helper change is contained to `warmup.go` (5 call sites, no other file
  references it).
- On a cluster where every worker carries a `NoSchedule` taint the new code fails fast with a
  clear message where base would have hung to timeout on a Pending pod. That is an
  improvement, not a regression.

## How to re-run

### L1 — unit (deterministic, no cluster)

```bash
go test ./test/e2e/testcase/v1alpha2/ -run 'TestVerifyF' -v
```

Expected on PR head `7b544482`: `TestVerifyF1_PreferNoScheduleMustNotBlockNodeSelection` FAILS,
the other two PASS.

### L3 — live (needs ≥2 Ready schedulable nodes + the rbgs controller)

```bash
KUBECONFIG=/path/to/config go test ./test/e2e/ -v -ginkgo.v \
  -ginkgo.focus='merge multi-role actions' -timeout 25m
```

Base → FAIL with `desired: 2`. Head → SUCCESS. A single-node cluster cannot decide P0.

### Automated next round

```bash
bash docs/verification/pr417-warmup-colocation/scripts/re-verify.sh
```

No sha needed — it resolves the current PR head from `verify-manifest.json`'s `pr` field and the
review delta from `.last-reviewed`.

## Continuing after the fix

All findings are `contract` polarity, so **green means fixed** — there are no canaries to invert.

| test | on PR head today | once F1 is fixed |
| --- | --- | --- |
| `TestVerifyF1_PreferNoScheduleMustNotBlockNodeSelection` | FAIL | PASS |
| `TestVerifyF1_BaselineRejectionsStillHold` | PASS | PASS (must stay) |
| `TestVerifyF2_DefaultPodTolerationsAreANoOp` | PASS | PASS |

If F1 comes back `Harness-update`, the author restructured `isNodeAvailable` — re-read the
helper and re-point the test rather than assuming a regression. If the author instead deletes
`defaultPodTolerations` (a reasonable reading of F2), `TestVerifyF2_...` will fail to compile;
drop that test, it has served its purpose.

### Kickoff prompt for a fresh session

> Resume the review pipeline for https://github.com/sgl-project/rbg/pull/417. The verification
> branch is `verify/pr417-warmup-colocation` on the `cheyang/rbg` fork; read
> `docs/verification/pr417-warmup-colocation/README.md` and run `scripts/re-verify.sh`. The live
> layer needs a multi-node cluster — use `ssh root@43.99.38.217` (3 nodes, controller already
> installed). Nothing has been published to GitHub yet.
