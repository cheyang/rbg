# Verification — PR 463: heal legacy update strategy types via a mutating webhook

PR: https://github.com/sgl-project/rbg/pull/463
Branch: `verify/pr463-legacy-strategy-heal` (based on PR head `1e4f70f7`)
Base: `main` (`07fc643`)

## Premise verdict (top of file, as required)

**P0 = Confirmed** (runsAgainst: base). Reproduced the claimed symptom on the live ACK
cluster (k8s v1.36.1). After #436's enum on `RoleInstanceSet.updateStrategy.type`, the live
API server rejects the legacy `Recreate` and `""` values with HTTP 422, and the v0.8.0
controller — which copies `Recreate` verbatim into the RoleInstanceSet — fails the SSA-apply
and never creates the RIS. This is exactly the break the PR describes.

**Premise refinement (not a refutation):** the PR body says #436 added the enum to *both*
the RBG `rolloutStrategy.rollingUpdate.type` and the RIS `updateStrategy.type`. The generated
CRDs (base == PR here) only carry the enum on the **RoleInstanceSet** field; the RBG/RBGS
field is still `type: string` (no enum). So the 422 surfaces on the controlled RoleInstanceSet
(reconciler SSA-apply), not on the RBG write itself. The fix is still correct: normalizing at
the RBG layer prevents the invalid value from reaching the RIS. Verified live — see the table.

## Observed vs. expected

| ID | Finding | Layer | Polarity | Expected (base) | Observed | Verdict |
| --- | --- | --- | --- | --- | --- | --- |
| P0 | enum rejects `Recreate`/`""`; base controller breaks RIS creation | live (ACK) | contract | 422 for Recreate/""; RIS absent | `results/enum-rejection-matrix.txt`: Recreate/""/Garbage -> 422 "Unsupported value"; RecreatePod/InPlaceIfPossible/InPlaceOnly -> accepted. `results/base-evidence.txt`: v0.8.0 reconcile log `RoleInstanceSet ... is invalid: spec.updateStrategy.type: Unsupported value: "Recreate"`; RIS never created. | **Confirmed** |
| F1 | PR self-heals on write (reconciler path) | live (ACK) | contract | RIS created with healed type | `results/fix-evidence.txt`: PR binary (webhooks=none) reconciled `pr463-rbg-recreate` (RBG type=Recreate) -> RIS `pr463-rbg-recreate-worker` created, `updateStrategy.type=RecreatePod`, READY 1/1. | **Confirmed** |
| F1 | defaulter/conversion/reconciler logic | unit | contract | normalize Recreate->RecreatePod, ""->InPlaceIfPossible, valid passthrough; Recreate<->RecreatePod round-trip; empty preserved | `go test ./api/workloads/... ./pkg/reconciler/... ./pkg/webhook/...` all GREEN. | **Confirmed** |
| F1 | CRD enum accepts valid types + update path | envtest (isolated real apiserver) | contract | valid types accepted; update of valid object succeeds | `test/envtest/testcase/rbg` 13/13 GREEN (incl. PR's "Should accept all valid v1alpha2 update strategy types", "Should allow updating a valid v1alpha2 rolloutStrategy"). | **Confirmed** |
| F1 | webhook heal at admission chain | — | contract | RBG stored Recreate -> normalized RecreatePod before persist | Not exercised live (out-of-cluster webhook serving not reachable by ACK apiserver; envtest harness does not wire admission webhooks). Proven at unit level (`*Defaulter.Default`) and transitively by the live reconciler proof — same `NormalizeUpdateStrategyType`. PR's own e2e upgrade suite phase 4 is the intended admission-chain proof. | **Confirmed (unit) / Not-reproduced (live admission)** |
| F2 | failurePolicy=Fail on new RIS mutating webhook | live (ACK) | observation | — | Cluster already runs `vrolebasedgroup.kb.io` with failurePolicy=Fail: scaling the controller to 0 blocked all RBG creates ("no endpoints"). RIS extends the established Fail-webhook pattern; not a new risk class. | **Not-a-new-risk-class** |
| F3 | RIS defaulter vs reconciler redundancy | review | — | — | Acknowledged; harmless (reconciler normalizes before SSA; defaulter covers direct user writes). | **nit** |
| F4 | snapshot objectBumps accounts only for Recreate fixture's RIS | — | — | — | Not-reproduced (needs v0.7.0->current upgrade e2e). Internally consistent iff v0.8.0 defaulted ""->InPlaceIfPossible in the RIS. | **Not-reproduced** |
| B1 | RBGS parent/child strategy non-convergence on the legacy-upgrade path (`rolesEqual` raw DeepEqual; `updateExistingRBGs`/`newRBGForSet` copy parent verbatim) | unit | contract (repro) + canary | needsUpdate stays true across reconciles (parent Recreate ≠ healed child RecreatePod); controller writes `Recreate` back | `results/b1-unit.txt`: `TestB1_NeedsUpdate_*=RED` (reproduction), `TestB1_NonConvergence_*` RED across 5 simulated reconciles, `TestB1_UpdateExistingRBGs_*`/`TestB1_newRBGForSet_*` canaries GREEN (write/copy `Recreate`). Harness-bites: applied `normalizedRolesCopy` + semantic compare → contracts GREEN, canaries FLIP; reverted. | **Confirmed (unit)** |

## Round 2 (head `b8e770eb`) — RBGS-layer gap

Round 1 verified the RBG→RIS link (P0/F1) and the webhook heal; it did **not** audit the RBGS
controller's compare/copy path. Round 2 (after commit `b8e770e "fix"`, a certmanager dedup +
e2e slice-helper refactor that does **not** touch the RBGS controller) audited that path and
found B1: the PR heals at admission and at the RBG→RIS reconciler boundary, but **not** at the
RBGS→RBG boundary. A legacy pre-webhook RBGS whose stored `GroupTemplate` still carries
`Recreate` is never healed (the controller only does `Status().Update`, which doesn't trigger
the spec defaulter), and every reconcile re-issues a child `Update` that the webhook heals back
to `RecreatePod` → `needsUpdate` stays true → RBGS-layer non-convergence on the very legacy-
upgrade path the PR targets. The RIS stays valid (reconciler normalizes before SSA), so the
PR's stated break IS fixed and there's no data corruption — this is a control-loop-churn /
non-convergence defect, not a re-introduction of the RIS 422.

