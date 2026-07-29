# Verification — PR #414 `--enable-v1alpha1-compat`

Reviewer-private harness for [sgl-project/rbg#414](https://github.com/sgl-project/rbg/pull/414).

| | |
|---|---|
| PR | `feat: add --enable-v1alpha1-compat flag to toggle v1alpha1 API compatibility` (diw-zw) |
| Reviewed head | `66a2500a` |
| Base | `8a54787d` (`main`) |
| Diff | 14 files, +569 / −55 |
| Predecessor | PR #413, closed — **same head branch** `0727-lagacy`, flag renamed. See `verify/pr413-legacy-workloads`. |

**Production code on this branch is untouched.** The only additions are three
`pr414_*_verify_test.go` files and this `docs/verification/` tree.

## Verdict

**Do not merge as-is.** The chart this PR ships cannot be installed — with
default values, on any cluster. Upstream CI already says so (`e2e-test` →
*Deploy controller*), and it reproduces here both offline and against a live
API server. Two further blocking-class findings are behavioural: one legacy role
terminally stops an entire RoleBasedGroup, and `RoleBasedGroupSet` re-creates
the exact infinite-backoff-for-a-configuration-error that this PR was written to
eliminate.

The feature's *design* is sound and its RBAC gating is correct — `03-rbac-gating.sh`
proves the two modes differ exactly as documented, and the flag/chart default
mismatch from the #413 round is genuinely fixed. The problems are in the seams.

## Observed vs. expected

Polarity matters when reading pass/fail:
**CONTRACT** = asserts correct behaviour → *fail means broken*.
**CANARY** = asserts the current suspected-wrong behaviour → *pass means broken;
if it flips red, the bug is fixed and the assertion must be inverted.*

| ID | Finding | Sev | Pol | Layer | Expected | Observed | Verdict |
|---|---|---|---|---|---|---|---|
| **F1** | `clusterrole.yaml` renders with `apiVersion:` swallowed into a YAML comment → chart uninstallable | **blocking** | contract | script + live | valid ClusterRole | 5/5 value shapes parse `apiVersion=None`; live `helm install --dry-run=server` → `apiVersion not set` | **REPRODUCED** |
| F2 | chart RBAC drifted from `config/rbac/role.yaml` after `make manifests` stopped syncing it | none | contract | script | in sync | 209 == 209 triples, empty symmetric difference | **DISPROVED** |
| F3 | flag's own guard: legacy kinds refused, RoleInstanceSet still served | none | contract | unit | refuse | refuses all 3, allows RoleInstanceSet | green (regression guard) |
| **F4** | one legacy role terminally stops the **whole** RBG; healthy siblings never reconciled, nothing retries | **blocking** | canary | unit (live **void**) | per-role degradation | `stop=true, err=nil` → `Result{}`, no requeue; `Ready=False(LegacyWorkloadsDisabled)` for the whole group | **REPRODUCED** (unit) |
| F5 | `.status.roleStatuses` frozen at last-healthy values, never refreshed | non-blocking | canary | unit | invalidated or refreshed | `Ready=False(LegacyWorkloadsDisabled)` next to `3/3 ready`, forever | **REPRODUCED** (unit) |
| F6 | `deleteOrphanRoles` skips legacy cleanup when disabled | non-blocking | contract | unit | cleanup still runs | orphan survives with compat off; **control**: deleted with compat on | **REPRODUCED** (unit only) |
| **F7** | `RoleBasedGroupSet` has no compat awareness → children rejected by the RBG webhook, reconcile error-loops forever with no terminal condition | **blocking** | contract | unit | fail fast + terminal condition | 5 reconciles, 10 rejections, 0 children, 0 conditions, error every time | **REPRODUCED** |
| F8 | legacy-type list triplicated across 2 packages; one copy unreachable | non-blocking | contract | unit | one source of truth | all 3 agree *today* | green (drift guard) |
| F9 | `cacheOptions(false)` drops `ByObject` entries → unbounded informer that would now also 403 | non-blocking | canary | unit | keep the label selector | no entry for Deployment/StatefulSet with compat off | **LATENT** |
| F10 | `ValidateWorkloadTypesUpdate` grandfathers by role *name*, so StatefulSet→Deployment is accepted | non-blocking | canary | unit | monotonic migration only | all 3 legacy→legacy swaps accepted; **controls** both bite | **REPRODUCED** (unit) |
| D1 | Helm nil-deref / RBAC drop when upgrading with an older `values.yaml` (raised by Copilot) | none | — | script | — | 5 shapes incl. `features=null` render byte-identically; value coalescing restores defaults | **DISPROVED** |
| N1 | no CI gate renders the chart, so F1 could only surface in the 4-minute e2e job | note | — | — | — | lint/unit/envtest/build all green on `66a2500a` | observation |
| N2 | `e2e-test-manifest` failure is a pre-existing restart-policy flake | none | — | — | — | fails on `restart_policy_stability.go:515`; PR touches no restart-policy code | attributed elsewhere |
| N3 | sandbox cluster stores `roleInstanceTemplate.restartPolicy` as a bare string, unreadable by current Go types | none | — | — | — | breaks the PR binary's informer; `RestartPolicyConfig` identical on `main`, untouched by this PR | environmental — **not PR #414**; voided the live F4 arm |

## F1 in detail — the blocker

`deploy/helm/rbgs/templates/rbac/clusterrole.yaml` opens with a block of literal
YAML comments, then its first Helm action:

```gotemplate
# ...files that don't define controller.features.v1alpha1Compat still include
# the indirect workload RBAC permissions.
{{- $features := .Values.controller.features | default dict -}}
```

The `{{-` left-chomp eats the newline that terminates the last comment line, so
`apiVersion:` — emitted right after the matching `{{- end -}}` — lands *inside*
the comment:

```yaml
# the indirect workload RBAC permissions.apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
```

The document therefore has no `apiVersion`. Every install path fails:

```
$ helm install rbgs deploy/helm/rbgs --dry-run=server
Error: INSTALLATION FAILED: unable to build kubernetes objects from release
manifest: error validating "": error validating data: apiVersion not set
```

That is the same string upstream `e2e-test` reports. A sibling template from the
same chart and cluster (`service_account.yaml`) is accepted server-side, so the
rejection is attributable to this file — not to the chart, the cluster, or the
reviewer's kubeconfig.

Fixes, any one of: drop the `-` from the first `{{-`; move the variable block
above the comments; or leave a blank line the chomp can consume.

## Layers

| Layer | Needs cluster | What it runs |
|---|---|---|
| script | no | `01`–`04`: chart rendering, RBAC drift, gating control, `make manifests` idempotency |
| unit | no | `go test -run TestVerifyPR414` across `internal/controller/workloads`, `cmd/rbgs`, `api/workloads/v1alpha2` |
| live | yes | `20`: `helm install --dry-run=server` (non-destructive) — **F1 confirmed here**. `30`: PR binary with `--enable-v1alpha1-compat=false` against a real cluster — **void on this cluster, see below** |

### Why the results are trustworthy

Every negative claim here has a positive control, because "nothing happened" is
the easiest thing in the world to mistake for a finding:

- **F6** — the `compat=true` subtest deletes the orphan, so the failure with
  `compat=false` is the flag, not a broken fixture.
- **F7** — the admission stand-in is an interceptor; **F7b** pins its error text
  against the *real* `RoleBasedGroupValidator` and checks `compat=true` accepts
  the same object, so the stand-in cannot drift into fiction.
- **F9** — `Service` (not gated by the flag) must stay label-selected in both
  modes, or the test is not reading the structure it thinks it is.
- **F10** — a brand-new legacy role must still be rejected, and migration to
  RoleInstanceSet must still be allowed.
- **F1** — a sibling chart template must be accepted by the same live API server.
- **F2/D1** — `03-rbac-gating.sh` proves the harness can tell compat on from
  compat off at all, which is what makes "no drift" mean something.

### The live arm of F4 is VOID — and that is the honest answer

`30-live-mixed-rbg.sh` was run three times and **never produced a usable result**.
It is recorded as void: not a pass, not a fail.

The first run *looked* like a result and was worthless. The PR binary's
`RoleInstanceSet` informer never synced, so it reconciled nothing, while the
in-cluster controller — still finishing its shutdown — did all the observed work
with **compat=true**. That is why a legacy `Deployment` appeared and the
condition read `Ready=False(RoleNotReady)` instead of `LegacyWorkloadsDisabled`.
Had the guards not been added, this would have been written up as "F4 not
reproduced".

Runs two and three, after hardening, correctly aborted at **G4** instead:

```
G4 CONTROLLER VOID: our binary never logged a reconcile of the legacy-free
  control RBG within ~120s, so silence below would prove nothing.
  log lines: 15   top errors:
    8 "error":"failed to list *v1alpha2.RoleInstanceSet: json: cannot unmarshal
       string into Go struct field ...restartPolicy of type RestartPolicyConfig"
```

Root cause is **environmental, not a property of this PR** (see `N3`): the
in-cluster `rbgs` image predates this branch and writes
`spec.roleInstanceTemplate.restartPolicy` as a bare string; the
`roleinstancesets` CRD carries `x-kubernetes-preserve-unknown-fields`, so that
shape is stored verbatim; and the PR-head Go types — `RestartPolicyConfig`, a
struct, **identical on `upstream/main` and untouched by this PR** — cannot
unmarshal it. A plain `kubectl get` showed an empty collection while the informer
saw three items, because informers do their initial LIST at
`resourceVersion=0` (watch cache) rather than a quorum read.

Scaling the in-cluster Deployment to 0 (**G2**) turned out to be insufficient to
isolate two controller versions on one cluster. F4 therefore rests on the unit
layer, which asserts the mechanism directly. A future round that wants the live
arm should use a cluster with no prior `rbgs` state, or add a `G7` attributing
created objects via `metadata.managedFields[].manager`.

The six guards, each added for a specific way a run lied:

| Guard | Aborts when |
|---|---|
| G1 `TREE DIRTY` | the binary would not be the code under review |
| G2 `RIVAL RUNNING` | in-cluster controller pods remain (a compat=**true** rival) |
| G3 `FIXTURE INVALID` | the workload-type annotation did not persist |
| G4 `CONTROLLER VOID` | **our own process log** does not show it reconciling the control RBG |
| G5 `DIED EARLY` | the binary exited before observations were read |
| G6 `CACHE BROKEN` | any `Failed to watch` — absence then proves nothing |

G4 is the important one: the earlier version accepted an *API object* as proof of
liveness, which anything in the cluster could have created.

## Re-verify after a fix

No sha needed — it resolves the current head from `verify-manifest.json` and the
delta start from `.last-reviewed`:

```bash
bash docs/verification/pr414-v1alpha1-compat-flag/scripts/re-verify.sh
LIVE=1 bash docs/verification/pr414-v1alpha1-compat-flag/scripts/re-verify.sh   # + live layer
```

Note that PR #414's branch has been force-pushed before (#413 → #414 rewrote
history on the same branch), so `re-verify.sh` prints a rebase hint rather than
assuming `.last-reviewed` is an ancestor.

## Environment

- Sandbox: `root@43.99.38.217` — Linux 5.10, go 1.24.1, helm 3.16.3, 2 vCPU / 3 GB.
- Cluster: ACK cn-hongkong, k8s **v1.36.1-aliyun.1**, `rbgs` installed in
  `rbg-system` (2 replicas). No `LeaderWorkerSet` CRD, so the LWS arm of F3 is
  unit-only. No validating webhook registered in-cluster.
- `20-live-helm-install.sh` is entirely dry-run — safe on shared clusters.
  `30-live-mixed-rbg.sh` scales `rbgs-controller-manager` to 0 and **restores the
  original replica count in its EXIT trap**; it creates and deletes only
  namespace `pr414-verify`.
