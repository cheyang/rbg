# PR #479 — isolate unhealthy timers by RoleInstanceSet

**PR:** https://github.com/sgl-project/rbg/pull/479
**Base:** `1a9653ad` (#470, the prerequisite retry logic)
**Head:** `5a322f23` (the #479 fix, single commit on top of #470)
**Verdict:** premise **CONFIRMED** (base) · fix **CONFIRMED** (head) · harness-bites **CONFIRMED** · live A/B **CONFIRMED**

## Premise (P0)

`instanceUnhealthySince` is a global `sync.Map` keyed by `types.UID` (RoleInstance UID).
`observeInstanceHealth(instances)` is called at the top of every reconcile with only the
*current* set's instances. Its cleanup pass deletes any UID absent from that slice, so
reconciling `decode` deletes `prefill`'s unhealthy start time (and vice versa). Under
interleaving, neither set's 10s unhealthy window (`stableUnhealthyDuration`) ever matures,
`isStablyUnhealthy` never becomes true, the unavailable budget stays exhausted, and both
rollouts remain stuck on the old template. #470's timed retries re-enqueue the controller
but cannot help while the start time keeps being deleted by the other set.

## Layer 1 — unit (deterministic)

| Claim | Branch | Polarity | Result |
|---|---|---|---|
| Cross-set observe wipes the other set's unhealthy timer; window never matures under interleaving | base `1a9653ad` | must reproduce | **CONFIRMED** — `TestPremiseCrossSetTimerWipe` |
| Cross-set observe preserves the other set's timer; window matures under interleaving | head `5a322f23` | must pass | **CONFIRMED** — `TestPremiseCrossSetTimerIsolated_HEAD` |
| Removing the set-scope guard makes the cross-set tests fail (tests actually bite) | head, guard removed | must fail | **CONFIRMED** — `TestUpdateStatefulInstanceSetRetriesUnhealthyRollout`, `TestObserveInstanceHealthAndIsStablyUnhealthy`, and the head premise test all FAIL; recreation/prune tests (same-set) still pass |

```
go test -mod=vendor -race ./pkg/reconciler/roleinstanceset/statefulmode -count=1   # PASS (head)
go vet  -mod=vendor ./pkg/reconciler/roleinstanceset/statefulmode                 # clean
```

The base harness (`premise_crossset_base_test.go`) does not compile on head because the
function signatures changed (`observeInstanceHealth(instances)` → `observeInstanceHealth(set, instances)`).
Run it from a base `1a9653ad` checkout.

## Layer 2 — live A/B on the provided ACK cluster (real environment)

Scenario (`live-rbg-dual.yaml`): a `RoleBasedGroup` with two standalone
`RoleInstanceSet` roles (`prefill`, `decode`), each 1 replica,
`podManagementPolicy: Parallel`, `rolloutStrategy: RollingUpdate { type: RecreatePod,
maxUnavailable: 1, maxSurge: 0 }`, and a pod template with a **failing readiness probe**
(httpGet `/healthz-nonexistent`) so each RoleInstance is unhealthy and consumes the
unavailable budget, gating progress on `isStablyUnhealthy`.

Method: build the controller binary from `1a9653ad` (base) and from `5a322f23` (head),
run it locally against the cluster (`--enable-webhooks none`, KUBECONFIG). Trigger a
rollout (template label `rollout-trigger` v2→v3), restart the controller (fresh in-process
timers), then **poke both RoleInstanceSets' annotations every 3 s** to simulate the
"repeatedly reconcile in turn" from the premise — this is the exact mechanism (cross-set
`observeInstanceHealth` cleanup), amplified to be deterministic.

| Phase | Controller | "Rolling update budget exhausted" events | Pod template label after ~33 s of interleaved pokes | Verdict |
|---|---|---|---|---|
| A | base (#470) | **41** | stuck at **v2** (old) | premise **reproduced live** |
| B | head (#479) | **2** (then `targets=[]`, rollout complete) | progressed to **v3** | fix **confirmed live** |

On base, the cross-set wipe resets each set's unhealthy start time on every interleaved
reconcile, so the 10 s window never matures and the old instance is never "free" to update.
On head, the set-scoped key + cleanup isolation means each set's timer is untouched by the
other's reconcile, matures after ~10 s, and the rollout proceeds.

> **Live infra note (not a PR defect):** the cluster's installed RoleInstance CRD has
> `spec.restartPolicy` as an **object** (pre-#424 form), while the #470/#479 controller API
> writes the deprecated **string** `restartPolicy`. Standalone roles always emit the string
> path (`GetRawRestartBackoff` returns nil for `StandalonePattern`), so RoleInstance
> creation was rejected. A **test-only bridge patch** made the reconciler always emit the
> object `restartPolicyConfig` (pruned by the CRD, `restartPolicy` defaulted) — applied
> **identically to base and head builds**, never touching `statefulmode` (the PR's subject),
> so the A/B stays a pure comparison of #479's effect. The patch is reverted on this branch;
> the shipped binaries were the only things that carried it.

## Findings (non-blocking)

- **F1 (minor, efficiency):** `pruneInstanceHealth()` is called at the top of every
  `Reconcile` and `Range`s the entire global `instanceUnhealthySince` map doing a lister
  `Get` per entry — O(N) per reconcile. The lister `Get` is an O(1) cache lookup so this is
  cheap in practice, but it is full-scan overhead on every reconcile, including when the
  map holds only the current set's entries. A lazier cadence (e.g. prune periodically or
  when the map grows) would avoid it.
- **F2 (minor, correctness/edge):** `pruneInstanceHealth` treats informer-cache
  `IsNotFound` as definitive and deletes *other sets'* timers. During a brief cache desync
  (controller restart before sync, watch reconnect) an existing set can be momentarily
  absent from the cache and read as `IsNotFound`; a reconcile of set B would then wipe set
  A's timers — reintroducing the cross-set reset the PR fixes, via cache inconsistency
  rather than the cleanup loop. The PR's comment says "retain records if the cache lookup
  fails for a reason other than NotFound"; it does not cover transient cache *inconsistency*
  that looks like NotFound. Low probability in steady state, but it is the same failure
  class.

No blockers / majors. Verdict for publishing: **COMMENT**.

## Re-verify

```
bash scripts/re-verify.sh
```
