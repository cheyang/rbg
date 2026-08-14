# Verification — PR 424 "restore restartPolicy as a string to keep v0.7.0 objects decodable"

- **PR under review:** https://github.com/sgl-project/rbg/pull/424
- **Head verified:** `1c67becd` (round 6; earlier rounds `843fd2d9`, `edaf2fb3`, `16969e7f`, `6fc49a96`). Merge base `de4fa49f`.
- **Production code touched by this branch:** none. The harness is additive
  (`test/verify/pr424/` + this directory).

The PR is a genuine and necessary fix: `restartPolicy` changed from a string to a struct
after v0.7.0, so v0.7.0 objects no longer decode and the whole List fails. Restoring the
string is the right call. What this harness examines is the *cost* of the chosen shape —
the new `restartPolicyConfig` field, the defaulting webhook that materializes it, and the
upgrade path away from the v0.8.0-alpha.x object shape.

## Observed vs expected

| id | claim | layer | polarity | result | evidence |
|----|-------|-------|----------|--------|----------|
| F1 | `make lint` fails (1 × copyloopvar, 3 × SA1019 on the deprecated field) | CI | contract | **FIXED in `5764a774`** | lint green on [run 31466365149](https://github.com/sgl-project/rbg/actions/runs/31466365149/job/93699979864) |
| F2 | Once the defaulting webhook has materialized `restartPolicyConfig.type`, editing the deprecated `restartPolicy` has no effect on any read-modify-write path | unit + envtest | contract | **Still broken at `edaf2fb3`** | `results/reverify`, `results/phase3.log` |
| F2 | …and it stays inert for every subsequent value | unit | canary | **Confirmed** (passes on HEAD) | `results/l1l2-full.log` |
| F3 | One un-migrated v0.8.0-alpha.x object fails the typed LIST for the entire resource type | envtest | canary | **Confirmed** | `results/l1l2-full.log` |
| F3 | A controller-runtime cache over that type never syncs | envtest | canary | **Confirmed** | `results/l1l2-full.log` |
| F3 | After the CRD upgrade the stored `restartPolicy` is pruned to `{}` on read, so the configured policy is unrecoverable via the API | envtest + live | canary | **Confirmed** | `results/phase3.log`, `results/l1l2-full.log` |
| F3 | The obvious `--type=json` `move` migration unbreaks the LIST but silently drops the policy | envtest | canary | **Confirmed** | `results/l1l2-full.log` |
| F3 | A merge patch that re-supplies the values does migrate correctly | envtest + live | contract | **Confirmed working** | `results/phase3.log` |
| F4 | The documented pre-upgrade migration, run in the documented order, silently deletes the policy | live | contract | **Still open** (docs untouched in round 2) | `results/phase2.log` |
| — | Writes to an un-migrated object are rejected | envtest | (hypothesis) | **Not reproduced** — CRD validation ratcheting accepts them, so in-place repair is possible | `results/l1l2-full.log` |
| — | A full `kubectl apply` of a v0.7.0 manifest is also broken | envtest | (hypothesis) | **Not reproduced** — the merge patch replaces `spec.roles` wholesale, which drops `restartPolicyConfig`, so the deprecated field is honoured again | `results/l1l2-full.log` |

Two hypotheses did **not** hold and are recorded as controls so the findings are not
overstated. F2's blast radius is read-modify-write paths (`kubectl edit`, a targeted JSON
patch, any typed-client PUT), not "the deprecated field never works".

## F2 — the deprecated field goes inert

`resolveRestartPolicyConfig` gives `restartPolicyConfig.type` absolute priority, and
`RoleBasedGroupDefaulter.Default` materializes that field on **every** admission. So after
the first admission the deprecated string is decorative: the value it resolved to is now
stored in the field that outranks it.

Live, on a real cluster with the PR build and the real webhook (`results/phase3.log`):

```
--- as stored after admission (new defaulting webhook) ---
restartPolicy       = "RecreateRoleInstanceOnPodRestart"
restartPolicyConfig = {"baseDelaySeconds":30,"maxDelaySeconds":600,"type":"RecreateRoleInstanceOnPodRestart"}
--- operator flips the deprecated field to None ---
restartPolicy       = "None"                                  <- operator asked for None
restartPolicyConfig = {... "type":"RecreateRoleInstanceOnPodRestart"}  <- what the controller obeys
```

