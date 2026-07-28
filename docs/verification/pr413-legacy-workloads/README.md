# pr413-legacy-workloads — bug verification

Reproducible evidence for the findings raised while reviewing
[sgl-project/rbg#413](https://github.com/sgl-project/rbg/pull/413)
(*feat: add `--enable-legacy-workloads` flag to conditionally disable legacy workload management*).

**Code under review:** `59e384d5eb42e5365ac26f062834d3ad55be99b9` (PR head), base
`8a54787d`. Production code on this branch is **untouched** — the only additions are two
`*_verify_test.go` files, this directory, and the scripts under `scripts/`.

> The PR was force-pushed during the review. The first round examined `e5f4bd60`; the author
> then pushed `59e384d5`, which **fixed F2 and reworked F3**. Everything below is against
> `59e384d5`. See "What changed between the two heads".

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `getOrCreateWorkloadReconciler`, `preCheck`, `deleteOrphanRoles`, `cacheOptions` with a fake client | `go test ./internal/controller/workloads/ ./cmd/rbgs/ -run TestVerifyPR413 -v` |
| 2. Script | generated-artifact drift and flag-default consistency, as the repo's own `lint` job checks them | `bash scripts/01-check-flag-default.sh`; `bash scripts/02-check-manifest-freshness.sh` |
| 3. Live | the real binary against a real cluster, authenticated as the controller ServiceAccount so the reduced ClusterRole is genuinely enforced | `scripts/10-live-setup.sh` → `scripts/20-live-scenario.sh` → `scripts/90-live-teardown.sh` |

> **Test polarity.** *Contract* tests assert the intended behavior: RED on buggy code, GREEN
> once fixed. *Canary* tests assert the current (suspected-wrong) behavior: GREEN now, and they
> FLIP TO RED once fixed — at which point invert them. "All green" is **not** the same as
> "all fixed" while canaries are present.

## Summary of results

| ID | Claim | Layer | Polarity | Verdict | Evidence |
|----|-------|-------|----------|---------|----------|
| F1 | Go flag default (`false`) contradicts the documented/chart default (`true`) | 2 | contract | **Confirmed** | `results/f1-flag-default.txt` |
| F2 | `deploy/kubectl/manifests.yaml` stale → `make manifests` dirties the tree → `lint` fails | 2 | contract | **Fixed in `59e384d5`** | `results/f2-manifest-freshness.txt` |
| F3a | Legacy kinds must be refused when the flag is off | 1 | contract | **Fixed in `59e384d5`** (kept as regression guard) | `results/l1-unit.txt` |
| F3b | One legacy role fails the **whole** RBG; healthy RoleInstanceSet roles in the same group never reconcile | 1 + 3 | canary | **Confirmed (unit + live)** | `results/l1-unit.txt`, `results/l3-decide.txt` |
| F4 | The controller stops cleaning up orphaned legacy workloads when the flag is off | 1 | contract | **Confirmed at unit level; live INCONCLUSIVE** (see below) | `results/l1-unit.txt`, `results/l3-f4-*.txt` |
| F5 | `cacheOptions(false)` drops the group-name selector, leaving a latent unfiltered cluster-wide informer | 1 | canary | **Latent** (no reachable read path on this head) | `results/l1-unit.txt` |
| D1 | Helm guard asymmetry between `manager.yaml` and `clusterrole.yaml` | 2 | — | **Disproved** | see below |

Also verified positively: the flag really does shrink RBAC. On the live cluster the ClusterRole
goes from **18 rules to 13**, and `kubectl auth can-i` as `rbgs-controller-sa` returns `no` for
`list`/`watch`/`create` on `deployments`, `statefulsets` and `leaderworkersets` while
`roleinstancesets` stays `yes` (`results/l3-setup-legacy-false.txt`).

## Per-finding detail

### F1 — flag default disagrees with every document describing it (Confirmed)

`cmd/rbgs/main.go:213` registers the flag with default `false`; `values.yaml`
(`legacyWorkloads.enabled: true`), the chart README and the PR body all say `true`.
Go's `flag` package does not print `(default false)` for a false bool, so `--help` does not
reveal it either:

```
  cmd/rbgs/main.go default: false
  values.yaml legacyWorkloads.enabled: true
```

Both *shipped* install paths now pass the flag explicitly, so they are unaffected. The exposure
is any other consumer — a downstream chart, a hand-written manifest, or running the binary
directly — which silently loses legacy workload reconciliation. A default of `true` makes the
safe behavior independent of every caller remembering the flag.

### F2 — generated installer was stale (Fixed in `59e384d5`)

On `e5f4bd60`, `config/manager/manager.yaml` gained `--enable-legacy-workloads=true` but
`deploy/kubectl/manifests.yaml` was never regenerated, so the committed installer ran the
controller with **no flag at all** — which, combined with F1, silently disabled legacy
workloads for every `kubectl apply` install while still granting the legacy RBAC. This was the
actual cause of the red `lint` job (`CRD validation failed. Please use 'make update-crd'`).
On `59e384d5` the arg is present at `deploy/kubectl/manifests.yaml:98695` and `make manifests`
leaves the tree clean. `scripts/02-check-manifest-freshness.sh` is retained as a regression
guard.

### F3a — legacy kinds are now refused (Fixed in `59e384d5`)

The author added an explicit reject in `getOrCreateWorkloadReconciler`. Verified that it fires
for all three legacy types and that `RoleInstanceSet` still works. Worth noting the guard
compares `workloadSpec.String()` (`"<apiVersion>/<kind>"`) against
`constants.DeploymentWorkloadType` etc.; those formats do match, so the guard is effective —
this was checked explicitly because a mismatch would have made it silently dead code.

### F3b — the refusal is whole-group, not per-role (Confirmed, canary)

`preCheck` (`rolebasedgroup_controller.go:316-330`) loops over roles, collects the rejection
into a `kerrors.NewAggregate`, and returns it. `Reconcile` calls `preCheck` first
(`:183-185`) and returns on error, so for an RBG that mixes one legacy role with healthy
RoleInstanceSet roles:

```
invalid role workload declarations: role legacy: legacy workload type apps/v1/Deployment
is not supported when legacy workloads are disabled
```

…nothing downstream runs — not `constructAndUpdateRoleStatuses` (`:210`), not `reconcileRoles`
(`:235`), and therefore not `deleteOrphanRoles` (`:573`). The healthy roles are collateral
damage and the RBG requeues with backoff forever, with no path to converge other than editing
the RBG or re-enabling the flag.

Who actually has such RBGs matters here: `RoleWorkloadTypeAnnotationKey` is documented as
"primarily used by the conversion webhook when converting v1alpha1 RoleBasedGroups that had
workload field set. New v1alpha2 RBGs should not use this annotation." So the population that
hits F3b is precisely the **converted-from-v1alpha1 backward-compatibility users** this flag
exists to serve.

Recorded as a *canary*: it passes today, documenting the behavior. If the author moves to
per-role degradation (or rejects legacy roles at admission with a clear message), it flips red
and should be inverted.

### F4 — the controller stops cleaning up orphaned legacy workloads (Confirmed at unit level; live inconclusive)

`deleteOrphanRoles` (`:697-720`) skips `CleanupOrphanedWorkloads` for Deployment/StatefulSet/
LWS when the flag is off. That function (`pkg/reconciler/deploy_reconciler.go:253-290`) is what
deletes RBG-owned workloads that no longer match any role, and nothing else covers it —
`RoleInstanceSetReconciler.CleanupOrphanedWorkloads` delegates to `CleanupOrphanedObjs` with the
RoleInstanceSet GVK only (`pkg/reconciler/roleinstanceset_reconciler.go:499-506`). So after the
intended migration (flip a role from `apps/v1/Deployment` to RoleInstanceSet, then disable
legacy) the controller itself no longer removes the old Deployment.

Unit layer proves the code path, with a **bidirectional control**: same fixture, flag on → the
orphan is deleted (`delete deploy deploy=mig-oldrole`); flag off → it survives.

**Live layer could not confirm it, and this lowers the severity.** On the ACK test cluster the
orphaned Deployment disappears within ~10s in *both* modes, with `0` `delete deploy` log lines
and `0` Forbidden — i.e. not by the controller:

| run | flag | orphan after 60s | controller delete attempts |
|-----|------|------------------|----------------------------|
| `30-live-f4-migration.sh MODE=disabled` | false | deleted | 0 |
| `30-live-f4-migration.sh MODE=enabled` (control) | true | deleted | 0 |
| `gccontrol.sh` — **no controller running at all** | n/a | deleted in <10s | n/a |

The third row is the decisive one: with no controller running, Kubernetes garbage collection
removes the RBG-owned Deployment anyway. So the live test cannot discriminate, and the stronger
claim that the workload leaks indefinitely and keeps holding GPUs is **not supported by
evidence on this cluster** — GC appears to cover it. What is proven is narrower: the controller
no longer performs its own cleanup of legacy workloads when the flag is off. Whether that leaves
a user-visible leak depends on GC behavior for the specific object graph, and should be
confirmed before the finding is presented as a leak.

One further constraint on any fix: with the reduced ClusterRole the ServiceAccount has no
`delete deployments` permission at all (`kubectl auth can-i delete deployments` → `no`), so
simply un-gating `deleteOrphanRoles` would trade a silent skip for a permanent `Forbidden`.
Cleanup and the delete permissions have to be kept together.

### F5 — dropped cache selector is a latent footgun (Latent)

`cacheOptions(false)` removes the `ByObject` entries for Deployment/StatefulSet
(`cmd/rbgs/main.go:637-648`). Those entries carried the group-name label selector; removing the
entry does not prevent an informer, it makes any future one **unfiltered and cluster-wide**,
which would need exactly the `list`/`watch` RBAC the chart removes. On this head the F3a reject
removes the only reachable cached read path, so this is latent rather than live. Keeping the
selector (or setting `DefaultLabelSelector`) regardless of the flag costs nothing.

### D1 — Helm guard asymmetry (Disproved)

`manager.yaml:51-52` defensively uses `default dict` / `hasKey` while `clusterrole.yaml:86`
dereferences `.Values.controller.features.legacyWorkloads.enabled` directly. Suspected either a
nil-pointer render error or a flag/RBAC mismatch. Rendered four value shapes with helm 3.16.3 —
default, `enabled=false`, `legacyWorkloads={}`, `features={}`. Helm's value coalescing restores
the chart defaults, and all three non-`false` shapes render **byte-identical** to default. Not
a bug; reporting it would have been wrong.

## Live run notes

- **Cluster:** ACK, cn-hongkong, Kubernetes v1.36.1, 3 nodes; release `rbgs` in `rbg-system`,
  zero pre-existing RoleBasedGroup objects, so the run was non-disruptive.
- **Why out-of-cluster:** the chart image (`v0.8.0-69fe55d`) does not contain the PR's code and
  the sandbox cannot push to a registry the cluster pulls from. `main.go:325` uses
  `ctrl.GetConfigOrDie()`, which honors `KUBECONFIG`, so the PR binary runs out-of-cluster with
  `--enable-webhooks=none`. It authenticates with a short-lived `rbgs-controller-sa` token
  (verified: `kubectl auth whoami` → `system:serviceaccount:rbg-system:rbgs-controller-sa`) so
  the reduced ClusterRole is genuinely enforced; an admin kubeconfig would have bypassed the very
  thing under test. The in-cluster controller is scaled to 0 for the duration.
- **Verified positively:** the flag does shrink RBAC. 18 → 13 rules, and as the SA,
  `list`/`watch`/`create` on `deployments`, `statefulsets` and `leaderworkersets` all return
  `no` while `roleinstancesets` stays `yes` (`results/l3-setup-legacy-false.txt`).
- **F3b live confirmation** (`results/l3-decide.txt`): an RBG with one `apps/v1/Deployment` role
  and one RoleInstanceSet role, flag off, observed for 60s — **no Deployment and no
  RoleInstanceSet were created at all**; the log shows 28 rejections, 14 `Failed to create
  workload reconciler` and 14 `Reconciler error`. The healthy `modern` role is starved exactly as
  the unit canary predicts. No `Forbidden`, confirming F3a's reject prevents the write attempt.
- **Four earlier live runs were void.** Recording them because each is an easy trap to repeat:
  1. **Wrong key prefix.** Fixtures used the v1alpha1 `rolebasedgroup.workloads.x-k8s.io/` prefix
     instead of v1alpha2's `rbg.workloads.x-k8s.io/` (`constants.RBGPrefix`,
     `api/workloads/constants/constants.go:26`). The legacy role silently defaulted to
     RoleInstanceSet, so the run proved nothing while looking like a clean pass.
     `20-live-scenario.sh` now asserts the stored annotation and aborts with `FIXTURE INVALID`.
  2. **Expired token.** The SA token was minted with `--duration=3600s` and expired mid-run;
     every list failed `Unauthorized` and the manager never reconciled, so nothing happened
     looked like a result. Now 8h, plus a liveness gate that aborts with `CONTROLLER VOID`
     unless the controller demonstrably reconciles a legacy-free RBG first.
  3. **Dirty tree.** A harness-bites patch was applied to the working tree while the scenario was
     building the binary from it, so the binary under test was not the code under review. The
     script now refuses to run on a dirty tree (`TREE DIRTY`) and logs the binary's sha256.
  4. **Leftover controller.** A controller process from a prior run was still reconciling and
     produced results attributed to the run under observation. Confirmed by re-checking with no
     controller running: applying a legacy RBG then creates nothing at all.
