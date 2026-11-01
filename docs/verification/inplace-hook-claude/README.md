# inplace-hook-claude — Reviewer A (Claude) verification of PR #537

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/537 ("fix: honor InPlaceUpdate hooks for
Stateful instances", addresses Task 3 of issue #534).

Branch: `verify/inplace-hook-claude`, based on the PR head
`2d648e5d6b2931883a71ec9d2d076367add3229d`. **Production code is untouched** —
the diff against the PR head is additive test files + this directory only.

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | lifecycle state machine, budget admission, hook waiting, expectation store | `go test ./pkg/reconciler/roleinstanceset/statefulmode/ ./pkg/reconciler/roleinstanceset/statelessmode/sync/ -run 'TestClaude' -v` |
| 2. Integration | real API server + real controllers (envtest) | `KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64 go test ./test/envtest/testcase/stateful/ -timeout 20m` |
| 3. Live | not run this round (debate setup: unit + integration only); the PR's E2E suites are green on head per statusCheckRollup | — |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. Bug-canary tests (assert current behavior) PASS now / FLIP to red when fixed.

## Problem premise (P0)

Run against the **base** branch `4a0765f78b0930114a4b73a64433ee8f27c34372`
without the patch, because the question is whether the problem exists today.

| | |
|---|---|
| Claimed symptom | Issue #534 (OPEN) Task 3: "The Stateful inPlaceUpdateInstance() path directly invokes the in-place update control." / "Stateful in-place updates bypass the lifecycle waiting protocol" |
| Claimed mechanism | no PreparingUpdate/Updating/Updated handshake before the Stateful in-place update, unlike statelessmode/sync/update.go |
| Linked issue | sgl-project/rbg#534, OPEN, Task 3 (the closes-reference in the PR body template resolves to PR #15, an unrelated closed PR — the real anchor is "Addresses Task 3 of #534") |
| Reported component | pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_control.go |
| Patched component | same file + pkg/inplace/instance/inplaceupdate/inplace_update_defaults.go |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | `TestClaudeP0StatefulInPlaceHookMustBlockSpecUpdate` FAILS on base — `applyTargetUpdate` returns `true` (revision transition performed, image flipped to nginx:2.0) while the blocking label is present; PASSES on head (waits in PreparingUpdate, image unchanged, no deletion). results/unit-base-4a0765f.txt vs results/unit-head.txt |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Stateful in-place update bypasses InPlaceUpdate hooks on base | 1 | **Confirmed** (bug exists on base; fixed on head) | unit-base FAIL / unit-head PASS |
| F1 | New `ResourceVersionExpectations.Expect(RoleInstance)` writes land on the per-instance controller's Pod-keyed store; no production code ever observes/deletes them | 1 | **Confirmed** (inert guard + process-lifetime entry leak) | TestClaudeF1… PASS on head, FAIL on base (writes are PR-introduced) |
| F2 | Stateful completion requires ALL handlers restored; stateless completes on partial restore (`!AllHooked` vs `!Hooked`) | 1 | **Confirmed divergence** (differential pair) | TestClaudeF2… (both halves PASS on head; stateless half also PASS on base = pre-existing stateless behavior) |
| F3 | Update targets in an unexpected lifecycle state (e.g. PreparingDelete after a pattern switch, or once Task 1 lands) now hard-error the reconcile instead of being skipped | 1 | **Confirmed new error path** | TestClaudeF3… PASS on head, FAIL on base (base updates without error) |
| F4 | `MarkNotReady=true` without the RoleInstanceCustomReady gate silently no-ops (Task 2 gap); the PR's markPodNotReady contract depends on the separate Task 2 fix | 1 | **Confirmed dependency** | TestClaudeF4… PASS on head |
| F5 | A matching InPlaceUpdate hook does not protect the recreate fallback: a non-in-place-able diff deletes the instance — including one already admitted to PreparingUpdate | 1 | **Confirmed, pre-existing** (passes on base too; Task 1 dependency) | TestClaudeF5… both PASS on head; first also PASS on base |

Head sweep: unit `./pkg/...` all green, `go vet` clean, `-race` clean
(results/unit-sweep-head.txt, results/vet-head.txt, results/race-head.txt).
Integration: full Stateful envtest suite (incl. the PR's new
"Stateful InPlaceUpdate lifecycle hook" Describe, both Pod management policies)
passes in 63s (results/envtest-stateful-head.txt). All 23 GitHub status checks
are SUCCESS on head 2d648e5 (attribution: upstream CI, observed at round start).

## Per-finding detail

### P0 — premise
`TestClaudeP0StatefulInPlaceHookMustBlockSpecUpdate` (contract): one instance at
old revision, `InPlaceIfPossible` strategy, InPlaceUpdate hook matching a label
on the instance, pending image-only update. On base, `applyTargetUpdate`
performs the update and returns `true` — the revision transition happened with
the blocker in place. On head, the instance waits in `PreparingUpdate` across
repeated applies, image stays `nginx:1.0`, nothing is deleted, and the return
value is `false` (waiting is not a transition).

### F1 — misdirected expectation registration
`inPlaceUpdateInstance` (stateful_instance_set_control.go:1347-1350) and
`refreshInstanceState` (:1288-1290) call
`instanceutils.ResourceVersionExpectations.Expect(got)` where `got` is a
**RoleInstance**. That store (pkg/reconciler/roleinstance/utils) is only ever
touched with **Pod** objects by every consumer:
instance_reconciler.go:125-126 (Observe/IsSatisfied on pods),
instance_update.go:95 (Expect pod UID), instance_status.go:523 (Expect gotPod),
instance_event_handler.go:93 (Delete pod). So the registered entries are never
observed, never deleted, and gate nothing — the staleness guard the author
apparently intended (mirroring statelessmode, which Expect()s on its own store
that its reconciler Observe()s) does not exist in stateful mode. Entries also
survive RoleInstance deletion (no Delete path for that UID), leaking map slots
for the process lifetime. `PatchRoleInstance` returns the pre-patch object, so
the registered version is stale as well. Functional impact: none observed (the
state machine is idempotent against stale cache); it is dead code + a slow
unbounded map growth with instance churn. Test proves: an entry exists under
the RoleInstance UID (probe with low RV stays unsatisfied) and Pod-based
Observe/Delete cannot touch it.

### F2 — stateful/stateless completion divergence
Stateful `refreshInstanceState` (:1268-1271) moves Updating→Updated when
`hook != nil && !IsRoleInstanceAllHooked` (waits for ALL handlers restored);
stateless `sync/update.go:144-150` moves Updating→Updated only when
`!IsRoleInstanceHooked` (NONE match) and otherwise completes to Normal — so
with a multi-handler hook partially restored after the update, **stateless
completes the handshake (Normal, readiness key released) while stateful keeps
waiting**. For single-handler hooks the two are identical. The stateful choice
is the stricter one and matches the PR body's claim, but issue #534 Task 3 says
"Follow the existing Stateless lifecycle semantics" — the modes now disagree in
this corner. Empty-handler edge also diverges (stateless Updating→Updated→Normal
over two reconciles; stateful straight to Normal). Differential pair:
TestClaudeF2PartialRestoreKeepsStatefulHandshakeOpen (stateful waits) +
TestClaudeF2StatelessPartialRestoreCompletesImmediately (stateless completes).

### F3 — hard error on unexpected lifecycle state
`inPlaceUpdateInstance`'s `default:` branch (:1367-1368) returns
"not allowed to in-place update instance %s in state %s" for any state outside
{"", Normal, PreparingUpdate, Updating, Updated} — notably **PreparingDelete**.
Stateless skips PreparingDelete targets instead (sync/update.go:94). Today a
Stateful-set instance can carry PreparingDelete only via a stateless→stateful
pattern switch; once issue #534 Task 1 lands, PreparingDelete becomes a normal
stateful state, and a stale-rev update target stuck in PreparingDelete would
wedge `updateStatefulInstanceSet` in an error/requeue loop instead of skipping
the target. Test documents the error on head (base performs the update without
error).

### F4 — MarkNotReady silently no-ops without the gate (Task 2 dependency)
`readiness.addNotReadyKey` (instance_readiness_utils.go:36-38) returns nil when
the instance lacks the RoleInstanceCustomReady readiness gate, and main's
`getInstanceReadyCondition` (instance_status.go:323+) does not factor the
CustomReady gate into Ready at all — both are the Task 2 half of issue #534,
deliberately out of this PR's scope ("combined NotReady validation with the
separate Task 2 fix passed"). Consequence on head-alone: entering PreparingUpdate
with MarkNotReady=true writes the state label but no CustomReady condition and
does not flip Ready — the documented markPodNotReady behavior is inactive until
Task 2 lands. The hook *waiting* itself works regardless (it does not depend on
the instance being NotReady), and the OrderedReady resume path
(`preparingUpdate && baseUnavail`) simply stays dormant. Sequencing note, not a
defect of this PR — but the merge order matters and the envtest coverage added
here exercises only MarkNotReady-absent configurations.

### F5 — recreate fallback is not hook-protected (pre-existing, Task 1 dependency)
Two canaries: (a) when the pending diff is not in-place-able from the start
(component size change), `CanUpdateInPlace` is false → `handled=false` →
`deleteInstance` runs immediately even though the hook matches — the instance
never reaches PreparingUpdate; (b) an instance already admitted to
PreparingUpdate by an in-place-able diff is **deleted while its hook still
matches** once a newer revision makes the diff non-in-place-able. The doc
comment "Waiting must never fall back to deletion" holds only while the diff
stays in-place-able. Base behaves the same (first canary passes on base), so
this is pre-existing recreate-fallback behavior, and deletion protection is
explicitly PreDelete's job (issue #534 Task 1, not in main yet). Recorded so
the gap is visible when Task 1 lands and so the doc comment can be qualified.

## Live run notes
Not run this round (unit + integration per the debate setup). The PR's own
E2E suites (E2E lifecycle/update/rollout, envtest, unit, lint) are all SUCCESS
on head 2d648e5 per GitHub statusCheckRollup.

## Proposed fixes (NOT applied to production here)
- **F1**: drop the two `Expect(got)` calls, or introduce a stateful-side
  ResourceVersionExpectation store that the stateful set reconciler
  Observe()s (mirroring statelessmode/reconciler.go:159-170).
- **F2**: pick one predicate for both modes (prefer the stricter
  `!IsRoleInstanceAllHooked`, i.e. change stateless) or document the divergence
  as intentional.
- **F3**: skip the target (log + return handled=true) for PreparingDelete
  instead of erroring, mirroring stateless sync/update.go:94.
- **F4**: merge after (or together with) the Task 2 readiness fix; consider a
  temporary log line when MarkNotReady is requested but the gate is absent.
- **F5**: when Task 1 lands, route the recreate fallback through the PreDelete
  hook; until then qualify the "never fall back to deletion" comment.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/inplace-hook-claude` of the reviewer fork
(https://github.com/cheyang/rbg.git), production code untouched, so it grafts
onto whatever the fixed code is.

```bash
git fetch https://github.com/cheyang/rbg.git verify/inplace-hook-claude
git checkout <fixed-ref>
git checkout FETCH_HEAD -- docs/verification/inplace-hook-claude \
  pkg/reconciler/roleinstanceset/statefulmode/inplace_hook_claude_verify_test.go \
  pkg/reconciler/roleinstanceset/statelessmode/sync/update_claude_verify_test.go
bash docs/verification/inplace-hook-claude/scripts/re-verify.sh   # no ref = current PR head
```

Polarity table (what flips when fixed):

| Test | Polarity | Fixed means |
|------|----------|-------------|
| TestClaudeP0… | contract | stays green (regression guard for the PR's own fix) |
| TestClaudeF1… | canary | flips red when the Expect calls are removed/redirected |
| TestClaudeF2…stateful / …stateless | canary pair | flip when either mode's completion predicate changes |
| TestClaudeF3… | canary | flips red if PreparingDelete targets become skip-not-error |
| TestClaudeF4… | canary | flips red when Task 2 makes the gate auto-added |
| TestClaudeF5… (x2) | canary | flip red when recreate fallback routes through PreDelete |

Kickoff prompt for a fresh agent:
"Checkout verify/inplace-hook-claude from https://github.com/cheyang/rbg.git,
read docs/verification/inplace-hook-claude/README.md, run
scripts/re-verify.sh against the new PR head of
https://github.com/sgl-project/rbg/pull/537, and report per finding
Fixed/Still-broken/Partial/Harness-update honoring the polarity table."