The write is accepted and reported as success. The role keeps recreating its instance.

This lands on exactly the population the PR exists to protect: someone upgrading from
v0.7.0 whose manifests only know the deprecated field.

### Candidate fix (used for the harness-bites check, not shipped here)

Only materialize `restartPolicyConfig.type` when the deprecated field is empty:

```go
case role.LeaderWorkerPattern != nil:
    if role.LeaderWorkerPattern.RestartPolicy == "" {
        role.LeaderWorkerPattern.RestartPolicyConfig = &resolved
    }
```

That keeps "config wins" for objects that genuinely set both (which the PR's own e2e matrix
pins) while leaving the deprecated field authoritative — and therefore editable — for
v0.7.0-shaped objects. With it applied, both F2 contract tests flip to PASS and the F2
canary flips to FAIL, as polarity requires. Reverted afterwards; production diff back to 0
lines.

Worth asking whether the write-back earns its keep at all. Its only benefit is making the
effective value visible on the stored object; its cost is ~200 lines, a new
`failurePolicy: Fail` admission dependency on the RBG write path, cluster-scoped
`mutatingwebhookconfigurations` RBAC, and this bug. The getters already resolve correctly
without it.

## F3 / F4 — the upgrade away from v0.8.0-alpha.x

The PR documents this break and tells operators to migrate before upgrading. The harness
shows the procedure does not work as written.

**The controller breaks before the CRD is upgraded.** `helm upgrade` rolls the controller
image, but CRDs go through a separate `crd-upgrade` Job. With the new binary and the old
CRD, on a real cluster carrying four ordinary RoleInstances (`results/after-logs.txt`):

```
"Failed to watch","type":"*v1alpha2.RoleInstance",
"error":"failed to list *v1alpha2.RoleInstance: json: cannot unmarshal object into
         Go struct field RoleInstanceSpec.items.spec.restartPolicy of type v1alpha2.RestartPolicyType"
```

The pods stay `1/1 Running` and Ready throughout, so no probe or dashboard reports it.
`TestL2_Canary_ControllerCacheNeverSyncs` confirms the cache never syncs, i.e. the
controller is stranded rather than skipping the one bad object.

**The documented migration, run in the documented order, deletes the policy.** Before the
CRD upgrade, `restartPolicyConfig` does not exist in the schema, so it is pruned — and the
patch still succeeds (`results/phase2.log`):

```
$ kubectl patch roleinstance nginx-cluster-backend-0 -n default --type=merge \
    -p '{"spec":{"restartPolicy":null,"restartPolicyConfig":{"type":"None"}}}'
Warning: unknown field "spec.restartPolicyConfig"
roleinstance.workloads.x-k8s.io/nginx-cluster-backend-0 patched

restartPolicy:       null
restartPolicyConfig: null      <- the policy is simply gone
```

**After the CRD upgrade, the stored values are unreadable.** The new schema types
`restartPolicy` as a string, so the API server prunes the stored object's properties on the
way out (`results/phase3.log`):

```
nginx-cluster-backend-0 restartPolicy= {}
nginx-cluster-backend-1 restartPolicy= {}
nginx-cluster-backend-2 restartPolicy= {}
nginx-cluster-frontend-0 restartPolicy= {}
```

`{}` still fails the typed decode, so the object is still broken — but the type and backoff
values are no longer retrievable from it. This makes the natural one-liner a trap:

```
kubectl patch ... --type=json -p '[{"op":"move","from":"/spec/restartPolicy","path":"/spec/restartPolicyConfig"}]'
```

It unbreaks the LIST, which is convincing, and moves nothing — the object comes back with
schema defaults and no `type`, so a role configured for `RecreateRoleInstanceOnPodRestart`
silently stops recreating.

**What does work**, once the CRDs are upgraded, is re-supplying the values explicitly:

```bash
kubectl patch roleinstance <name> -n <ns> --type=merge \
  -p '{"spec":{"restartPolicy":null,"restartPolicyConfig":{"type":"<type>"}}}'
```

Verified end to end on the live cluster: all four objects migrated, controller restarted,
decode errors went 0, workload stayed Ready (`results/phase3.log`). Note the intended
`<type>` has to come from the RoleInstanceSet template or the parent RBG, not from the
object being repaired.

