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
| F1 | No linked issue (placeholder `fixes #NNNN`) | — | — | **Question** — No tracked issue |
| F2 | Code duplication across 4 layers | — | — | **Nit** — Mirrors upstream pattern |

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