- **Stale CRDs.** The cluster's CRDs predated the PR's Go types (`restartPolicy` string vs
  `RestartPolicyConfig`), breaking the RoleInstanceSet informer with
  `json: cannot unmarshal string into Go struct field`. Fixed by applying the PR's
  `config/crd/bases/` server-side. **The sandbox cluster's CRDs are now the PR's version, newer
  than release 0.8.0-alpha.1; teardown does not revert them.**
- L1 verdicts are deterministic and machine-independent. L3 depends on this cluster — notably its
  GC behavior, which is what made F4 unverifiable live.
- The LWS CRD is **not** installed there, so the LeaderWorkerSet arm of F3a is unit-only.

## Proposed fixes (NOT applied here)

- **F1**: register the flag with `true`, matching `values.yaml` and the docs.
- **F3b**: degrade per role instead of failing the group — skip the legacy role (Warning event +
  a role status condition) and keep reconciling the rest; or reject legacy workload types at
  admission so the RBG never reaches a wedged state. Either way an operator flipping the flag
  should not lose control of unrelated roles.
- **F4**: always run `CleanupOrphanedWorkloads` for legacy types, gating only *creation* and
  *watches* on the flag — and keep `delete` on deployments/statefulsets/leaderworkersets in the
  reduced ClusterRole, otherwise the un-gated cleanup just fails `Forbidden` forever. Confirm the
  user-visible impact first: on the test cluster GC removed the orphan regardless.
