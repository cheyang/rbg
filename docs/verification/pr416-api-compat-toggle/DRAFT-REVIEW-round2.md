# DRAFT — round-2 review for PR #416 (NOT PUBLISHED)

Head `dcc7104aef6f6e8c16cf4342bb25baf9426c68ab`. Verdict: **REQUEST_CHANGES**.

Merged finding set: this round's own findings (F1–F10, P1 re-verified) plus the cross-file sweep
(R2-F11…R2-F21). Six inline comments, anchored on lines this PR actually touches. Everything that
can't be anchored (F4, P1, R2-F20, R2-F21) is folded into the summary.

---

## Review body

Most of this round I'd merge as-is, and I want to start there because the delta is a genuine
step up from the last one.

The rename lands the concept properly. `deprecatedWorkloadTypes` names the three workload kinds
the toggle actually controls instead of an API version, the flag now reads positively so you can
tell a pod's mode without a double negative, and the `hasKey` guard is the right call — `default`
would have resurrected an explicit `false`. Narrowing UPDATE to a delta check is the correct
mechanism, and keying the exemption by role name *and* requiring the type to be unchanged is
tighter than I expected: it blocks a deprecated→deprecated swap, which a looser "was deprecated
before" test would have let through.

I checked the four things that broke last round and all four are fixed: the shipped default now
agrees with the README table, the README prose, `NOTES.txt` and the Go flag default; the
discovery-mode annotation patch, the RBGSet child sync and the HPA scale path are all admitted
again; and an existing group can be scaled. The two error hints are unusually good — they name
the exact Helm value, the exact flag, and the v1alpha1 defaulting trap that makes a role carry a
type the user never wrote.

`hack/gen-helm-rbac` deserves specific credit. I didn't eyeball the diff — I normalised both
sides into `(apiGroup, resource, verb, resourceNames)` tuples: the enabled render and
`config/rbac/role.yaml` agree at **209 == 209** with an empty symmetric difference, and disabling
removes **exactly** the 32 deprecated tuples, no over- or under-removal. Output is deterministic
across repeated runs and unchanged when I reverse all 17 input rules, `resourceNames` survives a
split rule, and the mixed rule already live in your input
(`apps: [controllerrevisions, deployments, statefulsets]`) splits correctly. Since
`project-check.yml` already runs `make manifests` and fails on a dirty tree, chart RBAC is now
genuinely under CI. That closes the drift hole properly rather than relocating it.

**What I'd want fixed before merge is one thing wearing two hats: the exemption was implemented
at the admission layer, and the other two layers didn't follow.**

- `ValidateCreate` kept the whole-object check, and the RBGSet controller *grows and heals by
  creating* child RoleBasedGroups. So an exempt RBGSet can never scale up or replace a lost child.
- The RBAC and the watches are still removed unconditionally, so a group that is now admitted for
  writes still can't be reconciled.

Both contradict a sentence this PR adds. `deploy/helm/rbgs/README.md:36-38` now promises that an
exempt group "stays writable (and scalable) and the controllers can keep reconciling it." As
written: the first clause holds for RoleBasedGroup, the parenthetical fails for
RoleBasedGroupSet, and the last clause doesn't hold at all. That's what tips me to
REQUEST_CHANGES rather than a comment — not the severity in the abstract, but that someone will
read that paragraph, set `enabled=false`, and get a silently degraded cluster.

Worth stating plainly, because it changes how urgent this is: **the default is now `true` and
the docs agree**, so none of the above reaches anyone who doesn't opt in. If you'd rather land the
rename and the generator now and fix the opt-in path in the follow-up PR the description already
promises, I think that's defensible — but then please make the README stop advertising an exempt
path that doesn't work yet. Shipping the sentence is what makes this urgent, not shipping the code.

Four things I couldn't attach to a line, in descending order of how much they'd bother me:

