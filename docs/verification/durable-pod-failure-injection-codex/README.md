# Verification: PR sgl-project/rbg#514 — "test(e2e): use durable pod failure injection"

Reviewer: codex (Reviewer B). Round: initial. Base `8a7bcfcb`, head `288a840a`.

## Premise (P0) — runs against base

Claim (issue #490, OPEN): patching a live Pod's status to `Failed` is not durable —
kubelet owns pod status and rewrites it, causing intermittent timeouts or vacuous
passes in restart-policy e2e specs.

Verdict: **Confirmed-by-report** (not independently reproduced). The issue cites
failing CI runs on k8s 1.31/1.36 kind clusters, and PR #489 already fixed the same
mechanism in one spec. This is a tests-only PR, so the premise carve-out applies;
the live layer (real kubelet) was skipped per round policy. Component match: the
issue names the exact files this PR touches. Mechanism plausibility on base:
`SetPodFailed`/`SetPodEvicted` wrote to the kubelet-owned status subresource —
confirmed in the base code; all 12 call sites are removed at head.

## Findings

### F1 [major] New `k8s.io/client-go/tools/watch` import is not vendored — default `go build ./...` fails

`test/e2e/testcase/v1alpha2/inactive_pod.go:30` adds
`watchtools "k8s.io/client-go/tools/watch"`. The repo commits a `vendor/` tree
(used by the CLI release builds via `go build -mod vendor`, Makefile:227/236), and
that tree does not contain `client-go/tools/watch`. The Makefile's global
`GOFLAGS=-mod=mod` keeps CI green, but plain `go build ./...`, `go vet ./...`,
`go test ./test/e2e/...` (no `-mod` flag) and IDE/gopls analysis (vendor mode is
the default when `vendor/modules.txt` exists) break at head. Proven regression:
base builds clean in vendor mode, head fails (see results). Fix is one command:
`go mod vendor` (adds `vendor/k8s.io/client-go/tools/watch/` + one modules.txt
line; go.mod/go.sum unchanged) — verified by applying and reverting it.

Observed vs expected:

| claim | expected | observed (head) | evidence |
| --- | --- | --- | --- |
| F1: vendor-mode build works | exit 0 | exit 1, missing `client-go/tools/watch` | `results/f1-vendor-build-base-vs-head.txt` |
| F1 fix check: `go mod vendor` | build flips to pass | build passes; revert restores failure | `results/f1-fix-check.txt` |
| P1a: PR helper unit test | pass | PASS | `results/l1-unit.txt` |
| P1b: controller anchor identities | pass | PASS (incl. bite-checked tripwire) | `results/l1-unit.txt` |
| P1c: e2e compiles + spec tree constructs | pass | PASS; 11 changed specs registered under the CI `update` filter | `results/l2-spec-tree.txt` |

### Anchor tripwire (additive test, no production change)

`pkg/reconciler/roleinstance/sync/e2e_anchor_assumptions_review_test.go` encodes
the three controller identities the migrated e2e specs depend on:

1. First backoff equals `baseDelay` (so `firstRestartBackoffDeadline`'s
   `LastRestartTime + baseDelay` anchor matches `calculateRestartDelay`).
2. A container `RestartCount>0` on a Ready, generation-converged instance triggers
   `shouldRecreateInstance` (makes `RestartNginxContainer` a real trigger).
3. The `RestartTriggerPolicyIgnore` pod annotation suppresses that trigger.

Harness-bites: mutating `rc := restartCount - 1` to `rc := restartCount` fails (1);
disabling the `hasTriggerPolicyIgnore` skip fails (3). Both reverted; production
diff empty.

## Reasoning-only findings (no harness)

- F2 [nit]: PR body describes CI Kind image-export changes ("Export LWS, RBGS, and
  e2e workload images as linux/amd64 archives..."), but the diff has zero
  `.github/` changes; that fix landed separately in #516 (`d4a5dd05`, already in
  base). Stale PR description.
- F3 [minor]: new hard timing assertions (`time.Until(backoffDeadline) > 15s/45s`)
  add a flake mode on slow CI: if first-recreation+recovery approaches
  `baseDelay - margin`, the spec fails even with a correct controller. Deliberate
  non-vacuity tradeoff; margins ~25s over the author's documented ~50s recovery.
- F4 [nit]: `runRestartPolicyConfigOverridesLegacyTest` re-checks pod
  Ready+Running in an `Eventually` right after `waitForNginxContainerRestart`
  already guaranteed exactly that. Redundant.
- F5 [question]: inactive-pod cases 1/6 moved from Deployment-backed roles to
  RoleInstanceSet-backed roles. The Deployment-backed inactive-pod path is no
  longer exercised anywhere in e2e (Deployment lifecycle itself still covered in
  `deploy.go`). Intentional?

## Layers

- L1 unit: `results/l1-unit.txt`
- L2 integration (no cluster): `results/l2-spec-tree.txt`
- L3 live: intentionally skipped (debate policy); PR-head CI "E2E update" job
  (which runs these specs) is green.

## Re-verify

```bash
bash docs/verification/durable-pod-failure-injection-codex/scripts/re-verify.sh
# exit 0 iff F1 is fixed and all anchor/unit checks pass
```

## Continuing after the fix

1. Author runs `go mod vendor` and commits; re-run `re-verify.sh` — F1 flips to
   Fixed. No polarity inversions needed (all checks are contract tests).
2. Tripwire test is in-package and additive; keep or drop at uplift time.

Copy-paste kickoff for a fresh agent on another machine:

```bash
git fetch https://github.com/cheyang/rbg.git verify/durable-pod-failure-injection-codex
git checkout verify/durable-pod-failure-injection-codex
bash docs/verification/durable-pod-failure-injection-codex/scripts/re-verify.sh
```
