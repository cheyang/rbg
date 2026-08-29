# spurious-rollout-identity-labels — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/439
("Fix/upgrade spurious rollout and identity labels").

Layers run against the **code under review** (`1cf0b055`, PR head = origin/pr/439):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | pure logic: per-ordinal identity isolation + spec-replacement identity retention | `go test ./pkg/reconciler/roleinstanceset/statefulmode/... ./pkg/inplace/instance/inplaceupdate/... ./pkg/inplace/instance/... -run 'TestNewVersionedInstanceIdentityIsPerOrdinal|TestPatchUpdateSpecKeepsIdentity|TestInjectIdentityLabelsIntoComponents' -v` |
| 2. Integration (envtest) | **not run** — existing envtest suite does not cover in-place pod image update | `make test-envtest` (see F3) |
| 3. Live | real controller + real cluster | not run (no `KUBECONFIG`) |

> Test polarity: both F1 and F2 are **contract** tests (assert the intended correct behavior).
> They FAIL on buggy/pre-fix code and PASS when fixed. There are no bug-canaries here.

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| F1 | `newVersionedInstance` deep-copies `RoleInstanceSpec` so per-ordinal identity labels don't alias across instances (highest ordinal would otherwise win) and the shared set template stays clean | 1 | **Confirmed (fixed)** | PASS on `1cf0b055`; FAIL when `DeepCopy()` reverted to bare struct assignment — ordinal 0 got `test-set-1` and the shared template was polluted. `results/F1-pass.txt`, `results/F1-reverted-fail.txt` |
| F2 | `InjectVersionedRoleInstanceSpec` re-injects identity labels after the spec is replaced by the shared template, so an in-place update does not drop ordinal identity from the pod template | 1 | **Confirmed (fixed)** | PASS on `1cf0b055`; FAIL when `InjectIdentityLabelsIntoComponents(instance)` is removed — pod-template identity labels came back `""`. `results/F2-pass.txt`, `results/F2-reverted-fail.txt` |
| F3 | Identity labels survive a real in-place pod image update on the *running pod* (not just the spec) | 2 | **Uncovered** | No envtest exercises in-place pod image update; spec→pod derivation is mechanical K8s (pod labels come from the template) but is not asserted at integration level. See "F3" below. |

No `blocker`/`major` findings survived verification — the PR's two correctness fixes are
proven by contract tests that bite. Overall review verdict: **COMMENT** (no change-requesting
finding). The `e2e-test-manifest` CI failure on the PR is flaky on `main` (failures on
2026-08-26 and 2026-08-27 main runs) and is not attributed to this PR.

## Per-finding detail

### F1 — deep-copy guards shared-slice aliasing of identity labels (contract, unit)
- **Mechanism:** before the fix, `newVersionedInstance` did
  `Spec: setToUse.Spec.RoleInstanceTemplate.RoleInstanceSpec` — a struct assignment that
  *shares* the `Components` slice with the set's template. `InjectIdentityLabelsIntoComponents`
  then writes per-ordinal labels into that shared slice, so every instance built in the same
  reconcile sees the last-written (highest) ordinal, and the set's own template is mutated.
- **Fix under review:** `pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_utils.go:163`
  → `Spec: *setToUse.Spec.RoleInstanceTemplate.RoleInstanceSpec.DeepCopy()`.
- **Test:** `pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_utils_test.go::TestNewVersionedInstanceIdentityIsPerOrdinal` — builds 2 instances, asserts each carries its own ordinal **and** the set's shared template carries none.
- **Harness-bites (proves the test exercises the path):** reverting `DeepCopy()` → bare struct assignment makes the test FAIL with
  `ordinal 0: pod template statefulset.kubernetes.io/pod-name is "test-set-1", expected "test-set-0"` and
  `the set's shared template was given statefulset.kubernetes.io/pod-name="test-set-1"`.
- Artifacts: `results/F1-pass.txt`, `results/F1-reverted-fail.txt`.

### F2 — identity re-injection after spec replacement (contract, unit)
- **Mechanism:** the in-place update path (`defaultPatchUpdateSpecToRoleInstance`) replaces
  the instance spec with the shared `RoleInstanceSet` template (`spec.NewTemplate`), which
  carries no per-ordinal identity. Without re-injection the instance's pod template loses the
  labels saying which ordinal it is, and the running pods lose them on the next metadata patch.
- **Fix under review:** `pkg/inplace/instance/inplaceupdate/inplace_update.go:216` —
  `InjectVersionedRoleInstanceSpec` now calls `inplaceutil.InjectIdentityLabelsIntoComponents(instance)`,
  sourcing identity from the instance's own `Labels` (which survive the spec replacement).