- **F5**: keep the group-name selector for Deployment/StatefulSet regardless of the flag, or set
  `cache.Options.DefaultLabelSelector`.
- **Tests**: the disabled path has no coverage — `test/envtest/testutil/setup.go:205` hardcodes
  `true`. Worth a `cacheOptions(false)` unit test, a controller test for flag-off + a
  legacy-annotated RBG, Helm assertions for both ClusterRole variants, and an e2e for the
  enabled→disabled transition with pre-existing legacy workloads.
- **Docs**: `deploy/helm/rbgs/README.md` documents every other `controller.features.*` key but
  not `legacyWorkloads.enabled`.

## What changed between the two heads

`e5f4bd60` → `59e384d5` touched two files:
- `deploy/kubectl/manifests.yaml`: regenerated, +1 line (`--enable-legacy-workloads=true`) → F2 fixed.
- `internal/controller/workloads/rolebasedgroup_controller.go`: replaced the awkward
  `r.enableLegacyWorkloads || workloadSpec.Kind != lws` dynamic-watch guard with an explicit
  reject of legacy workload types → F3a fixed, F3b introduced/exposed, F5 rendered latent.

F1 and F4 were untouched by the force-push.

## Continuing after the fix (possibly on another machine)

Durable state lives entirely on branch `verify/pr413-legacy-workloads` (pushed to the
reviewer's fork): the harness, `verify-manifest.json`, `.last-reviewed`, and this table.
Machine-local things do not travel: the kubeconfig, envtest assets, and the sandbox checkout.

```bash
git fetch origin verify/pr413-legacy-workloads
git checkout origin/verify/pr413-legacy-workloads -- docs/verification/pr413-legacy-workloads
bash docs/verification/pr413-legacy-workloads/scripts/re-verify.sh
```

`re-verify.sh` takes no sha: it resolves the current PR head from `verify-manifest.json`'s `pr`
field and the review delta from `.last-reviewed`. It runs L1 + L2 and prints
**Fixed / Still-broken / Partial / Harness-update** per finding, treating a canary as fixed only
when it flips. Requires `git`, `go`, `jq`.

Read the results through the polarity table:

| Test | Now | When fixed |
|------|-----|------------|
| `F3a_LegacyKindsRejectedWhenDisabled` (contract) | PASS | PASS — regression guard |
| `F3b_OneLegacyRoleFailsEntireGroup_Canary` (canary) | PASS | **FLIPS RED** → invert it |
| `F4_OrphanedLegacyWorkloadLeaksWhenDisabled` (contract) | FAIL | PASS |
| `F5_CacheSelectorDropped..._Canary` (canary) | PASS | **FLIPS RED** → invert it |
| `01-check-flag-default.sh` (contract) | exit 1 | exit 0 |
| `02-check-manifest-freshness.sh` (contract) | exit 0 | exit 0 — keep as guard |

For L3, export a real `KUBECONFIG` and run `10-live-setup.sh` → `20-live-scenario.sh` →
`90-live-teardown.sh`. Always run teardown: it restores the baseline ClusterRole and the
controller replica count. Watch for `FIXTURE INVALID` — that means the fixture, not the PR, is
wrong.

### Kickoff prompt for a fresh agent

```text
Continue a verification task on branch verify/pr413-legacy-workloads (remote: the reviewer's
fork of sgl-project/rbg). Background: reviewing PR sgl-project/rbg#413 produced findings
F1-F5; a layered harness reproduced F1, F3b and F4, showed F2/F3a fixed by a force-push, and
disproved one hypothesis (D1). Read docs/verification/pr413-legacy-workloads/README.md,
section "Continuing after the fix", and follow it: run scripts/re-verify.sh (it discovers the
current PR head itself), mind the polarity table (F3b and F5 are canaries — they are FIXED when
they flip red, and must then be inverted), and re-review the .last-reviewed..head delta for new
findings. If a real cluster is available, run the L3 scripts and always finish with
90-live-teardown.sh. Report an observed-vs-expected table, then advance .last-reviewed and push.
Scope and clean up any test resources; no cluster-wide destructive actions.
```
