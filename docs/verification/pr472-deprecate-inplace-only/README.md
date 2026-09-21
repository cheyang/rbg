# Verification — PR #472: deprecate InPlaceOnly update strategy

PR: https://github.com/sgl-project/rbg/pull/472
Branch reviewed: `pr-472` (head `29185a038d49f0891cd3aeef893082972a91e6d3`), mergeBase `0aff5f65a7b94a54aff4f83fb978e3a264e7bc9e`.

## Premise (P0) — CONFIRMED

> "InPlaceOnly is not separately implemented and behaves identically to InPlaceIfPossible."
> — PR #472 motivation + the `Deprecated:` note added to `InPlaceOnlyUpdateStrategyType`.

**Verdict: Confirmed** (code mechanism + live A/B).

### Code mechanism (base branch)
- There is **no** `case InPlaceOnlyUpdateStrategyType` / strategy-specific branch anywhere in the
  reconciler. `git grep InPlaceOnlyUpdateStrategyType` over non-test, non-constant code returns nothing.
- The two real runtime branch points both key off `Type != RecreatePodUpdateStrategyType`:
  - `pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_control.go:788`
  - `pkg/reconciler/roleinstanceset/statelessmode/sync/update.go:182`
  → InPlaceOnly and InPlaceIfPossible take the **same** in-place path.
- On an in-place-incompatible change, `inPlaceUpdateInstance` (stateful, line 1024) returns
  `(false, nil)` — **not an error** — when `CanUpdateInPlace` is false; the caller then runs
  `deleteInstance` (Pod recreation). So InPlaceOnly **falls back to recreation, it does not fail**,
  exactly as the deprecation note claims (and contradicting the old `doc/features/update-strategy.md`
  wording "fail if not possible", which this PR corrects).
- `CanUpdateInPlace` is true iff the old→new revision diff is *only* `replace` ops on
  `spec.containers[x].image` (+ metadata) — `pkg/inplace/pod/inplaceupdate/inplace_update_defaults.go:79`.

### One divergence — but dead code
`ValidateInstanceSetUpdate` (`pkg/reconciler/roleinstanceset/statelessmode/core/implement.go:162`)
guards `if Type != InPlaceIfPossible { return nil }`, so an InPlaceOnly set would skip the
component-size-replace-patch validation. **This method has no production caller** (declared in
`core/api.go:49`, implemented `implement.go:162`, stubbed only in `update_test.go:154`) — so it has
**no runtime effect** and the deprecation note's "behaves identically" holds at runtime. See F1.

### Live A/B (ACK cluster, installed v0.8.0-0c00546d, pre-enum v1alpha2 CRDs)
The PR is comment/CRD-description-only (zero behavior change), so the installed controller is a
valid proxy for the PR-branch InPlaceOnly behavior. Two RBGs differing only in `rollingUpdate.type`
(`iso-ipo`=InPlaceOnly vs `iso-ipc`=InPlaceIfPossible):

| phase | change | InPlaceOnly (`iso-ipo`) | InPlaceIfPossible (`iso-ipc`) | identical? |
| --- | --- | --- | --- | --- |
| A | image-only (anolis nginx → nginx:stable-alpine) | in-place: pod names unchanged, imageID changed | in-place: pod names unchanged, imageID changed | ✅ |
| B | containerPort 80→8080 (non-image) | fell back to recreation; r-1 then r-0 recreated; converged Ready | fell back to recreation; r-1 then r-0 recreated; converged Ready | ✅ |

Neither strategy failed or stalled differently. Captured in `results/`.

> Note on a confound ruled out: an earlier run with both roles in **one** RBG and a Phase-A image
> change **before** Phase B showed the InPlaceIfPossible role churning revisions in a recreate loop
> while InPlaceOnly did not. That was a shared-RBG + recreate-after-in-place cascade in v0.8.0
> (the code comments at `shouldAdvanceCurrentRevision` reference exactly this class of bug), **not**
> a strategy-type divergence — the isolated one-shot test above reproduces identical behavior for
> both. Recorded for honesty; it does not affect the premise verdict.

## Backwards compatibility — CONFIRMED
- Live: `InPlaceOnly` is accepted by the pre-enum v1alpha2 CRD and reconciles pods to Ready.
- Diff: the enum value `InPlaceOnly` is **retained** in all regenerated CRDs
  (`rolebasedgroups`, `rolebasedgroupsets`, `roleinstancesets`) and in `deploy/kubectl/manifests.yaml`.
  Only the field description gains a "Note: InPlaceOnly is deprecated" line. Existing CRs using
  `InPlaceOnly` are not rejected. The PR's own test edits (constant → `UpdateStrategyType("InPlaceOnly")`
  literal) explicitly assert the deprecated value still passes through unchanged.

## CRD regeneration consistency — CONFIRMED
The deprecation note line was added to all 3 CRD bases and all 3 matching blocks in
`deploy/kubectl/manifests.yaml`; enum lists unchanged.

## Findings

| id | finding | severity | verdict |
| --- | --- | --- | --- |
| P0 | premise: InPlaceOnly behaves identically to InPlaceIfPossible | — | Confirmed (code + live) |
| F1 | `ValidateInstanceSetUpdate` diverges for InPlaceOnly but is dead code (no runtime effect) | minor | Confirmed-as-dead-code |
| F2 | rolling-update example deletes the `router` role (scope beyond deprecation) | nit | Confirmed |

## Verdict
**COMMENT.** No blocker, no major. The deprecation is accurate (premise Confirmed live), backwards
compatibility is preserved, and CRD regeneration is consistent. F1 is a pre-existing dead-code
stale guard (not touched by this PR, no runtime effect). F2 is a nit on example scope.

## How to run (live layer)
```bash
KUBECONFIG=<your kubeconfig> bash scripts/live_abtest.sh
```
Idempotent: creates namespace `pr472-verify`, applies `scripts/abtest-rbg.yaml` (two roles:
`ipo`=InPlaceOnly, `ipc`=InPlaceIfPossible), runs Phase A (image change) and Phase B (containerPort
change), captures to `results/`, then deletes the namespace. For the clean isolated one-shot variant
used for the decisive verdict, see `scripts/iso-ipo.yaml` + `scripts/iso-ipc.yaml`.

## Continuing after the fix
This is a doc-only PR; there is no production-code fix to graft. If the author addresses F1 (wires
in or deletes `ValidateInstanceSetUpdate`) or F2 (example scope), re-run the live A/B to confirm
InPlaceOnly and InPlaceIfPossible still behave identically. The manifest `pr` URL lets
`scripts/re-verify.sh` fetch the current PR head without a sha.
