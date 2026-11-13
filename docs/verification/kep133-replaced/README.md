# Verification — PR #577 "KEP-133: mark replaced by KEP-576" — Reviewer A (Claude)

- PR: https://github.com/sgl-project/rbg/pull/577
- Head reviewed: `d4d8563630aafba179e094210d6c53e7e1346583` (base `ea5e82ae59169e0e954fb1c7068f9480fb4b7375`)
- Docs-only PR (2 files under `keps/133-configmap-refine/`). Per the pipeline's
  carve-out, formal premise validation is skipped for docs-only PRs; the premise
  facts were still checked as claims (see T4) because for a docs PR the text is the product.

## Premise (informal, docs carve-out)

The PR claims: KEP-133's compact-format proposal was only partially implemented by
#134 (RBG-level shared ConfigMap with full address enumeration shipped in
v0.7.0/v0.8.0 as `refine`), the compact part was never implemented, and KEP-576
(open PR #578 + issue #576) supersedes it. **All verifiable claims check out**
(see T4 + T3): `reconcileRefinedDiscoveryConfigMap` creates one RBG-level ConfigMap;
`ConfigBuilder` emits per-instance full `address` entries with no `excludes` compact
field; #134's merge `3ddf8af6` is an ancestor of tags `v0.7.0` and `v0.8.0`; KEP-576's
`kep.yaml` declares `replaces: /keps/133-configmap-refine`.

## Observed-vs-expected table

| finding | check | polarity | layer | result | evidence |
| --- | --- | --- | --- | --- | --- |
| (premise facts) | T4-pr134-in-v070-v080, T4-full-enumeration-shipped, T4-rbg-level-configmap, T4-genealogy-backpointer | contract | unit (git history / tree inspection) | PASS | `results/checker-2026-11-14.txt` |
| (diff-shape claim) | T1-diff-shape, T1-yaml-shape | contract | unit | PASS — README minus the tombstone block is byte-identical to base (after EOF newline normalization); kep.yaml differs only in `status: provisional -> replaced` | `results/checker-2026-11-14.txt` |
| (status validity) | T2-status-enum | contract | unit | PASS — `replaced` is in the enum in `keps/NNNN-kep-template/kep.yaml` | same |
| F1 `kep133-deadlink-order` | T3-successor-link-canary | **canary** | unit (git tree) | PASS on current head = dead link **documented** (target `/keps/576-compact-discovery-configmap` ABSENT from the PR head tree; KEP-576 ships only via open PR #578, `CHANGES_REQUESTED`, not merged as of 2026-11-14) | same |

## How to run

```bash
# from a checkout of this branch
bash docs/verification/kep133-replaced/scripts/re-verify.sh          # fetches current PR head
bash docs/verification/kep133-replaced/scripts/re-verify.sh <sha>    # or a fixed ref
```

The checker itself: `python3 docs/verification/kep133-replaced/scripts/check_kep133_replaced.py <repo> <base-sha> <head-sha>`

## Polarity table (after a fix)

| check | today | when fixed |
| --- | --- | --- |
| T1, T2, T4-* | PASS | must stay PASS (regression guard for the diff-shape / factual claims) |
| T3-successor-link-canary (canary) | PASS (dead link documented) | **flips to FAIL** once KEP-576 is in the PR head tree — finding F1 fixed; then invert the assertion (promote to contract: "link target PRESENT") |

## Findings summary (see findings-claude.md / findings-claude.json in the run workspace)

- F1 `kep133-deadlink-order` [minor, open] — tombstone link + `status: replaced` point at a KEP that is not on main yet (open PR #578). Merge #578 first / together, or rebase this PR after #578 lands; re-check the tombstone's description of KEP-576 at merge time. (Agrees with the earlier Copilot review comment; independently confirmed with git evidence.)
- F2 `kep133-link-target-readme` [nit] — link to the directory; README.md would be more direct.
- F3 `kep133-seealso-metadata` [nit] — add `see-also: ["/keps/576-compact-discovery-configmap"]` to kep.yaml to mirror KEP-576's `replaces` back-pointer.
- F4 `kep133-release-versions-wording` [nit] — "released in v0.7.0/v0.8.0" omits v0.9.0-alpha.1–3 / current main, where `refine` is still the shipped mode.

No blockers, no majors. Production code untouched by this harness.

## Continuing after the fix / on another machine

```bash
git clone https://github.com/cheyang/rbg.git && cd rbg
git checkout verify/kep133-replaced-claude
bash docs/verification/kep133-replaced/scripts/re-verify.sh
```

Kickoff prompt for a fresh agent: "You are resuming the PR #577 verification on
branch verify/kep133-replaced-claude. Run
docs/verification/kep133-replaced/scripts/re-verify.sh (it fetches the current PR
head itself). Read the README table and polarity table first: T3 is a bug-canary —
it is FIXED when it flips to FAIL; then invert it. T1/T2/T4 are regression guards
and must stay PASS. Update results/, advance .last-reviewed to the new head sha,
commit and push to the fork remote."
