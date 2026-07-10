# KEP-380: Improve RoleInstance Restart Policy (Diagnosis Window + Loop Breaker)

<!-- toc -->
- [Background and Motivation](#background-and-motivation)
- [Goals](#goals)
- [Non-Goals](#non-goals)
- [Design Proposal](#design-proposal)
    - [Why not preserve/quarantine the crashed pod](#why-not-preservequarantine-the-crashed-pod)
    - [User Stories](#user-stories)
- [Design Details](#design-details)
    - [API Changes](#api-changes)
    - [Status Changes](#status-changes)
    - [Controller Logic](#controller-logic)
    - [Backoff / reset semantics](#backoff--reset-semantics)
    - [Interaction with existing mechanisms](#interaction-with-existing-mechanisms)
    - [Backward Compatibility](#backward-compatibility)
    - [Test Plan](#test-plan)
- [Alternatives Considered](#alternatives-considered)
- [Related](#related)
<!-- /toc -->

## Background and Motivation

The default restart policy `RecreateRoleInstanceOnPodRestart` (used by `LeaderWorkerPattern`
and `CustomComponentsPattern`) responds to any pod crash by deleting and recreating **all**
pods in the RoleInstance — leader plus all workers — rebuilding the group from scratch.

Whole-group rebuild is the *correct* semantics for tightly-coupled collective workloads
(e.g. NCCL / `torch.distributed`): when any rank process dies, the communicator is broken
and every rank must restart and re-rendezvous together. Standard NCCL cannot have a single
rank rejoin an existing communicator, so "recreate all" is the right primitive and this KEP
does **not** change it.

However, the current implementation (`pkg/reconciler/roleinstance/sync/instance_scale.go`,
`shouldRecreateInstance` → `calculateDiffsWithExpectation`) has two concrete deficiencies,
reported in issue #363:

1. **Loss of crash observability.** The crashed pod is deleted (as part of `toDeletePod: allPods`)
   before an operator can inspect it, so logs (`kubectl logs -p`), exit code, and failure
   reason are gone.
2. **Infinite rebuild loop.** If the crash cause persists (bad config, code bug, bad node),
   the controller rebuilds the whole group again immediately, with **no delay, no backoff,
   and no limit** — burning GPUs on an endless reschedule/re-init cycle.

## Goals

- Give operators a window to diagnose the crashed pod before the group is rebuilt.
- Always leave a durable, machine-readable trace of a crash even when nobody is watching.
- Break the infinite rebuild loop with a bounded number of consecutive rebuilds, then stop
  and surface the state for human/higher-level intervention.
- Keep the change **small and orthogonal** — no new per-pod recovery state machine, no
  polymorphic API field. Preserve the whole-group-rebuild semantics that NCCL depends on.
- Backward compatible: default behavior is effectively unchanged for existing users.

## Non-Goals

- Changing the "recreate the whole RoleInstance" semantics of
  `RecreateRoleInstanceOnPodRestart`. This is required by collective workloads.
- Per-role / per-pod selective rebuild ("only rebuild workers when the leader crashes").
  Out of scope; the policy is deliberately all-or-nothing.
- Preserving the crashed pod as a live group member (see
  [Why not preserve/quarantine the crashed pod](#why-not-preservequarantine-the-crashed-pod)).
- Handling `RestartPolicy=None` — unchanged; a Failed pod is replaced pod-by-pod as today.
- Cross-node causal ordering of cascading failures ("which rank died *first*"). We record
  evidence for all crashers; we do not claim to identify the root cause.

## Design Proposal

Add two independent, small levers on top of the existing whole-group-rebuild path, plus a
free crash-forensics event:

| Lever | Field | Default | Solves |
|---|---|---|---|
| **Diagnosis window** | `rebuildDelaySeconds` | `15` | observability (live) |
| **Loop breaker** | `maxConsecutiveRebuilds` | `0` = unlimited (opt-in) | infinite loop |
| **Forensics event** | — (always on) | — | observability (unattended) |

> **Default is infinite rebuild.** The loop breaker defaults to off
> (`maxConsecutiveRebuilds=0`) to preserve the existing "rebuild forever" behavior;
> operators opt in by setting a positive limit. The only default behavior change is the
> 15s diagnosis window plus the always-on crash-forensics event.

- **Diagnosis window.** When a crash is detected, wait `rebuildDelaySeconds` before deleting
  and recreating the group. During this window the crashed pod object still exists, so
  `kubectl logs -p`, `kubectl describe`, and container exit info are all reachable. Setting a
  large value (e.g. `1500`) turns this into a "hold for debugging" mode.
- **Loop breaker.** Count consecutive rebuilds that did not result in a sustained-Ready
  instance. Once the count reaches `maxConsecutiveRebuilds`, stop rebuilding and set a
  terminal `RestartBackoffExhausted` condition + a Warning event. The counter resets only
  after the instance has been Ready for a stability window (so a flapping crashloop cannot
  keep resetting it).
- **Forensics event.** Independent of the window: on every entry into rebuild, emit a Warning
  event and record `Status.LastCrashInfo` capturing, for the crashed pod(s), the pod name,
  node, container, exit code, reason, and finish time — sorted by termination time, the
  earliest flagged as `likelyRootCause`. This survives pod deletion and covers the
  unattended case where the diagnosis window closes before anyone looks. Full container logs
  are expected to come from the cluster's log-aggregation stack.

### Why not preserve/quarantine the crashed pod

An earlier direction (see issue #380) proposed keeping the crashed pod alive as a group
member ("one rebuild + hold"), or detaching it into quarantine, so `kubectl logs -p` keeps
working indefinitely. We reject both as the default:

- **Preserving a pod pins a GPU.** For GPU training the accelerator is the scarce resource.
  Holding a dead pod's GPU for minutes (or a TTL) means recovery needs N+1 GPUs and the fresh
  group may not schedule at all.
- **Keeping a half-alive rank fights NCCL.** A preserved pod is restarted in place on
  kubelet's CrashLoopBackOff clock (up to ~5 min), while the freshly recreated siblings form a
  new communicator and block at rendezvous waiting for it — they time out, crash, and (in the
  "hold" model) get ignored by the recovery lock. This can deadlock recovery.
- **"Which pod crashed first" is unreliable** in a cascading, cross-node, reconcile-sampled
  failure, so selecting *one* pod to keep is fragile.

The diagnosis-window + forensics-event approach gives the same observability value (a real
window for live inspection, plus a durable trace for post-hoc) **without** pinning GPUs,
without a per-pod recovery state machine, and without the rendezvous deadlock. The
selection problem disappears because we record *information about all crashers* instead of
*keeping one pod*.

### User Stories

1. *SRE debugging a crashloop.* Operator sets `rebuildDelaySeconds: 1500` on the role, waits
   for the next crash, and has 25 minutes to `kubectl logs -p` / `describe` the crashed pod
   before the group is rebuilt. Reverts to default afterward.
2. *Persistent misconfiguration.* A bad image crashes on startup every time. After
   `maxConsecutiveRebuilds` (opt-in; e.g. set to 10) rebuilds, the controller stops, sets
   `RestartBackoffExhausted`, and emits an event. The operator is alerted instead of the
   cluster silently thrashing GPUs forever.
3. *Unattended night failure.* A pod OOMs at 3am. Nobody is watching; the diagnosis window
   closes and the group recovers. In the morning the operator reads `Status.LastCrashInfo`
   and the recorded event to see which pod/node/exit-code caused it.

## Design Details

### API Changes

No change to the `RestartPolicy` field type (it stays the `RestartPolicyType` string — no
`IntOrString`-style polymorphic field, so all typed/generated clients keep working). Add a
new **sibling optional struct** `RestartPolicyConfig`, present on both patterns and
propagated to `RoleInstanceSpec`:

```go
// RestartPolicyConfig tunes the RecreateRoleInstanceOnPodRestart behavior.
// It has no effect when RestartPolicy is None.
type RestartPolicyConfig struct {
    // RebuildDelaySeconds is the delay between detecting a pod crash and recreating
    // the whole RoleInstance. During this window the crashed pod is preserved so
    // operators can inspect it (kubectl logs -p). Larger values act as a hold-for-debug.
    // +kubebuilder:validation:Minimum=0
    // +optional
    RebuildDelaySeconds *int32 `json:"rebuildDelaySeconds,omitempty"` // default 15

    // MaxConsecutiveRebuilds is the number of consecutive whole-group rebuilds tolerated
    // before the controller stops and marks the instance with RestartBackoffExhausted.
    // 0 means unlimited (legacy behavior). The counter resets after the instance has been
    // Ready for a stability window.
    // +kubebuilder:validation:Minimum=0
    // +optional
    MaxConsecutiveRebuilds *int32 `json:"maxConsecutiveRebuilds,omitempty"` // default 0 (unlimited)
}
```

Added to `LeaderWorkerPattern`, `CustomComponentsPattern`, and `RoleInstanceSpec` as
`RestartPolicyConfig *RestartPolicyConfig`. Helper accessors on `RoleSpec` and on
`RoleInstance` supply the defaults (`GetRebuildDelaySeconds`, `GetMaxConsecutiveRebuilds`),
mirroring the existing `GetRestartPolicy` pattern. Deepcopy and CRDs regenerated via
`make generate manifests`; the client-go applyconfiguration `WithRestartPolicyConfig`
builder is generated and wired in `roleinstanceset_reconciler.go` next to
`WithRestartPolicy`.

### Status Changes

Add to `RoleInstanceStatus`:

```go
// ConsecutiveRestarts counts whole-group rebuilds not yet followed by a sustained-Ready
// instance. Reset to 0 after the stability window. Drives MaxConsecutiveRebuilds.
// +optional
ConsecutiveRestarts int32 `json:"consecutiveRestarts,omitempty"`

// LastRestartTime is when the most recent whole-group rebuild was executed.
// +optional
LastRestartTime *metav1.Time `json:"lastRestartTime,omitempty"`

// LastCrashInfo records evidence about the crash(es) that triggered the most recent
// rebuild, for post-hoc diagnosis. Survives pod deletion.
// +optional
LastCrashInfo *RoleInstanceCrashInfo `json:"lastCrashInfo,omitempty"`
```

```go
type RoleInstanceCrashInfo struct {
    // ObservedTime is when the controller recorded this crash info.
    ObservedTime metav1.Time `json:"observedTime"`
    // Pods is the list of crashed pods that triggered the rebuild, sorted by
    // termination time (earliest first).
    Pods []CrashedPod `json:"pods,omitempty"`
}

type CrashedPod struct {
    PodName        string       `json:"podName"`
    NodeName       string       `json:"nodeName,omitempty"`
    Container      string       `json:"container,omitempty"`
    ExitCode       int32        `json:"exitCode,omitempty"`
    Reason         string       `json:"reason,omitempty"`
    FinishedAt     *metav1.Time `json:"finishedAt,omitempty"`
    RestartCount   int32        `json:"restartCount,omitempty"`
    // LikelyRootCause marks the earliest-terminated crasher. Best-effort, not a guarantee.
    LikelyRootCause bool `json:"likelyRootCause,omitempty"`
}
```

Add a new condition type `RoleInstanceRestartBackoffExhausted = "RestartBackoffExhausted"`,
set `True` when the loop breaker trips.

### Controller Logic

The change is localized to the recreate branch of `calculateDiffsWithExpectation` and its
helpers in `instance_scale.go`. Pseudocode:

```
on reconcile of an instance with RestartPolicy == RecreateRoleInstanceOnPodRestart:

  crashers := findCrashers(allPods)              // Failed, or RestartCount>0 not from in-place update
  if len(crashers) == 0:
      // happy path: maybe reset the loop counter
      if instanceReady && now - status.LastRestartTime > stabilityWindow && status.ConsecutiveRestarts > 0:
          status.ConsecutiveRestarts = 0
          clear RestartBackoffExhausted
      return normal diff

  if !shouldRecreateInstance(...):               // existing gates: was-ready, not updating, revs converged
      return normal diff

  // ---- loop breaker ----
  if maxConsecutiveRebuilds > 0 && status.ConsecutiveRestarts >= maxConsecutiveRebuilds:
      setCondition(RestartBackoffExhausted, True, count)
      emit Warning "RestartBackoffExhausted"
      return normal diff                         // STOP rebuilding; hand off to human

  // ---- diagnosis window ----
  if Restarting condition not yet set:
      status.LastCrashInfo = captureCrashInfo(crashers)   // durable trace
      emit Warning "PodCrashDetected" with crash info
      setRestartingCondition(instance)                    // records detection time
      requeueAfter(rebuildDelaySeconds)
      return normal diff                                  // hold; pod still inspectable
  if now < RestartingCondition.LastTransitionTime + rebuildDelaySeconds:
      requeueAfter(remaining)
      return normal diff

  // ---- execute whole-group rebuild (unchanged behavior) ----
  status.ConsecutiveRestarts++
  status.LastRestartTime = now
  emit Normal "ReCreateInstance"
  return expectationDiff{ toDeletePod: allPods }
```

`findCrashers` generalizes the existing `containerRestarted` / PodFailed checks and returns
the crashed pods (respecting the `restart-trigger-policy: Ignore` annotation and in-place
update exclusions already implemented). `captureCrashInfo` reads
`Status.ContainerStatuses[*].{State,LastTerminationState}.Terminated` for exit code / reason /
finishedAt and sorts by `FinishedAt`.

### Backoff / reset semantics

- The diagnosis window doubles as the minimum spacing between rebuilds. `rebuildDelaySeconds`
  is a *fixed* delay, not exponential — simplicity over sophistication; the loop breaker,
  not the delay, is what bounds a persistent failure.
- `ConsecutiveRestarts` increments only when a rebuild is actually executed.
- Reset to 0 requires the instance to be Ready **and** `now - LastRestartTime > stabilityWindow`
  (a constant, e.g. `max(60s, 2 × rebuildDelaySeconds)`). This hysteresis prevents a
  crashloop that briefly flips Ready from perpetually resetting the counter — the specific
  failure mode that would defeat the loop breaker.

### Interaction with existing mechanisms

- The in-memory `restartingCache` and the `Restarting` condition keep their current role as
  the **in-flight guard** (don't double-fire while a rebuild's delete/create is executing).
  The new counter/delay is a layer above them ("should we start the next cycle, and when").
- The existing "clear `Restarting` on Ready" logic in `instance_status.go` is unchanged.
  The new counter reset is gated additionally on the stability window.
- `RestartBackoffExhausted` is cleared whenever `ConsecutiveRestarts` resets to 0.

### Backward Compatibility

- `RestartPolicy` field type is unchanged → no break for typed/generated Go clients, no
  conversion changes for v1alpha1↔v1alpha2.
- `RestartPolicyConfig` is optional; when absent, defaults apply:
  `rebuildDelaySeconds=15`, `maxConsecutiveRebuilds=0` (unlimited).
- The default keeps infinite rebuild. The only observable default change is the 15s
  diagnosis window before each rebuild (down from immediate) plus the always-on
  crash-forensics event. The loop breaker is opt-in: set `maxConsecutiveRebuilds` to a
  positive value to bound retries. Users who want the exact pre-existing timing can also
  set `rebuildDelaySeconds: 0`.

### Test Plan

Unit (`instance_scale_test.go`):
- crash detected → `Restarting` set, no delete within the window, `requeueAfter` returned.
- window elapsed → whole-group delete diff produced, `ConsecutiveRestarts` incremented.
- `ConsecutiveRestarts` reaches `maxConsecutiveRebuilds` → no delete, `RestartBackoffExhausted`
  set, Warning event.
- `maxConsecutiveRebuilds=0` → never trips (legacy loop).
- sustained-Ready past stability window → counter resets, condition cleared.
- flapping Ready inside the window → counter does **not** reset.
- `captureCrashInfo` orders by `FinishedAt`, flags earliest `likelyRootCause`, respects
  `restart-trigger-policy: Ignore`.

Envtest (`test/envtest/testcase/restart_policy/`):
- end-to-end: crashing pod → delayed rebuild → recovery clears state.
- persistent crash → backoff exhausted terminal state.

## Alternatives Considered

1. **Preserve the crashed pod ("one rebuild + hold") / quarantine** — rejected; pins GPUs and
   deadlocks NCCL rendezvous. See
   [Why not preserve/quarantine the crashed pod](#why-not-preservequarantine-the-crashed-pod).
2. **`RestartPolicyOrString` polymorphic field** (string-or-struct) — rejected; breaks typed
   Go consumers and generated applyconfigurations, complicates conversion and CEL, mirrors the
   `IntOrString` legacy pain. A sibling optional struct achieves the same with none of the cost.
3. **Per-pod recovery state machine with exponential backoff and a single tracked crashed
   pod** — rejected as over-scoped; cannot express cascading multi-pod failures, entangles
   three sources of truth, and the level(entry)/edge(exit) mismatch can reset backoff so the
   loop breaker never fires. The counter + stability-window reset here is smaller and robust.
4. **Exponential backoff instead of a fixed delay** — deferred. The fixed delay + hard cap is
   simpler and sufficient; exponential backoff can be layered in later behind the same fields
   if a need appears.

## Related

- Issue #363 — original report: lost observability + infinite recreate loop.
- Issue #380 — RFC discussion this KEP supersedes.
- KEP-173 (inactive-pod-handling) — the two-level Failed-pod handling this builds on.