So the upgrade note needs the opposite order from what it currently says — CRDs first, then
migrate — plus a command, and a warning about the `move` trap.

## How to run

```bash
# L1 — deterministic, no cluster
go test ./test/verify/pr424/ -run '^TestL1' -v

# L2 — envtest (real API server; the webhook tests serve the real defaulter)
make setup-envtest
export KUBEBUILDER_ASSETS="$(pwd)/$(./bin/setup-envtest use 1.33.0 --bin-dir ./bin -p path)"
go test ./test/verify/pr424/ -run '^TestL2' -v -timeout 30m
```

`KUBEBUILDER_ASSETS` must be absolute — envtest resolves it per test-package working
directory, and a relative path fails with `fork/exec .../etcd: no such file or directory`.

L3 needs a real cluster **already running the current release**, so its stored RoleInstances
carry the object shape. See `liveNote` in `verify-manifest.json` and run
`scripts/l3-*.sh` in order. The rollback scripts are not optional: leaving the PR chart
installed leaves a `failurePolicy: Fail` MutatingWebhookConfiguration behind, and leaving
the PR CRDs installed leaves stored objects unreadable by the released controller.

Cluster used: ACK, Kubernetes v1.36.1-aliyun.1, 3 nodes, `rbgs` helm release in
`rbg-system` at chart 0.8.0-alpha.4 / image `v0.8.0-0c00546d`. Restored to that state
afterwards (`results/rollback2.log`).

## Continuing after the fix

```bash
git fetch origin verify/pr424-restart-policy-string-restore
git checkout verify/pr424-restart-policy-string-restore
bash docs/verification/pr424-restart-policy-string-restore/scripts/re-verify.sh
```

No sha needed — the current PR head comes from `manifest.pr`, and the review delta from
`.last-reviewed`.

Polarity when the fix lands:

- `TestL1_DeprecatedRestartPolicyGoesInertAfterDefaulting`, `TestL2_KubectlEdit…`,
  `TestL2_TargetedJSONPatch…` — contract: must turn **green**.
- `TestL1_Canary_ConfigTypeShadowsLaterLegacyEdits` — canary: must flip **red**, then
  invert it (or promote the corrected behaviour to a contract test).
- The F3 canaries stay red-meaning-green only if the wire break is actually addressed
  (e.g. a decoder accepting both shapes). If the team keeps the break and only fixes the
  docs, re-verify F3/F4 by reading `doc/features/failure-handling.md` for the corrected
  order + command, not by running the canaries.
- Controls (`…Control_…`, `TestL2_UnrelatedWrites…`) must stay green throughout; if one
  flips, the harness itself needs attention.

### Kickoff prompt for a fresh session

> Continue the review pipeline for https://github.com/sgl-project/rbg/pull/424. The
> verification branch is `verify/pr424-restart-policy-string-restore` on the `origin`
> fork; state lives in `docs/verification/pr424-restart-policy-string-restore/`. Run
> `scripts/re-verify.sh`, honour the polarity table in the README, review the
> `.last-reviewed..head` delta, then update the table and advance `.last-reviewed`. L3
> needs a cluster already running the released build; rollback scripts are mandatory.

## Round 2 — `843fd2d9..edaf2fb3`

Two commits landed: `5764a774` "deal with issue reported by 'make lint'" and `edaf2fb3`
"Add convertion test". `re-verify.sh` against `edaf2fb3`:

```
ID     POLARITY  LAYER        VERDICT        DETAIL
F2     contract  unit         STILL-BROKEN   pass=0 fail=1 miss=0
F2     canary    unit         STILL-PRESENT  pass=1 fail=0 miss=0
F2     contract  integration  STILL-BROKEN   pass=0 fail=2 miss=0
F3     canary    integration  STILL-PRESENT  pass=4 fail=0 miss=0
F3     contract  integration  FIXED          pass=1 fail=0 miss=0
```

`api/workloads/v1alpha2/{rolebasedgroup_defaulter,rolebasedgroup_types,helper}.go`,
`config/crd/` and `doc/features/failure-handling.md` are byte-identical to round 1, so
**F2, F3 and F4 are untouched**. The round-2 commits address F1 and F5 only.

### F1 — fixed

