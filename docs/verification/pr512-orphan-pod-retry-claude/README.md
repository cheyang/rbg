# Verification harness — PR #512 (Reviewer A, Claude)

PR: https://github.com/sgl-project/rbg/pull/512 — "fix: retry RoleInstance pod creation after orphan GC"
Issue: #511 — StatefulSet → RoleInstanceSet migration requires a manual reconcile to create the replacement Pod
Head under review: `a8ef35fd52f9a0ba5a02dd5bf7ee5abdcf177f86` (merge-base `a0b11e6ab95ca6dfcfdf6fca713352c8482ebd41`)

## Premise (P0) — CONFIRMED

Issue #511's mechanism reproduces on the merge-base at two layers:

- **Unit (differential):** on base, `createPods` hits `hasOrphanPod → continue` for a
  foreign-owned same-name Pod and returns `(false, nil)` — no error, no requeue, so nothing
  schedules a retry (`results/unit-base.log`, `TestOrphanPodContractForeignOwner` FAILS with
  "An error is expected but got nil").
- **Integration (differential, real controllers via envtest):** with a foreign-owned Pod
  squatting the instance's stable name, the base controller never records `FailedScale`, and
  after the foreign Pod is deleted **nothing recreates the replacement Pod** — both specs FAIL
  (`results/envtest-base.log`). The pod event handler
  (`pkg/reconciler/roleinstance/instance_event_handler.go`, `resolveControllerRef`) only maps
  events for pods whose controller ref is a RoleInstance, so the foreign Pod's deletion cannot
  wake the stalled instance. Exactly the reported symptom.
- On the PR head the same tests pass — the fix works.

## Harness layout