- **Test:** `pkg/inplace/instance/inplaceupdate/inplace_update_defaults_test.go::TestPatchUpdateSpecKeepsIdentity`.
- **Harness-bites:** removing the `InjectIdentityLabelsIntoComponents(instance)` call makes the test FAIL with
  `pod template statefulset.kubernetes.io/pod-name is "" after the update, expected "set-0"`.
- Artifacts: `results/F2-pass.txt`, `results/F2-reverted-fail.txt`.

### F3 — identity survival on the running pod through a real in-place update (integration, UNCOVERED)
- **What is missing:** the repo's envtest suite (`test/envtest/testcase/...`, Ginkgo) covers
  restart-policy recreation, backoff, and shared-service selection, but **no `It` exercises an
  in-place pod image update** (a `grep` for `inplace`/`InPlace` under `test/envtest/` is empty).
  So nothing asserts at integration level that a running pod keeps
  `statefulset.kubernetes.io/pod-name` and `rbg.workloads.x-k8s.io/role-instance-index`
  through a real in-place image update.
- **Why this is low-risk but still a gap:** the spec-level claim is proven (F2), and a pod's
  labels are derived from its template by ordinary K8s mechanics, so the downstream behavior is
  near-certain. But "near-certain" is not "verified", and this is exactly the behavioral link
  the PR's commit message stakes its user-facing benefit on.
- **Not fabricated here:** building a dedicated in-place-update envtest requires
  `setup-envtest` assets (kube-apiserver/etcd), generated CRD manifests, and a
  ControllerRevision image-rollback scenario. That is out of scope for this round; recorded as
  a tracked gap. The unit contract tests (F1, F2) stand as the proof.

## Proposed fixes (NOT applied to production here)
- **None required for correctness.** The two fixes are proven.
- **(minor, DRY)** `identityLabelKeys` (`pkg/inplace/instance/instance_util.go`) and the inline
  `identityLabels` maps in `updateIdentity`/`newVersionedInstance`
  (`stateful_instance_set_utils.go`) duplicate the same two keys. A single exported
  helper (e.g. `IdentityLabels(name, ordinal)`) reused at all three sites would prevent silent
  drift if a third identity label is added in one place only.
- **(nit)** `GetRawRestartBackoff` (`api/workloads/v1alpha2/helper.go`) returns the live internal
  pointer (`lwp.RestartPolicyConfig`). Read-only here, but returning a value or a `// do not
  mutate` doc removes a future footgun.
- **(gap)** add an envtest that triggers an in-place image update and asserts identity labels
  survive on the running pod (closes F3).

## Continuing after the fix (possibly on another machine)

The harness lives on branch `verify/spurious-rollout-identity-labels` (production code
untouched), so it grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git remote add upstream https://github.com/sgl-project/rbg.git  # if not present
   git fetch upstream pull/439/head:pr439        # current PR head
   git checkout <fixed-branch>
   git checkout verify/spurious-rollout-identity-labels -- \
     docs/verification/spurious-rollout-identity-labels \
     pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_utils_test.go \
     pkg/inplace/instance/inplaceupdate/inplace_update_defaults_test.go \
     pkg/inplace/instance/instance_util_test.go
   ```
   (If the test files already ship with the PR — they do — only the `docs/verification/` dir
   needs grafting onto later fixed commits.)
2. Prereqs: Layer 1 = Go toolchain only (`go version`; CI uses 1.26.6, 1.25 works for these
   tests). Layer 2 = `make setup-envtest` + `make manifests generate` (not run this round).
   Layer 3 = `export KUBECONFIG=...` for a real cluster.
3. Re-run, or use the one-liner:
   ```bash
   bash docs/verification/spurious-rollout-identity-labels/scripts/re-verify.sh   # no arg = current PR head
   bash docs/verification/spurious-rollout-identity-labels/scripts/re-verify.sh <fixed-ref>
   ```
   Exit 0 iff every contract finding is Fixed.
4. Read results via the polarity table: both tests are **contract** → they should be GREEN.
   (There are no bug-canaries to invert.)
5. Harness-bites: run Layer 1 once against pre-fix code (revert the two hunks, see F1/F2
   "Harness-bites" above) to confirm the tests still go red.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/spurious-rollout-identity-labels of
sgl-project/rbg. Background: a review of PR #439 produced findings F1 (deep-copy aliasing),
F2 (in-place identity re-injection), F3 (uncovered integration gap); a harness reproduced
F1/F2 as contract tests. The PR is fixed at <ref>. Read
docs/verification/spurious-rollout-identity-labels/README.md ("Continuing after the fix") and
follow it: graft the harness onto the fixed code, re-run the unit layer (F1/F2 contract tests
must be GREEN), optionally build+run the envtest layer to close F3 if setup-envtest is
available, run the harness-bites check (revert hunks → tests go red), and report an
observed-vs-expected table. Do not run cluster-wide destructive actions.
```
