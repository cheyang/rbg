# Verification — PR #500 "reject child resources controlled by a previous same-named parent"

Reviewer: **Claude (Reviewer A)**, round 2 (incremental).
PR head: `4f32bb9f8c6b8ff56d7da435482484561abed6c2` — previous head: `d2915172d7569d0f3821a1a2cf02dbd1a49bebcf` — base: `56c49531084191ad982c7b1512d64bb83acd2098`.
Harness branch: `verify/foreign-uid-children-claude` (pushed to the reviewer's fork `cheyang/rbg`). The branch still sits on the round-1 head `d2915172`; `re-verify.sh` grafts the harness onto the current PR head.

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

### Round 2 (incremental, head `4f32bb9f`)

Re-verify (`bash docs/verification/foreign-uid-children/scripts/re-verify.sh`, resolves the PR head itself):

| ID | Claim | Layer | Observed on head 4f32bb9f | Verdict |
| --- | --- | --- | --- | --- |
| P0 | premise (above) | unit (base) | unchanged; all base-FAIL / head-PASS | **Confirmed** (round 1) |
| PR500-FIX | the fix itself keeps working | unit | all 5 P0 tests PASS on 4f32bb9f | **Fixed** (regression suite) |
| F1 | an *orphaned* PodGroup whose spec already matches must still be adopted, else `deletePodGroup`'s `IsControlledBy` gate leaks it forever | unit | both `TestF1*OrphanMatchingSpec_AdoptionContract` PASS on 4f32bb9f (red on d2915172 and base) | **Fixed** — author added `!metav1.IsControlledBy(podGroup, rbg) ||` to the update condition in both schedulers, plus same-spec orphan test rows |
| F8 | replace-delete of a foreign/orphan ScalingAdapter must carry the observed UID as a precondition so a stale cache cannot wipe a newer adapter | unit | `TestF8Adapter_RecreateDeleteCarriesUIDPrecondition` PASS on 4f32bb9f (red on d2915172, harness test added this round) | **Fixed** — author added the UID precondition |

No-regression sweep on 4f32bb9f: `go test -mod=vendor ./internal/controller/workloads/... ./pkg/scheduler/...` all green, `go vet` clean, envtest integration layer `test/envtest/testcase/rbg/...` PASS in 47.6s against a real API server (`results/reverify/integration.out`).

### Round 1 (initial, head `d2915172`) — superseded rows kept for provenance

| ID | Claim | Layer | Observed on head | Verdict |
| --- | --- | --- | --- | --- |
| P0 | premise (above) | unit (base) | all base-FAIL / head-PASS | **Confirmed** |
| PR500-FIX | the fix itself keeps working | unit | all P0 tests pass on head | **Fixed** (regression suite) |
| F1 | orphaned same-spec PodGroup must still be adopted | unit | `TestF1*OrphanMatchingSpec_AdoptionContract` FAIL on head (both schedulers) | **Still-broken (round 1) → Fixed in round 2** |

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

## F1 detail (fixed in round 2)

In both `pkg/scheduler/k8s-scheduler-plugin/scheduler.go` and
`pkg/scheduler/volcano/scheduler.go`, the orphan adoption (`append(NewControllerRef)`)
originally lived **inside the spec-drift branch**. When a `--cascade=orphan` deletion
strips the controller ref and the recreated RBG has an **identical** gang spec, no
update fired, the orphan was never adopted, and `deletePodGroup`'s
`metav1.IsControlledBy` gate then refused to clean it up — the exact leak the PR body
said it fixes for the Volcano path.

Round-1 harness-bites check validated the one-line fix
(`|| !metav1.IsControlledBy(podGroup, rbg)` added to the update condition in both
schedulers); head `4f32bb9f` ships exactly that change plus same-spec orphan test
rows (`TestCreateOrUpdateSameSpecOrphanPodGroupAdopted`) in both scheduler suites,
and both F1 contract tests now pass.

## F8 detail (fixed in round 2)

`ReconcileScalingAdapter`'s replace path (`internal/controller/workloads/rolebasedgroup_controller.go`)
deleted the observed foreign/orphan adapter by namespace/name with no precondition, so a
stale-cache reconcile could delete a *newer* adapter created under the same name after
replacement. Head `4f32bb9f` adds `DeleteOptions.Raw.Preconditions.UID` set to the
observed adapter's UID; this round's harness test `TestF8Adapter_RecreateDeleteCarriesUIDPrecondition`
was red on `d2915172` and is green on `4f32bb9f`. Advisory note: the sibling delete in
the same function (scaling *disabled* path) still has no precondition — a same-class,
lower-severity residual, tracked in the round-2 findings as advisory only.

## Continuing after the fix

- Checkout this branch, run `bash docs/verification/foreign-uid-children/scripts/re-verify.sh`
  (no args — it resolves the current PR head from `manifest.pr`). Exit 0 iff every
  finding is Fixed.
- Polarity: all tests are **contract** tests (must PASS once fixed). No canaries.
- `.last-reviewed` holds `4f32bb9f…` (advanced after round 2); advance it after each
  reviewed round.

## Files

- `internal/controller/workloads/premise_foreignuid_test.go` — P0 set/adapter contracts + F8
- `pkg/scheduler/k8s-scheduler-plugin/premise_foreignuid_test.go` — P0 + F1 (plugin)
- `pkg/scheduler/volcano/premise_foreignuid_test.go` — P0 + F1 (volcano)
- `results/` — raw `go test -json` output per run; `results/reverify/` — round-2 re-verify raw output
