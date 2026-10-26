# Verification — PR #526 "name the policies applied from the v1alpha1 coordination annotation"

Reviewer: Claude (Reviewer A, debate pipeline, round 1, mode `initial`).

- PR head: `a9f998878b0767746d2ffff2b3f316723ef34d79`
- Merge-base: `8a7bcfcb5dbf104788b35e8cb143d6090a3f6c37`
- Fixes: issue #517 (open, `bug`)

## Premise (P0) — **Confirmed**

The claimed symptom was reproduced on the **base** branch (merge-base `8a7bcfcb`), with the
harness grafted onto it, against a real envtest apiserver:

```
apply CoordinatedPolicy <ns>/verify-coord-two: failed to create typed patch object
(...; workloads.x-k8s.io/v1alpha2, Kind=CoordinatedPolicy): .spec.policies: element 0:
associative list with keys has an element that omits key field "name" (and doesn't have default value)
```

The reconciler repeats this as `Reconciler error` and never gets past step 5, so no
CoordinatedPolicy, no RoleInstanceSet and no Pod appear — exactly what #517 reports.
Component match is exact: the issue names `buildCoordinatedPolicyApplyConfiguration`, which
is the function the PR touches.

Evidence: `results/base-verify-specs.out`

## Claim ledger

| ID | Claim | Polarity | Layer | Base | Head | Verdict |
| --- | --- | --- | --- | --- | --- | --- |
| P0 | #517 symptom exists on base (no policy, no workloads) | contract (premise) | integration | FAIL (reproduces) | n/a | **Confirmed** |
| coordname-policy-name-missing | With `.WithName(c.Name)`, an RBG carrying the coordination annotation gets a named CoordinatedPolicy and role workloads (incl. >1 policy: associative merge) | contract | integration | FAIL | **PASS** | **Fixed by PR** |
| coordname-strategy-less-gap | A v1alpha1 `coordination` entry with no `strategy` (legal: optional in v1alpha1) still fails the apply (`spec.policies[0].strategy: Required value`) and still blocks all workloads | canary | integration | FAIL (name error instead) | **PASS** (gap present) | Residual, **pre-existing**, non-blocking |

All runs used envtest 1.31.0 assets (`KUBEBUILDER_ASSETS=~/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64`).

## Harness

One additive file, no production code touched:

- `test/envtest/testcase/rbg/coord_migration_verify_test.go` — ginkgo specs in the existing
  envtest suite, label `verify:coordname`:
  - **contract** "names every applied policy so the apply is accepted and workloads appear"
    — 3 roles, **two** coordination entries (disjoint roles), asserts both named policies
    (incl. per-name maxSkew) and a RoleInstanceSet per role. The PR's own test covers a
    single policy; this adds the multi-element associative-list merge.
  - **canary** "a strategy-less v1alpha1 coordination is still rejected by the apiserver" —
    calls `EnsureV1alpha1CoordinatedPolicy` directly against the real apiserver and asserts
    the rejection (`spec.policies[0].strategy: Required value`).

Run it:

```bash
export KUBEBUILDER_ASSETS="$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64"
go test ./test/envtest/testcase/rbg/ -count=1 -ginkgo.focus "v1alpha1 coordination migration|converted from v1alpha1" -ginkgo.v -v -timeout 20m
```

## Harness bites (step 4)

- **Base graft** (`8a7bcfcb` + harness): contract spec FAILS with the #517 error. → `results/base-verify-specs.out`
- **Head with the one-line fix reverted**: contract spec FAILS with the same #517 error. → `results/head-nofix-contract.out`
- **Head** (`a9f9988`): contract spec PASSES. → `results/head-verify-specs.out`
- Full suites on head: `./test/envtest/testcase/rbg/` and `./internal/controller/workloads/` PASS. → `results/head-envtest-full.out`, `results/head-unit-full.out`
- Canary on head: PASSES (documents the residual gap). → `results/head-canary.out`

The same error appears with and without the fix line and disappears with it, so the test
exercises the real path, not an accident.

## Re-verify after the author pushes a fix

```bash
bash docs/verification/coord-policy-name/scripts/re-verify.sh            # auto-fetches PR 526 head
bash docs/verification/coord-policy-name/scripts/re-verify.sh <fixed-ref>
```

Per-finding verdicts: `Fixed / Still-broken / Partial / Harness-update`, canary polarity
applied (the strategy-less canary is fixed only when it FLIPS to fail — then invert its
assertion or promote the intended behavior to a contract test).

## Continuing after the fix (handoff)

- Graft this whole directory plus `test/envtest/testcase/rbg/coord_migration_verify_test.go`
  onto the fixed code (what `scripts/re-verify.sh` does automatically).
- Prereqs: Go ≥ 1.25, envtest 1.31.0 assets via
  `go run sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.25.2 use 1.31.0 -p path`.
- Expected: contract spec green; canary red only once the strategy-less gap is closed
  (invert it then).
- `.last-reviewed` holds `a9f9988…`; the review delta next round is
  `a9f9988…..<new head>`.

Kickoff prompt for a fresh agent: "You are resuming the review of
https://github.com/sgl-project/rbg/pull/526. Check out the branch
`verify/coord-policy-name-claude` (fork cheyang/rbg), read
`docs/verification/coord-policy-name/README.md`, run its `scripts/re-verify.sh`, report
per-finding verdicts, then review the `last-reviewed..head` delta."
