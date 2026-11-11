# Verification — PR #538 "feat: implement stateful selective scale-down" (Reviewer B / Codex)

- PR: https://github.com/sgl-project/rbg/pull/538
- Reviewed head: `2c347c471633b8ecee2c4d59079749b139632359` (base `fd160cc964b0e1438bead4ad7324d0a8f0bf5eac`)
- Harness branch: `verify/selective-scale-down-codex` (additive only; production code untouched — `git diff 07ae1194 -- ':(exclude)docs/verification/**' ':(exclude)*_verify_test.go'` is empty)

## P0 — Premise (runs against BASE, not the patch)

Issue #499 (OPEN): "Stateful mode does not expose persistent ordinal retirement."
Base `4a0765f7` contains zero references to `excluded-ordinals` / `committed-skip-ordinals`
outside `keps/` and `doc/` (see `results/premise-base.txt`); the base commit is the
docs-only KEP PR #513. **Verdict: Confirmed** — the feature gap exists; component named
by the issue (Stateful RIS membership) matches the patched component.

## Layer results (head 07ae1194)

| Layer | Command | Result |
|---|---|---|
| Unit + race | `go test -race -count=1 ./api/workloads/ordinals/... ./api/workloads/v1alpha2/... ./api/workloads/v1alpha1/... ./pkg/utils/... ./pkg/discovery/... ./pkg/reconciler/... ./internal/controller/workloads/...` | PASS (`results/unit-race.txt`) |
| Integration (real apiserver via envtest 1.31) | `go test ./test/envtest/testcase/selective_commit/... ./test/envtest/testcase/webhook/... ./test/envtest/testcase/rbg/...` | PASS (`results/envtest.txt`) |
| Live (kind cluster) | skipped per debate policy | — |

## Findings

| ID | Severity | Claim | Evidence |
|---|---|---|---|
| F1 | blocker (CI gate) | `make lint` fails on PR-added code: gocyclo 45>30 on `reconcileSelectiveMembership`, goconst on `"InvalidCommittedOrdinals"` (4 occurrences), prealloc ×3 in new test files. CI `lint` job 124766651800 is red because of this PR. | `results/lint-repro.txt` (local gocyclo repro + CI log lines) |
| F2 | minor | `validateCommittedMembership` drops *all* `FailedScale` conditions (any reason) when the committed record is invalid, replacing unrelated scale-failure history. | canary `TestVerifyF2FailedScaleConditionsReplacedOnInvalidRecord` (`results/f2-canary.txt`) |

F1 is a CI-gate finding: it does not map to a runtime contract test, so it is not in
`verify-manifest.json`'s `findings`; re-verification is `make lint` going green in CI.

## Harness-bites check (F2 canary)

Scoped the condition filter to `condition.Reason == "InvalidCommittedOrdinals"` (the
proposed fix) → canary flipped to FAIL ("unrelated FailedScale condition survived").
Reverted; production diff empty again; canary green. See `results/f2-canary.txt`.

## Continuing after a fix revision

```bash
git fetch https://github.com/cheyang/rbg.git verify/selective-scale-down-codex
git checkout verify/selective-scale-down-codex
bash docs/verification/selective-scale-down-codex/scripts/re-verify.sh   # no sha needed; resolves PR head from manifest.pr
```

Polarity: F2 is a canary — green means "current replace semantics still in effect",
red means the semantics changed (invert the assertions if the change is the scoped
filter). The PR's own suites (ordinals property tests, selective_commit envtest,
webhook admission envtest, rbg selective scale-down envtest) are all contract tests
and must stay green; `re-verify.sh` runs the unit layer plus the harness canary and
reports per-finding Fixed/Still-broken.

## Round 2 (head f3d44874, base fd160cc9)

Re-ran the full harness via `scripts/re-verify.sh f3d448746b02a10ebff161885e98d352d19d393f`
(unit + integration). Raw summary: `results/round2-reverify.txt`; machine output under
`results/reverify/`.

