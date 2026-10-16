# Verification — PR #500 "reject child resources controlled by a previous same-named parent"

Reviewer: **Claude (Reviewer A)**, round 1 (initial).
PR head: `d2915172d7569d0f3821a1a2cf02dbd1a49bebcf` — base: `56c49531084191ad982c7b1512d64bb83acd2098`.
Harness branch: `verify/foreign-uid-children-claude` (pushed to the reviewer's fork `cheyang/rbg`).

## Premise (P0) — CONFIRMED

The PR claims three components still have the same-name/new-UID ownership bug class
(fixed for RBG workloads by #487 and RoleInstance Pods by #497). All five premise
contract tests **fail on base** `56c4953` and **pass on the PR head**:

| Symptom claimed by the PR | Base 56c4953 | Head d2915172 | Test |
| --- | --- | --- | --- |
| Recreated Set deletes a foreign child on scale-down | FAIL (child deleted) | PASS | `TestP0Set_ForeignChildNotDeletedOnScaleDown` |
| Recreated Set counts foreign child as Ready | FAIL (ReadyReplicas 1) | PASS | `TestP0Set_ForeignChildNotCountedReady` |
| Leftover Bound adapter pins stale replicas | FAIL (3 → 9) | PASS | `TestP0Adapter_ForeignBoundAdapterDoesNotOverrideReplicas` |
| Foreign PodGroup spec rewritten (scheduler-plugins) | FAIL (spec 1→2, no error) | PASS (error + untouched) | `TestP0Plugin_ForeignPodGroupRejected` |
| Foreign PodGroup spec rewritten (volcano) | FAIL (spec 1→2, no error) | PASS (error + untouched) | `TestP0Volcano_ForeignPodGroupRejected` |

Raw evidence: `results/base-56c4953-premise.json`, `results/head-d2915172.json`.

## Observed-vs-expected table

| ID | Claim | Layer | Observed on head | Verdict |
| --- | --- | --- | --- | --- |
| P0 | premise (above) | unit (base) | all base-FAIL / head-PASS | **Confirmed** |
| PR500-FIX | the fix itself keeps working | unit | all P0 tests pass on head | **Fixed** (regression suite) |
| F1 | an *orphaned* PodGroup whose spec already matches must still be adopted, else `deletePodGroup`'s `IsControlledBy` gate leaks it forever | unit | `TestF1*OrphanMatchingSpec_AdoptionContract` FAIL on head (both schedulers) | **Still-broken (open, minor)** |

Integration layer: `test/envtest/testcase/rbg/...` (real API server) passes on head
(`results/head-envtest-rbg.txt`), i.e. the controller changes do not regress the
existing envtest lifecycle suite.

## How to run

```sh
# unit layer (P0 + F1)
go test -mod=vendor -count=1 ./internal/controller/workloads/ \
  ./pkg/scheduler/k8s-scheduler-plugin/ ./pkg/scheduler/volcano/ -run 'TestP0|TestF1' -v

# integration layer (envtest; assets per machine)
go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.22 use 1.31.0 --bin-dir /tmp/envtest-bin -p path
KUBEBUILDER_ASSETS=$(…) go test -mod=vendor -count=1 ./test/envtest/testcase/rbg/... -timeout 25m

# full re-verify (grafts harness onto the current PR head, prints per-finding verdicts)
bash docs/verification/foreign-uid-children/scripts/re-verify.sh
```

## F1 detail (the one open finding)

In both `pkg/scheduler/k8s-scheduler-plugin/scheduler.go` and
`pkg/scheduler/volcano/scheduler.go`, the orphan adoption (`append(NewControllerRef)`)
lives **inside the spec-drift branch**. When a `--cascade=orphan` deletion strips the
controller ref and the recreated RBG has an **identical** gang spec, no update fires,
the orphan is never adopted, and `deletePodGroup`'s `metav1.IsControlledBy` gate then
refuses to clean it up — the exact leak the PR body says it fixes for the Volcano path.
The PR's own orphan tests always seed spec drift (MinMember 1 vs desired 2), which is
why this escapes coverage.

Harness-bites check performed: with the one-line proposed fix
(`|| !metav1.IsControlledBy(podGroup, rbg)` added to the update condition in both
schedulers) both F1 tests pass and the entire `pkg/scheduler/...` suite stays green;
the fix was then **reverted** (production diff on this branch is empty).

## Continuing after the fix

- Checkout this branch, run `bash docs/verification/foreign-uid-children/scripts/re-verify.sh`
  (no args — it resolves the current PR head from `manifest.pr`). Exit 0 iff every
  finding is Fixed.
- Polarity: all tests are **contract** tests (must PASS once fixed). No canaries.
- When F1 is fixed, promote nothing — the tests already assert the correct behavior.
- `.last-reviewed` currently holds `d2915172…`; advance it after each reviewed round.

## Files

- `internal/controller/workloads/premise_foreignuid_test.go` — P0 set/adapter contracts
- `pkg/scheduler/k8s-scheduler-plugin/premise_foreignuid_test.go` — P0 + F1 (plugin)
- `pkg/scheduler/volcano/premise_foreignuid_test.go` — P0 + F1 (volcano)
- `results/` — raw `go test -json` output per run
