Picking this back up. The rebase request from my last review still stands, but I found something while looking at the design that I think changes the scope, so I wanted to get it to you before you spend time on the test coverage I asked for.

Short version: I don't think the scale subresource works as intended yet, and the e2e I asked for is what will surface it.

## The scale subresource's three paths don't agree on a unit (blocking)

`status.replicas` is `int32(len(rbglist.Items))` — a count of **child RoleBasedGroups**. But `status.selector` is `groupset-name=<name>`, which (correctly, and necessarily, given the label propagation) matches **every Pod of every role of every child**. Those are different units, and HPA doesn't treat the selector as informational. From `pkg/controller/podautoscaler/replica_calculator.go` (`GetResourceReplicas`):

```go
podList, err := c.podLister.Pods(namespace).List(selector)   // selects PODS
...
if len(podList) == 0 {
    return 0, 0, 0, time.Time{}, fmt.Errorf("no pods returned by selector while calculating replica count")
}
readyPodCount, unreadyPods, missingPods, ignoredPods := groupPods(podList, ...)
...
if math.Abs(1.0-usageRatio) <= c.tolerance {
    return currentReplicas, ...
}
return int32(math.Ceil(usageRatio * float64(readyPodCount))), ...   // <-- POD count
```

It computes `ceil(usageRatio × readyPodCount)` and writes that into `spec.replicas`, which this CRD defines as a **group** count. So every decision is off by the pods-per-group factor. Driving the real `updateStatus` and applying that formula:

```
groups=2, 2 pods/group -> status.replicas=2, selector matches 4 pods
  usageRatio=1.50 -> HPA writes spec.replicas=6 (groups); correct would be 3    2.0x
groups=2, 5 pods/group -> status.replicas=2, selector matches 10 pods
  usageRatio=1.50 -> HPA writes spec.replicas=15 (groups); correct would be 3   5.0x
groups=3, 1 pod/group  -> 5 vs 5                                                OK
```

The last row is the control: one Pod per group is the only shape where the units coincide. Both shapes your description motivates the PR with — 1P1D and 2P3D — have more than one.

What worries me more than the single bad decision is that the loop has a **wrong fixed point**, so it doesn't correct itself:

```
load needs 8 pods = 4 groups; starting from 2 groups
iter 0: groups=2 pods=4  usageRatio=2.000 -> HPA wants 8 groups
iter 1: groups=8 pods=16 usageRatio=0.500 -> HPA wants 8 groups   <-- settles
```

It stops at 8 groups when 4 is right, and won't scale back down, because `ceil(0.5 × 16) = 8`. Steady state is roughly `1/podsPerGroup` of the target utilization — permanently over-provisioned by that factor.

I don't think this is fixable by adjusting the selector: pointing it at child RoleBasedGroups instead of Pods gives HPA zero Pods and the hard error on the `len(podList) == 0` branch. As far as I can see the options are to make `status.replicas` and the selector agree on one unit, or to drive this with `Object`/`External` metrics (e.g. a per-group metric via KEDA) rather than `Resource` metrics. Happy to think through it with you — and I may well be missing a constraint from your setup, so please push back if so.

## Adopting it breaks HPA until every Pod is re-created (major)

`status.selector` is published the moment the new controller reconciles, but `groupset-name` only reaches Pods when their templates are re-rendered. In between, the selector matches nothing and HPA fails outright with `no pods returned by selector while calculating replica count` — no scaling in either direction. Worth noting in the PR that enabling this rolls every RoleBasedGroupSet-owned Pod, and ideally not publishing the selector before the Pods it selects exist.

## The `maps.Clone(matchLabels)` guards are load-bearing — worth a comment saying so

This one is a compliment with a caveat. The new code in `pod_reconciler.go`:

```go
for k, v := range groupSetLabels { podLabels[k] = v }
```

writes into the **caller's** map, and `constructRoleInstanceSetApplyConfiguration` reuses that same `matchLabels` for `spec.selector.matchLabels` — which is immutable on a RoleInstanceSet. I checked: through the production path the selector stays at exactly 3 keys, so you got this right. But it holds only because every one of the call sites wraps the argument in `maps.Clone`. Passing an uncloned map grows it from 3 keys to 5, and the resulting leak into an immutable selector can only be repaired by deleting and recreating the workload.

Since that's a contract with eight call sites rather than a local invariant, I'd suggest either having `ConstructPodTemplateSpecApplyConfiguration` copy its argument instead of mutating it, or at minimum a comment at the mutation explaining why callers must clone.

## About Copilot's four `maps.Clone` panic comments — you can dismiss them

I checked these properly rather than waving them off, because the mechanism Copilot describes is real: `maps.Clone(nil)` does return nil, and writing to it does panic with `assignment to entry in nil map`.

They're unreachable, though. `.Labels` is populated by `WithLabels(podLabels)` at the end of `ConstructPodTemplateSpecApplyConfiguration`; the generated `WithLabels` allocates whenever `len(entries) > 0`; and all eight production call sites pass `rbg.GetCommonLabelsFromRole(role)`, which is a three-key map *literal* and so is never nil or empty — including for a zero-valued RoleBasedGroup and RoleSpec. I verified all three construct functions run without panicking on label-less templates, with a control that does reproduce the panic when `podLabels` is nil, so the check would have caught it had any caller reached it.

If you want the invariant enforced locally rather than by convention, a one-line `if x == nil { x = map[string]string{} }` does it — the failure mode if someone later adds a caller passing empty labels is a controller crash-loop rather than a validation error. Your call; not blocking.

---

The tests behind all of the above are on a branch on my fork if useful: `verify/pr396-hpa-selector` in `cheyang/rbg`, under `docs/verification/pr396-hpa-selector/`. Two files, no production changes — `go test ./internal/controller/workloads/ -run TestVerifyPR396 -v` and `go test ./pkg/reconciler/ -run TestPR396 -v`.

Still needs the rebase against `main` before it can merge. But I'd sort out the units question first, since it may change what the code looks like.