| file | layer | what it proves |
| --- | --- | --- |
| `pkg/reconciler/roleinstance/sync/instance_scale_orphan_contract_test.go` | unit (contract) | `Scale` returns an error for a foreign-owned / controller-less same-name Pod and leaves the foreign Pod untouched; a Pod owned by the instance is skipped without error; the new all-or-nothing abort semantics are pinned (`TestOrphanPodContractMultiComponentAbort`); gang-scheduling still errors (control). |
| `test/envtest/testcase/stateful/orphan_pod_recovery_test.go` | integration (envtest, contract) | Real controller + real API server: (1) `RoleInstanceFailedScale` condition with the "is not controlled by RoleInstance" message while the foreign Pod persists, foreign Pod never adopted/deleted; (2) after GC removes the foreign Pod, the replacement Pod is created and owned by the **same** RoleInstance via the error-retry loop alone — no manual reconcile (the issue #511 contract). |

Fixture realism: the migration collision is simulated by re-pointing the live Pod's controller
ownerRef at a legacy StatefulSet — the exact API-server state the new RoleInstance sees after
the legacy workload is deleted but before GC removes its Pod (same stable name, foreign
controller). The ownerRef transition deterministically triggers a reconcile through the
production event handler, so there is no create/delete race.

## Observed vs expected

| claim | base (a0b11e6) | head (a8ef35f) |
| --- | --- | --- |
| P0 premise: foreign Pod blocks creation with no retry signal | FAIL (reproduced) | PASS |
| A2 FailedScale recorded, foreign Pod untouched | FAIL (no condition) | PASS |
| A3 auto-recovery after GC, no manual reconcile | FAIL (stuck — issue #511) | PASS |
| A4 owned same-name Pod skipped, no error | PASS | PASS |
| F1 first collision aborts sibling creation | FAIL (base created siblings) | PASS (new pinned semantics) |
| F2 bare (controller-less) collision reported | FAIL (silent skip) | PASS |
| full sweep `./pkg/reconciler/roleinstance/... ./internal/controller/workloads/...` + `go vet` | — | PASS (`results/full-sweep-head.log`) |
| full stateful envtest suite (5 specs incl. harness) | — | PASS |

All tests are **contract polarity** (assert intended behavior; red = bug present). No
bug-canaries. The base run doubles as the harness-bites check: the same binaries fail on
unpatched code and pass on patched code, so the tests exercise the changed path.

Raw output: `results/*.log`. Run everything with `bash scripts/re-verify.sh` (auto-resolves
the current PR head; see below).

## How to run

```bash
# unit layer
go test -mod=mod -count=1 -v ./pkg/reconciler/roleinstance/sync \
  -run 'TestOrphanPodContract|TestScaleErrorsAndCreatesAfterOrphanPodGC'

# integration layer (envtest, needs kubebuilder assets)
KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64 \
  go test -mod=mod -count=1 -v ./test/envtest/testcase/stateful \
  -run TestStatefulRoleInstanceSetController -ginkgo.focus "Migration orphan"

# everything against the current PR head (grafts this harness onto it)
bash scripts/re-verify.sh
```

## Review findings carried by this harness

- **F1 [minor]** `createPods` now aborts *all* sibling Pod creation on the first foreign-name
  collision (base created the non-colliding pods immediately). Eventually consistent via the
  retry, and during a migration every stable name typically collides anyway, but it is a
  behavior change worth being deliberate about — pinned by `TestOrphanPodContractMultiComponentAbort`.
- **F2 [nit]** stale comment at `pkg/reconciler/roleinstance/sync/instance_scale.go:149`
  still references `hasOrphanPod`, deleted by this PR.
- **F3 [minor]** permanent collisions emit no warning event (unlike `FailedCreate` /
  `FailedDelete` paths) and retry latency is bounded by controller-runtime's workqueue
  backoff cap (~1000s); the `FailedScale` condition is the only operator signal.
- **F4 [major]** upstream ships no integration/e2e test for the auto-recovery contract — the
  core mechanism of the fix (error → controller-runtime backoff retry → Pod created after GC
  without manual reconcile). This harness proves the behavior; an equivalent envtest spec
  should land in-repo (`test/envtest/testcase/stateful/` — this file can be lifted nearly
  verbatim).

## Continuing after the fix

Nothing is red on the current head. For future rounds:

```bash
git fetch https://github.com/sgl-project/rbg.git pull/512/head
bash scripts/re-verify.sh   # or: bash scripts/re-verify.sh <fixed-ref>
```

`re-verify.sh` grafts this harness onto the resolved PR head, runs unit + integration
layers, and prints per-finding Fixed / Still-broken / Partial / Harness-update. All tests
are contract polarity — a finding is fixed only when its test goes green. The
`.last-reviewed` marker (`a8ef35fd…`) drives the incremental review delta.

Kickoff prompt for a fresh agent: "You are resuming the review of
https://github.com/sgl-project/rbg/pull/512. Checkout branch
`verify/pr512-orphan-pod-retry-claude`, read
`docs/verification/pr512-orphan-pod-retry-claude/README.md`, run
`bash docs/verification/pr512-orphan-pod-retry-claude/scripts/re-verify.sh`, then review
the `last-reviewed..head` delta and update the table."

## Round 2 — head `9d1e7654` ("address RoleInstance migration review follow-ups")

Delta `a8ef35fd..9d1e7654` answers every open item from the round-1 debate:

| round-1 finding | delta fix | verdict at `9d1e7654` |
| --- | --- | --- |
| **ENVTEST-GAP** (blocking major: no upstream integration coverage for the retry mechanism) | new `test/envtest/testcase/stateful/orphan_gc_recovery_test.go` in the repo-convention location; focused run: "recreates a replacement Pod through the controller retry after orphan GC" **passed**; full stateful suite `ok` (42s) | **FIXED** |
| **FAILEDSCALE-LATCH** (major SHOULD: FailedScale never clears after recovery) | `setInstanceConditions` drops stale FailedScale/FailedUpdate from the live object when absent from fresh conditions (`controllerFailureConditionTypes`); unit `TestTransientFailureConditionsClearAfterRecovery` + A3 envtest now asserts the condition clears after recovery | **FIXED** |
| **SIBLING-ABORT** (minor: first collision aborted sibling creation) | non-gang: siblings still created, collision error aggregated and returned after; gang keeps all-or-nothing | **FIXED** — F1 harness rewritten to pin the reviewed semantics (`TestOrphanPodContractMultiComponentSiblingsProceed`, red on base) |
| **NO-WARNING-EVENT** (minor) | `FailedCreate` warning event on collision; asserted in the rewritten F1 test (new finding F4) | **FIXED** |

Full re-verify at `9d1e7654`: P0/A2/A3/A4/F1/F2/F4 all **FIXED** ("all findings fixed").
Harness-bites: the rewritten F1 test is red on the merge-base (no error, no event) and green
at head; the earlier round-1 base run already proved the envtest specs red on base.
Upstream sweep at head: `./pkg/reconciler/...` green, `go vet` clean, full stateful envtest
green, CI green at head.

Harness updates this round: F1 test renamed and rewritten (the PR's all-or-nothing abort
semantics were reversed by design in `9d1e7654` — pinned semantics now match the review);
A3 spec extended with the FailedScale-clears assertion; F4 added; one assertion corrected
(presence check → not-adopted check, since the foreign squatter exists by fixture).

### Round-2 verdict

**ready-to-merge.** The single gating finding (ENVTEST-GAP) and all three open
SHOULD/minors are fixed and verified at head; no new findings from the delta review
(the instance_status latch-drop and the createPods aggregation were both read
line-by-line and are covered by tests both in-tree and in this harness).
