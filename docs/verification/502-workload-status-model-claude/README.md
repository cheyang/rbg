# Verification — KEP-502 workload status model (PR #528) — Reviewer A (Claude)

- PR: https://github.com/sgl-project/rbg/pull/528
- Round 2 reviewed head: `6335eac61b7ef71d7479a8b2686829d7e59ce3e1` (round 1: `f05e1dd83d2691e4157bf1bd3548fe405b2a49d5`; base `885d655bd1b4c26e6d14837709eeda675f3ae886`)
- Harness branch: `verify/502-workload-status-model-claude` (pushed to the reviewer's fork `cheyang/rbg`)
- This is a **docs-only PR** (adds `keps/502-workload-status-model/{README.md,kep.yaml}`); the code
  under review is a design document, so the harness checks the *document's* contract: its factual
  claims about the current API, and the internal consistency of its machine-readable reason taxonomy.
- Live layer (L3): skipped — debate setup runs unit + integration only, and a KEP has no runtime.

## Round 2 (delta `f05e1dd..6335eac`) — re-verify verdict

The author pushed 4 docs commits addressing the round-1 consensus findings. Re-run of
`re-verify.sh` at the new head:

| Finding | Check | At f05e1dd | At 6335eac | Verdict |
| --- | --- | --- | --- | --- |
| F2 `Preparing` undefined (ledger F1-preparing-undefined) | C2 | fail | **pass** | **FIXED** — token dropped from the Progressing=True row |
| F3 `RolesNotReady`/`RoleNotReady` unacknowledged (ledger F5) | C3 | fail | **pass** | **FIXED** — dedicated distinction paragraph at README.md:184 |
| F4 `InPlaceUpdateNotReady` casing (ledger F6) | C4 | fail | **pass** | **FIXED** — now matches the code spelling |
| P0 motivation claims | C5 | pass | pass | premise still Confirmed |
| retained-field inventory | C1 | pass | pass | still accurate (incl. `rolloutReplacementState`) |
| YAML/kep.yaml + markdownlint | C6/C7 | pass | pass | tooling claims still reproduce |
| **F9 RBG transition-input vocabulary inconsistent** (new, revision regression) | C8 | **pass** | **fail** | **STILL-BROKEN** — see below |
| **F10 RIS activity reasons unenumerated** (new, pre-existing nit) | C9 | fail | fail | **STILL-BROKEN** — see below |

Round-1 findings owned by Reviewer B (Codex) were verified fixed by direct reading of the delta
(priority rule now lists `Activity` and states child-readiness subsumption at :169/:182, making the
RBG example derivable; `TemplateInvalid`/`DependencyCycle` added to the Progressing=False row plus
prose mapping at :194; the terminating `Ready` trigger relabeled and covered in risks/upgrade/unit
tests at :237/:282/:529/:464; TOC entries added at :15-16; the event-budget risk row at :119;
Q1 answered at :345; Q2 answered by the new Validation Ownership section :186-194; Q3 accepted
trade-off stated at :341; Copilot C4431017493 precedence fix at :339; O1 row split at :295-296).

### New finding F9 (revision regression, minor)

The revision rewired RBG recovery derivation to **conditions** but left two statements on the old
**phases** vocabulary:

- README.md:463 — "RBG derives recovery facts from lower-level conditions, not a RIS `Recovering` phase"
- README.md:502 — "RBG transitions use lower-level conditions and recovery facts" (changed from "phases" in this delta)
- README.md:70 — Goals: "Define how **lower-level phases** and recovery facts trigger RBG phase transitions" (unchanged)
- README.md:211 — RBG intro: "transitions based on the current target, **lower-level phases**, and recovery facts" (unchanged)

C8 is a pure consistency contract: it **passes at f05e1dd** (all statements said "phases") and
**fails at 6335eac** — a genuine revision regression, not a pre-existing issue. An implementer
reading the RBG intro would look for child *phase* values as transition inputs, the very thing
:463 disclaims (RIS deliberately has no `Recovering` phase). Fix: align :70/:211 to the conditions
vocabulary, or use "phases and conditions" — both wordings verified to flip C8 green
(`results/bite-check-round2.txt`).

### New finding F10 (pre-existing nit)

README.md:358 — "Activity reasons may include `ScalingUp`, `ScalingDown`, and `RollingUpdate`" omits
`Creating` and `Recovering`, both defined as RIS reasons in the activity/child-readiness family
table (:162, :165). The text is identical at f05e1dd and 6335eac, so this is a late discovery on
unchanged code, not a regression; its materiality rose because the revision made RIS recovery
reporting load-bearing (:345). An implementer writing RIS's reason validator needs the complete
set in one place. Fix: enumerate all five, or state the family table is the complete source.

Raw output: `results/unit-l2-at-head.txt` (full layer at head + differential at previous head),
`results/bite-check-round2.txt`, `results/reverify/` (re-verify.sh run at 6335eac).

## Premise (P0) — Confirmed

The KEP's motivation claims were all verified against the base/HEAD code (`C5`, green at both
reviewed heads):