`//nolint:staticcheck` on the three intentional deprecated-field reads, and the redundant
`tc := tc` removed. `lint`, `unit-test`, `envtest` and `build` are all green on `edaf2fb3`.

### F5 — fixed, and covered well

`edaf2fb3` appends 5 tests to the existing `rolebasedgroup_conversion_test.go`, covering
exactly what was missing: v1alpha1 empty → `RestartPolicyNone`, explicit `None` → `None`,
`RecreateRoleInstanceOnPodRestart` passthrough, a round-trip table asserting the resolved
policy survives v1alpha1 → v1alpha2 → v1alpha1, and the `CustomComponentsPattern` path.

### F6 — refactored, and it holds up

This was `dupl` (enabled in `.golangci.yml`, and `pkg/*` is not in the exclude list), not an
optional cleanup. `5764a774` collapses the two ~60-line copies into one
`patchWebhookCABundle` that reaches `Webhooks[i].ClientConfig.CABundle` through `reflect`.

`zz_verify_pr424_reflect_test.go` probes the paths the author's own 5 tests do not, because
this code runs in `bootstrapWebhookCerts` and the new webhook is `failurePolicy: Fail` — a
panic there would block every RoleBasedGroup write cluster-wide:

| case | why it matters | result |
|---|---|---|
| `caBundle` absent (nil) | exactly what `helm install` ships; `reflect.Value.Bytes()` on a nil field | **PASS**, no panic, bundle written |
| only one of three entries stale | the comparison loop `break`s on first mismatch, then writes all | **PASS**, all entries patched |
| already correct | guards the reconciler's 10-minute requeue from churning the object | **PASS**, `resourceVersion` unchanged |

So the refactor is behaviourally sound and F6 drops to a `nit`: `reflect` trades compile-time
field checking for runtime `FieldByName` lookups, and only the `Webhooks` field is
`IsValid()`-guarded — `ClientConfig`/`CABundle` are not, so a future third caller with a
different shape panics rather than failing to compile. Go generics or a two-line
`[]*[]byte` accessor would satisfy `dupl` without that trade. Not worth blocking on.

### Round 2 verdict

`REQUEST_CHANGES` stands, on F2 (major) and F3/F4 (major). F1 and F5 are resolved and the
F6 refactor is fine. The remaining asks are unchanged: keep the deprecated field editable
(or drop the write-back), and fix the upgrade note's ordering + give it a command that does
not silently discard the policy.

## Round 3 — `edaf2fb3..16969e7f`

Two commits: `04ecd11c` "remove mutating webhook" and `16969e7f` "check migration flow".
Net **-295 lines**. The round-1/2 harness no longer compiled (`RoleBasedGroupDefaulter` is
gone), so `l1_defaulter_inert_test.go` and `l2_webhook_inert_test.go` were replaced by
`l1_r3_test.go`, `l2_r3_nowebhook_test.go` and `l2_r3_migration_doc_test.go`. All green:

| id | claim | polarity | result |
|----|-------|----------|--------|
| F2 | the deprecated field stays authoritative across edits (unit) | contract | **FIXED** |
| F2 | `kubectl edit` / targeted JSON patch of the deprecated field takes effect | contract | **FIXED** |
| F2 | an explicit `restartPolicyConfig.type` still wins | contract | **FIXED** |
| F3 | the v0.8.0-alpha.x wire break and its blast radius | canary | still present *by design* |
| F4 | the documented RoleInstance / RoleInstanceSet migration commands work | contract | **verified working** |
| F4a | adapting the documented merge patch to a RoleBasedGroup drops the pod template | canary | **new gap** |
| F4b | `roleInstanceTemplate` is schemaless, so its old values are NOT pruned | canary | **doc inaccuracy** |

### F2 — fixed, by removing the mechanism rather than patching it

`04ecd11c` deletes `RoleBasedGroupDefaulter`, the `MutatingWebhookConfiguration` from all
three manifest sets, the `mutatingwebhookconfigurations` RBAC, and the caBundle plumbing.
So the whole cost I questioned in the round-1 comment is gone, not just the bug. The
deprecated field is now resolved at read time and nothing latches it:

