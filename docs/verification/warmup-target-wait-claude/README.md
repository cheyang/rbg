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
