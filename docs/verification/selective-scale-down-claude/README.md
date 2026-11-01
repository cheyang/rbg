# selective-scale-down-claude — bug verification

Reproducible evidence for the findings raised by Reviewer A (Claude) while
reviewing https://github.com/sgl-project/rbg/pull/538
(head `07ae1194c0481b2f39db2d41c32c08a6b2123b1d`, base
`4a0765f78b0930114a4b73a64433ee8f27c34372`, round 1 / initial).

Layers run against the **code under review** (`07ae1194`):

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
   bash docs/verification/selective-scale-down-claude/scripts/re-verify.sh
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
`bash docs/verification/selective-scale-down-claude/scripts/re-verify.sh`, and
report per-finding Fixed / Still-broken / Harness-update using the polarity
table in that directory's README."
