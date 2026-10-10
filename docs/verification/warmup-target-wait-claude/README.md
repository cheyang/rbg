# Verification — PR #478 (warmup target waits recoverable + CEL rule removal)

- **Branch:** `verify/warmup-target-wait-claude` (reviewer: Claude, first round)
- **PR:** https://github.com/sgl-project/rbg/pull/478 (head `9f61d4f7`, base `35e5d029`)
- **Layers run:** unit (fake-client `go test`) + integration (envtest with the real
  kube-apiserver 1.31.0 binary — the repo's CI version — plus 1.36.2 for a version
  contrast). L3/live skipped per the debate setup.

## Premise (P0) — CONFIRMED

The PR claims three problems. All three reproduce at the base branch; the first two
are fixed at head:

| Claim | Base branch (symptom) | Head (after PR) |
|---|---|---|
| P1: missing target RBG terminally fails a one-shot Warmup | `TestVerifyC1` red at base: first reconcile → `Failed/InvalidTarget` immediately | `TestVerifyC1` green: waits `Running` + `TargetReady=False/RoleBasedGroupNotFound`, proceeds when the target appears |
| P2: unscheduled target Pods → false `Completed/NoNodesMatched` | `TestVerifyC2` red at base: `Completed/NoNodesMatched`, `desired=0` | `TestVerifyC2` green: waits `TargetReady=False/TargetPodsNotScheduled` |
| P3: #466 CEL rule freezes status writes of stored invalid objects | envtest 1.31.0 A/B: status write with the #466 rule installed → **rejected** (`spec.targetNodes.customizedAction.containers[0]: Invalid value: "object": customized action container image must not be empty`); same write with the PR CRD → accepted; main-resource-path update → accepted (ratcheting) | rule deleted from CRD/types/manifests; controller validation retained (`TestVerifyC5` green at head and base) |

Extra checks on P3 (envtest, `results/integration-envtest-cel-ab.txt`):

- **1.36.2 contrast:** the CEL freeze reproduces on 1.36.2 too — the "freeze only
  shows up on <= 1.32" narrative in the PR test comment/KEP is empirically wrong
  (see finding F4). Removing the rule is right regardless; the reasoning should be
  corrected.
- **minLength sub-claim:** the PR body says the kept `minLength: 1` on
  `imagePreload.images` items is ratcheting-safe. `TestVerifyP0d` confirms: status
  write of a pre-existing `images: [""]` object is **accepted** on both 1.31.0 and
  1.36.2 after the minLength rule is added. The asymmetry (CEL blocks, minLength
  does not) validates the PR's exact design choice.

## Findings (observed vs expected)

| ID | Test | Polarity | Head (9f61d4f7) | Base (35e5d029) | Verdict |
|---|---|---|---|---|---|
| F1 | `TestVerifyF1TargetDeletedAfterPodsSucceededReachesTerminal` | contract | **FAIL** — stuck `Running`, `Succeeded=0` | PASS (terminal `Failed`) | bug present at head, introduced by this PR |
| F2 | `TestVerifyF2CountersAndCompletionFrozenDuringWait` | contract | **FAIL** — `Active=1, Succeeded=0` forever while the only warmup Pod is Succeeded | PASS | bug present at head, introduced by this PR |
| F4 evidence | `TestVerifyP0cCRDRuleFreezesStatusOn131`, `TestVerifyP0dImagesMinLengthRatchetClaim` | contract | PASS | n/a | premise confirmed; version narrative corrected |

F1/F2 mechanism: `reconcileUnfinished` returns at the not-ready gate
(`markTargetNotReady`) *before* `updateStatus`, so while a Warmup waits, its
status counters never advance and a Warmup whose work is already finished can
never reach a terminal phase. Triggers: target RBG deleted mid-run (F1), or any
new unscheduled Pod in a selected role appearing after the initial work completed
(F2). Unbounded when `globalTimeoutSeconds` is unset.

## Harness-bites check

- At head the *only* failures in the full upstream unit sweep
  (`go test ./internal/... ./api/... ./pkg/... ./hack/...`) are the two canaries
  F1/F2 (`results/upstream-unit-suite-at-head.txt`) — i.e. the harness detects the
  findings and nothing else.
- The PR's own tests all pass at head; the repo envtest suites
  (`rbg`, `restart_policy`, `webhook`) pass with envtest 1.31.0
  (`results/envtest-suites-at-head.txt`).
- `make manifests generate fmt` at head produces no diff — generated artifacts
  (CRD, manifests, deepcopy) are stable.

## Files

- `internal/controller/workloads/verify_pr478_claude_test.go` — unit layer
  (self-contained; compiles at base and head so the same tests show the premise
  symptoms at base and the fixed behavior at head).
- `internal/controller/workloads/verify_pr478_envtest_test.go` — integration
  layer (envtest A/B). `VERIFY_478_ASSETS` (colon-separated asset dirs) overrides
  the default 1.31.0.
- `fixtures/crd-with-cel-rule-466.yaml` — base (#466) Warmup CRD, i.e. with the
  CEL rule.
- `fixtures/crd-pre466-no-minlength.yaml` — PR CRD minus the images `minLength`
  marker, used to store a violating object before "upgrading".
- `scripts/re-verify.sh` — re-runs the layers against a fixed ref; manifest in
  `verify-manifest.json` (`pr` points at the PR URL, so the head is
  auto-discovered).

## Re-verify

From a checkout of this branch:

```bash
docs/verification/warmup-target-wait-claude/scripts/re-verify.sh \
  --layers unit,integration
```

Expected today: F1/F2 `STILL-BROKEN` (they are the findings), F4-evidence `FIXED`
(evidence tests green). After the author fixes F1/F2, F1/F2 flip to `FIXED`.