**Fix refinement proven by the harness:** normalizing only the write path (`updateExistingRBGs`/
`newRBGForSet`) is **insufficient** for convergence — the parent stays `Recreate`, so `rolesEqual`
still sees a divergence. The fix **must** also normalize in the compare (`rolesEqual`), or the
controller keeps re-issuing Updates.

**Limitation:** the hot-loop *rate* (does a no-op webhook-healed `Update` re-queue via `Owns`?)
is not proven this round — `test/envtest/testutil/setup.go` wires no admission webhooks, so the
existing envtest cannot observe it; needs a webhook-wired envtest or a live run (ACK). The
non-convergence itself is unit-proven and holds regardless of the loop rate.

## What was actually run (live, on ACK cn-hongkong)

1. **Premise — enum rejection (real API server, dry-run server-side validation).**
   Temporarily patched the enum `[RecreatePod, InPlaceIfPossible, InPlaceOnly]` onto the live
   `roleinstancesets` CRD v1alpha2 `updateStrategy.type` (snapshot saved at
   `results/ris-crd-live-snapshot.yaml`; restored after). `kubectl patch ... --dry-run=server`
   on an existing valid RIS: `Recreate`/`""`/`Garbage` -> 422; the three enum values -> accepted.
   See `results/enum-rejection-matrix.txt`.
2. **Premise flow — base controller (v0.8.0).** Created `pr463-rbg-recreate` (RBG
   `rolloutStrategy.rollingUpdate.type: Recreate` — accepted, RBG has no enum). The v0.8.0
   controller reconciled and SSA-applied the RIS with `Recreate` -> 422; RIS never created.
   See `results/base-evidence.txt`.
3. **Fix — PR reconciler (webhooks disabled).** Built `./cmd/rbgs` from the PR branch; scaled
   the v0.8.0 deployment to 0; ran the PR binary with `-enable-webhooks=none` against
   `KUBECONFIG`. Reconciled the same `Recreate` RBG -> RIS `pr463-rbg-recreate-worker` created
   with `updateStrategy.type=RecreatePod`, READY 1/1. The RBG itself stayed `Recreate`
   (webhooks off -> the heal happened at the reconciler/RIS layer, which is the part the
   live run can reach). See `results/fix-evidence.txt`.

> Pre-existing cluster note: stored RISes carried `roleInstanceTemplate.restartPolicy: {type:
> None}` (object form), which the newer v1alpha2 Go struct (string enum) cannot unmarshal,
> blocking the PR binary's RIS list. This is the *same class* of legacy-value problem the PR
> addresses, but for `restartPolicy` (out of scope). Those 3 RISes were temporarily patched to
> the string form to unblock the run and restored after. The unrelated `restartPolicy` errors
> in v0.8.0 logs are pre-existing and **not** caused by this PR.

## How to re-run

### Unit + envtest (any machine)
```bash
go test ./api/workloads/v1alpha2/... ./api/workloads/v1alpha1/... ./pkg/reconciler/... ./pkg/webhook/...
export KUBEBUILDER_ASSETS="$(setup-envtest use 1.36.2 -p path)"
go test ./test/envtest/testcase/rbg/
```

### Live A/B (needs a cluster running a pre-enum release, e.g. v0.8.0)
See `scripts/re-verify.sh`. Summary: patch the enum onto the RIS CRD, create a `Recreate` RBG,
show the base controller leaves the RIS absent (422), then run the PR binary (`-enable-webhooks=none`)
and show the RIS is created with `RecreatePod`. Restore the CRD + controller after.

## Continuing after the fix / next round

- The premise (P0) is **Confirmed**: a fix is warranted, and the PR's mechanism is sound.
- The reconciler-normalize path is **proven live**; the webhook-heal path is proven at unit
  level and is the PR's e2e-upgrade-phase-4 responsibility.
- Re-verify on a new commit: `bash scripts/re-verify.sh` (fetches the current PR head from the
  manifest `pr` URL; resolves the delta from `.last-reviewed`). Polarity: the live contract
  tests go GREEN on the fix; if the author changes the normalize mapping, the unit table tests
  flip and must be updated.
- Open question for the author (F4): confirm in the upgrade e2e that the empty-strategy
  fixture's RIS does not get a one-off repair bump (i.e. that v0.8.0 already stored
  `InPlaceIfPossible` for the empty case), since `objectBumps` records only the `Recreate` fixture.
