# Verification — KEP-133 refine mode redesign (PR #570) — Reviewer A (Claude)

- **PR:** https://github.com/sgl-project/rbg/pull/570 (docs-only: rewrites `keps/133-configmap-refine/README.md`)
- **Head:** `91cac0738171a561882406a46a72f992a81f2e33` · **Base:** `c2896cc0d9ccb8b72de0dda4838f52d8807c9ff1`
- **Harness branch:** `verify/kep133-refine-claude` (this branch) — production code untouched; only additive test files + this directory.

## Premise verdict (P0): partially refuted

| KEP claim about "today" | Verdict | Evidence |
| --- | --- | --- |
| "every role of an RBG gets its own ConfigMap … duplicated across all roles" | **Refuted** | `TestKEP133Claude_NewRBGDefaultsToRefineWithSingleEnumerationConfigMap`: a new RBG on the **base** branch defaults to `discovery-config-mode: refine` and gets **one** RBG-level ConfigMap with full enumeration. Shipped in PR #134 (in v0.7.0 / v0.8.0). |
| "New RBGs created after the controller upgrade get refine" | **Refuted** | Same test — refine is already the default for new RBGs today. |
| Content grows O(replicas) toward the 1 MiB limit | **Confirmed** | `TestKEP133Claude_EnumerationGrowsWithReplicas`: ~43–44 bytes/pod, linear (10 000 replicas → ~439 KB). |
| "older controllers ignore [the RBG-level ConfigMap] (they only manage per-role ConfigMaps)" | **Refuted** | `reconcileRefinedDiscoveryConfigMap` (base, internal/controller/workloads/rolebasedgroup_controller.go:453) actively rewrites the RBG-level ConfigMap for refine RBGs — shipped in v0.7.0/v0.8.0. |

The real problem (stored size grows with instance count) is confirmed; the duplication/rollout framing is stale.

## Observed vs expected

| Finding | Layer | Test | Result | Meaning |
| --- | --- | --- | --- | --- |
| F1 (blocker) rollout model contradicts shipped refine mode | unit | `TestKEP133Claude_NewRBGDefaultsToRefineWithSingleEnumerationConfigMap` | PASS (fact established) | KEP rollout/compat claims refuted |
| F2 (major) `base` templates carry FQDN suffix current builder never emits | unit | `TestKEP133Claude_AddressShapeIsShortForm` | PASS | KEP template examples refuted — renderer built from them fails the KEP's own equality gate |
| F3 (major) discovery follows committed child target, not the annotation | unit | `TestKEP133Claude_DiscoveryFollowsCommittedTargetNotAnnotation` | PASS | "projected from the annotation, semantics inherited verbatim" refuted |
| F4 (major) legacy CCP enumeration is spec-order, not name-sorted | unit | `TestKEP133Claude_CCPComponentOrderIsSpecOrder` | PASS | sorted-by-name rendering diverges from legacy ordering |
| F5 (major) snapshot ≠ "same effective contract as today" | code-path only | — | not run (live layer skipped) | whole-volume (non-subPath) ConfigMap mount ⇒ kubelet propagates updates today; init-container snapshot freezes them |
| F6 (minor) group.size example + O(roles×components) PRR claim inconsistent | unit | `TestKEP133Claude_GroupSizeEqualsRoleCount` | PASS | group.size = len(roles) = 3, KEP example says 4 |
| P0 (premise) enumeration size O(replicas) | unit | `TestKEP133Claude_EnumerationGrowsWithReplicas` | PASS | size problem confirmed |

Full raw output: `results/unit-l1.txt`.

## Polarity

All automated tests are **canaries of current behavior** (they assert what the code does today; each contradicts a corresponding KEP statement). This PR is documentation-only, so base and head code are identical — a green run re-establishes the same facts. When an implementation of KEP-133 lands:

- F2/F3/F4 tests **flip or get superseded** only if the address shape, exclusion source-of-truth, or component ordering deliberately changes; at that point invert/promote them per the new compat contract.
- F1's test keeps passing while the current default-mode behavior is unchanged; if a new mode value is introduced, add a test for the new selection logic.
- "All green" here means "codebase facts unchanged", **not** "the KEP is now correct".

## How to run

```bash
# from a checkout of this branch
GOFLAGS=-mod=vendor go test -count=1 -v -run 'TestKEP133Claude' \
  ./pkg/discovery/ ./internal/controller/workloads/

# or, grafting onto the current PR head anywhere:
bash docs/verification/kep133-refine-claude/scripts/re-verify.sh
```

## Harness-bites check

Because the PR is docs-only there is no production fix to apply/revert; the bite check is the exact-value assertions themselves: each test asserts exact addresses/ordering/counts, so it fails if the fact it pins were false. Verified both packages' full suites still pass with the harness added (`go test ./pkg/discovery/ ./internal/controller/workloads/` → ok).

## Continuing after the fix

When the KEP is revised or the implementation lands:

1. `git fetch` this branch, run `bash docs/verification/kep133-refine-claude/scripts/re-verify.sh <ref>`.
2. Re-read the polarity table above before interpreting results.
3. Update `verify-manifest.json` per finding (`fixed` / `still-broken` / `harness-update`) and advance `.last-reviewed`.

Kickoff prompt for a fresh agent: "You are resuming the KEP-133 PR #570 review harness. Read docs/verification/kep133-refine-claude/README.md and verify-manifest.json, run scripts/re-verify.sh against the current PR head, and report per-finding Fixed/Still-broken/Harness-update honoring the polarity table."