| Finding | Round-2 verdict | Evidence |
|---|---|---|
| F1 lint gate | **FIXED** — pinned golangci-lint v2.13.1 reports 0 issues on f3d44874; CI lint green | `results/round2-reverify.txt` |
| F2 FailedScale erasure | **STILL PRESENT** — canary still passes; predicate unscoped (constant extracted only) | re-verify unit |
| F3 surge on user-excluded ordinal | **STILL PRESENT** — new canary `TestVerifyF3SurgeLandsOnUserExcludedOrdinal` passes on head: committed [0,2,4] + E "1,3,5" + maxSurge=1 → surge ordinal 5. The ce03814f on-demand surge rework still consults only the committed record | re-verify unit |

Also green on head: PR's new envtest coverage for the managed-fields ownership fold
(`TestOrdinaryRISRemovesMetadataAfterManagedFieldsHandoff`), dense-orphan adoption
(`TestSelectiveDenseOrphanReapply`) and shared-metadata preservation
(`TestSelectiveMembershipPreservesSharedMetadata`); webhook owner-transition admission
tests; full unit sweep. `.last-reviewed` advanced to f3d44874.

## Round 3 (head 2c347c47, base fd160cc9 unchanged)

Delta `f3d44874..2c347c47`: five commits, each targeting a round-2 open finding
(eec29c95 discovery dense-orphan allowance, def9bf63 FailedScale predicate scoping,
e340f9f7 per-role invalid-input isolation, 5fc8c972 surge avoids mirrored exclusions,
2c347c47 recovery runbook + early-drain contract + recovery envtest).

Re-ran `scripts/re-verify.sh` (unit + integration). Raw summary: `results/round3-reverify.txt`;
machine output under `results/reverify/`.

| Finding | Round-3 verdict | Evidence |
|---|---|---|
| F2 FailedScale erasure | **FIXED** — canary flipped red on 2c347c47 (predicate now scoped to `InvalidCommittedOrdinals`); inverted to contract test `TestVerifyF2FailedScaleConditionsPreservedOnInvalidRecord`; bites check: FAILS on f3d44874 | `results/round3-reverify.txt` |
| F3 surge on user-excluded ordinal | **FIXED** — 5fc8c972 mirrors E onto the child RIS (allocation only) and `nextSurgeOrdinal` jumps exclusion ranges. Round-2 canary kept passing because it modeled the pre-mirror child (record only) — a shape the writer no longer produces (Harness-update); inverted to contract test `TestVerifyF3SurgeSkipsMirroredExclusions` (surge = 6, not excluded 5) + characterization `TestVerifyF3LegacyChildWithoutMirror` (pre-mirror upgrade window). Bites check: contract test FAILS on f3d44874 | `results/round3-reverify.txt` |
| F5 preCheck blocks whole RBG | **FIXED** — per-role validation + skip; cleanup now always runs (Step 8/9 `errors.Join`); Ready=False/event moved into `updateRBGStatus`. Author envtest `isolates invalid bypassed input while reconciling other roles and cleaning removed roles` green in my integration layer | `results/reverify/integration.out` |
| F6 corrupt record wedge | **FIXED (runbook route)** — guarded UID/resourceVersion/replicas repair runbook documented (EN + zh-CN); both recovery paths envtested through the full RBG controller incl. failed-guard cases | `results/reverify/integration.out` |
| F8 discovery gates dense-orphan adoption | **FIXED** — ownerless dense child with matching group-name label is readable by discovery (sparse/foreign still rejected); full-controller envtest (delete + recreate with exclusions, UID preservation, sparse ConfigMap addresses) green | `results/reverify/integration.out` |
| Q2 discovery early-drain contract | **ANSWERED** — docs now state commit-time shrinking is an intentional early-drain signal | doc/features/selective-scale-down.md |

Full sweep on head: unit 47,477 test results, 0 failures; integration `ok` for
selective_commit (24s), webhook (40s), rbg (100s) — includes the author's new
isolation / orphan-adoption / recovery / surge-at-6 envtests. Pinned golangci-lint
v2.13.1 on a pristine 2c347c47 checkout: 0 issues (CI lint also green).

Known residual (nit, documented): a child RIS last written by a pre-mirror
controller carries the committed record but no mirrored E; if a surged rollout is
already pending across the upgrade, one surge allocation can still land on an
excluded ordinal before the first post-upgrade sync mirrors E. Transient and
self-limiting; pinned by `TestVerifyF3LegacyChildWithoutMirror`.

`.last-reviewed` advanced to 2c347c47.
