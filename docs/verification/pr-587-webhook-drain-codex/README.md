# Verification — PR #587 "fix(deploy): drain webhook traffic before the controller manager shuts down"

- PR: https://github.com/sgl-project/rbg/pull/587 (fixes #583)
- Reviewed head: `1db0e04b00835a5b41cfd62a539b1d97422d15a4`
- Base: `1fc69f703695bc9355f5730ee3fb3f75cd0331b6`
- Reviewer: Codex (stage ② harness; production code untouched — this branch adds only `docs/verification/**`)

## Premise (P0) — CONFIRMED

Issue #583: after #575 removed the ~10s admission outage per rollout, a few RBG
admission requests still fail (`connection refused`, occasionally a timeout) right
when an old manager Pod is deleted. Mechanism checked against base code:

1. Base manager Deployment (all three install surfaces) has **no** `lifecycle`
   hook and no `terminationGracePeriodSeconds` (default 30s)
   — `results/base-absence.out`.
2. On SIGTERM the vendored controller-runtime v0.22.1 webhook server calls
   `srv.Shutdown(ctx)`, which closes the :9443 listener **immediately**; the
   manager's `GracefulShutdownTimeout` (default 30s, `cmd/rbgs` does not override
   it) does not delay that listener close — `results/shutdown-codepath.out`.
3. All six RBG webhooks in both install paths are `failurePolicy: Fail`, so any
   request the API server still routes to the terminating Pod is rejected.
4. The PR's own drain tests fail on base with
   `manager container: want an exec preStop hook, got lifecycle nil`
   — `results/base-chart-drain-tests.out` (premise polarity check at manifest
   level).

The live failure *rate* (1/2341 requests etc.) is the author's kind measurement;
the live layer was skipped per round instructions. The mechanism and the absence
of any drain window on base are positively confirmed.

## Claims checked (all contract polarity)

| id | claim | layer | result |
|----|-------|-------|--------|
| P0 | base has no drain window; SIGTERM closes the webhook listener at once | static+unit | **Confirmed** (`results/base-absence.out`, `results/base-chart-drain-tests.out`, `results/shutdown-codepath.out`) |
| C1 | head chart + kustomize base satisfy the drain contract via the PR's own tests | unit (`go test ./test/chart/`) | **Pass** at head (`results/head-chart-drain-tests.out`), **Fail** at base (= harness bites) |
| C2 | helm renders the hook under default and variant values (pprof, portAllocator, deprecated toggle); manifests.yaml Deployment carries it | integration (`scripts/check-drain-manifests.sh`) | **Pass** (`results/head-helm-render.out`, `results/head-manifests-deployment.out`) |
| C3 | `deploy/kubectl/manifests.yaml` is byte-identical to `kustomize build config/default` (kustomize v5.5.0, Makefile-pinned) | integration (`scripts/check-kustomize-sync.sh`) | **Pass** at head **and** at base (`results/head-kustomize-sync.out`, `results/base-kustomize-sync.out`) |

`/bin/sleep` in the runtime image: static — the Dockerfile runtime stage is
`alpine:3.18-update` (busybox; `/bin/sleep`, world-executable, exec hook needs no
shell; container runs as UID 65532). Author additionally verified on the published
image `rolebasedgroup/rbgs-controller:v0.9.0-73e05db4`, which is exactly the image
both install paths reference. No container runtime was available in the review
environment (`results/image-sleep-evidence.out`).

## How to run

From the repo root of the ref under test:

```bash
# L1 unit (needs go; needs helm for the chart-render test)
go test ./test/chart/ -run 'TestChartManagerDrainsBeforeShutdown|TestKustomizeManagerDrainsBeforeShutdown' -v

# L2 integration (needs python3+pyyaml, helm optional; kustomize auto-installed to bin/)
bash docs/verification/pr-587-webhook-drain-codex/scripts/check-drain-manifests.sh
bash docs/verification/pr-587-webhook-drain-codex/scripts/check-kustomize-sync.sh
```

One-shot, against the current PR head, from a checkout of this branch:

```bash
bash docs/verification/pr-587-webhook-drain-codex/scripts/re-verify.sh
```

## Continuing after a fix / on another machine

All state is on this branch (`verify/pr-587-webhook-drain-codex`):
manifest, `.last-reviewed`, scripts, captured results. `re-verify.sh` resolves the
current PR head from `manifest.pr` (machine-independent fetch), grafts
`harnessPaths`, runs L1+L2, and prints Fixed/Still-broken per claim; exit 0 iff
all fixed. All entries are **contract** polarity — green means the drain contract
holds; there are no canaries to invert. After a round, advance
`.last-reviewed` to the reviewed head sha.

L3 (live, not run this round): see `liveNote` in `verify-manifest.json` —
rollout-restart the manager under a dry-run-create loop on both install paths and
count `failed calling webhook` errors; expect 0 over ≥5 rollouts each.

Kickoff prompt for a fresh agent:
> Continue verification for https://github.com/sgl-project/rbg/pull/587. Clone the
> repo, `git fetch https://github.com/cheyang/rbg.git verify/pr-587-webhook-drain-codex`,
> checkout that branch, then run
> `bash docs/verification/pr-587-webhook-drain-codex/scripts/re-verify.sh`.
