# Verification — KEP-502 workload status model (PR #528) — Reviewer A (Claude)

- PR: https://github.com/sgl-project/rbg/pull/528
- Reviewed head: `f05e1dd83d2691e4157bf1bd3548fe405b2a49d5` (base `885d655bd1b4c26e6d14837709eeda675f3ae886`)
- Harness branch: `verify/502-workload-status-model-claude` (pushed to the reviewer's fork `cheyang/rbg`)
- This is a **docs-only PR** (adds `keps/502-workload-status-model/{README.md,kep.yaml}`); the code
  under review is a design document, so the harness checks the *document's* contract: its factual
  claims about the current API, and the internal consistency of its machine-readable reason taxonomy.
- Live layer (L3): skipped — debate setup runs unit + integration only, and a KEP has no runtime.

## Premise (P0) — Confirmed

The KEP's motivation claims were all verified against the base/HEAD code (`C5`, green):

| Claim in KEP motivation | Verified at head |
| --- | --- |
| RBG / RIS / RI lack `status.phase` | no `json:"phase"` in any of the three v1alpha2 status types |
| RBG declares `Progressing` / `RollingUpdateInProgress` but controllers never write them | `RoleBasedGroupProgressing`/`RollingUpdateInProgress` (and the bare string literals) have zero writers in `internal/` + `pkg/`; only Ready and GangConfigured are written |
| RBG Ready ignores revision convergence | `rolebasedgroup_controller.go:783-793` computes Ready purely from `role.Replicas == rs.Replicas == rs.ReadyReplicas` |
| RIS / RI `FailedScale` / `FailedUpdate` record operation failures | declared in both v1alpha2 enums; written by `statelessmode/reconciler.go:237,251` and `instance_reconciler.go:191,235` |

Premise verdict: **Confirmed** — the limitations the KEP describes are real on the current code.

## Observed vs expected

| Finding | Check | Layer | Polarity | At head | Meaning |
| --- | --- | --- | --- | --- | --- |
| F1 phantom `rolloutReplacementState` (reviewer hypothesis) | C1 | unit | contract | **pass** | **Refuted** — the field exists (`roleinstanceset_types.go:201`, present at base too). All 12 retained RIS fields + 3 preserved RI fields exist. |
| F2 `Preparing` candidate reason never defined | C2 | unit | contract | **fail** | Confirmed — defect present in the KEP |
| F3 `RolesNotReady` vs `RoleNotReady` near-collision unacknowledged | C3 | unit | contract | **fail** | Confirmed — defect present in the KEP |
| F4 `InPlaceUpdateNotReady` casing vs code `InplaceUpdateNotReady` | C4 | unit | contract | **fail** | Confirmed — defect present in the KEP |
| P0 motivation claims | C5 | unit | contract | pass | premise Confirmed |
| YAML examples / kep.yaml parse | C6 | unit | contract | pass | PR body claim reproduced |
| markdownlint-cli2 with repo config | C7 | integration | contract | pass | PR body claim reproduced |

Raw output: `results/unit-l1-at-head.txt`, `results/bite-check-with-proposed-fixes.txt`,
`results/markdownlint-integration.txt`.

## Harness-bites check

The proposed fixes were applied to a **scratch copy** (`/tmp/kep502-bite/README.md`):
drop `Preparing` from the Progressing=True row, add one sentence acknowledging that
`RolesNotReady` (Progressing waiting reason) is distinct from the retained `RoleNotReady`
(Ready=False reason), and spell the RI aggregated-readiness reason as `InplaceUpdateNotReady`
to match the code. Result: C2/C3/C4 flip to **pass** (see `results/bite-check-with-proposed-fixes.txt`),
and `git status keps/` stays empty — production files untouched.

## How to run

```bash
# from a checkout of this branch (or after re-verify.sh grafts the harness):
python3 -I docs/verification/502-workload-status-model-claude/scripts/check_kep502.py
bash    docs/verification/502-workload-status-model-claude/scripts/lint.sh .

# re-verify against the current PR head (fetches it, grafts the harness, runs both layers):
bash    docs/verification/502-workload-status-model-claude/scripts/re-verify.sh
```

Requires: git, jq, python3 (+PyYAML), and npx with a cached markdownlint-cli2 for the
integration layer (emits `not-run` → `HARNESS-UPDATE` if unavailable).

## Continuing after the fix

- All four checks are **contract** tests: on fixed KEP text they go green. There are no canaries.
- After the author amends the KEP, `re-verify.sh` (no argument) fetches the current PR head and
  reports F2/F3/F4 as Fixed / Still-broken per check.
- C1 guards the retained-field inventory (both the "RIS retains ..." and the
  "Preserve restartCount ..." sentences) against future edits naming nonexistent fields.
- C5 guards the motivation's factual claims; if a future controller starts writing RBG
  `Progressing` (e.g. a partial implementation lands before the KEP is agreed), C5 flips red and
  the premise needs re-review.
- `.last-reviewed` holds `f05e1dd…`; advance it after each reviewed round.

### Kickoff prompt (fresh agent, from this branch alone)

> You are resuming a review of https://github.com/sgl-project/rbg/pull/528 (KEP-502, docs-only).
> Read `docs/verification/502-workload-status-model-claude/verify-manifest.json` and this README.
> Run `bash docs/verification/502-workload-status-model-claude/scripts/re-verify.sh` (no args) to
> test the current PR head; report F2/F3/F4 verdicts and review the `last-reviewed..head` delta it
> prints. All checks are contract-polarity (green = fixed). Do not touch production files.