```
TestL2_R3_KubectlEditOfDeprecatedFieldTakesEffect        PASS
TestL2_R3_TargetedJSONPatchOfDeprecatedFieldTakesEffect  PASS
TestL2_R3_ExplicitConfigStillWinsOverDeprecatedField     PASS
```

The last one matters: removing the defaulter did not remove the documented precedence.

**F6 also resolved as a side effect.** With only one webhook kind left, the `reflect`-based
`patchWebhookCABundle` is gone from `pkg/webhook/certmanager.go` (-89 lines) and `dupl` has
nothing to complain about. `patchOneValidatingWebhook` is back to plain typed code.

### F3 — still present, and that is the accepted design

The four canaries still pass. That is not a regression: the PR chose to accept the
v0.8.0-alpha.x wire break and handle it with a documented migration instead of a decoder
that takes both shapes. They would only flip if such a decoder were added. What round 3
checks is F4, whether the documented procedure is right.

### F4 — the procedure is now correct, with two gaps

`16969e7f` rewrote the Upgrading section into a numbered procedure. It fixes everything the
round-1 comment asked for and more: CRDs first then migrate then roll the controller, a
copy-pasteable merge patch, an explicit warning not to use the JSON patch `move` form, a
note that `<type>` must be re-supplied, a backup step, and the ratcheting correction. It
also explains why a plain `helm upgrade` is the wrong entry point.

Verified against a real API server:

- `TestL2_R3_DocumentedRoleInstanceSetMigrationWorks` — the RoleInstanceSet command works,
  including the doc's point that the fields sit directly under `roleInstanceTemplate` with
  no nested `spec` key (`RoleInstanceTemplate` inlines `RoleInstanceSpec`).
- `TestL2_MergePatchMigrationPreservesThePolicy` — the RoleInstance command still works.

**Gap A (major).** Step 2 lists `RoleBasedGroup` and `RoleBasedGroupSet` among the objects to
migrate but gives a command only for the other two kinds. A RoleBasedGroup's policy lives at
`spec.roles[i].leaderWorkerPattern.restartPolicy`, and for a custom resource a merge patch
replaces the whole `roles` array. Adapting the documented command is the obvious move and it
reports success while dropping the rest of the role:

```
TestL2_R3_Canary_RoleBasedGroupMergePatchDropsPodTemplate
  after the merge patch: template=false size=... restartPolicyConfig=&{None ...}
  -> the API server accepted it; the pod template is gone
```

An indexed JSON patch is safe and is what the doc should carry
(`TestL2_R3_RoleBasedGroupJSONPatchMigrationIsSafe`, kept as a control):

```bash
kubectl patch rolebasedgroup <name> -n <ns> --type=json -p '[
  {"op":"remove","path":"/spec/roles/0/leaderWorkerPattern/restartPolicy"},
  {"op":"add","path":"/spec/roles/0/leaderWorkerPattern/restartPolicyConfig",
   "value":{"type":"<type>"}}]'
```

**Gap B (minor).** The doc says the API server "prunes the old object shape on read for every
affected resource ... so no object can tell you its previous value". `roleInstanceTemplate` is
declared `x-kubernetes-preserve-unknown-fields` with no properties, so it is not pruned and
the values are still readable there:

```
RoleInstanceSet spec.roleInstanceTemplate.restartPolicy after CRD upgrade:
  found=true value={"baseDelaySeconds":30,"type":"RecreateRoleInstanceOnPodRestart"}
```

Harmless direction (it only makes operators do unnecessary work), but "every" and "no object"
are too strong. RoleInstance and RoleBasedGroup are pruned; RoleInstanceSet is not.

### Round 3 verdict

F1, F2, F5 and F6 are resolved. F4's procedure is correct where it is written. `REQUEST_CHANGES`
now rests on Gap A alone: the doc tells operators to migrate RoleBasedGroups and the natural
reading of its own example destroys the role definition. That is a one-command fix.

## Rounds 4-5 — `16969e7f..6fc49a96`

`6fc49a96` "don't provide migration method" replaces the whole migration procedure with
"these two pre-releases are unsupported, delete and recreate". That closes both gaps from
round 3 by removing the thing they were about. Given what round 3's L3 run showed (the
historical ControllerRevision data also carries the old shape, clearing it strands the
RoleInstance revision labels, and repairing that triggers a full recreate), dropping the
procedure is the more honest option.

