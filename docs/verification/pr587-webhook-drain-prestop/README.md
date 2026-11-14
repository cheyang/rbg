# Verification: PR #587 — drain webhook traffic before the controller manager shuts down

PR: https://github.com/sgl-project/rbg/pull/587 (fixes #583)
Reviewed head: `1db0e04b00835a5b41cfd62a539b1d97422d15a4` (merge-base `1fc69f70`)

## Premise (P0) — Confirmed

The PR claims a few admission requests fail at the moment an old manager Pod is deleted
during a rollout: SIGTERM stops the webhook server immediately, while the API server keeps
routing requests until endpoints drain, and every RBG webhook is `failurePolicy: Fail`.

**Verdict: confirmed**, on two legs (see `verify-manifest.json` → `premise.evidence`):

1. Mechanism reading: the exec `preStop` keeps the container running (SIGTERM is delivered
   only after the hook returns) while the endpoints controller + kube-proxy drain the Pod —
   the standard Kubernetes drain pattern. Exec form (not the native `sleep` action, K8s
   1.30+) is required by RBG's documented minimum of 1.28, and the image ships busybox
   `/bin/sleep`, runnable as UID 65532.
2. The author's published kind measurements: failures on `main` (1/2839 Helm, 3/2692
   kubectl), zero failures after the patch (0/2820 Helm over 5 rollouts, 0/5662 kubectl
   over 10 rollouts).

Independent live repro was **not** run here: no kind/docker on this machine, and the
shared-cluster rules forbid installing a second RBG controller. `verify-manifest.json` →
`liveNote` has the exact repro recipe for a kind-capable machine.

## Findings

| # | polarity | layer | claim | result |
|---|----------|-------|-------|--------|
| F1 | contract | unit | Manager Deployment carries exec preStop `/bin/sleep N` (N>0) and grace ≥ N+30 on both install paths | PASS at head; both tests FAIL at merge-base (negative control) |

No behavioral findings beyond F1: the diff is additive pod-spec fields on the controller's
own Deployment; user-workload rendering is untouched (golden rule 1 is not engaged).

## How to run

Unit layer (needs go + helm, no cluster):

```bash
# contract tests — PASS on the PR head, FAIL on the merge-base
go test ./test/chart/ -run 'TestChartManagerDrainsBeforeShutdown|TestKustomizeManagerDrainsBeforeShutdown' -v
```

Generated-freshness (enforced in CI by project-check.yml; reproduced locally):

```bash
bin/kustomize build config/default | cmp - deploy/kubectl/manifests.yaml   # byte-identical at head and at base
```

Live layer: see `liveNote` in `verify-manifest.json`.

## Observed vs expected

| check | expected | observed | evidence |
|---|---|---|---|
| New tests on merge-base (negative control) | FAIL | FAIL, `want an exec preStop hook, got lifecycle nil` (both paths) | `results/base-chart-test.txt` |
| Full `test/chart` suite on PR head | PASS | 7/7 PASS | `results/head-chart-test.txt` |
| `kustomize build config/default` vs committed bundle | identical | byte-identical at head; also at base (author's "only two fields" claim holds) | `results/kustomize-freshness.txt` |
| CI on head | green | all checks pass incl. `make test-chart` in project-check.yml | gh pr checks 587 |

## Continuing after new pushes

`bash docs/verification/pr587-webhook-drain-prestop/scripts/re-verify.sh` — resolves the
current PR head from `manifest.pr`, grafts `harnessPaths`, re-runs the unit layer, and prints
the `last-reviewed..head` delta. Advance `.last-reviewed` after each round and push.

Kickoff prompt for a fresh machine: "continue the review pipeline for
https://github.com/sgl-project/rbg/pull/587 — verification branch
`verify/pr587-webhook-drain-prestop` on the reviewer's fork."