**The `helm upgrade` guard (carried, unresolved).** `upgrade-guard.yaml` still `fail`s on any
upgrade, and the repo documents `helm upgrade --install` in 10 places — including this chart's own
README at line 64, which both forbids upgrades at lines 57/148 and prescribes one at 64. Also
`Makefile:249`, `doc/install.md:30` and `:66`, both READMEs at line 77,
`test/stress/scripts/deploy-controller.sh:52`, `doc/dev/how_to_develop.md:301`, and two CI
workflows. Unchanged since last round; flagging that it's still open rather than re-arguing it.

**`rbg rollout undo` is refused while the toggle is off.** `cmd/cli/cmd/rollout/rollout_undo.go:123`
restores roles from a ControllerRevision, so rolling back across a workload-type change or a role
rename hits the delta check and is rejected — a documented user-facing feature failing with an
update-path message about a "newly added role". Not in the diff, so no inline, but it's the same
root cause as the first inline comment and worth fixing together.

**The LWS → RoleInstanceSet env contract (carried).** Migrating an LWS-backed role injects
`RBG_LWP_LEADER_ADDRESS` / `RBG_LWP_GROUP_SIZE` / `RBG_LWP_WORKER_INDEX` but not the `LWS_*` names
user containers actually read. No admission error, no reconcile error, nothing on status — the
container just starts and fails. The tree already declares `DeprecatedEnvLws*` constants for
exactly this and references them nowhere. This matters more this round, not less, because the
README now actively steers people to migrate.

**The PR description.** It still describes the previous design: `compatibility.v1alpha1.enabled`,
"(default: `false`)", `--disable-v1alpha1-compatibility`, and "the validating webhook rejects
RBGs/RBGSets using legacy workload types" — that last one is no longer true for UPDATE, which is
the most important behavioural change in the delta. Nothing ships from it, but it tends to become
the squash message and the changelog entry, and it's the third of the three documents that
disagreed with the shipped default last round; the other two were fixed.

---

## Inline 1 — BLOCKER

**`api/workloads/v1alpha2/rolebasedgroup_admission.go` line 61**

> A pre-existing RoleBasedGroupSet with a deprecated template can never scale up or heal

The delta check on `ValidateUpdate` is the right fix and the doc comment on
`validateNoNewDeprecatedWorkloadTypes` makes exactly the right argument: a controller's own
idempotent write shouldn't be denied by a rule aimed at user intent. But it's only on the update
path. This line still runs the whole-object check, and one of the three writers that comment
names reaches the API server through **CREATE**, not UPDATE.

`rolebasedgroupset_controller.go:231` calls `client.Create` on an object built by `newRBGForSet`
(line 509), which copies `rbgset.Spec.GroupTemplate.Spec.Roles` verbatim. The shipped webhook
lists `operations: [CREATE, UPDATE]` on `rolebasedgroups` with `failurePolicy: Fail`, so that
create is intercepted and hard-denied.

The result is internally inconsistent. For a pre-existing RBGSet whose template uses a deprecated
type:

- the RBGSet's own updates are exempt — `RoleBasedGroupSetValidator` lets them through;
- its existing children sync fine — identical roles, empty delta;
- but raising `spec.replicas` creates nothing, and a child lost to node failure, GC or
  `kubectl delete` is never recreated.

So the parent is permitted to keep a template it is structurally unable to satisfy, and the only
symptom is a replica shortfall plus a controller retrying a denied create forever. Nothing on the
object says why.

I verified this by driving the real `scaleUp` against the real validator for all three deprecated
types; the control (a `RoleInstanceSet` template through the same `newRBGForSet` → `ValidateCreate`
path) passes, so it's attributable to the workload type and not to the call path. I also checked
the blast radius: `rolebasedgroupset_controller.go:231` is the **only** site in the tree that
creates a RoleBasedGroup, so the fix surface is one call site.

What I'd suggest: extend the same delta reasoning to create when the object is
controller-generated. A child carries `GroupSetNameLabelKey` plus an owner reference to its
RBGSet, so the validator can look up the parent and allow a role whose type matches the parent's
template. Whichever mechanism you pick, it shouldn't be possible for `RoleBasedGroupSetValidator`
to admit a template that `RoleBasedGroupValidator` then refuses to instantiate — that asymmetry is
the actual bug, and it'll keep producing variants otherwise. `rbg rollout undo` (see the summary)
is the same root cause.

