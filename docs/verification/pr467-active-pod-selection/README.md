# pr467-active-pod-selection — bug verification

Reproducible evidence for the findings raised while reviewing
[PR #467](https://github.com/sgl-project/rbg/pull/467) (`test: select active pods in restart policy e2e`).

Harness runs against the **PR head** (`de5cfe57986776ce91ebe8e7b19587a1326cfeb7`, `pr-467`).
Production code is untouched — the diff is purely additive (this dir + one `_test.go`).

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `findActivePod` / `filterActivePods` selection semantics (pure logic over a pod slice) | `GOFLAGS=-mod=vendor go test ./test/e2e/testcase/v1alpha2/ -run 'TestActivePod' -count=1 -v` |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when fixed.
> `TestActivePodPremiseNaiveItemsZeroCanPickTerminal` is a canary documenting the base behavior —
> it passes today because naive `Items[0]` *can* pick a terminal pod AND `findActivePod` does not;
> if `findActivePod` regressed to `&pods[0]`, the `FindActive*` contract cases flip to fail (the bite).

## Problem premise (P0)

Run against the **base** branch (without the patch), because the question is whether the problem exists today.

| | |
|---|---|
| Claimed symptom | "CI job e2e-test failed in the restart-policy stability test while waiting for a Pod to be recreated with a new UID." (PR body) |
| Linked issue | none (PR body section Ⅲ: "NONE") — tests-only PR, premise carved out; symptom taken from the cited CI run |
| Reported component | restart-policy stability e2e selection logic (`test/e2e/testcase/v1alpha2`) |
| Patched component | same — `restart_policy_stability.go` (`findActivePod`, `filterActivePods`, 6 selection sites) |
| Component match | **Yes** |
| **Verdict** | **Confirmed** (mechanism + cited CI failure) |
| Evidence | Cited base run `34807912107` (PR #466 e2e) failed: `[FAILED] Timed out after 150.001s. / pods should be recreated with new UIDs` on `e2e-backoff-test-role-1-0-*`. Mechanism proven at L1: naive `Items[0]` picks a terminal pod when one leads the slice (`results/harness-bite-naive-red.txt`); controller deletes+recreates all pods on `RecreateInstanceOnPodRestart` (`pkg/reconciler/roleinstance/sync/instance_scale.go:109-120`), and async deletion leaves a lingering terminal/terminating pod in the next `List` → `SetPodFailed` on an already-terminal pod is a no-op → no UID change → the `Eventually("recreated with new UIDs")` times out. Post-fix CI `e2e-test` on PR #467 is green. |

> Note on confidence: the flake is non-deterministic, so "reproduction" here is the **mechanism**
> (proven at L1 + bite check) plus the cited CI log, not a locally-triggered timeout. I did not run
> the live Kind e2e on base (no `KUBECONFIG` in this environment). That is consistent with
> "Confirmed" — there is positive evidence the symptom occurred — not "Not-reproduced".

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Base e2e can select a terminal pod via `Items[0]`, causing the recreation-wait timeout | 1 (unit, mechanism) | **Confirmed** | `results/harness-bite-naive-red.txt` + cited CI run `34807912107`; `results/unit-layer-green.txt` shows the fix selects the active pod |
| F1 | No unit test for `findActivePod` / `filterActivePods` selection semantics | 1 | **Confirmed (gap real); addressed by this harness** | 4 contract tests now pin the predicate; `results/unit-layer-green.txt` |
| F2 | nit: `findActivePod` and `filterActivePods` duplicate the `IsPodActive` iteration | — | nit (no test) | `restart_policy_stability.go:636,654`; optionally delegate one to the other |
| F3 | nit: `findActivePod` returns a pointer aliasing the input slice | — | nit (no test) | `restart_policy_stability.go:660`; add a one-line aliasing note to the doc comment |

### Findings not raised (cleared during review)
- Importing `k8s.io/kubernetes/pkg/controller` — **not** a concern: `k8s.io/kubernetes v1.34.2` is already a direct dep in `go.mod`, and `kubecontroller.IsPodActive` is already used in production (`pkg/reconciler/roleinstance/utils/instance_utils.go:139`) and `test/utils/utils.go:185`. The refactor is consistent with repo practice.
- `filterActivePods` refactor to `IsPodActive` is **behavior-preserving** — `TestActivePodFilterEquivalenceToInlinePredicate` proves it matches the pre-PR inline predicate (`DeletionTimestamp==nil && Phase!=Failed && Phase!=Succeeded`) on every phase/deletion combination.
- Nil-deref safety: every `findActivePod` caller asserts `NotTo(BeNil())` before dereferencing `.Name`; gomega aborts on the assertion, so a nil return fails the test with a clear message instead of panicking.

## Verdict
No blockers / majors. The fix is correct, targeted, and behavior-preserving; CI is green.
Review verdict: **COMMENT** (premise Confirmed; one minor `missingTests` gap that this harness
fills; two nits). Approving is never automatic — publish only on explicit instruction.

## Harness-bites check
Temporarily regressed `findActivePod` to naive base-style selection (`return &pods[0]`, guarded for
empty). Re-ran: `TestActivePodFindActiveSkipsTerminalAndDeleting` and
`TestActivePodPremiseNaiveItemsZeroCanPickTerminal` flip to **FAIL** (`results/harness-bite-naive-red.txt`).
Reverted; production diff empty. The harness genuinely exercises the selection path.

## Continuing after the fix (resume / next machine)

All durable state lives on branch `verify/pr467-active-pod-selection` pushed to the reviewer's fork.
Pull, then from a checkout of that branch:

```bash
# Re-verify against the latest PR head (auto-fetched via manifest.pr; no sha needed):
bash docs/verification/pr467-active-pod-selection/scripts/re-verify.sh

# Or against an explicit fixed ref:
bash docs/verification/pr467-active-pod-selection/scripts/re-verify.sh <fixed-ref>
```

Per-finding outcome: `Fixed` (contract tests green; canary flipped and must be inverted) /
`Still-broken` / `Partial` / `Harness-update`. Exit 0 iff all findings Fixed.

### Polarity table (what goes green / what flips when the fix lands)
| Test | Polarity | On PR head | If `findActivePod` regresses to `&pods[0]` |
|------|----------|-----------|-------------------------------------------|
| `TestActivePodFindActiveSkipsTerminalAndDeleting` | contract | PASS | **FAIL** (bite) |
| `TestActivePodFilterExcludesTerminalAndDeleting` | contract | PASS | PASS (filter unchanged) |
| `TestActivePodFilterEquivalenceToInlinePredicate` | contract | PASS | PASS (filter unchanged) |
| `TestActivePodPremiseNaiveItemsZeroCanPickTerminal` | canary (documents base mechanism) | PASS | **FAIL** (bite) |

### L3 (live) — not run here
A live Kind e2e run on base would reproduce the timeout; on the fix it should pass. Post-fix CI
`e2e-test` on PR #467 is already green (`gh pr checks 467 --repo sgl-project/rbg`). To run live:
provide `KUBECONFIG` and run the full `test/e2e` ginkgo suite with the `restart-policy` label filter.

### Copy-paste kickoff prompt (fresh agent, another machine)
> Clone `cheyang/rbg`, checkout `verify/pr467-active-pod-selection`, pull. Read this README. Run
> `bash docs/verification/pr467-active-pod-selection/scripts/re-verify.sh` (auto-resolves PR #467
> head from the manifest). Report per-finding Fixed/Still-broken. Advance `.last-reviewed` to the
> reviewed sha, commit, push. One active reviewer at a time.