Two findings this round, plus the answer to the revision question that prompted them.

### R4-a — the upgrade note overstates what is unchanged (minor)

> **From v0.7.0**: no action required. The `restartPolicy` wire format is unchanged.

True for what a user writes on a RoleBasedGroup. Not true for the `roleInstanceTemplate`
the controller writes, which is what ControllerRevision hashes. v0.7.0 stored a string
(`WithRestartPolicy(role.GetRestartPolicy())`), this release stores an object
(`WithRestartPolicyConfig(...)`):

```
v0.7.0 shape -> ris-76c7b4b4d5  {"restartPolicy":"None"}
this release -> ris-766d7fb8f   {"restartPolicyConfig":{"baseDelaySeconds":30,"maxDelaySeconds":600,"type":"None"}}
```

### R4-b — v1alpha1 LWS empty policy inverts, and a new test pins it (minor)

`api/workloads/v1alpha1/rolebasedgroup_types.go` documents the empty value as
pattern-dependent: Recreate for LWS, None for STS/Deploy. `convertRestartPolicyV1alpha1ToV2`
maps every empty value to `None`, and conversion writes it into `restartPolicyConfig.Type`
explicitly, so the v1alpha2 pattern default never applies:

```
converted restartPolicyConfig = &{Type:None BaseDelaySeconds:<nil> MaxDelaySeconds:<nil>}
expected "RecreateRoleInstanceOnPodRestart", got "None"
```

The standalone case still yields `None` correctly, so a fix has to stay pattern-aware. The
mapping is identical on the merge base, so this is not a regression from this PR. What is
new is `TestRoleBasedGroup_ConvertTo_RestartPolicyEmptyMapsToNone`, whose comment states the
inverted result is intended. Agreed with Copilot's independent find on the same spot.

### Does the revision churn restart pods? No.

This was the open question from round 3. Answered with the real RoleInstanceSet controller
running (`test/envtest/testcase/restart_policy/zz_verify_pr424_revision_test.go`, riding the
existing envtest suite), by building a v0.7.0-shaped template and switching it to the new
shape:

```
before          = pr424-churn-84956f689  (rev=1)
currentRevision = pr424-churn-84956f689     <- unchanged
updateRevision  = pr424-churn-7d8bc64449 (rev=2)   <- new revision created
instances       = {pr424-churn-0: c272c512...}     <- same UID, 45s Consistently
```

Both assertions together are the result: the churn is real, and the RoleInstances are not
recreated. It resolves through `CanUpdateInPlace` -> `onlyUpdateRevision` because the pod
template is untouched. Caveat: envtest has no kubelet, so the pods never reach Running
(`pods "pr424-churn-0-main-0" already exists` in the log) and the in-place path does not
finish. The recreate question is answered; the tail of the in-place update is not observed.

That drops the severity from suspected blocker to minor. Observable cost is one revision per
RoleInstanceSet, one `controller-revision-hash` relabel across pods, and
`updateRevision != currentRevision` until it settles.

### Impact surfaces checked and ruled out

- `rollout undo` / `diff` / `history` read the **RoleBasedGroup**-level revision, whose data
  comes from `getRBGPatch` and contains `spec.roles` as the user wrote it. With the defaulting
  webhook gone the controller no longer rewrites user objects, so that shape is stable and
  `ApplyRevision` keeps working. Not affected.
- The `role-revision-*` label on RoleInstanceSet is computed by the RBG controller from the
  role spec and passed down as `revisionKey`. Unchanged user spec means unchanged value.
- `revisionHistoryLimit` (default 10) loses one slot to the churn. Only matters near the cap.

### R5 — a peer review claim that did not hold

A comment on the PR claimed a v0.7.0 role-level `restartPolicy: None` silently flips to the
Recreate default. Checked against the v0.7.0 CRD schema and by running the conversion:

| v0.7.0 CRD version | `spec.roles[].restartPolicy` |
|---|---|
| v1alpha1 | present |
| v1alpha2 | absent (policy is on the pattern) |

```
role-level None     -> restartPolicyConfig={Type:None}      effective "None"
role-level Recreate -> restartPolicyConfig={Type:Recreate}  effective "Recreate"
```

