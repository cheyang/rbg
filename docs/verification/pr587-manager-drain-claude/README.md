# pr587-manager-drain — verification (Reviewer A, Claude)

Reproducible evidence for the review of https://github.com/sgl-project/rbg/pull/587
(head `1db0e04b00835a5b41cfd62a539b1d97422d15a4`, base `1fc69f703695bc9355f5730ee3fb3f75cd0331b6`).

The PR adds an exec `preStop: /bin/sleep 5` hook and `terminationGracePeriodSeconds: 35`
to the controller-manager container in the Helm chart and `config/manager` (plus the
regenerated `deploy/kubectl/manifests.yaml`), so a terminating Pod keeps serving webhook
traffic for 5 s while it leaves the Service endpoints. The PR also ships
`test/chart/manager_shutdown_test.go` asserting both install paths.

Layers (this round: L1 only; live layer skipped per round policy — no KUBECONFIG):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (chart render) | preStop + grace period present in Helm and kustomize paths | `go test ./test/chart/ -run 'ManagerDrains' -v` |
| 1b. Script | `deploy/kubectl/manifests.yaml` in sync with `kustomize build config/default` | `bash docs/verification/pr587-manager-drain-claude/scripts/check-kustomize-sync.sh` |
| 2. Integration | — (not needed; no runtime code changed) | — |
| 3. Live | kind rollout-restart webhook drain harness from #583/#575 | skipped this round, see liveNote |

> Polarity: both checks are **contract** tests — they assert the intended behavior,
> so they FAIL on code without the fix and PASS with it.

## Problem premise (P0)

Run against the **base** branch, without the patch.

| | |
|---|---|
| Claimed symptom | "a few requests still fail right when an old manager pod is deleted ... as `connection refused` or occasionally a timeout" (issue #583, `failurePolicy: Fail` webhooks) |
| Linked issue | #583, OPEN, created 2026-11-13 (bug label) — matches the PR body 1:1 |
| Reported component | manager Deployment in Helm chart + `deploy/kubectl/manifests.yaml` |
| Patched component | exactly those three manifest files — match |
| **Verdict** | **Confirmed** (mechanism-level; live layer not run this round) |
| Evidence | On base: no `lifecycle` in either manifest; controller-runtime v0.22.1 closes the webhook listeners as soon as the signal context is canceled (`vendor/sigs.k8s.io/controller-runtime/pkg/webhook/server.go:243-256`); `GracefulShutdownTimeout` is not overridden in `cmd/rbgs/main.go` so the 30 s default applies (`.../pkg/manager/internal.go:55`); all six webhooks are `failurePolicy: Fail` in both install paths; the controller image is alpine-based (`FROM ...alpine:3.18-update`) so busybox `/bin/sleep` exists for UID 65532. Issue #583 carries measured base-branch failures (1/2839 Helm, 3/2692 kubectl) from the #575 harness. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Premise (problem exists on base) | mechanism | Confirmed | see above |
| C1 | Chart manager Deployment drains before shutdown (preStop exec sleep N>0, grace ≥ N+30) | 1 | Confirmed-fixed on head | `results/unit-head.json` PASS; `results/unit-base.json` FAIL (`want an exec preStop hook, got lifecycle nil`) |
| C2 | Kustomize base `config/manager/manager.yaml` carries the same drain | 1 | Confirmed-fixed on head | same artifacts |
| K1 | `deploy/kubectl/manifests.yaml` == `kustomize build config/default` byte for byte | script | Pass on head and base | `results/kustomize-sync.txt`, `results/kustomize-sync-base.txt` |

Harness-bites checks performed (Step 4):

1. Contract tests grafted onto the **base** commit fail:
   `TestChartManagerDrainsBeforeShutdown` / `TestKustomizeManagerDrainsBeforeShutdown` →
   `manager container: want an exec preStop hook, got lifecycle nil`
   (`results/unit-base.json`).
2. Second failure branch: with the chart grace period reverted to 30 (preStop intact),
   the test fails with `terminationGracePeriodSeconds = 30, want at least 35`
   (assertion-level verification, not left in the tree).
3. Kustomize fidelity of the `kubectl kustomize` proxy verified on base (0-line diff)
   before trusting it on head.

## Per-finding detail

- **C1/C2 (PR's own contract)**: `go test ./test/chart/ -run 'ManagerDrains' -json` —
  2/2 pass on head, 2/2 fail on base. The test also enforces `terminationGracePeriodSeconds
  >= sleep + 30`, matching controller-runtime's default `GracefulShutdownTimeout` (30 s,
  not overridden in `cmd/rbgs`).
- **K1**: `kubectl kustomize config/default` reproduces the committed
  `deploy/kubectl/manifests.yaml` exactly on both base and head, so the only kubectl-path
  delta is the two new fields, as the PR claims.

## Findings raised (none blocking)

No blocker or major findings. Non-blocking notes live in the debate document
(`findings-claude.md`): N1 hard-coded 5 s / 35 s not tunable via `values.yaml`;
N2 `managerGracefulShutdownSeconds = 30` duplicates controller-runtime's default and
would silently drift if `cmd/rbgs` ever overrides it; Q1 maintainer preference on values
exposure; R1 residual risk that 5 s is too short on large clusters (author acknowledged).

## Live run notes

Not run this round (no KUBECONFIG; live layer out of scope per round policy).
Author's measured evidence (PR body): 0 failed requests over 5 rollouts/2820 requests
(Helm) and 10 rollouts/5662 requests (kubectl manifests) vs. non-zero on main.
Re-run signal to check when live is available: 0 occurrences of
`failed calling webhook "mrolebasedgroup.kb.io"` during `rollout restart`.

## Proposed fixes (NOT applied to production here)

None required for merge. Optional: expose `controller.terminationGracePeriodSeconds` /
`controller.preStopSleepSeconds` in `values.yaml`; see N1 in the debate document.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr587-manager-drain-claude` of
https://github.com/cheyang/rbg.git (production code untouched — the only diff vs. the PR
head is under `docs/verification/pr587-manager-drain-claude/`).

1. Graft it onto whatever code you want to check:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/pr587-manager-drain-claude
   git checkout <ref-to-test>
   git checkout FETCH_HEAD -- docs/verification/pr587-manager-drain-claude test/chart/manager_shutdown_test.go
   ```
2. Prereqs: Layer 1 = go toolchain + `helm` on PATH (tests skip without helm unless CI=1);
   layer 1b = `kubectl`. No cluster needed.
3. Re-run: `go test ./test/chart/ -run 'ManagerDrains' -v` and
   `bash docs/verification/pr587-manager-drain-claude/scripts/check-kustomize-sync.sh`.
4. Or run everything at once: `bash docs/verification/pr587-manager-drain-claude/scripts/re-verify.sh`
   (no arg: fetches the current PR head via the manifest's `pr` URL; prints
   Fixed/Still-broken per finding and exit 0 iff all fixed).
5. Polarity: both findings are contract tests — green means the drain config is present.
   There are no canaries to invert.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/pr587-manager-drain-claude of
https://github.com/cheyang/rbg.git. Background: review of sgl-project/rbg PR #587
(preStop drain for the controller manager); the harness on that branch verified the
fix's contract tests and manifest sync. Read docs/verification/pr587-manager-drain-claude/README.md
("Continuing after the fix") and follow it: graft the harness onto the current PR head,
run scripts/re-verify.sh plus scripts/check-kustomize-sync.sh, re-check any new
commits in the last-reviewed..head delta, and report an observed-vs-expected table.
Live layer (kind rollout harness) only if a KUBECONFIG is provided. Do not modify
production code; do not push to upstream.
```
