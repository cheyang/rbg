# KEP-133 "mark replaced by KEP-576" (PR #577) — review verification (Codex)

Reproducible evidence for the review of https://github.com/sgl-project/rbg/pull/577
(docs-only KEP status change; head `d4d8563630aafba179e094210d6c53e7e1346583`,
base `ea5e82ae59169e0e954fb1c7068f9480fb4b7375`, mode: initial round 1).

The PR changes only `keps/133-configmap-refine/{README.md,kep.yaml}`: adds a
"Status: Replaced" tombstone pointing at `/keps/576-compact-discovery-configmap`,
flips `kep.yaml` `status: provisional -> replaced`, and trims trailing blank
lines. Verification therefore targets (a) the factual premise the tombstone
asserts about shipped code, (b) template legality of the new status, and
(c) the one finding — the tombstone's link target does not exist in any tree
this PR can land on by itself.

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (codebase-fact) | `pkg/discovery` builder output shape; kep.yaml template enum; tombstone link target existence | `go test ./pkg/discovery/ -run 'TestKEP133' -v` |
| 2. Integration | skipped — docs-only PR; no API-server behavior changes | — |
| 3. Live | skipped — no runtime change | — |

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom (PR body) | "KEP-133's compact-format proposal was only partially implemented by #134, which shipped the RBG-level shared ConfigMap carrying the **full address enumeration** (released in v0.7.0/v0.8.0) as the `refine` mode. The compact part was never implemented, and the shipped behavior is not accurately described by any KEP today." |
| Linked issue | none closing; mentions #576 (OPEN 2026-11-13, "Compact declarative discovery ConfigMap (supersedes KEP-133)") — matches |
| Reported component | docs/design record (`keps/133-configmap-refine`) vs shipped `pkg/discovery` refine mode |
| Patched component | the same KEP-133 docs — matches |
| **Verdict** | **Confirmed** |
| Evidence | `TestKEP133_Premise_RefineModeShipsFullEnumeration` PASS on head and on base (results/reverify-against-base-ea5e82ae.txt): built config for replicas=3 lists 3 per-instance `address:` entries, no compact `excludes` format. Independently: PR #134 merge commit `3ddf8af6` (KEP + `feat(configmap): imply refine`) is contained in tags `v0.7.0`/`v0.8.0`; `constants.RefineDiscoveryConfigMode = "refine"` exists. |

## Summary of results

| ID | Claim under test | Layer | On head d4d85636 | Verdict |
|----|------------------|-------|----------------|---------|
| P0 | refine mode ships full per-instance enumeration (premise) | 1 | PASS (also PASS on base) | premise confirmed |
| C1 | `replaced` is template-legal; KEP-133 kep.yaml uses it | 1 | PASS | supports the change |
| F1 | tombstone link target `/keps/576-compact-discovery-configmap` exists in the same tree | 1 | **FAIL** | finding open: merge-order dependency on PR #578 |

F1 detail: `keps/576-compact-discovery-configmap/` exists only in the still-open
PR #578 (head `b83bff8c`), not on `main` and not in PR #577's own tree
(`git ls-tree` on both base and head: absent). KEP-576's kep.yaml reciprocally
declares `replaces: ["/keps/133-configmap-refine"]`, so the pairing is
intentional — but if #577 merges before #578, main carries a dead link and a
`replaced` status whose replacement is absent. Self-healing once #578 lands.

## Harness-bites check (step 4)

- F1 bites: with `keps/576-compact-discovery-configmap` materialized from
  `b83bff8c` onto this branch (`git checkout b83bff8c -- keps/576-compact-discovery-configmap`),
  `TestKEP133_TombstoneLinkTargetExists` flips to PASS
  (results/harness-bites-f1-target-materialized.txt); the tree was restored
  afterwards and the production diff is empty (the harness is additive only).
- Cross-run matrix (results/reverify-*.txt):
  - vs base `ea5e82ae`: P0 FIXED(passes on base — premise is about shipped code,
    and this docs PR changes no code); C1/F1 fail there because the tombstone
    and the `replaced` status do not exist pre-PR (contract not applicable).
  - vs PR #578 head `b83bff8c`: P0 passes; C1 fails (KEP-133 still
    `provisional` on that tree — C1 pins THIS PR's own change); F1 fails
    (tombstone README absent there). The true F1 flip evidence is the
    materialized-target run above.

## Continuing after a fix / next round

- Re-run everything: `bash docs/verification/kep-133-mark-replaced-codex/scripts/re-verify.sh`
  (no arg → auto-fetches current PR #577 head; or pass an explicit ref).
- Expected end state once #578 has merged first and #577 is rebased:
  P0 pass, C1 pass, F1 pass → `RESULT: all findings fixed`.
- `.last-reviewed` holds `d4d8563630aafba179e094210d6c53e7e1346583`; advance it
  after each round.
- Kickoff prompt for a fresh machine: "Check out branch
  `verify/kep-133-mark-replaced-codex` from the reviewer fork
  (https://github.com/cheyang/rbg.git), read
  `docs/verification/kep-133-mark-replaced-codex/README.md`, then run
  `bash docs/verification/kep-133-mark-replaced-codex/scripts/re-verify.sh`."
