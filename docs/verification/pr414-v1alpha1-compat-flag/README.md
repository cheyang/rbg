# Verification — PR #414 `--enable-v1alpha1-compat`

Reviewer-private harness for [sgl-project/rbg#414](https://github.com/sgl-project/rbg/pull/414).

| | |
|---|---|
| PR | `feat: add --enable-v1alpha1-compat flag to toggle v1alpha1 API compatibility` (diw-zw) |
| Reviewed head | `0151936a` (round 2; round 1 reviewed `66a2500a`) |
| Base | `8a54787d` (`main`) |
| Diff | 15 files, +594 / −66 |
| Predecessor | PR #413, closed — **same head branch** `0727-lagacy`, flag renamed. See `verify/pr413-legacy-workloads`. |

**Production code on this branch is untouched.** The only additions are three
`pr414_*_verify_test.go` files and this `docs/verification/` tree.

## Verdict — round 2 (`0151936a`)

**F1 is fixed. The two behavioural blockers are untouched.**

`0151936a` repairs the Helm template, and that is now confirmed three ways:
5/5 value shapes render a valid ClusterRole offline, all 12 documents in the
chart render valid under all 5 shapes, and a live k8s v1.36.1 API server accepts
both the ClusterRole and the whole release server-side. Upstream `e2e-test` went
from failing at *Deploy controller* to **passing in 29m44s** — the suite ran to
completion on this PR for the first time.

Nothing else changed. **No canary flipped**, so every behavioural finding stands
exactly as in round 1:

- **F4** — one legacy role still terminally stops the whole RoleBasedGroup
  (`stop=true, err=nil` → no requeue, no backoff); healthy `RoleInstanceSet`
  siblings are still abandoned.
- **F7** — `RoleBasedGroupSet` still has no compat awareness and no validating
  webhook, so it still error-loops forever with no terminal condition: the same
  problem this PR fixes one level down.

The feature's *design* remains sound and its RBAC gating is correct
(`03-rbac-gating.sh` proves the two modes differ exactly as documented). The
problems are in the seams.

### Where F1 came from

Worth knowing, because it changes what to ask for. CI on this branch:

| head | e2e-test |
|---|---|
| `59e384d5` (PR #413 head) | pass |
| `62e64713` | pass |
| `66a2500a` — *"Deal with copilot comments"* | **fail** at *Deploy controller* |
| `0151936a` — *"deal with e2e test"* | pass (29m44s) |

The chart breakage was introduced by the commit that answered Copilot's
comments — specifically the nil-safety comment block added above the variable
block, for the concern recorded here as `D1`, which this harness disproved in
both the #413 and #414 rounds. A correct rebuttal existed; acting on the comment
anyway cost a total outage of the install path. Nothing else in that commit is
at fault, and the author's own rebuttal on the `EnableV1Alpha1Compat` zero-value
comment was right.

`N1` is the systemic half of this: `lint`, `unit-test`, `envtest` and `build`
were all green while the chart was uninstallable. Only the 30-minute e2e job
noticed. `scripts/05-chart-render-all.sh` is written to be lifted into CI as-is.

## Observed vs. expected

Polarity matters when reading pass/fail:
**CONTRACT** = asserts correct behaviour → *fail means broken*.
**CANARY** = asserts the current suspected-wrong behaviour → *pass means broken;
if it flips red, the bug is fixed and the assertion must be inverted.*

Round-2 column: what the same test says on `0151936a`.

| ID | Finding | Sev | Pol | Layer | Round 1 (`66a2500a`) | Round 2 (`0151936a`) |
|---|---|---|---|---|---|---|
| **F1** | `clusterrole.yaml` renders with `apiVersion:` swallowed into a YAML comment → chart uninstallable | **blocking** | contract | script + live | **REPRODUCED** — 5/5 shapes parse `apiVersion=None`; live rejected | ✅ **FIXED** — 5/5 shapes valid; live accepts ClusterRole *and* whole release |
| F1b | *(new)* same defect class anywhere else in the chart | — | contract | script | not tested | ✅ clean — 12 docs × 5 shapes all valid |
| **F11** | *(new)* `warmup.go` e2e change is unnecessary for CI and unrelated to this PR | minor | — | review | — | see below |
| F2 | chart RBAC drifted from `config/rbac/role.yaml` after `make manifests` stopped syncing it | none | contract | script | **DISPROVED** — 209 == 209 triples | unchanged — still 209 == 209 |
| F3 | flag's own guard: legacy kinds refused, RoleInstanceSet still served | none | contract | unit | green | green |
| **F4** | one legacy role terminally stops the **whole** RBG; healthy siblings never reconciled, nothing retries | **blocking** | canary | unit (live **void**) | **REPRODUCED** — `stop=true, err=nil` → no requeue | ⚠️ **canary did not flip — still broken** |
| F5 | `.status.roleStatuses` frozen at last-healthy values, never refreshed | non-blocking | canary | unit | **REPRODUCED** — `Ready=False` next to `3/3 ready` | ⚠️ canary did not flip — still broken |
| F6 | `deleteOrphanRoles` skips legacy cleanup when disabled | non-blocking | contract | unit | **REPRODUCED**; control (compat on) deletes it | ⚠️ still FAIL, control still passes |
| **F7** | `RoleBasedGroupSet` has no compat awareness → children rejected by the RBG webhook, reconcile error-loops forever with no terminal condition | **blocking** | contract | unit | **REPRODUCED** — 5 reconciles, 10 rejections, 0 children, 0 conditions | ⚠️ **still FAIL — unchanged** |
| F8 | legacy-type list triplicated across 2 packages; one copy unreachable | non-blocking | contract | unit | green (all 3 agree) | green |
| F9 | `cacheOptions(false)` drops `ByObject` entries → unbounded informer that would now also 403 | non-blocking | canary | unit | **LATENT** | unchanged — still latent |
| F10 | `ValidateWorkloadTypesUpdate` grandfathers by role *name*, so StatefulSet→Deployment is accepted | non-blocking | canary | unit | **REPRODUCED** — all 3 swaps accepted | ⚠️ canary did not flip — still broken |
| D1 | Helm nil-deref / RBAC drop when upgrading with an older `values.yaml` (raised by Copilot) | none | — | script | **DISPROVED** — 5 shapes render byte-identically | still disproved — **and acting on it caused F1** |
| N1 | no CI gate renders the chart, so F1 could only surface in the 30-minute e2e job | note | — | — | observation | still open; `05-chart-render-all.sh` is a liftable gate |
| N2 | `e2e-test-manifest` failure is a pre-existing restart-policy flake | none | — | — | fails at `restart_policy_stability.go:515` | still failing, **different spec** in the same family (`:355`), 70/106 specs ran |
| N3 | sandbox cluster stores `roleInstanceTemplate.restartPolicy` as a bare string, unreadable by current Go types | none | — | — | environmental — **not PR #414** | unchanged — still voids the live F4 arm (`exit 4`) |

## F11 — the `warmup.go` change does not belong in this PR

`0151936a` also rewrites `test/e2e/testcase/v1alpha2/warmup.go`, generalizing the
*"merge multi-role actions"* spec from "1 warmup Pod with 2 containers" to
"1 Pod per unique node, total containers == number of roles".

The generalization itself is reasonable. Two things make it worth raising:

1. **It is not needed for CI.** Both e2e jobs create their cluster with
   `kind create cluster` and **no `--config`** (`.github/workflows/e2e-test.yml:112`
   and `:297`), i.e. a single-node cluster. Both RBG pods therefore always
   co-locate, `uniqueNodeCount` is always 1, and the new assertions reduce to
   exactly the old ones. CI history confirms the old spec was not the problem:
   `62e64713` passed `e2e-test` with the old assertions, and the only failure on
   `66a2500a` was at *Deploy controller*, before any spec ran.
2. **It is unrelated to `--enable-v1alpha1-compat`.** Bundled into a feature PR it
   is harder to review and cannot be backported on its own.

If the intent is to support multi-node dev clusters, then on such a cluster the
spec's *stated purpose* — the merge path — becomes nondeterministic and can go
unexercised while the test still passes. In that case pin both roles to one node
(`nodeSelector`/`podAffinity`) so the merge stays covered, and add a separate spec
for the spread case. Either way this belongs in its own PR.

## F1 in detail — the blocker (fixed in `0151936a`)

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

**`0151936a` took the second option** — the variable block moved above `---`, and
the `-}}` right-chomps on the `if`/`end` became plain `}}`. Verified fixed:

```
$ helm template rbgs deploy/helm/rbgs --show-only templates/rbac/clusterrole.yaml | head -2
---
apiVersion: rbac.authorization.k8s.io/v1          # survives, all 5 shapes

$ helm upgrade rbgs deploy/helm/rbgs -n rbg-system --dry-run=server
   ACCEPTED (whole release renders and validates)
```

## Harness corrections made in round 2

Recorded because two of them would have produced wrong review feedback.

| # | Problem | Consequence if unfixed |
|---|---|---|
| H1 | `20-live-helm-install.sh` reported **F1 REPRODUCED** on any failure, whatever the cause | **False positive.** After the fix it still failed — because the throwaway release name `rbgs-f1-dryrun` collided with the ownership annotations of the real `rbgs` release. It would have told the author F1 was still broken. Now the finding is **signature-gated** on `apiVersion not set`; anything else is `INCONCLUSIVE` (exit 2), never a finding. Arm B also reuses the existing release name so ownership cannot collide. |
| H2 | `re-verify.sh` compared `HEAD` to the PR head for **equality** | Printed "rebase before trusting these results" *immediately after a correct rebase* — the harness commit sits **on top of** the PR head. Trains the reader to ignore the one warning that matters. Now tests **ancestry**, reports `PR head + N harness commit(s)`, and additionally warns if a harness commit touches code under review. |
| H3 | F1 was only checked on one template | Added `05-chart-render-all.sh`: every template, 5 value shapes, asserting `apiVersion`+`kind` on every document, plus a direct grep for the glued-into-comment signature. Doubles as the CI gate `N1` recommends. |

Two process notes from this round, both self-inflicted and worth not repeating:
overwriting a script while a detached job was executing it corrupted that job
mid-read (`line 95: ating.txt: command not found`), and `git checkout -- .` (used
to undo `make manifests` worktree mutations) silently reverted uncommitted script
fixes, so an "already fixed" script ran in its old form. Kill the job before
editing its scripts; commit harness fixes before running anything that cleans the
tree.

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
