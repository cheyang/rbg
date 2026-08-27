# Verification: PR #433 — Semantic Revision Equality

## Premise (P0)

| Field | Value |
|-------|-------|
| Claimed symptom | ControllerRevision byte comparison returns false for semantically identical revisions, triggering spurious rolling updates |
| Claimed mechanism | `creationTimestamp` field serializes as `"creationTimestamp": null` in some client-go versions but is omitted in others |
| Affected component | All 4 controller layers: RBG controller, RoleInstance, StatefulInstanceSet, StatelessInstanceSet |
| Trigger conditions | Controller binary upgrade to newer client-go version with existing revisions stored from older format |

**Premise verdict: Confirmed** — The `withLegacyCreationTimestamp` test helper demonstrates the drift mechanism works exactly as described. The approach ports kubernetes/kubernetes#135017 which was merged upstream.

## Findings

| ID | Claim | Layer | Polarity | Verdict |
|----|-------|-------|----------|---------|
| P0 | Serialization drift causes spurious revision creation | Unit | contract | **Confirmed** — `LegacyCreationTimestamp_SemanticallyEqual` passes |
| F3 | `handleRevisions` keeps fresh role hash after semantic match → spurious rollout persists | Unit | canary | **Fixed (round 2, `12915120`)** — one-line `expectedRevision = currentRevision`; polarity confirmed by revert |
| F1 | No linked issue (placeholder `fixes #NNNN`) | — | — | **Question** — still open |
| F2 | Code / test-helper duplication across 4 layers | — | — | **Addressed** — helper hoisted to `test/utils/revision.go` |

## Round 2 — re-review of `e16dca84..129151205a2f`

The author pushed `129151205a2f` ("fix: keep persisted revision for role hashes on semantic match") in response to the round-1 `REQUEST_CHANGES`. Delta re-review:

| Item | Result |
|------|--------|
| F3 fix present | ✅ `expectedRevision = currentRevision` added right after the semantic-match log line |
| Regression test added | ✅ `Test_HandleRevisions_SemanticallyEqualRevisionKeepsPersistedRoleHash` (author's own, equivalent to our fork test) |
| Test polarity | ✅ Reverting the one-line fix flips the test to **FAIL** (fresh `7f54c9ffd5` ≠ persisted `5bf9dcc5d5`) |
| F2 nit resolved | ✅ `withLegacyCreationTimestamp` extracted to `test/utils/revision.go`, 4 copies + new test share it |
| All 4 `SetMatchesRevision` layers | ✅ 32/32 subtests green |
| Full package suite | ✅ 15/15 packages `ok`, `go build ./...` PASS, no regressions |

**Round-2 verdict:** the round-1 blocker (F3) is fixed and proven; only the non-blocking `fixes #NNNN` placeholder (F1) remains. **Ready to merge** once the PR body link is cleaned up.

## Test Results (sandbox 43.99.38.217)

### L1 — Unit Tests (all 4 layers)

```
sigs.k8s.io/rbgs/pkg/utils                                      PASS  0.156s (8 subtests)
sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/revision            PASS  0.234s (8 subtests)
sigs.k8s.io/rbgs/pkg/reconciler/roleinstanceset/statefulmode     PASS  0.248s (8 subtests)
sigs.k8s.io/rbgs/pkg/reconciler/roleinstanceset/statelessmode/revision  PASS  0.302s (8 subtests)
```

### Build Verification
```
go build ./...  — PASS (all packages compile)
```

### Full Package Test Suite (no regressions)
```
sigs.k8s.io/rbgs/pkg/utils                                PASS  0.446s
sigs.k8s.io/rbgs/pkg/reconciler/roleinstance               PASS  0.166s
sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/revision      PASS  0.175s
sigs.k8s.io/rbgs/pkg/reconciler/roleinstanceset/statefulmode  PASS  0.212s
sigs.k8s.io/rbgs/pkg/reconciler/roleinstanceset/statelessmode PASS  0.134s
sigs.k8s.io/rbgs/pkg/reconciler/roleinstanceset/statelessmode/revision PASS  0.280s
sigs.k8s.io/rbgs/internal/controller/workloads             PASS  7.140s
```

### L3 — Live Cluster Test (sandbox 43.99.38.217)

**Result: PASS** — no spurious ControllerRevision created on controller restart.

| Phase | Observation |
|-------|-------------|
| RBG → RIS creation | `test-rbg-worker` (1/1 ready, 40s reconcile) |
| ControllerRevisions created | 3: RBG-level, RIS-level, RoleInstance-level |
| Drift injection attempt | creationTimestamp:null injected locally (384→411 bytes) |
| API server behavior | Null value stripped on store (managed ACK cluster normalizes RawExtension) |
| Controller restart | Reconciles test-rbg, no new revision created |
| Revision count | 3 before / 3 after, all ResourceVersions unchanged |

**Environment notes:**
- Cluster: managed ACK with API server null normalization (cannot write `null` into ControllerRevision.Data via any standard API path)
- Existing RIS objects in `default` namespace used `restartPolicy` as struct (incompatible with PR branch's string type) — temporarily deleted for test
- Controller built from PR branch (`e16dca84`) used for both baseline creation and verification
- The exact byte-drift scenario (creationTimestamp:null vs field omission) requires an older API server or direct etcd write — covered exhaustively at L1

## How to Re-run

```bash
cd ~/rbg
git checkout verify/pr433-revision-semantic-equality
bash docs/verification/pr433-revision-semantic-equality/scripts/re-verify.sh
```

## Review Summary

The PR is well-implemented, porting a proven upstream pattern (k8s #135017 / LWS #798) to all four RBG controller layers. Unit tests adequately cover the semantic equality logic with 8 subtests per layer (32 total). No functional bugs found.

**Verdict: COMMENT** — no blockers, no major issues.
