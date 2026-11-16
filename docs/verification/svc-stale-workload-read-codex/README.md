# Verification — PR #591 "fix: avoid stale workload cache reads after apply"

Reviewer: codex. Reviewed head: d7fdde9edbb286da42b4187eeff24356d6fa8060 (base 1fc69f703695bc9355f5730ee3fb3f75cd0331b6).
Production code untouched by this harness (additive test files only).

## Premise (P0) — CONFIRMED

Claim (PR body): the headless Service reconcile reads the workload from the
informer cache immediately after creating/applying it, so a lagging cache
breaks or delays the reconcile.

Reproduced on the **base** branch: with a client wrapper whose workload Gets
always miss (simulated informer lag) over a backing store that has the object,
`RoleInstanceSetReconciler.Reconciler` fails with

    roleinstancesets.workloads.x-k8s.io "group-worker" not found

and no Service is written, while the RoleInstanceSet itself exists in the
store. Evidence: `results/unit-premise-base.txt`
(test source kept in `base/verify_premise_base_test.go`; it compiles only
against the base because the PR changes the function signature).

The identical stale-cache scenario passes on the PR head and the Service
carries the fresh workload identity: `results/unit-stale-cache-head.txt`.

## Claims and polarity

| id | claim | layer | polarity | head result |
|----|-------|-------|----------|-------------|
| C1 | Role reconcile tolerates a stale workload cache; svc ownerRef identity comes from the fresh workload (RIS + STS) | unit | contract | PASS |
| C2 | `utils.ApplyObjectConfiguration` returns the API-server-populated object (UID, resourceVersion, server defaults visible) | integration (envtest) | contract | PASS |
| C3 | `metadata.resourceVersion` in the apply config remains an optimistic-concurrency precondition through the new `client.Apply()` path (stale RV => Conflict) | integration (envtest) | contract | PASS |
| C4 | Headless Service ownerRef UID equals the server-assigned RoleInstanceSet UID on both the create and re-apply paths | integration (envtest) | contract | PASS |
| C5 | A successful workload reconcile never returns a nil workload (the svc builder dereferences it) | unit | contract | PASS |

## How to run

```bash
# unit layer (no cluster needed)
go test ./pkg/reconciler/ -run 'TestVerify' -count=1 -v

# integration layer (envtest; needs kubebuilder assets, K8s 1.31 as in CI)
export KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64
go test ./pkg/utils/ -run 'TestVerifyApply' -count=1 -v
go test ./pkg/reconciler/ -run 'TestVerifyReconcileUsesServerIdentity' -count=1 -v

# whole harness against a new PR head (grafts harness, runs unit+integration)
bash docs/verification/svc-stale-workload-read-codex/scripts/re-verify.sh
```

`re-verify.sh` resolves the current PR head from `manifest.pr`; the reviewed
point is pinned in `.last-reviewed`.

## Observed vs expected

- P0 premise: expected symptom reproduced on base (NotFound from the stale
  workload read; no Service written). On head the same scenario succeeds.
- C1: expected PASS on head — observed PASS; svc ownerReference name/UID match
  the stored workload. (The fake backend does not allocate UIDs; server-UID
  fidelity is covered by F4 against a real API server.)
- C2: expected non-empty UID/RV and visible server defaulting — observed
  (`results/envtest-apply-writeback-head.txt`); UID stable across applies, RV
  advances on change.
- C3: expected Conflict for a stale resourceVersion — observed Conflict; the
  stale write does not land. This guards the selective-membership race
  property (`WithResourceVersion` precondition) after the migration from
  `client.Patch(..., client.Apply, ...)` to `client.Apply(...)`.
- C4: expected svc ownerRef UID == RIS UID on create and re-apply — observed.
- C5: expected non-nil workload with identity on every success path — observed.

Harness-bites check: C1's stale-cache wrapper would make the reconcile fail
exactly like the base if the code ever re-read the workload from the client
again; the base counterpart test demonstrates that failure mode.

## Repo test suites on the reviewed head (for context)

- `go test ./pkg/reconciler/ ./pkg/utils/ ./internal/controller/workloads/` — PASS
- `KUBEBUILDER_ASSETS=... go test ./test/envtest/testcase/...` — PASS
  (all 7 suites, incl. `selective_commit`, which exercises the new
  `client.Apply()` path and its resourceVersion precondition against a real
  API server via the PR's updated `guardedClient.Apply` interceptor)
- Results: `results/unit-full-head.txt`

## Continuing after changes

The harness is additive; `git checkout <new-head>` then re-apply the
`harnessPaths` (or use `scripts/re-verify.sh` which does it). All findings are
contract polarity: they must PASS on a correct revision. If the PR changes the
reconciler signatures again, expect `Harness-update`, not a fix signal.
