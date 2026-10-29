# Verification — PR #478 (warmup target waits recoverable + CEL rule removal)

- **Branch:** `verify/warmup-target-wait-claude` (reviewer: Claude)
- **PR:** https://github.com/sgl-project/rbg/pull/478 (base `35e5d029`, #466)
- **Round 1:** head `9f61d4f7` — harness added, findings F1/F2 (open), F3/F4/F5.
- **Round 2:** head `60b1b7cb` ("make target readiness terminal-safe and
  deterministic") — F1/F2 fixed and verified; F4 refuted; new minor findings F6/F7.
- **Layers run:** unit (fake-client `go test`) + integration (envtest with the real
  kube-apiserver binaries — 1.31.0 = the repo's CI version, plus 1.33.0/1.34.1/1.36.2
  for the version matrix). L3/live skipped per the debate setup.

## Premise (P0) — CONFIRMED (round 1, carried; re-checked at round-2 head)

The PR claims three problems. All three reproduce at the base branch; the first two
are fixed at head:

| Claim | Base branch (symptom) | Head 60b1b7cb |
|---|---|---|
| P1: missing target RBG terminally fails a one-shot Warmup | `TestVerifyC1` red at base: first reconcile → `Failed/InvalidTarget` | green: waits `Running` + `TargetReady=False/RoleBasedGroupNotFound`, proceeds when the target appears (also via the new RBG watch) |
| P2: unscheduled target Pods → `Completed/NoNodesMatched` false success | `TestVerifyC2` red at base | green: waits `TargetReady=False/TargetPodsNotScheduled` |
| P3: #466 CEL rule freezes status writes of stored invalid objects | envtest 1.31.0 A/B: status write with the #466 rule → **rejected**; PR CRD → accepted; main-path update → accepted (ratcheting) | rule deleted; controller validation retained (`TestVerifyC5`) |

**Version matrix (round 2 — this overturns round-1 F4):** the #466 CEL rule blocks
the status-subresource write of a pre-existing blank-image object on **k8s 1.31.0
only**; on 1.33.0, 1.34.1 and 1.36.2 the write is **accepted** (ratcheting exempts
the unchanged field on the status path there). The PR's "<= 1.32 frozen, 1.33+ safe"
narrative is **correct**. Round-1's recorded 1.36.2 rejection did not reproduce in
5 fresh runs (3× at `60b1b7cb`, 2× at the round-1 head `9f61d4f7`) — treat that one
data point as an anomaly. The `minLength` sub-claim stays confirmed
(`TestVerifyP0d`): status writes of a pre-existing `images: [""]` object are
accepted on every version tested.

## Findings (observed vs expected)

| ID | Test | Polarity | Round 1 (9f61d4f7) | Round 2 (60b1b7cb) | Verdict |
|---|---|---|---|---|---|
| F1 | `TestVerifyF1TargetDeletedAfterPodsSucceededReachesTerminal` | contract | FAIL (stuck `Running`) | **PASS** | **FIXED** |
| F2 | `TestVerifyF2CountersAndCompletionFrozenDuringWait` | contract | FAIL (frozen counters) | **PASS** | **FIXED** |
| F4 | `TestVerifyP0c`/`TestVerifyP0d` (+ version matrix) | contract | 1.36.2 rejection recorded | 1.36.2 accepts (5 runs) | **REFUTED** — PR narrative correct |
| F6 | `TestVerifyN3ExpiredTimeoutNotBypassedByStaleSucceededNode` | contract (red = bug) | n/a | **FAIL** | **OPEN** (minor) |
| F7 | `TestVerifyN1TargetDeletedMidRunWithActivePodReachesTerminal` | contract (+ logged warts) | n/a | PASS + warts observed | **OPEN** (minor) |

- **F6 (minor, new):** `warmupReachedTerminalState` counts succeeded/failed nodes
  that are no longer in the desired set toward `len(terminalNodes) >=
  len(desiredNodes)`. After target pod churn replaces one node with another
  (rolling update during the warmup window), a succeeded warmup Pod on the
  now-undesired node masks a desired node with no terminal pod, so an **expired
  global timeout is bypassed**: the job stays `Running` and a warmup Pod is
  created after the deadline (`results/…` N3 failure message). The PR's own stated
  contract is "a job whose nodes have *already reached a terminal state* is
  allowed to complete" — here node-b has not, so the deadline should fire.
- **F7 (minor, new):** a Warmup whose warmup Pod is still ACTIVE when the target
  RBG disappears mid-run now completes (good — F1's fix covers this, N1 green),
  but the terminal record is incoherent: phase `Completed` with reason
  `NoNodesMatched` ("No nodes matched the target configuration" — false: nodes
  were matched and warmed), `Active=1` on a Completed job, and `status.startTime`
  clobbered to the deletion moment (the `desired==0` branch of `updateStatus`
  sets it unconditionally). N1's log line records the observed values.

## Harness-update note (round 2 fixtures)

Round-1 fixtures built the target RBG with an **empty spec**. Commit `60b1b7cb`
derives the expected pod count from the RBG *spec* (`countTargetPodsNotReady`), so
an RBG without spec roles is no longer a realistic target (a real RBG lists its
roles; a role-less RBG has no pods at all). `verify478RBG` now carries a `worker`
role (1 replica; C4 uses replicas=0 for the genuine "nothing to do" case). With
the realistic fixture C2/C4 are green at head and C2 stays red at base, so the
premise-evidence property is preserved. This is a harness update, **not** evidence
of a fix or a regression.

## Harness-bites check (round 2)

- Full upstream unit sweep at head: the **only** failure is the intended F6 canary
  (`results/unit-sweep-60b1b7cb-round2.txt`).
- Repo envtest suites (`rbg`, `restart_policy`, `webhook`) pass on envtest 1.31.0
  (`results/envtest-suites-60b1b7cb-round2.txt`).
- Integration layer (P0c/P0d) passes on 1.31.0; version matrix captured in
  `results/envtest-version-matrix-round2.txt`.
- `go build ./...` and `go vet` clean. Generated-artifact stability was verified
  textually (the only generated-file changes are the CEL-rule deletion and the
  `globalTimeoutSeconds` description, propagated consistently to both
  `config/crd/bases` and `deploy/kubectl/manifests.yaml`); controller-gen was not
  re-run locally this round.
- RBAC: the new `Watches(&RoleBasedGroup{})` is covered by the pre-existing
  `rolebasedgroups: get;list;watch` grant (`rbgs-controller-role` in
  `deploy/kubectl/manifests.yaml`).

## Files

- `internal/controller/workloads/verify_pr478_claude_test.go` — unit layer
  (self-contained; compiles at base and head). C1–C5 premise checks, F1/F2 finding
  contracts, N1–N3 round-2 tests.
- `internal/controller/workloads/verify_pr478_envtest_test.go` — integration layer
  (envtest A/B + version matrix). `VERIFY_478_ASSETS` (colon-separated asset dirs)
  overrides the default.
- `fixtures/crd-with-cel-rule-466.yaml` — base (#466) Warmup CRD, with the CEL rule.
- `fixtures/crd-pre466-no-minlength.yaml` — PR CRD minus the images `minLength`
  marker, used to store a violating object before "upgrading".
- `scripts/re-verify.sh` — re-runs the layers against the current PR head.
- `results/` — raw output per round.

## Re-verify

From a checkout of this branch:

```bash
docs/verification/warmup-target-wait-claude/scripts/re-verify.sh --layers unit,integration
```

Expected now: F1/F2 `FIXED`, F4-evidence green, F6 `STILL-BROKEN` (the open minor),
F7 contract green (warts logged). Exit 0 iff all findings are fixed, so F6 keeps it
non-zero until the timeout-bypass fix lands.

## Continuing after the fix

- F6 fix candidates: restrict `warmupReachedTerminalState`'s terminal-node count to
  nodes **in `desiredNodes`** (intersect, don't compare raw lengths), i.e.
  `desired ⊆ terminalNodes`. The N3 canary then flips green.
- F7 fix candidates: on mid-run target deletion record a distinct terminal reason
  (e.g. `Failed/TargetGone`, or Complete with a truthful reason), avoid the
  `desired==0` `NoNodesMatched` branch when warmup Pods exist, and don't clobber
  `status.startTime` in that path.
- Polarity table: all tests here are **contract** tests except N3, which asserts
  the *correct* behavior (fail on expired timeout) and is red while F6 is present.

## Round 3 — head `b0915b21` ("settle mid-run empty targets correctly")

The PR was rebased onto a new base `ecdfd6e6` and gained three commits
(`e313d507` keep in-flight jobs non-terminal and arm the timeout safety net,
`82ce44bc` extract phase transition from updateStatus, `b0915b21` settle mid-run
empty targets correctly). The rebased round-2 content is `99d49326`.

### Findings verdicts (differential: previous `99d49326` → head `b0915b21`)

| ID | Test | 99d49326 (prev) | b0915b21 (head) | Verdict |
|---|---|---|---|---|
| F4a | `TestVerifyN1TargetDeletedMidRunWithActivePodStaysRunning` | **FAIL** (false `Completed/NoNodesMatched`, Active=1) | **PASS** | **FIXED** |
| F4b | `TestVerifyN4AllTargetPodsUnscheduledMidRunWaitsAndWarmsReplacement` | **FAIL** (false completion; replacement node never warmed) | **PASS** | **FIXED** |
| F5 | `TestVerifyN5OrphanActivePodOnDepartedNodeFailsOnGlobalTimeout` | **FAIL** (wedged `Running`, orphan kept, no requeue) | **PASS** (Failed/GlobalTimeoutExceeded, pod deleted) | **FIXED** |
| F6 | `TestVerifyN3ExpiredTimeoutNotBypassedByStaleSucceededNode` | **FAIL** | **PASS** | **FIXED** |
| F7 | N1 terminal stage (reason `WarmupCompleted`, preserved `startTime`) | wart observed | **PASS** | **FIXED** |
| F8 | `TestGeneratedWarmupCRDDoesNotValidateContainerImages` (new item-schema assertion) | guard incomplete | **PASS** | **FIXED** |
| F1/F2/F4-evidence | unchanged | PASS | PASS | still FIXED / refuted |

- **Polarity note (harness update):** N1's round-2 assertion demanded a *terminal*
  phase with an active Pod; the round-2 debate settled on the opposite contract
  (stay `Running`, timeout armed — completing was the bug). N1 was rewritten to the
  agreed contract, which is why it now *fails* on the round-2 head and passes on
  `b0915b21`. N4/N5 are new repros for the round-2 F4b/F5 blockers/major.
- **Base control (`ecdfd6e6`):** C1/C2 still red (premise problems 1/2 reproduce on
  the new base). N1 red at base with `Failed/InvalidTarget` immediately (the
  premise-1 one-shot failure), N4 red (premise-2 false completion), N5 red (the
  base's startTime-anchored dual clock never expires with an unset startTime).
- **Harness-bites check for F8:** re-adding the exact #466
  `items.x-kubernetes-validations` rule to the generated CRD makes the guard fail
  at both WarmupActions embedding paths (`rolebasedgroupwarmup_crd_test.go:135`);
  clean head is green.
- **Upstream sweep at head:** `go test ./internal/controller/workloads/
  ./api/workloads/v1alpha2/ ./pkg/...` — 0 failures. `go build ./...` and
  `go vet` clean. CRD `config/crd/bases` and `deploy/kubectl/manifests.yaml`
  are structurally identical, and the #466 CEL message is absent from both.
- **Integration layer:** P0c/P0d green on envtest k8s 1.31.0.
- **CI on `b0915b21`:** all checks SUCCESS (queried via the GitHub API at round
  start), including e2e suites; attribution unknown.
- **Q2 residual (open, empirical):** a terminal `Completed` job after mid-run
  target deletion carries `TargetReady=False/RoleBasedGroupNotFound` alongside
  `Complete=True/WarmupCompleted` (probe run at head). Carried as an open
  question, not a blocker.
- **Q3 (round-2 open question) is now resolved by the PR:** the expected target
  Pod count honors a bound ScalingAdapter's effective replicas
  (`applyTargetScalingAdapterReplicas`, mirroring the RBG controller's in-memory
  override, on a DeepCopy), with the author's
  `TestReconcile_ScalingAdapterReplicasGateExpectedTargetPods` and RBAC verified
  (`controller-role` grants `get` on `rolebasedgroupscalingadapters`).

## Round 4 — head `92cb7ef` ("harden target readiness and collapsed-settle semantics")

Delta `b0915b21..92cb7ef` = two commits (`3bd283a5` harden target readiness and
collapsed-settle semantics, `92cb7ef` mark deleted targets unknown after start),
answering the round-3 SHOULD list. Three new harness contract tests
(`TestVerifyR4*`) were added; N1's TargetReady assertion was kept polarity-stable
(not-True + reason) so F4a/F7 attribution is unaffected by the condition's
False→Unknown evolution, with the exact Unknown contract pinned by
`TestVerifyR4Q2DeletedTargetMidRunRecordsTargetReadyUnknown`.

### Findings verdicts (differential: previous `b0915b21` → head `92cb7ef`)

| ID | Test | b0915b21 (prev) | 92cb7ef (head) | Verdict |
|---|---|---|---|---|
| F9 | `TestVerifyR4CollapsedSettleHonorsMaxFailedNodes` (new) | **FAIL** (within tolerance settled `Failed/InvalidTarget`) | **PASS** | **FIXED** |
| Q4 | `TestVerifyR4CrossRoleSurplusDoesNotMaskPendingTargetPod` (new) | **FAIL** (aggregate subtraction masked the worker's deficit) | **PASS** | **FIXED** (promoted from open question) |
| Q2 | `TestVerifyR4Q2DeletedTargetMidRunRecordsTargetReadyUnknown` (new) | **FAIL** (`TargetReady=False` on a terminal Completed job) | **PASS** | **FIXED** (promoted from open question) |
| F10 | `TestVerifyR4F10CollapseKeepsCustomizedActionDiagnostics` (new) | **FAIL** | **FAIL** | **OPEN** (minor, new; see below) |
| F1/F2/F4a/F4b/F5/F6/F7/F8 + premise | unchanged | PASS | PASS | still FIXED / confirmed |

Base control (`ecdfd6e6`): F10 **passes** (the base's terminal failure path
preserves the diagnostics), so F10 is a PR-introduced diagnostics regression:
base=pass, previous=fail, head=fail.

- **F9 fix mechanism (verified):** `failCollapsedDesiredSet` now consults
  `spec.policies.maxFailedNodes` — within tolerance the settle completes
  `Completed/WarmupCompleted` with `status.failed` counting the unsucceeded
  nodes ("N node(s) warmed up and M node(s) failed within tolerance…"), beyond
  it fails `Failed/MaxFailedNodesExceeded`. `ReasonTargetNoLongerSelectsNodes`
  is removed; no stale references remain (grep-verified). The PR replaced
  `TestReconcile_TargetDeletedWithRetryableFailedPodFails` with
  `TestReconcile_CollapsedDesiredSetHonorsMaxFailedNodes` (3 subtests).
- **Q4 fix mechanism (verified):** `countTargetPodsNotReady` computes expected
  and observed Pods per selected role; a surplus in one role can no longer
  satisfy another role's deficit. PR test
  `TestReconcile_CrossRoleSurplusDoesNotMaskPendingTargetPod`.
- **Q2 fix mechanism (verified):** `setTargetUnknown` records
  `TargetReady=Unknown/RoleBasedGroupNotFound` once a started Warmup's target
  disappears, both while Running and on the terminal settle. KEP adds the
  Unknown row and the "Complete/Failed is authoritative" guidance.
- **N2 (fixed):** the production-unreachable duplicate failure branch in
  `updateEmptyDesiredPhase` was deleted; collapsed-set semantics now have a
  single implementation (diff-verified).
- **N3 (fixed):** the `rolebasedgroupscalingadapters` kubebuilder RBAC marker
  was added. `make manifests` at head regenerates a **clean tree** (verified —
  the aggregated manager ClusterRole already carried `get`), so the permission
  is declared, not an undeclared dependency.
- **Q1 (resolved as documented):** the KEP gains a "Controller upgrade
  compatibility" paragraph and the PR body a matching upgrade note: the
  creation-anchored timeout can immediately fail pre-upgrade `Running` Warmups;
  pausing does not pause the clock.
- **F10 (new, minor, OPEN — Copilot C4381790591 first half, empirically
  confirmed):** a mid-run target collapse erases
  `status.customizedActionResults` (1→0 in the probe) and drops the
  `CustomizedAction*` conditions **while the job is still Running** (active=1),
  and again on the within-tolerance settle, because
  `evaluateCustomizedActionResults`/`preserveCustomizedActionResults` scope the
  diagnostics to the *current* `desiredNodes` map. Base preserves them (the
  `failWarmupJob` path keeps diagnostics, pinned by the repo's own
  `TestFailWarmupJobPreservesCustomizedActionDiagnosticsWithoutDesiredNodes`).
  Copilot's second claim (the global-timeout failure path loses diagnostics) is
  **refuted** — `TestReconcileGlobalTimeoutPreservesCustomizedActionDiagnostics`
  is green at head. Bounded: diagnostics-only; phases, counters and reasons are
  unaffected. Contract test red at head and previous head, green at base.
- **Two more round-4 Copilot advisories assessed:**
  - C4381790485 (same-role rollout surplus masking a Pending replacement):
    mechanism is real but **matches the documented contract** — `TargetReady=True`
    requires all *expected* (spec-derived) selected Pods to be scheduled, and the
    KEP explicitly says a newly pending target Pod does not block completion once
    warmup Pods exist. A semantics change, not a defect → carried as open
    question Q5 (maintainer decision), non-gating.
  - C4381790640 (`targetWaitRequeueAfter` returns 0 when the remaining budget is
    exhausted at requeue time): mechanism code-confirmed; the exploitable window
    is the µs between the `globalTimeoutExpired` check and the requeue
    computation, and a controller restart or any target event recovers → nit
    (N4), pre-existing PR code, non-gating.
- **Harness-bites:** every new `TestVerifyR4*` contract test is red on
  `b0915b21` (the round-3 content) and green on `92cb7ef` — the differential
  itself proves the tests bite. F10 is red at head (open finding) with its
  precondition assertion green.
- **Upstream sweep at head:** `go test ./internal/controller/workloads/
  ./api/workloads/v1alpha2/` green; `go build`/`go vet` clean; `make manifests`
  produces no diff.
- **Integration layer:** P0c/P0d green on envtest k8s 1.31.0 (run by
  `re-verify.sh`, `results/reverify/integration.out`).
- **CI on `92cb7ef`:** all checks SUCCESS (23 rollup entries / 21 unique,
  queried via the GitHub API), including all e2e suites; attribution unknown.

## Round 5 — head `407b5216` ("support partial CRD startup and preserve diagnostics" + test flake fix)

Delta `92cb7ef16..407b5216`: two commits pushed 2026-10-27 in response to the round-4
review and diw-zw's comments.

- **`b4b29ff0`** — fixes every item the round-4 review left open:
  - **F10 (the round-4 open minor):** new `customizedActionDiagnosticNodes` = desired ∪
    (owned Pods carrying a customized action) ∪ (previously recorded results).
    `failWarmupJob` / `updateStatusWithTarget` / `reconcileUnfinished` now use it, so a
    mid-run target collapse no longer erases `customizedActionResults` or the
    CustomizedAction conditions (the old code skipped all of that when
    `desiredNodes == nil`). Two new regression tests pin it:
    `TestUpdateStatusKeepsCustomizedActionDiagnosticsAfterTargetCollapse`,
    `TestFailWarmupJobKeepsCustomizedActionDiagnosticsAfterTargetCollapse`.
  - **N4 (zero-requeue edge):** `targetWaitRequeueAfter` now returns `time.Millisecond`
    instead of `0` when the budget is already expired, so the timer can never be
    disabled between the expiry check and the requeue computation
    (`TestTargetWaitRequeueAfterExpiredBudgetSchedulesReconcile`).
  - **N5 (settle message):** the empty-desired settle message omits the
    "and 0 node(s) failed within tolerance" clause when nothing failed.
  - **Partial CRD startup** (diw-zw's comment): `shouldWatchTargetRBGs` now handles the
    CRD-missing / CRD-establishing cases (`TestShouldWatchTargetRBGsWhenCRDIs{Missing,Established}`).
- **`407b5216`** — test-only: widens the `TestReconcile_TargetWaitRequeueHonorsGlobalTimeout`
  fixture margin (59s → 55s) so scheduling jitter cannot flip the "deadline still active"
  case into expiry.

### Full re-verify at `407b5216` (2026-10-29)

All 13 findings FIXED (`results/reverify-round5-full-at-head-407b5216.txt`), including
F10 — the round-4 open item. Premise checks C1–C5 green at head. Upstream unit sweep
`./internal/controller/workloads/...` green (includes the delta's own new tests).
CI green at head (22/22, incl. envtest + E2E). L3/live skipped per the debate setup.

Artifact note: `results/reverify/unit.parsed` carried a stale `C2 fail` line contradicting
its own `unit.out` (zero fail events; C2 PASS) — re-parse and a direct `-v` re-run both
pass; the line is a write artifact, not a failure.

### Round-5 verdict

**ready-to-merge.** No open findings; every MUST and every minor item from rounds 1–4 is
fixed and differentially verified. Merge decision rests with the maintainer (diw-zw is
actively reviewing; the PR's GitHub `CHANGES_REQUESTED` label is the round-3 review's
stale REQUEST_CHANGES, superseded by this branch's evidence).
