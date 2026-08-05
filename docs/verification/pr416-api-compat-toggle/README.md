# Verification — PR #416 (`feat: restrict v1alpha1 API compatibility RBAC via Helm toggle`)

Reviewer-private verification harness for
<https://github.com/sgl-project/rbg/pull/416>.

| | |
|---|---|
| Reviewed head | `dcc7104aef6f6e8c16cf4342bb25baf9426c68ab` |
| Previous round head | `a69ceada83126c3a7dbd1ab9e1578b3e10dc6ed4` |
| Base | `8a54787d698f76f3f754e844342d5dbd71244885` |
| Author | @diw-zw, head branch `0730-api-compat` |
| Round | **2** (fourth round of the #413 → #414 → #416 lineage) |
| Delta reviewed | 3 commits, 16 files, +728 / −133 |

**Production code on this branch is untouched.** The branch merges the PR head so the
harness compiles natively; the only additions are the `pr416_*_test.go` harness files and
this `docs/verification/` tree.

---

## What the delta did

| Commit | What it changes |
|---|---|
| `38ca9b0e` | Renames the flag to `--enable-deprecated-workload-types` (default **true**) and narrows UPDATE validation to a *delta* check — a role that already carries a deprecated type stays writable |
| `f0b4e8fc` | Adds `hack/gen-helm-rbac` so the chart ClusterRole is generated from `config/rbac/role.yaml` instead of hand-synced |
| `dcc7104a` | Always renders the flag, using `hasKey` so an explicit `false` is not resurrected by sprig's falsy-is-empty rule |

The framing flipped from "disable v1alpha1 compatibility" to "enable deprecated workload
types". That is a genuine improvement: the toggle now names what it actually controls
(three workload kinds), not an API version.

---

## Round-1 findings, re-verified against this head

| ID | Sev (r1) | Claim | Round-2 verdict | Evidence |
|----|---------|-------|-----------------|----------|
| **F1** | blocker | Shipped Helm default is the opposite of the documented default | **FIXED** | [`f1-r2.txt`](results/round2/f1-r2.txt) |
| **F2a** | blocker | Pre-existing legacy RBG can never be reconciled again | **FIXED** | [`unit-r2.txt`](results/round2/unit-r2.txt) |
| F2b | — | Harness-bites control for F2a | Premise inverted → retargeted | ditto |
| **F2c** | major | Existing legacy RBG cannot even be scaled | **FIXED** | ditto |
| **F3** | major | Reconcile path has no compat gate; legacy reconcilers still built, LWS watch re-arms | **STILL BROKEN** | ditto |
| **F4** | major | Upgrade guard hard-fails the command the repo documents in 10 places | **STILL BROKEN** | [`f4-r2.txt`](results/round2/f4-r2.txt) |
| F5 | non-blocking | `make manifests` no longer syncs the chart ClusterRole | **FIXED** — generated + CI-gated | see “F5 is properly fixed” |
| F5b | — | Generated artefacts in sync | **Green** (clean tree) | ditto |
| **F6** | major | Partial value block hard-fails the render or silently strips RBAC | **PARTIAL** → R2-F11/F12 | [`f11-flag-rbac.txt`](results/round2/f11-flag-rbac.txt) |
| F7 | — | Regression guard: #414's render blocker does not recur | **Green**, now non-vacuous | [`l3-r2.txt`](results/round2/l3-r2.txt) |
| **F8** | blocker | Disabling makes the *entire* v1alpha1 API unusable | **STILL BROKEN — but now acknowledged** | [`unit-r2.txt`](results/round2/unit-r2.txt) |
| **F8b** | blocker | Same defaulting chain rejects every v1alpha1 RBGSet | **STILL BROKEN** | ditto |
| **F9** | blocker | RBGSet child-sync denied by the UPDATE rule | **PARTIAL** → R2-F13 | ditto |
| **F10** | blocker | HPA-driven scale of a legacy RBG denied | **FIXED** | ditto |
| **P1** | major | LWS → RoleInstanceSet silently renames the pod env contract | **STILL BROKEN** (untouched by the delta) | ditto |
| N1 | note | No CI job renders or double-installs the chart | Partly addressed — drift is now CI-gated, render/upgrade still is not | — |
| N2 | note | `deleteOrphanRoles` skips legacy cleanup when disabled | Unchanged (rename only) | — |

**Net movement: 3 of 6 blockers fixed (F1, F2a, F10), 1 major fixed (F2c), 1 non-blocking
fixed (F5). One blocker is now partial (F9). Two blockers (F8, F8b) and three majors
(F3, F4, P1) are unchanged.**

### What the delta genuinely got right

* **F1 is fixed with a working control.** Shipped default (`true`), README value table
  (`true`), README prose, `NOTES.txt` and the Go flag default now all agree, and the
  toggle is *effective*: `=false` removes all 8 deprecated-workload RBAC lines while the
  `=true` control keeps them. Three rounds of documented-vs-shipped disagreement end here.
* **Grandfathering works, and is not too loose.** `validateNoNewDeprecatedWorkloadTypes`
  keys the exemption by role name and requires the type to be *unchanged*. Pinned by
  regression guards: a no-op update and a scale of a pre-existing deprecated role are
  accepted (F2a/F2c/F10), while a newly-added deprecated role and a
  deprecated→deprecated swap are still rejected. Each denial has an ENABLED control.
* **F5 is properly fixed, not just relocated.** `go run ./hack/gen-helm-rbac` sits inside
  the `manifests` recipe, and `.github/workflows/project-check.yml:43-46` runs
  `make manifests` then fails on a non-empty `git status --porcelain`. A hand-edit to the
  chart ClusterRole is reverted by the generator and a committed one fails CI. Output is
  deterministic across 8 runs, and the committed file equals the generated one.
* **The error messages are genuinely good.** Both hints name the exact Helm value, the
  exact controller flag, and the v1alpha1-defaulting trap that makes a role carry a
  deprecated type the user never wrote.

---

## New round-2 findings

| ID | Severity | Claim | Verdict | Evidence |
|----|----------|-------|---------|----------|
| **R2-F13** | **blocker** | The RBGSet's own update is grandfathered, but the child RBG it must **CREATE** is not — a pre-existing RBGSet can never scale up or replace a deleted child | **Reproduced** | [`unit-r2.txt`](results/round2/unit-r2.txt) |
| **R2-F11** | major | A *string* `"false"` renders flag `false` but leaves the deprecated RBAC in place — the privilege reduction the value advertises silently does not happen | **Reproduced** | [`f11-flag-rbac.txt`](results/round2/f11-flag-rbac.txt) |
| **R2-F12** | major | `enabled: ""` / `{}` / `[]` strips the RBAC *and* renders a flag the manager cannot parse → CrashLoopBackOff, with no render-time warning | **Reproduced** | ditto |
| **R2-F14** | major | The generator's deprecated-resource list is a *second* hand-maintained copy; a resource missing from it is emitted **ungated**, i.e. it fails open (over-grant) | **Reproduced** | [`gen-r2.txt`](results/round2/gen-r2.txt) |
| R2-F15 | major (latent) | A `nonResourceURLs`-only rule is silently dropped from the chart, exit 0 | Reproduced (not reachable at this head) | ditto |
| R2-F16 | major (latent) | A multi-document `role.yaml` silently loses every document after the first | Reproduced (not reachable at this head) | ditto |
| R2-F17 | non-blocking | Non-strict unmarshal: `aggregationRule` is parsed then dropped, a misspelled key vanishes, `kind` is never checked | Reproduced | ditto |
| R2-F18 | non-blocking | No test for the generator, though its functions are already pure | Confirmed | ditto |
| R2-F19 | non-blocking | The README value-table row says the webhook rejects roles that *use* a deprecated type, contradicting the prose three lines below it (and the whole point of the delta) | Confirmed | — |
| R2-F20 | non-blocking | `rbg rollout undo` restores roles from a ControllerRevision, so rolling back across a workload-type change or a role rename is refused | Confirmed | — |
| R2-F21 | non-blocking | The PR description still describes the round-1 design (`compatibility.v1alpha1.enabled`, "default: `false`", `--disable-v1alpha1-compatibility`, and a whole-object UPDATE rejection) — the third of the three documents round 1 flagged; the other two were fixed | Confirmed | [`doc-consistency.txt`](results/round2/doc-consistency.txt) |
| R2-N3 | note | The exemption is keyed by role **name**, so renaming a pre-existing deprecated role reads as "newly added" and is rejected — but **not reachable as a controller operation** (a template rename is refused at the parent) | Documented, not asserted as a defect | [`unit-r2.txt`](results/round2/unit-r2.txt) |

### Every operator write, and which one is still denied

An exhaustive inventory of writes to `rolebasedgroups` / `rolebasedgroupsets` —
**W1 is the only one still denied**:

| # | Site | Verb | Target | Roles in payload | Fate |
|---|------|------|--------|------------------|------|
| **W1** | `rolebasedgroupset_controller.go:231` | **CREATE** | main | **yes** (`newRBGForSet` copies the template verbatim) | **DENIED** → R2-F13 |
| W2 | `rolebasedgroupset_controller.go:465` | UPDATE | main | yes | OK (delta rule) |
| W3 | `rolebasedgroupscalingadapter_controller.go:511` | UPDATE | main | yes | OK — F10 fixed |
| W4 | `rolebasedgroup_controller.go:363` | PATCH | main | annotations only | OK — F2a fixed |
| W5 | `rolebasedgroup_controller.go:782` → `pkg/utils/utils.go:83` | PATCH (SSA) | `…/status` | no | not intercepted |
| W6 | `rolebasedgroupset_controller.go:332` | UPDATE | `…/status` | no | not intercepted |
| W7 | `cmd/cli/cmd/rollout/rollout_undo.go:123` | UPDATE (user CLI) | main | yes | OK unless the revision changes a role's name or type → R2-F20 |

W2/W3/W4 are proved accepted by driving the *real* production functions
(`updateExistingRBGs`, `updateRoleReplicas`, `ensureDiscoveryConfigMode`) through a client
that runs the real validators, each with a toggle-on control — so F2a, F10 and F9's update
leg are fixed in fact, not just on paper. The operator never creates or updates the
`rolebasedgroupsets` main resource at all, so there is no second create-path exposure.

### R2-F13 — the remaining half of F9, in one paragraph

A RoleBasedGroupSet does not update its children's roles in place: its controller
**creates** one RoleBasedGroup per replica —
`internal/controller/workloads/rolebasedgroupset_controller.go:231`
(`r.client.Create(ctx, rbg)`), reached whenever a child is missing. The validating webhook
covers `verbs=create;update` on `rolebasedgroups`, and `ValidateCreate` still runs the
strict whole-object check with no delta exemption. So for a pre-existing RBGSet whose
template uses a deprecated workload type, the parent is grandfathered but it can no longer
materialise a child: scaling up, or any child being deleted (node drain, GC, manual
`kubectl delete`), leaves a replica that can never come back while the controller retries
the denied create forever. Round 1 flagged the update path as F9; round 2 fixed that path
and left this one. `TestPR416R2_ChildRBGCreateIsStillDenied` proves it for all three
deprecated types, and asserts both premises first — that the parent update *is* exempt,
and that the same child create *is* accepted when the types are enabled — so it cannot
pass vacuously. `TestPR416R2_F9Create_*` then confirms it end-to-end through the real
`RoleBasedGroupSetReconciler.Reconcile`.

Three details make it worse than a plain denial:

* **`spec.replicas` on RoleBasedGroupSet is a scale subresource**
  (`rolebasedgroupset_types.go:77`) and the webhook rule does not cover
  `rolebasedgroupsets/scale`. An HPA can raise replicas with admission never seeing it;
  the refusal then happens asynchronously inside the controller, where nobody is looking.
* **The failure is not even recorded on the object.** `scaleUp`'s error short-circuits
  `Reconcile` at `:184-189`, *before* `updateStatus` at `:332`. Observed after the denial:
  `replicas=0 readyReplicas=0 conditions=0`. Round 1's "endless silent error loop with no
  sign on the object" complaint recurs verbatim.
* **It needs no user action at all.** Replicas unchanged, a child lost to GC or an
  accidental `kubectl delete`, and the RBGSet can never replace it.

Because a create-path check stricter than the update-path check will strand *any*
controller that materialises children from a stored template, the fix is structural rather
than a special case: validate the child CREATE as a delta against the owning RBGSet's
`groupTemplate` via `ownerReferences`, or exempt the controller's own service account.

### R2-F11 — why a string breaks it

The two templates apply different predicates to the same value:

* `manager.yaml:57` **prints** it — `{{ if hasKey $dwt "enabled" }}{{ $dwt.enabled }}{{ else }}true{{ end }}`
* `clusterrole.yaml:12` tests **Go-template truthiness** — `or (not (hasKey $dwt "enabled")) $dwt.enabled`

A non-empty string is truthy, so `"false"` leaves the RBAC gate open while
`strconv.ParseBool` reads the printed `false` and disables the types in the controller.
Result: the operator stops using Deployments/StatefulSets/LeaderWorkerSets but keeps
full `create,delete,get,list,patch,update,watch` on them — the exact permissions this PR
exists to remove. Reachable through `--set-string`, a quoted `enabled: "false"` in a
values file, or any tool that stringifies scalars (ArgoCD `helm.parameters` with
`forceString: true`). Same for `"0"`, `"f"`, `"F"`, `"False"`, `"FALSE"`.

Verified against the **real binary**, not just a model of it:

```
$ /tmp/rbgs-mgr --enable-deprecated-workload-types=          → invalid boolean value "" … parse error
$ /tmp/rbgs-mgr --enable-deprecated-workload-types=map[]      → invalid boolean value "map[]" … parse error
$ /tmp/rbgs-mgr --enable-deprecated-workload-types=false      → (accepted)
```

A single normalizing helper in `_helpers.tpl` that all three consumers share — and that
`fail`s on a non-boolean — closes R2-F11, R2-F12 and the pre-existing `controller: null`
nil-deref together.

### Negative results — checked, no defect

Recorded so a later round does not re-litigate them, and because two are now *constraints
the design depends on*:

* **The delta check cannot misfire via conversion.** Both CRDs have storageversion
  `v1alpha2` and the webhook is registered for `v1alpha2`, so `oldObject` is read from etcd
  already in the webhook's version and is never conversion-derived. `GetWorkloadType()`
  returns the same string on both sides of a v1alpha1 round-trip, because `ConvertFrom`
  restores `workload` from the role-workload-type annotation. **No reachable path makes
  `oldRoles` empty** — which matters, because an empty `oldRoles` would make every role
  look newly added and reject everything. That is now a property the design *relies* on.
* **Role reorder is handled correctly** — the map is keyed by name.
* **Status subresource writes bypass the webhook**, so no status write is denied. Adjacent
  near-miss: `syncRBGMetadata` (`rolebasedgroupset_controller.go:497-506`) wipes the child's
  `metadata.annotations`. If the workload-type annotation lived there, every template sync
  would look like a type change — a second blocker. It lives on `RoleSpec.Annotations`, so
  it is safe, but only by one field location.
* **The generator is deterministic** — byte-identical across repeated runs; no Go
  map-iteration nondeterminism. The committed chart file equals its own output.

---

## Layers

| Layer | What it proves | How to run |
|-------|----------------|------------|
| **script** (offline) | F1, F4, F5, F5b, F6, R2-F11, R2-F12, R2-F14…F17 | `for s in 01 02 03 04 05 07 08 09; do bash scripts/$s-*.sh; done` |
| **unit** | F2a, F2c, F3, F8, F8b, F9, F10, P1, R2-F13, R2-N3 | `go test ./api/workloads/... ./internal/controller/workloads/ ./pkg/discovery/ -run 'TestVerifyPR416\|TestPR416R2' -count=1 -v` |
| **live** (read-only) | F7 — a real API server accepts every rendered document, in **both** toggle states | `bash scripts/20-live-dryrun.sh` |
| **binary** | R2-F12 — the real manager rejects the unparseable flag values | `go build -o /tmp/rbgs-mgr ./cmd/rbgs && /tmp/rbgs-mgr --enable-deprecated-workload-types= --help` |

Environment: go 1.24.1, helm v3.16.3, kubectl v1.36.2; live cluster ACK cn-hongkong
k8s v1.36.1-aliyun.1, used **read-only** (`--dry-run=server`) throughout.

### Why the harness is trustworthy

* **Every reproduction has a passing control.** R2-F13 asserts both premises (parent
  update exempt, child create accepted when enabled) before claiming the denial. R2-F11's
  script requires ≥3 shapes to *agree* before it reports any mismatch, so a broken grep
  cannot manufacture the finding — it reports `HARNESS PROBLEM` instead. F1 checks the
  `=true` control keeps the RBAC that `=false` removes.
* **R2-F12 is confirmed against the compiled binary**, not inferred from `strconv` docs.
* **Only stdout is parsed.** Round 1 mis-scored three value shapes by feeding helm's
  kubeconfig warnings on stderr into a YAML parser; every script here keeps the streams
  separate.
* **The live layer is now non-vacuous.** Script 20's second shape still used the removed
  `compatibility.v1alpha1.enabled` path, so it was silently re-rendering the default and
  the "both shapes" claim proved nothing. Retargeted to
  `controller.deprecatedWorkloadTypes.enabled=false`; 12/12 objects accepted in each.

### Harness defects found and fixed this round

1. The whole unit layer failed to **compile** against the new head — the flag rename
   changed `DisableV1alpha1Compatibility` to `EnableDeprecatedWorkloadTypes`. Two helpers
   took a `disabled bool`, so the fix was a polarity flip (`!disabled`), not a
   substitution; a blind rename would have inverted every one of those assertions.
2. `01-helm-default-rbac.sh` and `20-live-dryrun.sh` still set the removed
   `compatibility.v1alpha1.enabled` path. Helm ignores an unknown `--set` key, so both
   scripts were silently testing the default twice. Script 01 reported a `HARNESS PROBLEM`
   (correctly); script 20 reported success for a shape it never rendered. Both retargeted.
3. `05-manifests-freshness.sh` reported drift that was really this harness's own edited
   files. Re-run in a clean scratch worktree: `make manifests generate` → empty
   `git status`, no drift.

### Process incident worth recording

A concurrent helper running in the same clone executed `git reset --hard
origin/verify/pr416-api-compat-toggle`, discarding a committed harness fix and two script
rewrites. Recovered from the reflog (`3a7cb87d`), tagged `r2-recover`, and pushed
immediately so origin — not the working tree — is the durable copy. The skill's "one
active reviewer at a time" guardrail exists for exactly this; scratch worktrees are not
enough, because a reset moves the shared *branch ref*. Commit and push before fanning out.

---

## Verdict

**Recommend `REQUEST_CHANGES`.** The delta is real progress and the direction is right —
the toggle finally names what it controls, the default is finally self-consistent, the
chart RBAC is finally generated and CI-gated, and the grandfathering logic is careful
about not being too loose. But one blocker remains open in a new place (R2-F13: a
grandfathered RBGSet cannot make a child), two round-1 blockers are untouched (F8, F8b),
and R2-F11 means the PR's own stated purpose — shrinking the RBAC surface — silently does
not happen for a plausible class of values.

Smallest set that would clear the blockers:

1. **R2-F13** — have the RBGSet controller's child create take the same delta treatment,
   or exempt writes whose roles match the parent template that was already accepted.
2. **F8/F8b** — either default the v1alpha1 conversion to `RoleInstanceSet`, or scope the
   check to roles whose workload was set *explicitly*. Right now the README documents the
   trap rather than removing it.
3. **R2-F11/F12** — one shared `_helpers.tpl` predicate that `fail`s on a non-boolean.

F3, F4, P1 and the generator's fail-open list (R2-F14) are non-blocking for this PR but
should not ship silently.

---

## Continuing after the fix

```bash
git fetch origin && git checkout verify/pr416-api-compat-toggle
bash docs/verification/pr416-api-compat-toggle/scripts/re-verify.sh
```

`re-verify.sh` takes **no sha** — it resolves the current PR head from `manifest.pr` and
the delta start from `.last-reviewed`. Run the script and live layers separately (they
need `helm` and a cluster).

**Polarity — read this before scoring a run.** Most findings are **contract** tests: red
while broken, green when fixed. But the imported write-path tests are **canaries** — they
assert the *denial*, so they **pass today** and are fixed only when they **flip to red**,
at which point invert them. The two polarities bracket R2-F13 from both sides:

| Test | Polarity | Today | When fixed |
|---|---|---|---|
| `TestPR416R2_ChildRBGCreateIsStillDenied` | contract | **RED** | green |
| `TestPR416R2_F9Create_RBGSetScaleUpChildCreateIsDenied` | canary | **PASS** | flips to fail → invert |
| `TestPR416R2_F9Create_SelfHealRecreateOfDeletedChildIsDenied` | canary | **PASS** | flips to fail → invert |
| `TestPR416R2_CreatePathIsStrictForTheExactControllerPayload` | canary | **PASS** | flips to fail → invert |

So "the whole unit package is green" does **not** mean R2-F13 is fixed. Check the contract
test by name. Polarity is recorded per finding in `verify-manifest.json`.

Per-finding, after a fix lands:

* **R2-F13** → `TestPR416R2_ChildRBGCreateIsStillDenied` passes (child create accepted).
* **R2-F11/F12** → `08-flag-rbac-agreement.sh` exits 0 (no shape disagrees, every flag
  value parseable).
* **F3** → `getOrCreateWorkloadReconciler` refuses deprecated types when the toggle is off.
* **F4** → no `helm upgrade` call site remains, or the guard becomes conditional.
* **F8/F8b** → a defaulted v1alpha1 RBG/RBGSet is accepted with the toggle off.
* **P1** → the LWS → RoleInstanceSet migration preserves the pod env contract.
* **Regression guards that must STAY green:** `TestPR416R2_Grandfathering_*` (F2a, F2c,
  F10 and the not-too-loose half), `01-helm-default-rbac.sh` (F1), `20-live-dryrun.sh` (F7).

### Kickoff prompt for a fresh agent

> Continue the review pipeline for <https://github.com/sgl-project/rbg/pull/416>. State
> lives on branch `verify/pr416-api-compat-toggle` in the `cheyang/rbg` fork; read
> `docs/verification/pr416-api-compat-toggle/README.md` and run `scripts/re-verify.sh`
> (no sha needed). Round 2 reviewed head `dcc7104a`. The live layer must stay read-only on
> any shared cluster — this chart's cluster-scoped object names are fixed and would
> clobber the existing install. Do not run `git reset`/`checkout` in a clone that has this
> branch checked out; commit and push before spawning helpers.

---

## Supplementary evidence added after the round-2 verdicts

Three additions that do not change any verdict above, plus one harness-automation fix.

**F3's reachability changed even though its code did not.** Round 1 masked F3: the webhook denied
the controller's first write, so `Reconcile` stopped at step 0 and never reached the ungated
reconciler factory. Round 2 deliberately admits that write — which means the ungated path is now
*reached* for exactly the pre-existing objects the exemption was written to protect. That also
makes F3 a documentation defect: `README.md:36-38` of the chart now asserts an exempt group
"stays writable (and scalable) and the controllers can keep reconciling it". The admission clause
holds; the reconcile clause does not. Same for `README.md:33` and the Go flag help, which both say
"the controller stops watching them" — contradicted for LeaderWorkerSet by `dynamicWatchCustomCRD`
re-arming the watch at runtime. Detail in
[`f3-escalation.md`](results/round2/f3-escalation.md).

**R2-F14 is a fails-open risk with no current instance.** The gate list *can* over-grant, but on
the input at this head it does not: normalising both sides into
`(apiGroup, resource, verb, resourceNames)` tuples gives **209 == 209** against
`config/rbac/role.yaml` with an *empty* symmetric difference, and disabling removes **exactly** the
32 deprecated tuples — no over-removal, no under-removal. Also verified independent of
controller-gen's rule ordering (all 17 rules reversed → identical tuple sets), with `resourceNames`
preserved across a split rule and the mixed rule that is already live in the real input
(`apps: [controllerrevisions, deployments, statefulsets]`) splitting correctly. Detail in
[`rbac-generator-audit.md`](results/round2/rbac-generator-audit.md).

**Two more negative results**, in
[`negative-results-supplement.txt`](results/round2/negative-results-supplement.txt): `enabled: null`
in a values *file* is safe and agrees across both templates (helm treats an explicit null as key
deletion), which bounds R2-F11/R2-F12 to non-boolean scalars and empty collections — a
`_helpers.tpl` normaliser must preserve that. And R2-F13's premise was verified directly rather
than assumed: the shipped webhook really does list `operations: [CREATE, UPDATE]` with
`failurePolicy: Fail`, and `rolebasedgroupset_controller.go:231` is the *only* site in the tree that
creates a RoleBasedGroup, so its fix surface is one call site.

**Harness automation fix.** `harnessPaths` listed 2 of the 4 harness test files and
`layers.unit.cmd` ran only `./internal/controller/workloads/`. Since `re-verify.sh` grafts exactly
`harnessPaths`, F8/F8b (`api/workloads/v1alpha1`), F9/F10 (`api/workloads/v1alpha2`) and P1
(`pkg/discovery`) were never executed by the automation and reported `HARNESS-UPDATE`; their
round-2 verdicts were obtained by running those packages by hand. Both fields now cover all four
packages, so the next round's `re-verify.sh` produces real verdicts for them.

---

# Round 3 — head `aac6056d`

| | |
|---|---|
| Reviewed head | `aac6056d5c615dae4c90ce8431d404043c5d2032` |
| Previous round head | `dcc7104aef6f6e8c16cf4342bb25baf9426c68ab` |
| Round | **3** (fifth of the #413 → #414 → #416 lineage) |
| Delta reviewed | 4 commits, 27 files, +459 / −235 |
| Environment | same remote Linux sandbox; ACK cn-hongkong used **read-only** (`--dry-run=server`) |

## The delta

| Commit | What it changes |
|---|---|
| `681332fa` | Chart: docs and NOTES rewritten around "fresh installation only" |
| `6fb8397a` | `+kubebuilder:deprecatedversion:warning` on the v1alpha1 kinds |
| `68850e6f` | `gen-helm-rbac`: strict decode, `kind: ClusterRole` assertion, dropped-rule guard, derived gate, unit tests |
| `aac6056d` | `cacheOptions` bounds StatefulSet/Deployment unconditionally |

## The headline: the design was reverted, not advanced

`aac6056d` **deletes `validateNoNewDeprecatedWorkloadTypes`** and routes `ValidateUpdate`
back through the strict whole-object check, for `RoleBasedGroup` and `RoleBasedGroupSet`
alike. Round 2's grandfathering — the thing that closed round-1 blockers F2a, F2c, F10 and
half of F9 — is gone.

This is a deliberate scope decision rather than a regression, and it is now stated
consistently in six places (PR body, root README, chart README, `values.yaml`, `NOTES.txt`,
flag help): `false` is for a **fresh installation only**, with no exemption for objects that
already use a deprecated workload type. Under that premise the round-1 denials are
*unreachable* rather than *fixed* — no such object can exist.

The whole design now rests on that single premise. So round 3 tested the premise.

## R3-F22 — "fresh installation only" is enforced nowhere, and the repo documents the bypass

**Severity: major.** Evidence: [`f22-fresh-install-invariant.txt`](results/round3/f22-fresh-install-invariant.txt)
(`scripts/10-fresh-install-invariant.sh`, read-only).

The PR's stated enforcement is `templates/upgrade-guard.yaml`. Four structural facts, each
independently checked, show it does not cover the case that matters:

1. **The chart declares no CRDs** — `crds/` is empty and no template emits a
   `CustomResourceDefinition` (they come from the `crd-upgrade` pre-install Job). So
   `helm uninstall` removes neither the CRDs nor any `RoleBasedGroup`/`RoleBasedGroupSet`.
   Those objects survive an uninstall, guaranteed.
2. **The guard keys on `.Release.IsUpgrade`**, which is **false** on a reinstall. Confirmed
   with a control: `helm template --is-upgrade` does fail, a plain render does not.
3. **The repo instructs exactly that path in four places** — `upgrade-guard.yaml:2`
   ("Please uninstall and reinstall"), `NOTES.txt:17`, chart `README.md:65` and `README.md:155`
   ("Only fresh `helm install` is supported. To update, uninstall and reinstall.").
4. **Nothing inspects the cluster.** No template uses Helm `lookup` to find existing
   RoleBasedGroups; `helm install --set controller.deprecatedWorkloadTypes.enabled=false`
   renders successfully (416 lines, flag `false`, 0 deprecated RBAC lines) whatever is
   already in the cluster.

So the documented update procedure — uninstall, reinstall — produces a *fresh release over
surviving objects*, and the toggle can be set `false` in that same command. The result is
the state the PR says cannot exist, and its consequences are already proved by the round-1
write-path tests, all RED again at this head:

| Write | Site | Fate with a pre-existing deprecated role |
|---|---|---|
| annotation patch | `rolebasedgroup_controller.go:363` | **denied** (F2a) |
| `kubectl scale` | user | **denied** (F2c) |
| RBGSet template sync | `rolebasedgroupset_controller.go:465` | **denied** (F9) |
| HPA / scale adapter | `rolebasedgroupscalingadapter_controller.go:511` | **denied** (F10) |
| child RBG create | `rolebasedgroupset_controller.go:231` | **denied** (R2-F13) |

Nothing is written to the object's status on any of them, so the object is unreconcilable
and silent about it.

**Suggested fix:** a pre-install check that fails the install when `enabled=false` and any
`RoleBasedGroup`/`RoleBasedGroupSet` already uses a deprecated workload type. The
`crd-upgrade` Job is the precedent that this is feasible in-chart with its own
ServiceAccount. That makes the premise real and closes F2a/F2c/F9/F10/R2-F13 as a *class*,
instead of one path at a time — which is what the previous two rounds each attempted.

## R3-F23 — the new dropped-rule guard cannot fire (R2-F15 only partly fixed)

**Severity: major (latent).** Evidence: `hack/gen-helm-rbac/pr416_r3_gen_test.go`,
[`gen-r3.txt`](results/round3/gen-r3.txt).

`68850e6f` answers R2-F15 with a guard in `run()`:

```go
if len(blocks) < len(role.Rules) { return error }
```

But `splitRules` is **not count-preserving**: a rule whose resources straddle the gate emits
*two* blocks. A split inflates the count and pays for a drop.

Measured against the committed `config/rbac/role.yaml`: **16 rules → 18 blocks**. Two rules
already straddle (`apps: controllerrevisions+deployments+statefulsets`, and the matching
`/status` rule), so the guard tolerates **two silently dropped rules today**. Adding one
`urls=` kubebuilder marker strips that permission from the chart ClusterRole with exit 0, no
error, and no CI diff.

The PR's own `TestSplitRulesDropsResourcelessRule` asserts `assert.Empty(t, blocks)` with no
error — it pins the drop as acceptable at the `splitRules` level, so the fix is tested at the
wrong layer.

**Fix:** reject any input rule that produced no block (per-rule coverage), not a count
comparison. Verified by the harness-bites check: adding that check turns all three R3
generator tests green (and requires updating `TestSplitRulesDropsResourcelessRule`).

## What the delta genuinely fixed

* **R2-F11 / R2-F12 — fixed, and structurally.** `clusterrole.yaml` and `manager.yaml` now
  read the toggle with the *identical* expression
  (`or (not (hasKey $d "enabled")) $d.enabled`), so the granted RBAC and the rendered flag
  cannot disagree. All value shapes agree and every rendered flag is parseable —
  no more `enabled: ""` → CrashLoopBackOff. [`08-flag-rbac-agreement`](results/round3/)
* **R2-F16 / R2-F17 — fixed.** `yaml.UnmarshalStrict` + a `kind: ClusterRole` assertion:
  a misspelled `resource:` key and a multi-document input now exit 1 with a clear message
  instead of silently emitting a short ClusterRole.
* **R2-F18 — fixed.** `hack/gen-helm-rbac/main_test.go` (223 lines) covers the derived gate,
  rule splitting, subresources and each rejected input shape. Output is deterministic.
* **R2-F19 — fixed.** The chart README's value-table row no longer contradicts the prose
  three lines below it.
* **F3b — fixed, with the right reasoning.** `cacheOptions` now lists StatefulSet and
  Deployment unconditionally. The comment is correct: `ByObject` is per-type configuration,
  not an allowlist, and creates no informer, so gating it only stripped the label bound from
  an informer that would start anyway.
* **The deprecation markers are correct.** All six v1alpha1 kinds point at v1alpha2 kinds
  that exist (`RoleInstance`, `RoleInstanceSet`, `ClusterEngineRuntimeProfile`, …), and the
  controller does not read v1alpha1 `Instance`/`InstanceSet` outside one test constant, so
  there is no self-inflicted deprecation-warning spam.
* **F1, F5, F5b, F7 stay green.** Default consistent and toggle effective; no chart/RBAC
  drift (209 == 209); generated artefacts in sync after `make manifests generate`; a real
  API server accepts all 12 rendered objects in both shapes.

## Round-3 verdicts

| ID | Sev | Claim | R3 verdict | Evidence |
|----|-----|-------|-----------|----------|
| **R3-F22** | **major** | "Fresh install only" is unenforced; the documented uninstall+reinstall path reaches `enabled=false` over surviving objects | **Reproduced (new)** | [`f22`](results/round3/f22-fresh-install-invariant.txt) |
| **R3-F23** | major (latent) | The dropped-rule guard is a count comparison a split can mask; slack is already 2 | **Reproduced (new)** | [`gen-r3`](results/round3/gen-r3.txt) |
| F2a/F2c/F9/F10/R2-F13 | — | Controller + user writes denied for a pre-existing deprecated object | **Red by design** — unreachable iff R3-F22 is closed | [`unit-r3`](results/round3/unit-r3.txt) |
| **F3** | major | Legacy reconcilers still built when disabled; nothing stops a read/write of a type whose RBAC is gone | **Still broken** | ditto |
| **F4** | major | Upgrade guard hard-fails `helm upgrade --install`, the command the repo documents | **Still broken** | [`04`](results/round3/) |
| **F8 / F8b** | blocker | v1alpha1 defaulting makes the whole v1alpha1 API unusable when disabled | **Still broken — documented** | [`unit-r3`](results/round3/unit-r3.txt) |
| **P1** | major | `LWS_*` → `RBG_LWP_*` env rename has no alias shim; containers fail at runtime, silently | **Still broken** (untouched) | ditto |
| R2-F14 | non-blocking | Gate list is still a second list — a deprecated type added to the validator but not to `mustGroupResources` is emitted **ungated** (fails open) | **Narrowed, not closed** | [`09`](results/round3/) |
| R2-F20 | non-blocking | `rbg rollout undo` refused across a workload-type change | Unchanged | — |
| R2-F21 | non-blocking | PR description described the round-1 design | **Fixed** — body now matches the code | — |
| R2-F11/F12/F16/F17/F18/F19, F3b | — | see above | **Fixed** | [`08`](results/round3/), [`09`](results/round3/) |
| F1, F5, F5b, F7 | — | Regression guards | **Green** | [`01`](results/round3/), [`03`](results/round3/), [`05`](results/round3/), [`20`](results/round3/) |

**Net: 7 findings fixed this round (R2-F11, F12, F16, F17, F18, F19, F21 + F3b). 2 new
(R3-F22, R3-F23). The grandfathering that closed 4 round-1 findings was reverted, so those
denials are live again — acceptable only if R3-F22 is closed.**

## Harness changes this round

* **11 round-2 tests retired**, not deleted: `r3RetireGrandfatheringAssertion(t)` skips each
  with a pointer to the reversal commit and the replacement pins. They asserted the
  grandfathering contract, which no longer exists — reporting them as failures would have
  been a false regression claim.
* **New design pins** (`api/workloads/v1alpha2/pr416_r3_design_test.go`, GREEN): whole-object
  rejection on update for both kinds, and create/update producing an *identical* verdict —
  the asymmetry that was R2-F13. Each denial has an ENABLED control, so none can pass
  vacuously. A fourth design flip cannot happen silently.
* **New generator tests** (`hack/gen-helm-rbac/pr416_r3_gen_test.go`, RED): R3-F23, written
  against a `dropDetected` helper that mirrors `run()`'s acceptance logic, so they go green
  wherever the fix lands.
* **Fixed a harness false positive:** `05-manifests-freshness.sh` counted this harness's own
  `pr416_*` test files under `api/` as generated-artefact drift and reported "F5b
  REPRODUCED". It now excludes them; F5b is genuinely clean.

Production code remains untouched on this branch.

## Design note — how the guard should actually work

F4 and R3-F22 both live in `templates/upgrade-guard.yaml`, and both have the same
root cause: the guard checks `.Release.IsUpgrade` instead of checking whether the
cluster is in a state this chart version can serve. The design exploration behind a
replacement — measured on a real cluster rather than reasoned from documentation — is
in **[`UPGRADE-GUARD-DESIGN.md`](UPGRADE-GUARD-DESIGN.md)**, with raw output in
[`results/round3/design/`](results/round3/design/).

Four things it establishes:

| Question | Measured answer |
|---|---|
| Can a template read the previously installed chart version? | **No.** The Deployment has no version label, and the release Secret's `version` label is the *revision number*; the chart version is gzipped inside `data`. |
| When is `lookup` populated? | Only on a real install/upgrade or `--dry-run=server`. **EMPTY** on `helm template`, `--is-upgrade`, and client-side `--dry-run` — so a fail-closed *template* check false-fails `helm diff`. |
| Is `semverCompare` safe with the `0.8.0-alpha.3` prerelease threshold? | **Yes**, all 8 cases correct. Version comparison is not the risky part. |
| Which carrier refuses more cleanly? | Template `fail` leaves the release untouched; a **hook Job** leaves a `failed` revision (recoverable without `--force`) but sees real state, reuses the controller's Go constants, and **is skipped by `helm diff` so it cannot false-fail**. |

Conclusion: gate on **objects, not version numbers**, carried by a
`pre-install,pre-upgrade` hook Job with `hook-weight` below `crd-upgrade`'s `-4`.
One mechanism closes F4 (the documented `helm upgrade --install` becomes idempotent
again, so none of the 10 call sites need changing) and R3-F22.

A chart-version marker was also built and tested end-to-end, and **cannot deliver the
stated intent**: the marker ships with this PR, while `0.8.0-alpha.3` is already
released without it, so an upgrade from alpha.3 always lands in the "cannot tell"
branch. Related trap — the PR branch's `Chart.yaml` is still `0.8.0-alpha.2`, *below*
the threshold, so shipping as-is would have the chart's own guard refuse the next
upgrade.