v1alpha2 never had a role-level field to lose, and for v1alpha1 the conversion reads it and
preserves `None`. The direction that does break is the empty-value case in R4-b, which is the
opposite flip and hits roles that left the field unset. Recorded here because the claim is on
the PR and could send a fix in the wrong direction.

### Rounds 4-5 verdict

`COMMENT`. Nothing blocks merge from my side; the two round-1/2 `REQUEST_CHANGES` reviews were
dismissed against the commits that resolved them. R4-b is pre-existing and wants its own
change; the only ask on this PR is not to land a test asserting the inverted behaviour.

## Round 6 — `6fc49a96..1c67becd`

`7878f352 fix(conversion): preserve empty restartPolicy for pattern-aware default` plus
`1c67becd conversion`. Four files: the conversion, the new getter, the conversion tests, and
one doc line. **R4-b is fixed.**

```
ID     POLARITY  LAYER        VERDICT
F2     contract  unit         FIXED
F2     contract  integration  FIXED
R4b    contract  unit         FIXED
R5     contract  unit         FIXED
F3     canary    integration  STILL-PRESENT   (accepted design)
R4a    canary    unit         STILL-PRESENT   (measured not to restart pods)
```

`re-verify.sh` prints `RESULT: not all findings fixed` only because F3 and R4a are canaries
that still pass. Neither is an open defect: F3 is the accepted wire break for the two
unsupported pre-releases, and R4a is revision churn that was measured not to recreate pods.
See `_polarity_note` in the manifest.

### The fix

`convertRestartPolicyV1alpha1ToV2` is now a plain cast, so an empty v1alpha1 value stays
empty and the v1alpha2 getter supplies the pattern-aware default. That is the direction the
round-4 comment asked for. The reverse path switched from `GetRestartPolicy()` to a new
`GetRawRestartPolicyType()`, which resolves through `resolveRestartPolicyConfig` with an empty
defaultType so no pattern default is baked in:

```go
func (r *RoleSpec) GetRawRestartPolicyType() RestartPolicyType {
	if r == nil { return "" }
	if lwp := r.LeaderWorkerPattern; lwp != nil {
		return resolveRestartPolicyConfig(lwp.RestartPolicyConfig, lwp.RestartPolicy, "").Type
	}
	if ccp := r.CustomComponentsPattern; ccp != nil {
		return resolveRestartPolicyConfig(ccp.RestartPolicyConfig, ccp.RestartPolicy, "").Type
	}
	return ""
}
```

Round-trip checked by hand across the three cases: empty stays empty, `None` stays `None`,
`Recreate` stays `Recreate`. StandalonePattern and the nil receiver both return empty, which
matches a v1alpha1 STS/Deploy role that left the field unset.

The test that pinned the wrong behaviour was renamed
(`RestartPolicyEmptyMapsToNone` -> `RestartPolicyEmptyPreserved`), its comment corrected, and
the round-trip table split into `expectedRaw` and `expectedResolved`. Separating the stored
representation from the resolved value is a better shape than what the round-4 comment
suggested.

### Checked, no new problem

- Conversion still materializes a non-nil `RestartPolicyConfig` with an empty `Type`.
  `resolveRestartPolicyConfig` falls through empty `Type` to the deprecated field and then to
  the pattern default, so the effective policy is right, and `GetBaseDelaySeconds()` still
  returns 30 because the config's delay pointers are nil.
- v1alpha1 `Components` roles map to `CustomComponentsPattern`, whose default is Recreate. The
  v1alpha1 field doc only spells out LWS and STS/Deploy, so there is nothing to contradict.
- Only the conversion, the new getter, the tests and one doc line changed. No revision or
  reconciler behaviour was touched, which is why R4a is unchanged.

### Doc

The alpha bullet now says "the released images for these two pre-releases" and adds the blast
radius (one stale object blocks listing the whole resource type). That addresses the concern
about `deploy/helm/rbgs/Chart.yaml` still carrying `0.8.0-alpha.4` on this branch, since the
destructive instruction is now aimed at the released artifacts.

The `From v0.7.0: no action required. The restartPolicy wire format is unchanged.` line is
unchanged, so R4-a stands as the one open comment.

### Round 6 verdict

`COMMENT`. Nothing blocks merge. R4-b is resolved in this PR rather than deferred, so the
follow-up issue is no longer needed. R4-a is a one-line doc wording point.