| Claim in KEP motivation | Verified at head |
| --- | --- |
| RBG / RIS / RI lack `status.phase` | no `json:"phase"` in any of the three v1alpha2 status types |
| RBG declares `Progressing` / `RollingUpdateInProgress` but controllers never write them | `RoleBasedGroupProgressing`/`RollingUpdateInProgress` (and the bare string literals) have zero writers in `internal/` + `pkg/`; only Ready and GangConfigured are written |
| RBG Ready ignores revision convergence | `rolebasedgroup_controller.go:783-793` computes Ready purely from `role.Replicas == rs.Replicas == rs.ReadyReplicas` |
| RIS / RI `FailedScale` / `FailedUpdate` record operation failures | declared in both v1alpha2 enums; written by `statelessmode/reconciler.go:237,251` and `instance_reconciler.go:191,235` |

Round-2 note: the new "Validation Ownership and Failure Semantics" section claims admission
(webhook) rejects dependency cycles. Verified against code: role dependencies are intra-object
(`RoleSpec.Dependencies []string`, `rolebasedgroup_types.go:272`) and `ValidateRoleDependencies`
(acyclicity check) is already called from admission on create and update
(`rolebasedgroup_admission.go:60,88`) — the KEP's classification matches the current code.

Premise verdict: **Confirmed** — the limitations the KEP describes are real on the current code.

## Round 1 observed vs expected (head `f05e1dd`)

| Finding | Check | Layer | Polarity | At f05e1dd | Meaning |
| --- | --- | --- | --- | --- | --- |
| F1 phantom `rolloutReplacementState` (reviewer hypothesis) | C1 | unit | contract | **pass** | **Refuted** — the field exists (`roleinstanceset_types.go:201`, present at base too). All 12 retained RIS fields + 3 preserved RI fields exist. |
| F2 `Preparing` candidate reason never defined | C2 | unit | contract | **fail** | Confirmed — defect present in the KEP (fixed in round 2) |
| F3 `RolesNotReady` vs `RoleNotReady` near-collision unacknowledged | C3 | unit | contract | **fail** | Confirmed — defect present in the KEP (fixed in round 2) |
| F4 `InPlaceUpdateNotReady` casing vs code `InplaceUpdateNotReady` | C4 | unit | contract | **fail** | Confirmed — defect present in the KEP (fixed in round 2) |
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

- All checks are **contract** tests: on fixed KEP text they go green. There are no canaries.
- After the author amends the KEP, `re-verify.sh` (no argument) fetches the current PR head and
  reports F2/F3/F4 (round 1, fixed at 6335eac) and F9/F10 (round 2, still open) per check.
- C1 guards the retained-field inventory (both the "RIS retains ..." and the
  "Preserve restartCount ..." sentences) against future edits naming nonexistent fields.
- C5 guards the motivation's factual claims; if a future controller starts writing RBG
  `Progressing` (e.g. a partial implementation lands before the KEP is agreed), C5 flips red and
  the premise needs re-review.
- C8 guards the RBG-transition-input vocabulary (phases vs conditions) — the round-2 regression.
  C9 guards the RIS activity-reason enumeration against the family table.
- `.last-reviewed` holds the last reviewed PR head; advance it after each reviewed round.

### Kickoff prompt (fresh agent, from this branch alone)

> You are resuming a review of https://github.com/sgl-project/rbg/pull/528 (KEP-502, docs-only).
> Read `docs/verification/502-workload-status-model-claude/verify-manifest.json` and this README.
> Run `bash docs/verification/502-workload-status-model-claude/scripts/re-verify.sh` (no args) to
> test the current PR head; report the F2/F3/F4/F9/F10 verdicts and review the `last-reviewed..head`
> delta it prints. Round-1 findings (F2/F3/F4) were fixed at 6335eac; F9/F10 were open there.
> All checks are contract-polarity (green = fixed). Do not touch production files.