---

## Inline 2 — BLOCKER

**`deploy/helm/rbgs/README.md` line 37**

> "the controllers can keep reconciling it" isn't true yet — admission was fixed, RBAC and watches weren't

Anchoring here rather than on the Go code because this sentence is the clearest statement of
intent in the PR, and it's the part that's wrong. The admission half landed. Three other places
still remove support unconditionally, so with the deprecated RBAC gone the reconcile now fails one
step *later* than it did before this delta rather than not failing:

| Where | With the toggle off |
|---|---|
| `getOrCreateWorkloadReconciler` → `reconciler.NewWorkloadReconciler` | No gate at all. Still returns a Deployment/StatefulSet/LWS reconciler, which then reads and writes those types |
| `dynamicWatchCustomCRD`, `rolebasedgroup_controller.go:1609` | Re-registers `Owns(&lwsv1.LeaderWorkerSet{})` at runtime with no compatibility check, even though `SetupWithManager:1056` deliberately skipped it |
| `cacheOptions`, `cmd/rbgs/main.go` | Drops the `ByObject` label selector for StatefulSet/Deployment instead of keeping it. `ByObject` is per-type config, not an allowlist — removing the entry doesn't prevent an informer, it only removes the bound on one that starts |

This is *more* reachable after this delta, not less, which is why I'm raising it again rather than
letting it ride. Previously the webhook denied the controller's first write, so `Reconcile` stopped
at step 0 and never got here. Now that write is admitted by design — so the ungated path is
reached for exactly the pre-existing objects the exemption was written to protect, and fails on
`Forbidden` against `statefulsets`.

Two doc knock-ons while you're in here: line 33 and the `--enable-deprecated-workload-types` help
text both say "the controller stops watching them", which the `dynamicWatchCustomCRD` re-arm
contradicts for LeaderWorkerSet specifically.

And whichever way this goes, a group that can't be reconciled should say so on itself rather than
only in controller logs:

```
type=Degraded  reason=DeprecatedWorkloadTypeDisabled
message="role \"worker\" is backed by apps/v1/StatefulSet, which is disabled on
         this cluster; migrate to RoleInstanceSet (see <link>)"
```

That's cheap and it turns all of this from "the controller is silently looping" into something an
operator can find.

---

## Inline 3 — major

**`deploy/helm/rbgs/templates/manager/manager.yaml` line 57**

> Two value shapes make this flag and the ClusterRole disagree, and one crash-loops the manager

The `hasKey` reasoning in the comment above is right and the ordinary shapes all behave. Two
non-boolean shapes slip through, both because this line *prints* the value while
`clusterrole.yaml:12` tests Go-template truthiness:

**A quoted string silently keeps the RBAC.** `enabled: "false"` renders
`--enable-deprecated-workload-types=false`, but a non-empty string is truthy in the template, so
the ClusterRole keeps all 8 deprecated entries. The operator stops *using* the deprecated types
while retaining full `create`/`delete`/`patch` on them — precisely the privilege reduction this PR
exists to deliver, silently not happening. Same for `"0"`, `"f"`, `"F"`, `"False"`, `"FALSE"`.
This isn't exotic: `--set-string`, a quoted value in a values file, and ArgoCD's `forceString` all
produce it.

**An empty or collection value crash-loops the manager.** `enabled: ""`, `{}` or `[]` strips the
RBAC *and* renders a flag value (empty, `map[]`, `[]`) that `flag.BoolVar` can't parse, so the
manager exits at startup. Confirmed against the compiled binary, not inferred. helm renders all
three with no error and no warning.

Worth knowing where the boundary is: `enabled: null` in a values file is **fine** — I expected it
to break and it doesn't, because helm treats an explicit null in user values as deleting the key,
so both templates coalesce back to the default and agree. So a fix has to preserve that.

A single normalising helper in `_helpers.tpl` that all three consumers share, and that `fail`s on
a non-boolean, would close both of these and the pre-existing `controller: null` nil-deref at the
same time. Failing loudly at render time seems clearly better than either half-applying the
setting or shipping a pod that won't start.

