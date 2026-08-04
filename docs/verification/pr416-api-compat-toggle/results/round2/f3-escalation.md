# Round 2 — F3 is unchanged in code, but escalated in consequence

Reviewed head `dcc7104aef6f6e8c16cf4342bb25baf9426c68ab`.

## What round 2 changed, and why it matters here

Round 2 fixed F2a/F2c/F9/F10 by making the UPDATE check delta-only
(`validateNoNewDeprecatedWorkloadTypes`), so a pre-existing group with a deprecated workload
type is no longer frozen. The chart README states the intent plainly:

> Roles that already use a deprecated type are exempt: the check validates the change, not the
> whole object, so an existing group stays writable (and scalable) and the controllers can keep
> reconciling it.

That sentence is the contract this file tests. Admission now honours it. The rest of the
controller does not.

In round 1 this was masked: the webhook rejected the controller's first write, so `Reconcile`
stopped at step 0 and never reached the code below. Round 2 deliberately admits that write —
which means `Reconcile` now runs on to a path that has no gate at all, with the RBAC for those
types removed by this same PR. **The defect is the same; round 2 is what makes it reachable.**

## Three ungated arms, all still present at this head

### 1. The workload-reconciler factory has no gate

`getOrCreateWorkloadReconciler` → `reconciler.NewWorkloadReconciler` takes no flag. Proven by
`TestVerifyPR416_F3_DeprecatedReconcilerStillBuiltWhenDisabled`, which builds the reconciler
with `enableDeprecatedWorkloadTypes: false`:

```
F3 REPRODUCED: still built a *reconciler.DeploymentReconciler   for apps/v1/Deployment
F3 REPRODUCED: still built a *reconciler.StatefulSetReconciler  for apps/v1/StatefulSet
F3 REPRODUCED: still built a *reconciler.LeaderWorkerSetReconciler for leaderworkerset.x-k8s.io/v1/LeaderWorkerSet
control_roleinstanceset_still_built  PASS
```

The control passes, so the factory works and this is not "the factory is broken".

### 2. The LeaderWorkerSet watch re-arms itself at runtime

`SetupWithManager` correctly skips `Owns(&lwsv1.LeaderWorkerSet{})` when the toggle is off
(`rolebasedgroup_controller.go:1056`). But `getOrCreateWorkloadReconciler` calls
`dynamicWatchCustomCRD(ctx, workloadSpec.Kind)` at line 615, and that function
(line 1609) re-registers the very same watch with **no compatibility check**:

```go
case utils.GetLwsGVK().Kind:
    _, lwsExist := watchedWorkload.Load(utils.LwsCrdName)
    if !lwsExist {
        watchedWorkload.LoadOrStore(utils.LwsCrdName, struct{}{})
        runtimeController.Owns(&lwsv1.LeaderWorkerSet{}, builder.WithPredicates(WorkloadPredicate()))
```

So the claim "the controller stops watching these resources" (chart README, values.yaml comment,
and the `--enable-deprecated-workload-types` flag help text) does not hold for LeaderWorkerSet:
the watch is skipped at startup and then re-armed on the first reconcile of an LWS-backed role —
against a ClusterRole that no longer grants `list`/`watch` on `leaderworkersets`.

### 3. `cacheOptions` drops the selector instead of keeping it

```go
if enableDeprecatedWorkloadTypes {
    byObject[&appsv1.StatefulSet{}] = cache.ByObject{Label: keyExistsSelector}
    byObject[&appsv1.Deployment{}]  = cache.ByObject{Label: keyExistsSelector}
}
```

`ByObject` is per-type configuration, not an allowlist. Removing the entry does not prevent an
informer for that type from starting — it only removes the bound on one that does. Combined
with arm 1 (a reconciler IS built and DOES issue cached reads), the first read of a Deployment
starts an **unbounded, cluster-wide** informer, which then cannot establish its watch because
this PR removed `list`/`watch` from the ClusterRole. Keeping the selector in both modes costs
nothing.

## Net effect

With `controller.deprecatedWorkloadTypes.enabled=false` and a pre-existing StatefulSet-backed
RoleBasedGroup, the object is admitted for writes (round 2's fix works) and then fails during
reconcile on `Forbidden` against `statefulsets`, with no terminal condition and no event — the
same silent retry loop round 1 described, moved one step later.

So the README's "the controllers can keep reconciling it" is not true today. The admission half
of that promise landed; the RBAC and watch halves did not.
