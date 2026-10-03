# pr488-customized-action-reporting — review verification

Reproducible evidence for the findings raised while reviewing
[PR #488](https://github.com/sgl-project/rbg/pull/488) (`feat: enhance CustomizedAction execution reporting`,
closes [issue #486](https://github.com/sgl-project/rbg/issues/486)).

Harness runs against the **PR head** (`b5eadcb770e134d90aa1311f5f630702d6715c1b`, branch `feature/486-customized-action`).
Production code is untouched — the diff is purely additive (this dir + one `_test.go`).

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `buildWarmupPod` deadline semantics, `evaluateCustomizedActionPod` timeout mapping, `evaluateCustomizedActionResults` policy-insensitivity | `go test ./internal/controller/workloads/ -run 'TestVerifyPR488' -count=1 -v` |
| 2. Integration (fake client) | full `Reconcile` on the `InvalidWarmupSpec` failure path → result-wipe behavior | same command (same package) |
| 3. Live (real cluster) | PR-head manager on the dedicated Aliyun ACK test cluster: timeout kill, merged-pod deadline (R1), ImagePullFailed→Timeout sequence | see `results/live-layer.txt` (round 1, cluster state fully restored after) |

> Test polarity: contract tests assert intended behavior (PASS on correct code).
> `TestVerifyPR488_MergedPodDeadlineGovernsPreloadContainers`, `TestVerifyPR488_SmallestTimeoutWinsAcrossActions`,
> `TestVerifyPR488_CompletionPolicyDoesNotAffectEvaluation` and `TestVerifyPR488_InvalidSpecWipesExistingCustomizedActionResults`
> are **canaries** documenting current behavior — they pass today and must **flip to FAIL** if the
> semantics change (e.g. deadline scoped away from preload containers, `completionPolicy` starts
> being read, or invalid-spec failures start preserving results).

## Problem premise (P0)

Run against the **base** branch (`7ed1860c`), because the question is whether the capability exists today.

| | |
|---|---|
| Claimed symptom | issue #486: "There is no per-`CustomizedAction` timeout. The completion semantics for multiple containers are implicit. Container exit codes and termination messages are not clearly exposed in the `RoleBasedGroupWarmup` status. Users must inspect the generated Pod manually to understand why a customized action failed." |
| Linked issue | #486, OPEN, created 2026-09-25, feature request |
| Reported component | `RoleBasedGroupWarmup` CRD + controller (`api/workloads/v1alpha2/rolebasedgroupwarmup_types.go`, `internal/controller/workloads/rolebasedgroupwarmup_controller.go`) |
| Patched component | exactly those files + new `rolebasedgroupwarmup_customized_action.go` — matches the issue's "Related resources" list |
| Component match | **Yes** |
| **Verdict** | **Confirmed** |
| Evidence | `results/premise-p0-base-vs-head.txt` — base has 0 occurrences of `CompletionPolicy`/`CustomizedActionResult`/timeout validation in `api/`+`internal/`; head defines and validates them. For a feature request, absence of the capability on base is the symptom. |

## Summary of results

| ID | Claim | Layer | Polarity | Verdict | Evidence |
|----|-------|-------|----------|---------|----------|
| P0 | Base lacks timeout / completion policy / execution-result reporting | static | — | **Confirmed** | `results/premise-p0-base-vs-head.txt` |
| R1 | `timeoutSeconds` becomes a Pod-level `ActiveDeadlineSeconds` on the **merged** warmup Pod — it bounds image-preload containers in the same Pod, the smallest timeout across actions truncates the rest, and (per k8s semantics) the clock includes scheduling + image-pull time | 1+3 | canary | **Confirmed (mechanism, as designed & documented)** | `results/unit-layer.txt`; **live**: `results/live-layer.txt` scenario B — pod `verify-b-merged-wfr8c` carried `image-preload-0` + `custom-1` with `ADS=3`, failed `DeadlineExceeded`, warmup `reason=Timeout`. Field comment in `rolebasedgroupwarmup_types.go` says "limits execution of the merged warmup Pod… smallest configured timeout is used". The PR's own e2e (`reports image pull failure before customized action timeout`) demonstrates the deadline firing during an image pull and being reported as `Timeout`. |
| R2 | `completionPolicy` is accepted, validated and defaulted but never read by any evaluation code (AllSucceeded semantics hard-coded) | 1 + static | canary | **Confirmed** | `results/static-r2-r3.txt` (only API def + validation sites); `TestVerifyPR488_CompletionPolicyDoesNotAffectEvaluation` proves results are identical with/without the field |
| R3 | Issue #486 acceptance criterion says condition `type: CustomizedAction`; PR implements two conditions `CustomizedActionComplete` / `CustomizedActionFailed` | static | — | **Confirmed (deviation, deliberate per PR summary "mutually exclusive terminal conditions")** | `results/static-r2-r3.txt`; needs maintainer sign-off since merging auto-closes #486 |
| R4 | `InvalidWarmupSpec` / `InvalidTarget` failure paths pass `desiredNodes=nil` → previously reported `CustomizedActionResults` and the `CustomizedActionComplete` condition are wiped from status | 2 | canary | **Confirmed** | `TestVerifyPR488_InvalidSpecWipesExistingCustomizedActionResults`; `results/unit-layer.txt` |
| T1 | PR's own unit tests pass locally | 1 | — | **Green** | `results/pr-own-tests.txt` (`internal/controller/workloads`, `api/workloads/...` all ok; `go build ./...` clean) |
| Timeout→reason mapping | `DeadlineExceeded` pod → result State=Failed, Reason=Timeout (issue acceptance criterion) | 1+3 | contract | **Confirmed (incl. live)** | `TestVerifyPR488_DeadlineExceededMapsToTimeout`; **live** `results/live-layer.txt` scenario A — pod ADS=3 killed `DeadlineExceeded`, status `reason=Timeout`, `CustomizedActionFailed=True` condition, Warning event with node/pod/message. Scenario C reproduced the `ImagePullFailed → Timeout` event sequence. |

### Cleared during review (no finding raised)
- **RBAC / security**: no RBAC or permission changes; pods are built exactly as before (user securityContext preserved); Events recorded via the existing recorder. No new attack surface.
- **Generated artifacts in sync**: `zz_generated.deepcopy.go`, CRD yaml and `deploy/kubectl/manifests.yaml` all updated consistently (only v1alpha2 served — no conversion-webhook round-trip concern). CI `check-changes`/`lint`/`build` green.
- **Upgrade/rollback**: pods created by an older controller (no mapping annotation) hit `fallbackCustomizedActionMappings` (prefix `custom-`), so evaluation degrades gracefully rather than erroring; legacy CRs without the new fields validate fine (empty policy = AllSucceeded, no deadline added).
- **Result retention**: succeeded/failed warmup Pods are never deleted while the CR exists (only `failWarmupJob` deletes *active* pods), so `CustomizedActionResults` does not flap after completion.
- **Test quality**: the PR's unit tests assert real outcomes (fake-client `Reconcile` for the global-timeout diagnostics path, condition transitions, event dedup) — not vacuous.

## Verdict

No blockers / majors. The implementation is faithful to issue #486's contract, the test coverage
(unit + reconcile + labeled real-cluster e2e for all five failure modes) satisfies the issue's
acceptance criteria, and CI is green. The findings above are **minor** design/edge notes (R1, R2,
R3, R4) plus nits — none blocks merge on evidence.

Review verdict: **COMMENT**. Approving is never automatic — publish only on explicit instruction.

Nits not carried into the harness: `panic` on an unreachable `json.Marshal` failure in
`buildWarmupPod`; `sort.Strings` re-run inside the dedup loop; pods mid-deletion transiently drop
out of `evaluateCustomizedActionResults`; duplicate container names across actions produce
duplicate logical entries; legacy pods lose original container names in the fallback path.

## Harness-bites check

Two temporary "fixes" applied to production code, canaries re-run, then reverted (production diff
verified empty afterwards):

1. `ActiveDeadlineSeconds: nil` in `buildWarmupPod` → `TestVerifyPR488_MergedPodDeadlineGovernsPreloadContainers`
   and `TestVerifyPR488_SmallestTimeoutWinsAcrossActions` flip to **FAIL**.
2. Preserve `CustomizedActionResults` in `failWarmupJob` when `desiredNodes == nil` →
   `TestVerifyPR488_InvalidSpecWipesExistingCustomizedActionResults` flips to **FAIL** (results retained).

The harness genuinely exercises the paths it claims to.

## Continuing after the fix (resume / next machine)

All durable state lives on branch `verify/pr488-customized-action-reporting` pushed to the
reviewer's fork (`cheyang/rbg`). Pull, then from a checkout of that branch:

```bash
# Re-verify against the latest PR head (auto-fetched via manifest.pr; no sha needed):
bash docs/verification/pr488-customized-action-reporting/scripts/re-verify.sh

# Or against an explicit ref:
bash docs/verification/pr488-customized-action-reporting/scripts/re-verify.sh <fixed-ref>
```

Per finding it prints Fixed / Still-broken / Partial / Harness-update. Canaries count as *fixed
only when they flip to FAIL* — then invert them (promote the new behavior to a contract test).

Polarity table for this round:

| Test | Polarity | Today | If author scopes deadline away from preload / reads policy / preserves results on invalid-spec |
|------|----------|-------|--------------------|
| `TestVerifyPR488_MergedPodDeadlineGovernsPreloadContainers` | canary | PASS | flips FAIL → invert |
| `TestVerifyPR488_SmallestTimeoutWinsAcrossActions` | canary | PASS | flips FAIL (only if min-selection changes) → invert |
| `TestVerifyPR488_DeadlineExceededMapsToTimeout` | contract | PASS | stays PASS (regression guard for the issue's acceptance criterion) |
| `TestVerifyPR488_CompletionPolicyDoesNotAffectEvaluation` | canary | PASS | flips FAIL once evaluation reads the policy → invert |
| `TestVerifyPR488_InvalidSpecWipesExistingCustomizedActionResults` | canary | PASS | flips FAIL if results are preserved → invert |

Layer prerequisites: `go` ≥ repo toolchain (1.27), repo vendored deps (no network needed for L1/L2).
L3 was run in round 1 against the dedicated Aliyun ACK test cluster (3 nodes, k8s v1.36.2): old
controller scaled 2→0, PR-head CRD applied, PR-head manager run out-of-cluster
(`--enable-webhooks none`), scenarios in a throwaway `pr488-verify` namespace. Cluster state fully
restored afterwards (CRD from backup, controller 2/2 Running, namespace deleted). If re-running L3,
follow the same recipe and restore in reverse order; capture to `results/live-layer.txt`.

Kickoff prompt for a fresh agent resuming from this branch alone:

> You are resuming the review verification of sgl-project/rbg PR #488. Branch
> `verify/pr488-customized-action-reporting` holds the harness
> (`docs/verification/pr488-customized-action-reporting/` + `internal/controller/workloads/customized_action_verification_test.go`).
> Read the README's observed-vs-expected table and `.last-reviewed`, run
> `bash docs/verification/pr488-customized-action-reporting/scripts/re-verify.sh` (fetches current PR
> head from `manifest.pr`), then incrementally review the `last-reviewed..head` delta and update the
> table. One active reviewer at a time; commit and push the advanced `.last-reviewed` when done.