---

## Inline 4 — major

**`hack/gen-helm-rbac/main.go` line 71**

> This list is a third hand-maintained copy of "which workload types are deprecated", and it fails open

The generator itself is solid — see my note in the summary about the 209-tuple check. My concern
is this map specifically, and what happens when it drifts.

"Which workload types are deprecated" is now written down in three places:
`isDeprecatedWorkloadType` in `rolebasedgroup_validation.go`, the gated `Owns()` calls in
`rolebasedgroup_controller.go`, and this map. Nothing ties them together. A resource that's in the
other two but missing here is emitted **outside** the conditional, i.e. still granted when the
toggle is off — so this fails **open**, toward over-grant, with no test and no CI signal. F5's
named failure mode (a human forgetting to hand-sync the chart) is genuinely fixed; the judgement
call about which rules belong inside the gate moved from a Makefile comment into an unverified Go
string map.

To be fair about the current state: on the input at this head the map is exactly right — I
verified there's no over-grant or under-removal today. So this is a risk with no live instance,
not a bug you're shipping. But it's the kind that surfaces as an over-permissioned controller two
releases later, and it's cheap to close now.

Two options that would: derive the map from the same constants
`isDeprecatedWorkloadType` switches on, so there's one source of truth; or add a test asserting
that the set of gated `(apiGroup, resource)` pairs equals the set implied by those constants. The
second is a few lines and would also catch R2-F15…F17 below.

---

## Inline 5 — non-blocking

**`hack/gen-helm-rbac/main.go` line 129**

> Three input shapes are silently dropped, and the worst case emits a rules-less ClusterRole with exit 0

Grouping these because they share one cause: `splitRules` partitions on `.Resources`, and anything
that doesn't fit that shape falls out with no error and no post-condition check that output rule
count matches input.

1. **A `nonResourceURLs`-only rule vanishes.** It yields neither a kept nor a gated block. Not
   reachable at this head (no `kubebuilder:rbac:urls=` marker exists), but controller-gen does
   emit them, and since the generator is now the *only* copy of the chart ClusterRole there's no
   longer a diff for CI to catch when it starts happening.
2. **A multi-document `role.yaml` loses everything after the first document**, because
   `sigs.k8s.io/yaml.Unmarshal` decodes one. Latent — controller-gen writes a single document.
3. **Non-strict unmarshal with no `kind` assertion.** `kind: Role` is accepted and rendered as the
   chart ClusterRole; `aggregationRule` is parsed then never emitted; and a misspelled key
   (`resource:` for `resources:`) makes that rule disappear. All at exit 0.

Worst case of (3): the generator exits 0 and writes a template whose rules list is empty, which
`helm template` renders as `rules: None` — a completely permission-less controller, with
`make manifests` succeeding. The existing `len(role.Rules) == 0` guard doesn't catch it because
the rules do exist in the input and are lost during the split.

`UnmarshalStrict` plus a `kind` check closes (2) and (3) together; rejecting rules with both
`Resources` and `NonResourceURLs` empty, passing `nonResourceURLs`-only rules through ungated, and
asserting `len(blocks) >= len(rules)` before writing closes (1). None of this blocks the PR — but
a generator whose whole purpose is preventing silent RBAC drift shouldn't have three silent-drop
paths, and `splitRules` / `isDeprecated` / `render` are already pure functions, so each case above
is a few table-test lines from being caught instead of latent.

---

## Inline 6 — non-blocking

**`deploy/helm/rbgs/README.md` line 33**

> This table row contradicts the prose three lines below it, and describes the behaviour the delta removed

The row says the webhook "rejects roles that use them". The prose at 36-38, `NOTES.txt`,
`values.yaml` and the Go flag help all correctly say that roles which **already** use one are
exempt. This row is the only place with the stricter wording, and it happens to describe exactly
the behaviour this delta was written to remove — so a reader who only skims the value table comes
away with the pre-delta model.

Suggest matching the prose: "rejects roles that *start* using them".
