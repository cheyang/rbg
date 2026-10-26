# Verification — PR #512 `fix: retry RoleInstance pod creation after orphan GC`

Reviewer: codex (Reviewer B). Harness is additive only: production code diff of this
branch vs. the PR head is empty (`pkg/`, `api/`, `internal/` untouched; only new
`_test.go` files and this `docs/verification/` tree).

- PR: https://github.com/sgl-project/rbg/pull/512 (fixes issue #511)
- head: `a8ef35fd52f9a0ba5a02dd5bf7ee5abdcf177f86`
- base: `a0b11e6ab95ca6dfcfdf6fca713352c8482ebd41`

## P0 — premise verdict: **CONFIRMED**

Issue #511 claims that during StatefulSet → RoleInstanceSet migration the
replacement Pod is not created automatically: the RoleInstance stays unready
(reporting `pod ... is not controlled by RoleInstance ...`) until a manual
reconcile.

- Component match: the report names the RoleInstance pod-creation path; the PR
  touches exactly `pkg/reconciler/roleinstance/sync/instance_scale.go`.
- Mechanism confirmed on base by two independent layers:
  - **Unit**: on base, `Scale` with a foreign-owned same-name Pod returns
    `(false, 0, nil)` — no error, no requeue (the old `hasOrphanPod` + `continue`
    path). The same test passes on head. See `results/unit-base.out` vs
    `results/unit-head.out`.
  - **Integration (envtest, real API server)**: on base, after the foreign Pod
    is deleted, **no replacement Pod appears within 60s** — the foreign Pod's
    deletion event resolves to the StatefulSet owner, not the RoleInstance
    (`podEventHandler` drops non-RoleInstance owner refs), and nothing else
    requeues the reconcile. The RoleInstance also never gains a `FailedScale`
    condition. This is exactly the stuck state from issue #511. On head the
    full scenario passes in ~15s. See `results/integration-base.out` vs
    `results/integration-head.out`.

## Findings and tests

| id | claim | polarity | layer | base | head | evidence |
|----|-------|----------|-------|------|------|----------|
| P0 / F-fix | foreign same-name Pod blocks creation and yields a retry signal; replacement created after GC | contract | unit | FAIL (no error) | PASS | `results/unit-{base,head}.out` |
| F-fix-integration | same scenario end-to-end via the real RoleInstance reconciler + envtest API server | contract | integration | FAIL (60s timeout, exact #511 symptom) | PASS (~15s) | `results/integration-{base,head}.out` |
| F-regression-owned | already-owned / owned-terminating same-name Pod is skipped quietly (no error storm) | contract | unit | PASS | PASS | `results/unit-*.out` |
| F2 | partial collision (size-2 component, one foreign ordinal) now aborts the whole create batch; base created the sibling | canary | unit | FAIL (created 1 sibling) | PASS (0 created + error) | `results/unit-*.out` |

Harness-bites: the contract tests are red on base and green on head (the PR is
the fix), and the two regression contracts are green on both — so the tests
exercise the changed path, not a coincidental pass. The canary (F2) encodes
current head behavior; flip it if maintainers choose create-what-you-can +
aggregated error instead.

## How to run

Prerequisites: go 1.24+, vendor dir present (repo default). For the integration
layer: envtest binaries (e.g. via `bin/setup-envtest use 1.34.1 --bin-dir ...`,
or the cached set under `$HOME/.local/share/kubebuilder-envtest/k8s/`).

```bash
# unit layer (fast, no API server)
go test -mod=vendor ./pkg/reconciler/roleinstance/sync/ -run 'TestVerify' -count=1 -v

# integration layer (envtest)
export KUBEBUILDER_ASSETS=$(ls -d "$HOME"/.local/share/kubebuilder-envtest/k8s/*-linux-amd64 | sort -V | tail -1)
go test -mod=vendor ./test/envtest/testcase/orphanmigration/ -count=1 -timeout 5m
```

To re-run against a future fix revision from a checkout of this branch:

```bash
bash docs/verification/roleinstance-orphan-pod-retry/scripts/re-verify.sh   # no arg: fetches current PR head
```

Exit 0 iff every finding is fixed; the canary counts as fixed only when it flips.

## Continuing after the fix / handoff

- All durable state is on this branch: harness tests, this README,
  `verify-manifest.json`, `.last-reviewed` (advanced per round), `results/`.
- Kickoff prompt for a fresh machine/agent: "Check out
  `verify/roleinstance-orphan-pod-retry-codex` from the cheyang/rbg fork, read
  `docs/verification/roleinstance-orphan-pod-retry/README.md`, then run
  `bash docs/verification/roleinstance-orphan-pod-retry/scripts/re-verify.sh`."
- Machine-local: envtest binaries under `$HOME/.local/share/kubebuilder-envtest`
  (re-download via `setup-envtest` if absent).
