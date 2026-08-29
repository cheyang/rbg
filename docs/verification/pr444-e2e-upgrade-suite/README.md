# Verification — PR #444: e2e v0.7.0 → current upgrade-compatibility suite

**PR:** https://github.com/sgl-project/rbg/pull/444
**Branch (this harness):** `verify/pr444-e2e-upgrade-suite` (reviewer's fork)
**PR head reviewed this round:** `3f854c12759386117e310c9e34ec0ad7c6317911`
**Previous round:** `06d5b4df52709724d3621df644d30dedcd5bbdf4`

## Round 2 re-verify (3f854c12) — all findings Still-broken

Delta: one commit (`3f854c12` "fold leader-only endpoints only where the narrowing
happened") tightening `checkServicesStable` so the leaderOnly fold applies only when the
selector actually narrowed. Same risk-class as F8, but **not a fix for any of the 10
findings** — `specs.go` (F1), `restartController` (F2), `checkNoRestarts` (F4),
`hasDefaultRestartDelays` (F8) were all untouched. No new findings. Full detail:
`results/round2.txt`.

| Finding | Round 1 | Round 2 | Note |
|---------|---------|---------|------|
| F1 | canary PASS (gap) | canary PASS (gap) | Still-broken |
| F4 | contract FAIL (repro) | contract FAIL (repro) | Still-broken |
| F5 | canary PASS (gap) | canary PASS (gap) | Still-broken |
| F8 | canary PASS (gap) | canary PASS (gap) | Still-broken |
| F2/F3/F6/F7/F9/F10 | static | static, lines unchanged | Still-broken |

**Live layer (L3) this round** (kubectl against the running cluster, read-only): the
cluster already runs rbgs at the current version with a live controller, so the upgrade
suite itself can't run here (`requireCleanCluster` would refuse; no helm/kind). For **F2**
I corroborated the substrate: the CRD conversion `caBundle` is populated and is
byte-identical to the validating-webhook `caBundle` (one CA, re-patched by the controller
together on cert rotation), served by the live controller at `rbgs-webhook-service`. This
confirms `waitCRDConversionCABundle` is a correct gate to add. The race itself was **not
reproduced** — that needs restarting the shared production controller, a disruptive action
declined on a non-test cluster.
**Layers run:** unit (deterministic). No `KUBECONFIG` was provided, so the live e2e
layer (L3) was not run; F2/F3/F7 carry a `liveNote` instead. Production/test code under
review is **untouched** — the only added file is `test/e2e/upgrade/verify_harness_test.go`.

## Round 2b — A/B/C promoted from inspection to runnable tests

A reviewer-challenge round (largely Copilot-sourced) argued four of the findings —
**A** (mid-rollout fixture excluded from every detector), **B** (`RBGSnapshot.Generation`
captured but never compared), **C** (`ownerSources` omits `ScalingAdapter`), **D**
(`captureAll` lists `RoleBasedGroupList` only, so the RBGSet root is never captured) —
are *silent false-passes*, i.e. a real upgrade regression slips through green. I had
originally filed those as code-inspection-only and overstated them as "verified." That
was wrong. I now have **runnable tests** for three of the four:

| Finding | Round 2 (inspection) | Round 2b (runnable) | Bites-check |
|---------|----------------------|---------------------|-------------|
| A | inspection | **H5 canary PASS** — `countSurvivors` returns the partition while a surviving pod's restartCount bumped + label rewrote; the settle detectors (run with the mid-rollout RBG in the skip list, exactly as specs.go does) report nothing; the same detectors without the skip DO catch it. Silent false-pass reproduced. | arm (c): drift is detectable when not excluded |
| B | inspection | **H6 canary PASS** — snapshots differing only in `RBGSnapshot.Generation` (1→2, stored spec rewritten — the #433 mechanism) are reported by no detector. Silent false-pass reproduced. | applied a Generation comparison to `checkSameRBGSet` → H6 flipped FAIL → reverted → PASS |
| C | inspection | **H7 canary PASS** — `ownerSources()` returns RIS/RI/Deploy/STS/LWS, no `ScalingAdapter`; a ScalingAdapter-backed RBG's owner is never captured. | appended `ScalingAdapter` to `ownerSources()` → H7 flipped FAIL → reverted → PASS |
| D | inspection | still inspection-only — `captureAll` is a capture-path gap needing a fake controller-runtime client; `test/e2e` has no `fake.NewClientBuilder` usage, so a runnable capture test is a larger lift not done this round. | — |

Raw output: `results/unit-run.txt` (H1–H7; canaries PASS, H2 contract FAIL = reproduction).
Production code is **untouched** (every bites-check was applied and reverted; `git diff
snapshot.go` is empty).

**Verdict implication.** A, B, C, D are `major` and three of them (A/B/C) now have
runnable proof that a real upgrade regression can pass green. Under the severity policy,
`major` → `REQUEST_CHANGES`. The earlier COMMENT verdict was wrong to call these
non-silent; the corrected verdict is **REQUEST_CHANGES**. *Not yet posted* — gated on
explicit user confirmation (see end of file).

## Premise (P0) — Confirmed

Coverage premise, not a runtime symptom: the base branch ships no upgrade-compatibility
suite, and the two motivating regressions — #433 (revision-serialization spurious rollout)
and #439 (spurious-rollout / identity labels) — were both merged after being found by hand,
not by CI. The gap the PR addresses is real. (Tests-only PR; the problem-establishment step
is the carve-out, but the premise is recorded for completeness.)

## Observed-vs-expected

| ID | Sev | Finding | Layer | Polarity | Verdict | Evidence |
|----|-----|---------|-------|----------|---------|----------|
| F1 | major | Phase-3 settle guard runs only `checkSameRBGSet`+`checkNoPodChurn`+`checkNoRestarts`; misses ongoing label/selector/owner churn during the 30s window | unit | canary | **Confirmed (gap)** | H1: the three settle detectors report nothing for a pure label drift; `checkPodMetadataStable` reports the same drift. specs.go:318-320 |
| F2 | major | `restartController` omits `waitCRDConversionCABundle`; cold restart can leave the CRD conversion caBundle stale → phase-4 v1alpha1 TLS error | static + L3 | — | **Confirmed (gap, static)** | upgrade.go:268-271 vs waitForUpgradeReady 142-148. Impact not demonstrated (needs cluster). |
| F3 | minor | `teardownFromRelease` deletes CRDs without waiting for NotFound → back-to-back runs hit `requireCleanCluster` on Terminating CRDs | static | — | **Confirmed (gap, static)** | install.go:292-298 (Delete loop, no wait) |
| F4 | minor | `checkNoRestarts` ignores containers present in `after` but absent in `before` (upgrade-injected sidecar invisible if pod not recreated) | unit | contract | **Confirmed (reproduced)** | H2 FAILS on PR code; reverse-loop fix flips it green (bites-checked). snapshot.go:538-551 |
| F5 | minor | `PodFacts.Phase` and `OwnerUIDs` captured but never asserted | unit | canary | **Confirmed (gap)** | H3: a Running→Failed flip is reported by no detector. snapshot.go:48-74 |
| F6 | minor | Phase-4 v1alpha1 pod churn compared vs phase-1 `before`, not a local pre-annotation baseline → misattributes upgrade churn to the v1alpha1 write | static | — | **Confirmed (gap, static)** | specs.go:~679-683 |
| F7 | minor | `requireCleanCluster` checks CRDs registered, not leftover CRs (narrow: prior failed run with stuck finalizers) | static | — | **Plausible (narrow)** | install.go:106-108 |
| F8 | minor | `hasDefaultRestartDelays` treats absent fields as matching default → folds an empty config, hiding a conversion bug | unit | canary | **Confirmed (gap)** | H4: `hasDefaultRestartDelays({}) == true`. snapshot.go:711-721 |
| F9 | nit | Mid-rollout partition check can't distinguish "partition removed" vs "partition ignored" | static | — | **Confirmed (diagnosis)** | specs.go:282-288 |
| F10 | nit | `checkStillReady` iterates `beforeSnap` only; a role with no before status entry is never compared | static | — | **Plausible** | snapshot.go:1003-1026 |

**No blockers.** Two majors (F1, F2) are gaps that could mask a real upgrade regression
or produce a misleading failure; both are one-line / one-call fixes. The rest are narrow
coverage gaps.

## How polarity reads here

- **contract** (F4): asserts the intended behavior. FAILS on the PR code — that red result
  *is* the reproduction. Goes green once the author fixes the detector.
- **canary** (F1, F5, F8): asserts the current (gap) behavior. PASSES on the PR code
  (gap present); flips RED once the gap is closed — **invert it then** (promote to a
  contract test).

So "all green" today does **not** mean "fixed": three canaries are green precisely because
the gaps exist. After the author's fixes, F4 stays green and F1/F5/F8 flip red and must be
inverted. `scripts/re-verify.sh` applies this automatically.

## Proposed fixes (not shipped)

- **F1:** add `checkPodMetadataStable(fs, quietFirst, quietAfter)` (and ideally the rest of
  the detector set) to the phase-3 settle comparison in `specs.go`, so the "is the controller
  still moving" guard covers the same fields the upgrade proof does.
- **F2:** add `waitCRDConversionCABundle(f)` after `waitValidatingWebhookCABundle(f)` in
  `restartController` (upgrade.go).
- **F4:** add a reverse loop over `afterFacts.RestartCounts` in `checkNoRestarts`
  (snapshot.go) reporting containers absent from `before`. (Bites-checked: this exact
  patch makes H2 pass.)
- **F3:** wait (`Eventually` on `listOwnedCRDs` → empty) before `teardownFromRelease` returns.
- **F5:** assert `Phase` (and `OwnerUIDs`) in a detector.
- **F6:** capture a pod baseline immediately before the v1alpha1 annotation, like
  `storedBefore`.
- **F8:** require both delay fields to be **present** and matching, not "absent or matching".
- F7/F9/F10: narrow; see manifest `staticFindings`.

## How to run

```bash
# unit layer only (no cluster)
go test ./test/e2e/upgrade/ -run 'TestVerifyHarness' -v
```

Raw output: `results/unit-run.txt`.

## Continuing after the fix (handoff)

From a checkout of this branch on any machine:

```bash
# grafts this harness onto the PR's current head, runs the unit layer, prints
# Fixed / Still-broken / Partial / Harness-update per finding (canaries flip on fix)
bash docs/verification/pr444-e2e-upgrade-suite/scripts/re-verify.sh
```

With no arg, `re-verify.sh` fetches the current PR head from `manifest.pr`
(`https://github.com/sgl-project/rbg/pull/444`) and resolves the delta start from
`.last-reviewed`. Exit 0 iff every finding is Fixed (canaries counted as fixed only when
they flip to fail).

**Polarity table after a fix lands:**

| Finding | Before fix | After fix | Action |
|---------|-----------|-----------|--------|
| F1 (canary) | PASS (gap) | FAIL (gap closed) | invert → contract |
| F4 (contract) | FAIL (repro) | PASS | none |
| F5 (canary) | PASS (gap) | FAIL (gap closed) | invert → contract |
| F8 (canary) | PASS (gap) | FAIL (gap closed) | invert → contract |
| F2/F3/F6/F7/F9/F10 | static | re-check by inspection (re-read the cited lines) | — |

### Kickoff prompt for a fresh agent (resume from the branch alone)

> You are resuming the review-pipeline verification of PR
> https://github.com/sgl-project/rbg/pull/444. The harness lives on branch
> `verify/pr444-e2e-upgrade-suite` in the reviewer's fork. Read
> `docs/verification/pr444-e2e-upgrade-suite/README.md` and `verify-manifest.json`,
> then run `bash docs/verification/pr444-e2e-upgrade-suite/scripts/re-verify.sh`
> (no sha — it auto-fetches the PR head). Report per-finding Fixed/Still-broken/Partial,
> inverting canaries that flipped. F2/F3/F6/F7/F9/F10 are static — re-check by reading the
> cited lines; if a fix changed the code shape, mark Harness-update and adjust. Advance
> `.last-reviewed`, commit and push.

## Live layer (L3) — not run

`make test-e2e-upgrade` against a clean kind cluster would exercise F2 (read a v1alpha1
fixture through the conversion webhook right after `restartController`) and F3/F7
(back-to-back runs hitting `requireCleanCluster`). Not run this round: no `KUBECONFIG`,
and one full run is ~60m with published images.
