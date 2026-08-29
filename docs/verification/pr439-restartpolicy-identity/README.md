# pr439-restartpolicy-identity — bug verification

Reproducible evidence for the findings raised while reviewing
[sgl-project/rbg#439](https://github.com/sgl-project/rbg/pull/439).

Layers run against the **code under review** (PR head `1cf0b055`, base `a747e804`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (L1) | the exact PR code: restart-policy template gate, DeepCopy of the shared template, in-place identity re-injection | `bash scripts/bite-check.sh` (proves the harness bites) then `go test -run 'TestRoleInstanceSetReconciler_RestartPolicyTemplateRepresentation\|TestNewVersionedInstanceIdentityIsPerOrdinal\|TestInjectIdentityLabelsIntoComponents\|TestPatchUpdateSpecKeepsIdentity' ./pkg/reconciler/ ./pkg/reconciler/roleinstanceset/statefulmode/... ./pkg/inplace/instance/...` |
| 2. Integration (L2) | not added — L1 + the PR author's envtest `test/envtest/testcase/restart_policy/` already cover the logic | — |
| 3. Live (L3) | the live ACK cluster via `$KUBECONFIG` | `KUBECONFIG=/root/.kube/config bash scripts/live-scenario.sh` (read/create-only on a throwaway ns; auto-teardown) |

> Polarity: **all findings are contract tests** (assert the intended correct behavior; FAIL on
> buggy code, PASS when fixed). No bug-canaries. `bite-check.sh` reverts each fix to prove the
> test goes RED without it.

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| F1 | A role that configured no backoff delays must keep the deprecated `restartPolicy` **string** in the RoleInstanceSet template (only switch to `restartPolicyConfig` when a delay is actually set). Reverting the gate rewrites the string into the config and moves the revision hash → spurious rollout on upgrade. | 1 | **Confirmed** (L1). L3 partial — see notes. | `results/f1-unit.txt`, `results/bite-check.txt` (RED on revert). `results/f1-live-reproduction.txt` for the live cluster signal. |
| F2 | In one reconcile, every ordinal's RoleInstance must carry its **own** ordinal identity labels. A struct assignment shares the Components slice with the set's template and the highest ordinal wins. | 1 | **Confirmed** (L1) | `results/f2f3-unit.txt`, `results/bite-check.txt` (ordinal 0 → `test-set-1` on revert) |
| F3 | An in-place update that replaces the whole spec with the shared (identity-less) template must re-inject the instance's ordinal labels. | 1 | **Confirmed** (L1) | `results/f2f3-unit.txt`, `results/bite-check.txt` (labels empty on revert) |
| F4 | `GetRawRestartBackoff` returns a live pointer, not a copy. | — | nit, no fix/test required | helper.go:346 — callers only nil-check it; safe today |

### Severity decision (inherited from stage ①)

F1 was raised as `major` (REQUEST_CHANGES) **because it was untested**. Stage ② added a
contract test (`TestRoleInstanceSetReconciler_RestartPolicyTemplateRepresentation`) that proves
the claim and bites (RED on the pre-fix code). With the claim now proven at L1, F1 is
**downgraded from major → resolved-by-proof**: the fix is correct and now covered. F2/F3 were
already covered by the PR author's tests; stage ② confirmed they bite. Net: **no blocker
remains**; verdict for the PR is COMMENT, not REQUEST_CHANGES.

## Per-finding detail

### F1 — restart-policy template representation (commit f7a50152)
**Mechanism.** `pkg/reconciler/roleinstanceset_reconciler.go:170-180` builds the
RoleInstanceSet template. Pre-fix (#394, main-only — **not in any release tag**) it always wrote
`WithRestartPolicyConfig(...)`, rewriting the deprecated `restartPolicy` string that a v0.7.0
install had stored. The two are semantically equal (the getters fold the string in and default
the delays to the same values), but the serialized form differs, so the RoleInstanceSet
revision hash moves on upgrade and the role rolls with nothing to roll to.

The PR gate:
```go
if backoff := role.GetRawRestartBackoff(); backoff != nil &&
    (backoff.BaseDelaySeconds != nil || backoff.MaxDelaySeconds != nil) {
    // delays can only live in the config
    roleInstanceTemplateConfig.WithRestartPolicyConfig(...)
} else {
    roleInstanceTemplateConfig.WithRestartPolicy(role.GetRestartPolicy()) // string
}
```
`GetRawRestartBackoff` (helper.go:346) reports what the role's pattern actually set, without
applying defaults, so a role that configured backoff is told apart from one that merely inherits it.

**Command.** `go test -run 'TestRoleInstanceSetReconciler_RestartPolicyTemplateRepresentation' -v ./pkg/reconciler/`
**Observed vs expected (fixed):** legacy-string case → `restartPolicy` set, `restartPolicyConfig` nil ✔;
config-type-only case → string ✔; backoff-delays case → config with the delays ✔.
**On revert (bite):** the two no-backoff cases FAIL (`restartPolicy string set=false, want true;
restartPolicyConfig set=true, want false`) — the exact rewrite the fix prevents.

### F2 — per-ordinal identity labels / DeepCopy (commit 1cf0b055)
**Mechanism.** `newVersionedInstance` did `Spec: setToUse.Spec.RoleInstanceTemplate.RoleInstanceSpec`
(struct assignment) — sharing the Components slice backing array and the pod-template label maps
with the set's template. Every instance built in one reconcile wrote its identity into the same
maps; the highest ordinal won (ordinal 0's pods came up labelled as ordinal 1). The PR deep-copies.
**Command.** `go test -run 'TestNewVersionedInstanceIdentityIsPerOrdinal' ./pkg/reconciler/roleinstanceset/statefulmode/`
**On revert:** ordinal 0's pod template `statefulset.kubernetes.io/pod-name` is `"test-set-1"`,
`role-instance-index` is `"1"` ✔ (the bug reproduced).

### F3 — in-place update re-injects identity (commit 1cf0b055)
**Mechanism.** `defaultPatchUpdateSpecToRoleInstance` does `instance.Spec = *newRoleInstanceSpec`
(replacing the whole spec with the set template, which carries no ordinal identity) and now calls
`InjectVersionedRoleInstanceSpec` → `InjectIdentityLabelsIntoComponents`, which copies the
identity off the instance's **own** labels back into every component's pod template. (Taking the
labels as an argument instead is how one ordinal's identity reached another's pods.)
**Command.** `go test -run 'TestPatchUpdateSpecKeepsIdentity' ./pkg/inplace/instance/inplaceupdate/`
**On revert:** pod-template `statefulset.kubernetes.io/pod-name` is `""` after the update ✔.

## Live run notes (L3)

Cluster: ACK, `KUBECONFIG=/root/.kube/config`. The in-cluster manager is
`rolebasedgroup/rbgs-controller:v0.8.0-0c00546d` — **older than the PR base a747e804** and
containing #394, i.e. the buggy code.

Against that manager, an LWP RoleInstanceSet role (replicas=2, no restart policy configured)
stored its template with `restartPolicy = {"type":"RecreateRoleInstanceOnPodRestart"}` (an
**object**) and `restartPolicyConfig = null`. The RoleInstance CRD requires `spec.restartPolicy`
to be a **string** (`enum=[None, RecreateRoleInstanceOnPodRestart]`), so RoleInstance creation
was rejected — `FailedCreate` events, **0 pods**, RBG stuck `Ready=False`. See
`results/f1-live-reproduction.txt`.

**Important caveats (stated plainly):**
1. The in-cluster build predates the PR base, so the exact serialized form it produced is not
   necessarily what the PR-base code produces, and this live symptom is **not** what the PR
   claims to fix (the PR's claim is revision-hash stability, proven at L1). Do not attribute
   the live creation failure to the PR.
2. Deploying the PR manager to verify the fix live was **not safe** on this shared cluster: a
   cluster-wide controller would also reconcile `default/nginx-cluster` (a 23-day-old, non-test
   RBG) and `pr433-test`, risking an unwanted rollout. Namespace-scoping would require editing
   `cmd/rbgs/main.go` (production), which is off-limits for the harness. So L3 reproduces the
   buggy area only; the **fix is proven at L1** (contract + bite check).

## Proposed fixes (NOT applied to production here)
The harness ships **no production changes**. The PR's own fixes are the proposed fixes; this
verification only proves them. Optional nits:
- **F4**: `GetRawRestartBackoff` could return a copy if future callers mutate; none do today.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr439-restartpolicy-identity` (production code untouched), so
it grafts onto whatever the fixed code is.

```bash
# 1. get the harness onto the fixed code
git fetch origin verify/pr439-restartpolicy-identity
git checkout <fixed-ref>
git checkout origin/verify/pr439-restartpolicy-identity -- docs/verification/pr439-restartpolicy-identity pkg/reconciler/roleinstanceset_reconciler_test.go

# 2. prove the fixes still hold (L1) — all contract tests must be GREEN
bash docs/verification/pr439-restartpolicy-identity/scripts/bite-check.sh

# 3. one-line re-verify (no sha — resolves the current PR head from the manifest)
bash docs/verification/pr439-restartpolicy-identity/scripts/re-verify.sh

# 4. L3 (needs a real cluster + $KUBECONFIG; read/create-only, auto-teardown)
KUBECONFIG=/path/to/kubeconfig bash docs/verification/pr439-restartpolicy-identity/scripts/live-scenario.sh
```

Polarity after a fix: all tests are contract, so **green = fixed**. If a test comes back RED on
the fixed code, the fix regressed — file it. (No canaries to invert.)

### Cross-machine kickoff prompt (copy-paste into a fresh agent)
> Resume the review-pipeline verification for https://github.com/sgl-project/rbg/pull/439.
> The harness is on branch `verify/pr439-restartpolicy-identity` under
> `docs/verification/pr439-restartpolicy-identity/`. Read its `README.md` and
> `verify-manifest.json`, then run `scripts/bite-check.sh` and `scripts/re-verify.sh` against the
> current PR head (resolved from the manifest's `pr` field — no sha needed). Report the per-finding
> table (Fixed / Still-broken / Partial). All tests are contract polarity: green = fixed. If live
> verification is requested, run `KUBECONFIG=... scripts/live-scenario.sh` (read/create-only,
> auto-teardown) — but do NOT deploy a cluster-wide manager on a shared cluster.
