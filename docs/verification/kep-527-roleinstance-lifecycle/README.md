# Verification harness — KEP-527 RoleInstance Lifecycle Hooks (PR #546)

Reviewer: Codex (Reviewer B). Round: initial. Reviewed head: `6e8419abef232541fe4b0df7bf6d609972902887`, base: `3612013e6f779401831f5947dc124cc4313faddb` (merge-base `09accf63`).

## What kind of PR this is

Documentation-only: the PR adds `keps/527-roleinstance-lifecycle/{README.md, README.zh-CN.md, kep.yaml}` (1,794 lines). No production code changes, so there is no Go/envtest layer to run. The harness is the docs-PR analogue of a unit test:

- **Claims checks (group 1)** — the KEP's factual statements about the *existing* system (RIS lifecycle model, `RoleInstanceCustomReady` + readiness-gate behavior, RI→Pod ownership, absence of an RI finalizer/phase field, admission webhook infra, API group) are asserted against the codebase with grep-level checks. A fail here after a rebase means the codebase moved → Harness-update / re-review the KEP's premises.
- **Finding checks (group 2)** — one check per review finding; `fail` on the reviewed head reproduces the finding, `pass` means the KEP text now addresses it. For design-level findings (F1–F3) the check is a **text proxy**: it proves the KEP *addresses the topic*, not that the added design is correct — that still needs human review.
- **Hygiene checks (group 3)** — markdownlint (the PR's own verification claim), EN/zh-CN section parity, whitespace.

All checks use **contract polarity**: pass = intended state.

## Premise (P0): confirmed

The claimed problem (issue #527: container hooks cannot express instance-level coordination; existing RIS lifecycle is limited to preDelete/inPlaceUpdate with executor-registration markers) is real and matches the codebase. All ten codebase-truth checks pass on the head. Scope divergence vs the issue (4 hooks instead of the requested 5; `preCreate`/`postDelete` dropped, `preRecover` added) is disclosed in the KEP's Drawbacks, and the PR only says `Refs #527` (no auto-close misfire).

## Observed vs expected (head 6e8419ab)

| Finding | Check | Expected (fixed state) | Observed on head | Verdict |
| --- | --- | --- | --- | --- |
| F1 preDelete vs GC cascade | CheckDeletionCascadeSpecified | pass | fail | reproduced (open) |
| F2 phase persistence undefined | CheckPhaseStorageSpecified | pass | fail | reproduced (open) |
| F3 old/new lifecycle coexistence | CheckOldNewCoexistenceRule | pass | fail | reproduced (open) |
| F4 kep.yaml defects (3 checks) | CheckKepYaml{NoSegmentComment,AuthorsSet}, CheckSeeAlsoDirsExist | pass | fail ×3 | reproduced (open) |
| F5 preDelete gate lifetime | CheckPreDeleteGateLifetime | pass | fail | reproduced (open) |
| Codebase-truth claims (10) | group 1 | pass | pass ×10 | KEP accurate |
| Hygiene (3) | group 3 | pass | pass ×3 | clean |

Raw output: `results/check-kep527-head-6e8419ab.tsv`, `results/reverify/unit.tsv`.

Harness-bites proof: the finding checks were re-run with simulated fix text applied (segment comment removed, authors filled, see-also pointed at an existing KEP, cascade-prevention / phase-storage / mutual-exclusion / preDelete-gate sentences added); all 7 flipped fail → pass, then the production files were reverted byte-identical (`git status` clean for `keps/`).

## How to run

```bash
# against the checked-out ref (must contain keps/527-roleinstance-lifecycle):
bash docs/verification/kep-527-roleinstance-lifecycle/scripts/check-kep527.sh

# against the current PR head, auto-fetched (from a checkout of THIS branch):
bash docs/verification/kep-527-roleinstance-lifecycle/scripts/re-verify.sh
```

## Continuing after a revision

1. Fetch + check out this branch, run `re-verify.sh` (no args) — it fetches the
   current PR head, grafts the harness, prints per-finding Fixed/Still-broken.
2. Review the delta printed as `last-reviewed..head`, then advance the marker:
   `echo <head-sha> > docs/verification/kep-527-roleinstance-lifecycle/.last-reviewed`
   and commit + push this branch.
3. A group-1 fail after a rebase is a Harness-update: the codebase moved; adjust
   the check and re-assess the KEP claim it backs.

Kickoff prompt for a fresh agent: "Check out verify/kep-527-roleinstance-lifecycle-codex from the cheyang/rbg fork, run docs/verification/kep-527-roleinstance-lifecycle/scripts/re-verify.sh, review the printed delta of PR sgl-project/rbg#546, then update the README table and .last-reviewed."

## Proposed fixes (summary)

- F1: specify how member Pods survive until `preDelete` completes (orphan Pods / remove ownerReferences at RI deletion start, controller-ordered Pod deletion), and correct the risk-table mitigation ("add finalizer" retains the API object only — kubelet still terminates containers once deletionTimestamp is set).
- F2: state where the top-level phase is persisted (new RI label or status field); restart recovery reconstructs from it.
- F3: state the coexistence rule between `RoleInstanceSetSpec.lifecycle` (existing) and the proposed Role-level lifecycle (mutual exclusion / precedence / deprecation plan) — Open Question 1 made concrete.
- F4: remove the "Segment" comment from kep.yaml, fill authors/reviewers, fix or drop the dangling `/keps/502-workload-status-model` see-also.
- F5: define the lifetime of the `preDelete.markNotReady` gate.
