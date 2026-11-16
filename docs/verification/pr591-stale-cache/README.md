# Verification — PR #591 "fix: avoid stale workload cache reads after apply"

Reviewer: Claude (Reviewer A, debate pipeline, round 1, mode `initial`).

- PR head: `d7fdde9edbb286da42b4187eeff24356d6fa8060`
- Merge-base: `1fc69f703695bc9355f5730ee3fb3f75cd0331b6`
- Fixes: none — the PR body carries a literal `fixes #XXXX` placeholder; the problem statement
  comes from the PR body itself (the linked #15 is a closed PR about an unrelated change).

## Premise (P0) — **Confirmed**

The claimed symptom was reproduced on the **merge-base** with the harness grafted onto it,
against a real envtest API server. `reconcileHeadlessService` on base does
`r.client.Get(workload)` through the informer-backed client right after the same reconcile
pass created the RoleInstanceSet; with the cache lagging the write, the Get returns
NotFound and the whole role reconcile errors:

```
roleinstancesets.workloads.x-k8s.io "group-worker" not found
```

Component match is exact: the PR touches precisely the call chain
(`reconcileRoleInstanceSet`/`reconcileStatefulSet` → `reconcileHeadlessService`) that
exhibits the symptom, and removes the cache read in favor of the API-server response.
Evidence: `results/base-premise.out` (red on base) vs `results/head-harness.out` (green on
head) — the identical contract test, `TestPR591LaggingCacheHeadlessService`.

## Claim ledger

| ID | Claim | Polarity | Layer | Base | Head | Verdict |
| --- | --- | --- | --- | --- | --- | --- |
| P0 / pr591-stale-cache-read | The public `Reconciler` must create and keep in sync the headless Service (with the live workload UID in its ownerRef) even while the informer cache lags the workload write performed by the same pass — create path, SSA apply path, and selective-membership path | contract | integration (envtest) | FAIL | PASS | **Fixed by PR** |
| pr591-apply-response-identity | `utils.ApplyObjectConfiguration` (ApplySpec) returns the server-committed object: `metadata.uid`, `metadata.resourceVersion`, defaulted spec and managedFields; the exact `FromUnstructured(applied.Object, …)` conversion used by the reconciler preserves that identity in the typed workload | contract | integration (envtest) | n/a (symbol absent on base) | PASS | **Confirmed mechanism** |
| pr591-ssa-wire-equivalence | The `client.Patch(client.Apply)` → `client.Apply` migration keeps SSA semantics: same field manager `rbg`, force ownership, `metadata.resourceVersion` precondition, single conditional writer for selective membership, ownership fold before ordinary SSA (covered by the PR's own envtest suite, re-run here) | contract | integration (envtest) | PASS | PASS | **No regression** |

## Observed vs expected

- **P0 (base)**: expected `NotFound`-driven reconcile error, observed exactly that
  (`base-premise.out`). The Service is not created during the first pass.
- **P0 (head)**: expected success — observed: reconcile succeeds, headless Service exists,
  `ownerReferences[0].uid` equals the live RoleInstanceSet UID, kind/APIVersion correct,
  across create/apply/selective paths (`head-harness.out`).
- **Mechanism (head)**: the returned unstructured carries `metadata.uid`/`resourceVersion`
  equal to the live object, `managedFields` contains the `rbg` field manager, and the typed
  round-trip preserves UID/resourceVersion/replicas (`head-harness.out`).
- **Full suite (head)**: all unit + envtest packages pass, including the PR's own
  `selective_commit` guardedClient.Apply assertions (`head-full-suite.out`).

### Harness-bites check

`TestPR591LaggingCacheHeadlessService` is red on the merge-base and green on the PR head —
the test genuinely discriminates buggy from fixed code, so its pass on head is meaningful
(the same property that makes it a valid regression test for future rounds).

## How to run

From a checkout of this branch (the harness is additive; production code is untouched):

```bash
KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:-$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64}" \
  go test ./pkg/reconciler/ -run 'TestPR591' -count=1 -v
```

For the next round, after the author pushes a fix:

```bash
bash docs/verification/pr591-stale-cache/scripts/re-verify.sh
```

(no ref — it resolves the current PR head from `manifest.pr`, grafts the harness, runs the
integration layer, and prints Fixed / Still-broken / Partial / Harness-update per finding).
Note the mechanism file (`pr591_mechanism_test.go`) references symbols introduced by the
PR, so it only compiles on the PR head and descendants; the premise file
(`pr591_premise_test.go`) compiles on both base and head (it was used for the base run).

## Files

- `pkg/reconciler/pr591_premise_test.go` — P0 contract test + `laggingCacheClient`
  (models the informer-backed client: writes mark the key unobserved, lagging Gets return
  NotFound, non-lagging Gets restore the GVK the way informer objects carry it).
- `pkg/reconciler/pr591_mechanism_test.go` — M1/M2 mechanism tests for
  `utils.ApplyObjectConfiguration` and the typed conversion.
- `scripts/re-verify.sh` — re-verification loop for later rounds.
- `results/` — captured raw output (`base-premise.out`, `head-harness.out`,
  `head-full-suite.out`).

## Findings summary (round 1)

No blocker or major findings. The PR's fix is correct and verified; the
`Patch → Apply` migration is request-for-request equivalent (same field manager, same
force flag, same body — confirmed against the vendored controller-runtime v0.22.1
`unstructuredClient.Apply` implementation and by the envtest wire assertions).

- `pr591-missing-premise-test` [minor, missingTests]: the PR itself ships no test for the
  stale-cache scenario it fixes (unit tests pass pre-populated workload objects; envtest
  runs on a non-lagging client). This harness adds one; consider folding
  `TestPR591LaggingCacheHeadlessService` (or a variant) into the PR.
- `pr591-dead-apply-all` [nit, pre-existing]: `ApplyAll` has no callers (as `PatchAll`
  before it) and its status leg would SSA the spec-apply response to the status
  subresource; consider deleting it in a follow-up.
- `pr591-issue-link-placeholder` [minor, question]: `fixes #XXXX` placeholder; the only
  linked item (#15) is a closed PR about an unrelated change. No issue auto-closes, but
  the linkage should be cleaned up or pointed at a real issue.

## Continuing after the fix (handoff)

On another machine: clone this repo, fetch this branch
(`verify/pr591-stale-cache-claude` from `https://github.com/cheyang/rbg.git`), check it
out, install envtest assets (`setup-envtest use 1.31.0`), then run `scripts/re-verify.sh`.
All durable state (harness, manifest, `.last-reviewed`, results) lives on this branch.
L1/L2 verdicts are deterministic across machines. The canary/contract polarity table: all
three findings are contract tests — they must PASS on fixed code; any FAIL means
still-broken. Kickoff prompt: "You are resuming PR #591 verification; checkout
verify/pr591-stale-cache-claude, run
docs/verification/pr591-stale-cache/scripts/re-verify.sh, and report per-finding verdicts."
