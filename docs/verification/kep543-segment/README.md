# KEP-543 Segment — Verification Harness (Reviewer: Claude, PR #569)

PR: https://github.com/sgl-project/rbg/pull/569 (docs-only: `keps/543-segment/{README.md,README.zh-CN.md,kep.yaml}`)
Round 2 head: `d8d53364` (delta `bd378ebd..d8d53364`, 3 commits) · base/merge-base: `ea5e82ae` · Branch: `verify/kep543-segment-claude`
Production code on this branch is **untouched except additive test files** — the diff is this harness only.

## Premise (P0) — Confirmed (re-confirmed in round 2)

Issue [#543](https://github.com/sgl-project/rbg/issues/543) is OPEN and asks for exactly this
design: a rule-level `segment` field in CoordinatedPolicy, `groupIndex = ordinal / ratio`
assignment, frozen identity labels, complete-Segment operation for gang/scaling/rolling-update,
and a KEP under `keps/<n>-segment/`. Symptom, mechanism, and component match the PR one-to-one
(evidence: `results/premise.txt`, re-checked in `results/r2-reverify.txt`). The PR legitimately
says "Related to #543" (not "fixes"), so nothing auto-closes; this is a design proposal,
consistent with the issue's KEP follow-up ask.

## Round 2 observed vs expected

The round-2 revision (commits `ae7c60b9`, `80e8897a`, `d8d53364`) addressed every round-1
[MUST]/[SHOULD] item except B-F3 (admission state machine) and C1 (advisory). Full re-run
output: `results/r2-reverify.txt`.

| ID | Claim (falsifiable) | Layer / test | Polarity | Observed at d8d53364 | Verdict |
| --- | --- | --- | --- | --- | --- |
| P0 | Issue #543 open + matches the KEP scope | `check_premise.sh` (gh) | contract | OPEN, matches | **Confirmed** |
| AC2 | markdownlint 0 issues on both READMEs | `check_docs.sh [1]` | contract | 0 issues | **Pass** |
| AC3 | all relative links + TOC anchors resolve | `check_docs.sh [2]` | contract | all resolve (incl. new Address Discovery anchor) | **Pass** |
| AC4 | EN/ZH content parity | `check_docs.sh [3]` | contract | 105 headings, 28 code blocks, 28 tables, 13 key numbers | **Pass** |
| AC5 | every "existing behavior" claim resolves to real code | `check_code_refs.sh` | contract | 16/16 symbols found | **Pass** |
| F1 (A-F1) | discovery mechanism/owner + integration/e2e coverage | `check_f1_discovery.sh` | contract | (a) PASS, (b) PASS | **Fixed** |
| F1 code claims | committed-ordinal source, surge ordinals ≥ spec.replicas | Go canaries `TestVerifyKEP543_CommittedOrdinalsAreSpecBased`, `TestVerifyKEP543_SurgeOrdinalsAtOrAboveReplicas` | contract | PASS (`results/kep543-canary.log`) | **Proven** |
| B-F1 | B=0 requires positive effective maxSurge; floor acknowledged | `check_b1_zero_budget.sh` + Go canary `TestVerifyKEP543_ExplicitZeroFloorsToOneOnlyWithoutSurge` | contract | PASS on head, FAIL on bd378ebd (control) | **Fixed** |
| R2-F1 | fail-fast semantics not paired with stale per-rule isolation language | `check_r2_consistency.sh` | contract | FAIL on head, PASS on bd378ebd, PASS under simulated fix | **Reproduced (new)** |
| F2 (A-F2) | segBatchSize tradeoff documented | doc trace | contract | paragraph present (mature 5-group example, alpha tradeoff) | **Fixed** |
| F3 (A-F3) | scale-in below minSegments outcome stated | doc trace + code | contract | paragraph present, matches `incompatibleGangConfigRequeue = 5m` | **Fixed** |
| F4 (A-F4) | rename gang boundary + test bullet | doc trace | contract | paragraph + PodGroup-active rename bullet present | **Fixed** |
| F5 (A-F5) | PR body stale note | fresh prBody | differential | note gone | **Fixed** |
| B-F2 | label examples use `<ruleID>-<groupIndex>` | doc trace | contract | `e7652f82cdee-N` == SHA-256("kv-cache")[:12] (verified) | **Fixed** |
| B-F3 | B=0 surge-admission state machine defined | doc trace | contract | region unchanged; still undefined | **Open (question)** |
| C1 | 100%-budget revision exposure | doc trace | hypothesis | unaddressed (advisory only) | **Open (advisory)** |

## Findings carried by this harness

- **R2-F1 [minor, revision-regression, OPEN]** — commit `80e8897a` made Segment validation
  fail-fast (sparse membership / invalid member-mode / ratio violations now "stop the entire
  RBG reconciliation"), but the neighboring bullet still reads "Non-Segment Stateless roles
  are unaffected" (README.md:382, ZH:380). Under fail-fast that is stale: non-Segment roles'
  RBG-level reconciliation is frozen too; only their running instances are unaffected. The
  doc also never states that blast radius (one bad Segment rule blocks target/parameter/
  discovery updates for the whole RBG). Contract check: PASS on bd378ebd → FAIL on d8d53364;
  bites-verified (`results/r2-contract-checks.txt`).
- **F1/A-F1 [major, FIXED at d8d53364]** — discovery owner named (existing RBG discovery
  ConfigMap builder), enumeration source decided (committed base target: RBG role replicas
  before a child exists, else child RIS committed ordinals), surge exclusion by construction,
  ratio validation ordered before the discovery ConfigMap update, integration + e2e bullets
  added. All code claims verified by canaries.
- **B-F1 [major, FIXED at d8d53364]** — B=0 redesigned to require positive effective
  maxSurge; admission rejects explicit 0 + zero/omitted surge; reconciliation reports
  percentage round-down-to-zero; the surge-conditional RIS floor is now acknowledged and
  aligned with (canary-pinned) instead of contradicted.
- **F2–F5 [minor/nit, FIXED at d8d53364]** — see `verify-manifest.json` for per-finding fix
  evidence.
- **B-F3 [minor, OPEN question]** — unchanged region; how candidate update scope, admit(),
  and partition lowering interrelate at B=0 (+ surge-quota reservation across groups) is
  still undefined. No executable artifact until implementation.
- **C1 [minor, OPEN advisory, unconfirmed]** — recorded from Reviewer C input in round 1;
  not re-verified this round.

## How to re-run

From a checkout of this branch:

```sh
bash docs/verification/kep543-segment/scripts/re-verify.sh        # current PR head
bash docs/verification/kep543-segment/scripts/re-verify.sh <ref>  # specific commit
```

Re-verify grafts the scripts + the Go canary test onto the fetched PR head in a temp
worktree (additive only), runs P0 + doc-quality + code-refs + F1 + B-F1 + R2-F1, prints
per-finding verdicts, and prints the `last-reviewed..head` delta range with the command to
advance `.last-reviewed`. Exit 0 iff every finding is fixed.

Individual checks:

```sh
bash docs/verification/kep543-segment/scripts/check_docs.sh <repo-root>
bash docs/verification/kep543-segment/scripts/check_code_refs.sh <repo-root> <ref>
bash docs/verification/kep543-segment/scripts/check_f1_discovery.sh <repo-root>
bash docs/verification/kep543-segment/scripts/check_b1_zero_budget.sh <repo-root>
bash docs/verification/kep543-segment/scripts/check_r2_consistency.sh <repo-root>
bash docs/verification/kep543-segment/scripts/check_premise.sh
go test ./pkg/reconciler/roleinstanceset/statefulmode/ -run 'TestVerifyKEP543' -count=1
```

## Polarity table (what goes green / what flips after a fix)

| Check | Now (d8d53364) | After the R2-F1 doc fix |
| --- | --- | --- |
| check_premise.sh | pass (needs gh) | pass |
| check_docs.sh (lint/links/parity) | pass | pass — keep as regression suite for doc edits |
| check_code_refs.sh | pass | pass — fails if the code reuse claims drift from reality |
| check_f1_discovery.sh | pass (fixed in round 2) | pass — now guards against the mechanism/coverage being dropped again |
| check_b1_zero_budget.sh | pass (fixed in round 2) | pass — guards the B=0 redesign |
| Go canaries (3×) | pass | pass — fail as Harness-update if the RIS code shape changes |
| check_r2_consistency.sh | **red (reproduction)** | **green** — requires the per-rule isolation sentence to be reworded (or the fail-fast semantics scoped per-rule again) |

## Continuing after the fix

Requirements: git, python3, `gh` (premise check only — SKIPPED otherwise),
`npx markdownlint-cli2` (lint — SKIPPED otherwise), Go toolchain (canaries only).
No cluster: this is a docs-only PR, so unit + "integration" here means repo-tree-level doc
validation against the actual code the doc cites, plus Go unit canaries pinning the code
claims the design relies on.

Kickoff prompt for a fresh agent resuming from this branch alone:

> You are resuming the KEP-543 / PR #569 verification (branch
> `verify/kep543-segment-claude`). Read
> `docs/verification/kep543-segment/verify-manifest.json` and this README, run
> `bash docs/verification/kep543-segment/scripts/re-verify.sh` (no args; it fetches the
> current PR head), apply the polarity table above when interpreting results, then commit
> the advanced `.last-reviewed` and push to the reviewer fork remote `fork`.

## CI note (round 2)

All non-skipped checks are SUCCESS at head `d8d53364` (see `results/r2-ci.txt`): the
round-1 `envtest` `TestRestartPolicy` timeout flake (upstream issue #574) is gone, and
markdown-check / lint / build / unit-test / copyright are green. No CI finding this round.
