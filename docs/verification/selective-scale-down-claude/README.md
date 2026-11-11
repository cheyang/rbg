# selective-scale-down-claude — bug verification

Reproducible evidence for the findings raised by Reviewer A (Claude) while
reviewing https://github.com/sgl-project/rbg/pull/538.

**Current round: 3 / incremental** — head `2c347c471633b8ecee2c4d59079749b139632359`,
base `fd160cc964b0e1438bead4ad7324d0a8f0bf5eac` (unchanged), previous reviewed
head `f3d448746b02a10ebff161885e98d352d19d393f`. Round 1 ran against head
`07ae1194` / base `4a0765f`; round 2 against `f3d44874` / `fd160cc`.
The branch was rebased onto the round-3 head so the working tree is
head + harness (harness commits only; production code untouched).

## Round 3 results (2026-11-12)

The author pushed five commits (`f3d44874..2c347c47`), one per open finding:
`eec29c95` dense-orphan discovery (discovery-gates-adoption), `def9bf63`
FailedScale preservation (F4), `e340f9f7` role-scoped invalid input (F5),
`5fc8c972` surge exclusions (F3), `2c347c47` docs for recovery/early-drain
(F6 + Q2). Re-verify and new round-3 harness evidence:

| ID | Round 2 | Round 3 | Note |
|----|---------|---------|------|
| F1 lint gate | fixed | **FIXED (kept green)** | pinned golangci-lint v2.13.1: 0 issues on 2c347c47 (results/round3/lint.out); CI `lint` check green. |
| F3 surge on excluded ordinal | still-broken | **FIXED** | 5fc8c972 mirrors E onto the child RIS for allocation; `nextSurgeOrdinal` jumps committed gaps AND exclusion ranges. Harness-update: the test now models the mirrored input (the writer writes it — pinned separately). Surge lands on 6, not 5. Residual: a legacy pre-upgrade child (record, no mirror) keeps record-only allocation for one reconcile window — pinned by the legacy canary subtest; transient, documented here, not tracked as a finding. |
| F4 FailedScale erasure | still-broken | **FIXED** | def9bf63 scopes the drop predicate to `Reason == invalidCommittedOrdinalsReason` (the exact one-line scoping requested). Contract test green. |
| F5 preCheck blocks whole RBG | open (reasoning) | **FIXED** | e340f9f7 moves the gate into reconcileRoles per role; Step 8/9 are joined so cleanup always runs; Ready=False + Warning retained via updateRBGStatus (Step 4, runs even when roles fail). Verified by MY OWN envtest (results/round3/envtest-claude-r3.out): invalid role frozen with identities kept, sibling role's selective scale-down commits, removed role's child RIS deleted, repair unfreezes. |
| F6 corrupt record wedges controllers | open | **RESOLVED (runbook + tests)** | 2c347c47 documents the guarded operator repair (UID/RV/replica JSON-patch test-ops, restore-verified-sparse vs clear-verified-dense) and the author's envtest covers both paths incl. failed guards leaving the record unchanged — ran green in my full suite re-run. The wedge-until-manual-repair behavior is unchanged by design; the missing runbook is delivered. |
| discovery-gates-adoption | open (code-path) | **FIXED** | eec29c95 lets memberOrdinals read an ownerless dense child with the matching group-name label (sparse orphans and foreign owners still rejected — pinned by my unit subtests). My own full-controller envtest: delete RBG, orphan all dependents (set/services/CM/revisions), recreate with E → child adopted (same UID), members [0,2,4], survivors keep UIDs, **persisted discovery ConfigMap carries the sparse addresses** (also closes round-2 AC-10's envtest half). |
| Q2 discovery early-drain | open | **ANSWERED** | docs now state the commit-time removal contract explicitly ("early-drain signal, not a guarantee…"). |
| F7 / M2 memberOrdinals coverage | narrowed-open | **CLOSED** | Build-level matrix (round 2) + the envtest ConfigMap assertions above; my TestClaudeMemberOrdinalsBranches extended with the 5 new branches (claimable orphan, sparse orphan, foreign label, invalid-input freeze/no-members). |
| REG commit/controller suites | green | green | all envtest suites re-run green on 2c347c47 (results/round3/envtest-all.out: rbg 110s, selective_commit, webhook, stateful, restart_policy, deprecation*). |

Bite check for every new/updated round-3 test: grafted onto the pre-fix head
`f3d44874` in a throwaway worktree — all FAIL there (mirror absent → E
annotation ignored; ownerless dense orphan rejected; surge on 5), all PASS on
`2c347c47`. F3's round-2-shaped test was also re-run by re-verify against the
new head before the harness update (fail → harness-update → updated → pass).

New unit pins added this round:
- `TestClaudeWriterMirrorsExcludedOrdinalsForSurge` /
  `TestClaudeMirrorIsAtomicWithRecordAndReplicas` (pkg/reconciler) — the
  mirror rides in the same conditional write as replicas+record; a rejected
  patch leaves no half-applied E.
- `TestClaudeValidateRoleExcludedOrdinalsScopesToTheOffendingRole`
  (internal/controller/workloads) — the per-role backstop names only the
  offending role.
- envtest `test/envtest/testcase/rbg/claude_round3_test.go` — F5 isolation +
  dense-orphan adoption + ConfigMap assertion, written independently of the
  author's specs (same contracts, different scenarios).

## Round 2 results (2026-11-11)

The author rebased onto new main `fd160cc` (feature files byte-identical; the
single conflict hunk in `updateRBGStatus` adopts upstream #533) and added 10
fix commits addressing the round-1 review. Re-verify (same harness, polarity
unchanged, against `f3d44874`):

| ID | Round 1 | Round 2 | Note |
|----|---------|---------|------|
| F1 lint gate | fail (blocker) | **FIXED** | pinned golangci-lint v2.13.1: 0 issues on head (6 before); CI `lint` check SUCCESS. Constant extracted, writer split into helpers, test slices preallocated. Kept as a green regression pin. |
| F3 surge on excluded ordinal | fail | **STILL-BROKEN** | surge reworked to on-demand allocation (`materializeSurge`), but new slots still come from `skips.Next()` over the committed gap record only — E above the last member (E="1,3,5", members [0,2,4]) still yields surge ordinal 5. results/reverify/unit.out |
| F4 FailedScale erasure | fail | **STILL-BROKEN** | only the lint goconst half was fixed (constant extracted); the drop predicate still removes every FailedScale condition while the record is invalid. The author's own test pins the replace semantics, so this is now a conscious-decision disagreement, still minor. results/reverify/unit.out |
| F7 memberOrdinals coverage | green | green | author added `TestConfigBuilderBuildCommittedSparseMembership` (Build-level, exact addresses) — coverage gap closed |
| REG commit/controller suites | green | green | re-verified on the rebased head; base change did not break contracts |

Additionally verified green on head (outside the manifest, full suites):
author's new envtests (`TestOrdinaryRISRemovesMetadataAfterManagedFieldsHandoff`,
`TestSelectiveDenseOrphanReapply`, `TestSelectiveMembershipPreservesSharedMetadata`),
full `rbg` + `webhook` envtest suites, full unit tree, and the round-1 Copilot
advisories are fixed (surge layout memory: `TestSurgeLayoutMaterializesOnlyActiveDemand`;
shared-SSA-key deletion: `metadataOwnership` exclusive-ownership check).

Layers run against the **code under review** (round 2: `f3d44874`; round 1: `07ae1194`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | sparse topology, membership validation, discovery ordinals | `go test ./pkg/reconciler/roleinstanceset/statefulmode/ ./pkg/discovery/ ./internal/controller/workloads/ -run 'TestClaude' -v` |
| 2. Integration | real API server (envtest): membership commit + full RBG controller suite | `KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64 go test ./test/envtest/testcase/selective_commit/ ./test/envtest/testcase/rbg/ -run 'TestConditionalMembershipWithLegacyApplyOwnership\|TestRBGController'` |
| 3. Lint gate | the repo's required golangci-lint check, wrapped as a go-test JSON canary | `bash docs/verification/selective-scale-down-claude/scripts/lint-as-gotest.sh` (set `GOLANGCI_LINT_BIN` or let it install the Makefile-pinned v2.13.1) |

Live (L3, kind cluster) is **not** part of this round; see `liveNote` in
`verify-manifest.json`.

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code /
> PASS when fixed. No bug-canaries were needed this round; every finding below
> is encoded as a contract test that is red on `07ae1194` and should go green
> once fixed, except F7 which is coverage (green already, kept as a regression
> pin).

## Problem premise (P0)

Answered before the findings, run against the **base** branch without the patch.

| | |
|---|---|
| Claimed symptom | issue #499: "Operators must retire a selected RoleInstance, including all component pods, while preserving healthy higher-ordinal instances and preventing the retired identity from being recreated"; "Stateless RoleInstanceSet has a specified-delete flow, but Stateful mode does not expose persistent ordinal retirement." |
| Linked issue | #499, OPEN, created 2026-10-15 (KEP). PR references it with `Refs #499`, **no `fixes` keyword**, so no auto-close risk. |
| Reported component | Stateful-mode RoleInstanceSet scaling |
| Patched component | same (statefulmode topology), plus a new RBG-level membership writer, RIS-level validation, discovery |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | `TestClaudePremiseDenseScaleDownCannotRetireMiddleOrdinal` passes on base `4a0765f` (results/premise-base.out) and on head (results/premise-head.out): scaling 5→3 on base removes exactly ordinals 4,3 and scale-up recreates them — ordinal 1 cannot be retired while 2–4 survive. Base also carries a `reserveOrdinals` stub that is hardcoded empty (`stateful_instance_set_utils.go:571` on base: `reserveOrdinals: sets.New[int]()`), i.e. the capability genuinely does not exist. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | capability gap exists on base | 1 | Confirmed | results/premise-base.out |
| F1 | required `lint` CI gate fails, PR-attributed (6 findings) | lint | Confirmed | CI job 124766651800 + results/lint-head.out; base run: 0 issues (differential) |
| F3 | surge allocation can place a surge instance on a user-excluded ordinal | 1 | Confirmed | results/unit-head.out — `TestClaudeSurgeOrdinalAvoidsUserExcludedOrdinals` fails: surge ordinal = 5 with E="1,3,5" |
| F4 | an invalid committed record erases genuine FailedScale conditions | 1 | Confirmed | results/unit-head.out — `TestClaudeInvalidRecordPreservesUnrelatedFailedScaleCondition` fails |
| F7 | discovery `memberOrdinals` branches untested by the PR | 1 | Coverage added (all 5 branches pass) | results/unit-head.out — `TestClaudeMemberOrdinalsBranches` |
| REG | author's membership-commit + controller suites still green | 2 | Pass | results/integration-head.out |

## Per-finding detail

### F1 — lint gate (blocker)
`golangci-lint` (Makefile-pinned v2.13.1, same as CI) reports 6 issues on the
head, **all in files this PR adds**: goconst (`InvalidCommittedOrdinals`
repeated 3×, `internal/controller/workloads/roleinstanceset_controller.go:121`),
gocyclo 45 > 30 (`reconcileSelectiveMembership`,
`pkg/reconciler/selective_membership.go:55`), and 4 prealloc nits in the new
test files. The same command on base reports 0 issues. CI job
`lint` (run 41278923058 / job 124766651800) fails on exactly these lines.
Repro: `bash scripts/lint-as-gotest.sh` → `{"Action":"fail",...}`.

### F3 — surge collides with a user-excluded ordinal (minor)
Mechanism: `computeTopology` derives surge identities from the committed gap
record only (`skips.Members(replicas+maxSurge)`). The record
(`Record(members)`) encodes holes **below the last member only**, so a user
exclusion above the last member (E="1,3,5" with members [0,2,4]) never enters
it. Mid-rollout, the surge instance is then created at ordinal 5 — an ordinal
the user asked never to be used. Observed: `topo.ordinal(3) == 5` while
`Parse("1,3,5").Contains(5)`. Expected: surge avoids the exclusion input. The
surge instance is transient (it collapses back once the base member updates),
so severity is minor, but the doc promises "skip them on scale-up".
Bite-check: the same test's positive control (E="1,3") passes — the assertion
fails precisely because ordinal 5 is excluded, not for an unrelated reason.

### F4 — genuine FailedScale condition erased (minor)
Mechanism: `validateCommittedMembership` drops **every**
`RoleInstanceSetConditionFailedScale` condition while the record is invalid
(`validationErr != nil || …`), and on repair removes only the
`InvalidCommittedOrdinals` one — so a genuine scale-failure condition (written
by statelessmode with an empty Reason) is silently lost forever. Bite-check:
changing the drop predicate to `condition.Reason == "InvalidCommittedOrdinals"`
only makes `TestClaudeInvalidRecordPreservesUnrelatedFailedScaleCondition`
pass; the patch was reverted (production diff empty).

### F7 — discovery memberOrdinals coverage (missing tests)
The PR added `ConfigBuilder.memberOrdinals` (5 branches: dense fallback,
NotFound+absent, NotFound+present/initial planner, owner-UID guard, committed
record) with zero tests — `config_builder_test.go` gained only a scheme
registration. `TestClaudeMemberOrdinalsBranches` pins all five; every branch
behaves as documented (no bug found). The PR should carry equivalent coverage.

### REG — regression pins (author's suites, re-run on this round)
`TestConditionalMembershipWithLegacyApplyOwnership` (real API server,
membership commit atomicity, legacy SSA ownership handoff) and
`TestRBGController` (full ginkgo suite incl. the new
"Stateful selective scale-down" DescribeTable) both pass on the head.

## Refuted during review (no harness entry needed)
- Merge-patch panic: `reconcileSelectiveMembership` does an unchecked
  `content["metadata"].(map[string]interface{})`, but
  `MergeFromWithOptimisticLock` always injects `metadata.resourceVersion` into
  the patch data (controller-runtime v0.22.1 `pkg/client/patch.go:86`), so the
  key always exists. Refuted.
- Nil `ScaleStrategy` deref: `RoleInstanceSetSpec.ScaleStrategy` is a value
  field. Refuted.
- apiReader nil: `NewRoleInstanceSetReconciler` always sets it; the only other
  construction site (deleteOrphanRoles) never reconciles membership. Refuted.

## Proposed fixes (NOT applied to production here)
- **F1**: extract an `invalidCommittedOrdinalsReason` constant; split
  `reconcileSelectiveMembership` into ~4 helpers (validate parent/child state,
  compute target, build patch, apply); preallocate the 4 test slices. All
  mechanical; the split also helps reviewability of the conditional writer.
- **F3**: have surge allocation consult the role's exclusion input (or extend
  the persisted record to cover exclusions above the last member). Simplest
  local fix: pass `E` into `computeTopology` and run
  `excluded.Next(...)`-style allocation for the surge window.
- **F4**: drop only `Reason == "InvalidCommittedOrdinals"` conditions (see
  bite-check patch above).
- **F7**: upstream `TestClaudeMemberOrdinalsBranches` (and a discovery
  ConfigMap assertion in the envtest suite).

## Continuing after the fix (possibly on another machine)

The harness lives on branch `verify/selective-scale-down-claude`
(production code untouched; the only tracked-file changes are additive test
files + this docs tree). It grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/selective-scale-down-claude
   git checkout <fixed-ref>
   git checkout FETCH_HEAD -- docs/verification/selective-scale-down-claude \
     pkg/reconciler/roleinstanceset/statefulmode/claude_premise_test.go \
     pkg/reconciler/roleinstanceset/statefulmode/claude_harness_test.go \
     internal/controller/workloads/claude_harness_test.go \
     pkg/discovery/claude_harness_test.go
   ```
2. Re-run everything with one command:
   ```bash
   bash docs/verification/selective-scale-down-claude/scripts/re-verify.sh --layers unit,integration,lint
   ```
   (no argument: it fetches the current PR head from the manifest's `pr` URL).
   Expected after the fixes: F1, F3, F4 flip to FIXED; F7 and REG stay green.
3. Advance the round marker afterwards:
   ```bash
   echo <new-head-sha> > docs/verification/selective-scale-down-claude/.last-reviewed
   git add ... && git commit -m "review: advance last-reviewed"
   ```

Polarity table (what goes green / what flips):

| Test | Now (07ae1194) | After fix |
|------|----------------|-----------|
| TestClaudePremiseDenseScaleDownCannotRetireMiddleOrdinal | pass (documents the base gap) | pass (annotation absent ⇒ dense) |
| TestClaudeLintGateGreen | **fail** | pass |
| TestClaudeSurgeOrdinalAvoidsUserExcludedOrdinals | **fail** | pass |
| TestClaudeInvalidRecordPreservesUnrelatedFailedScaleCondition | **fail** | pass |
| TestClaudeMemberOrdinalsBranches | pass | pass |
| TestConditionalMembershipWithLegacyApplyOwnership / TestRBGController | pass | pass |

Kickoff prompt for a fresh agent: "You are resuming Reviewer A's verification
of sgl-project/rbg PR #538. Checkout any ref of that PR, graft branch
`verify/selective-scale-down-claude` from fork `cheyang/rbg` onto it (paths in
`docs/verification/selective-scale-down-claude/verify-manifest.json`), run
`bash docs/verification/selective-scale-down-claude/scripts/re-verify.sh --layers unit,integration,lint`, and
report per-finding Fixed / Still-broken / Harness-update using the polarity
table in that directory's README."
