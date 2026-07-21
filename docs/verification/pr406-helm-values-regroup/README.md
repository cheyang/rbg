# pr406-helm-values-regroup — bug verification

Reproducible evidence for findings raised while reviewing
[sgl-project/rbg#406](https://github.com/sgl-project/rbg/pull/406)
*(refactor(helm): group values under `global` / `controller` / `crdUpgrade`)*.

Harness grafted onto the **code under review**: PR head `86109e5`
(base merge-base `c962917`).

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `helm template` render assertions (Go tests shelling out to `helm`) | `go test ./test/helm/ -run 'TestStressDeployScript\|TestRenderSucceedsWithNullGlobal' -v` |
| 2. Integration | — (not needed; `helm template` fully exercises the claims) | — |
| 3. Live | — (no cluster behavior involved) | — |

Prereq: `helm` in `PATH` (tested with v3.1.1). Tests self-skip if helm is absent.

> Test polarity: all tests here are **contract** tests — they assert the intended behavior,
> so they FAIL on the buggy PR head and PASS once the finding is fixed. No bug-canaries.

## Summary of results

| ID | Claim | Sev | Verdict | Evidence |
|----|-------|-----|---------|----------|
| F1 | `test/stress/scripts/deploy-controller.sh` still passes pre-refactor flat `--set` paths (`image.tag`, `replicaCount`, `resources.*`, `controllerTuning.*`, `pprof.*`); Helm silently drops them, so the stress deploy uses chart defaults | Blocking | **Confirmed** | 3 tests red on PR head → green under fix. `results/unit-buggy-head.out`, `results/unit-fixed-proof.out` |
| F2 | Templates deref `.Values.global.imagePullSecrets` without safe navigation; `helm template --set global=null` fails with a nil-pointer | Non-blocking (medium) | **Confirmed** | `TestRenderSucceedsWithNullGlobal` red on PR head → green under fix. Same artifacts |
| F3 | `deploy-controller.sh` sets `pprof.port` — never a valid chart key (chart uses `containerPort`) | Low / pre-existing | Confirmed by inspection | Not gated by a test; pre-dates this PR |
| F4 | No `NOTES.txt` / upgrade note warns that existing user override files using old top-level keys will be silently ignored after upgrade | Nit / migration | Advisory | Not gated by a test |

## Per-finding detail

### F1 — stress deploy script overrides silently dropped *(Blocking)*
`deploy/helm/rbgs/values.yaml` moved `image`, `replicaCount`, `resources`, `controllerTuning`
(→ `controller.tuning`), and `pprof` under `controller.*`. The PR updated the Makefile, the
two CI workflows, and `tools/release/update-release.sh`, **but not**
`test/stress/scripts/deploy-controller.sh` (which is not in the PR diff). Its `--set` lines
(55–65) still use the old flat paths. Helm accepts unknown top-level keys silently, so every
override is a no-op and the stress deployment falls back to chart defaults — wrong image tag
(`v0.8.0-69fe55d` instead of the locally built `${IMAGE_TAG}`), 2 replicas, default resources,
default reconcile/QPS tuning. That quietly invalidates stress-test runs.

The tests read the path the script *actually* uses (regex over the `--set …="${VAR}"` lines)
and assert the override reaches the render, so they track the script rather than a hardcoded
string:
- `TestStressDeployScriptImageTagOverrideHonored` — `${IMAGE_TAG}` path → sentinel tag must appear in the rendered image.
- `TestStressDeployScriptTuningOverrideHonored` — `${MAX_RECONCILES}` path → `--max-concurrent-reconciles=77` must appear.
- `TestStressDeployScriptReplicaOverrideHonored` — `${REPLICAS}` path → `replicas: 7` must appear.

Observed on PR head: sentinels absent (image stays `…:v0.8.0-69fe55d`, arg stays
`--max-concurrent-reconciles=10`, `replicas: 2`). Expected after fix: sentinels present.

### F2 — `global.imagePullSecrets` lacks safe navigation *(Non-blocking, medium)*
`manager.yaml:22` and `crd-upgrade.yaml:18` both do
`{{- with .Values.<comp>.imagePullSecrets | default .Values.global.imagePullSecrets }}`.
`global` is a Helm-reserved subchart key with null-coalescing semantics, so a parent chart or
an override file can leave `.Values.global` null; the unguarded dereference then aborts the
render. The author guarded `controller.features` and `controller.pprof` with `default dict`
(addressing gemini-code-assist), but left `global` unguarded.

Observed on PR head: `Error: … manager.yaml:22:68 … nil pointer evaluating interface {}.imagePullSecrets`.
Expected after fix: render succeeds.

### F3 — `pprof.port` was never a valid key *(pre-existing, low)*
Line 65 of the stress script sets `pprof.port`, but the chart only ever read
`pprof.containerPort` / `bindAddress`. This override was a no-op before this PR too. Worth
correcting to `controller.pprof.containerPort` while fixing F1.

### F4 — no migration/upgrade note *(nit)*
This is a breaking rename for anyone with a custom values file using the old top-level keys
(`image`, `imagePullSecrets`, `resources`, `schedulerName`, `controllerTuning`, `pprof`,
`portAllocator`). Those overrides will be silently ignored after upgrade (same failure mode as
F1). Consider a `NOTES.txt` warning or a CHANGELOG/upgrade note.

## Proposed fixes (NOT applied to production here — verified by temporary apply + revert)
- **F1**: update `test/stress/scripts/deploy-controller.sh` `--set` paths to the `controller.*`
  namespace: `controller.image.tag`, `controller.replicaCount`, `controller.resources.*`,
  `controller.tuning.*`, `controller.pprof.enabled`.
- **F2**: parenthesize for safe navigation in both templates —
  `| default (.Values.global).imagePullSecrets`.
- **F3**: `--set controller.pprof.containerPort=` (fold into the F1 edit).
- **F4**: add a `templates/NOTES.txt` or upgrade note documenting the renamed keys.

Harness-bites check performed: applying F1+F2 flipped all four tests green; reverting restored
an empty production diff and the tests went red again (`results/unit-fixed-proof.out`).

## Continuing after the fix (possibly on another machine)

The harness lives on branch `verify/pr406-helm-values-regroup` (pushed to the reviewer's fork,
`origin` = `cheyang/rbg`); production code is untouched, so it grafts onto whatever the fixed
code is. One-liner:

```bash
git fetch origin verify/pr406-helm-values-regroup
git checkout verify/pr406-helm-values-regroup
bash docs/verification/pr406-helm-values-regroup/scripts/re-verify.sh
#   ^ no sha needed: it fetches the current PR #406 head (from manifest.pr),
#     grafts the harness, runs the unit layer, and prints Fixed/Still-broken per finding.
```

Prereqs: Go toolchain + `helm` in `PATH`. No envtest, no cluster.

Read results via the polarity table: both findings are **contract** — they should be GREEN
(FIXED) once the PR is corrected. To re-check the reproduction, run the unit command against
the pre-fix PR head; it should still go red.

After reviewing the `last-reviewed..head` delta, advance the marker (re-verify.sh prints the
exact command) and commit it so the next round resumes cleanly.

### Kickoff prompt for a fresh agent
```text
Continue the review pipeline for https://github.com/sgl-project/rbg/pull/406. The verification
branch verify/pr406-helm-values-regroup (remote origin = cheyang/rbg) holds a helm-render
harness for findings F1 (stress deploy script uses stale --set paths) and F2 (global
nil-pointer). Read docs/verification/pr406-helm-values-regroup/README.md, run
scripts/re-verify.sh (auto-discovers the PR head; needs helm + go), report Fixed/Still-broken
per finding honoring contract polarity, review the last-reviewed..head delta for new findings,
then advance .last-reviewed and push.
```
