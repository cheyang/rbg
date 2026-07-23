# DRAFT — PR #394 round-2 review comment (NOT yet posted)

Target: https://github.com/sgl-project/rbg/pull/394 (head `ad7cd462`)

---

Thanks for the refactor into `RestartPolicyConfig` — sharing one struct across the
RBG patterns and `RoleInstanceSpec` is the right shape, and it fixes one of the four
issues cleanly. I re-ran the verification harness against `ad7cd462`. Summary: **1 of
4 resolved; B1 is a partial fix; B2 and B5 still reproduce.**

### ✅ B4 (negative delay) — fixed via CRD validation
`RoleInstanceSpec.RestartPolicy` now uses the shared `RestartPolicyConfig`, which
carries `+kubebuilder:validation:Minimum=0`. The apiserver now rejects a negative
`baseDelaySeconds` on **RoleInstance** too, not just RBG:
```
RoleInstance ... spec.restartPolicy.baseDelaySeconds in body should be greater than or equal to 0
```
The old asymmetry is gone. (The code path `checkRestartBackoff` still doesn't clamp a
negative value defensively — it returns `0s` = "no backoff" — but with the CRD gate in
place that's now unreachable through the API. Fine to leave as-is, or add a clamp for
defense-in-depth.)

### ⚠️ B1 (int64 overflow) — partially fixed; a hole remains
The new guard
```go
if restartCount >= 62 { if maxDelaySeconds > 0 { return maxDelaySeconds }; return 0x7FFFFFFF }
```
closes `rc≥62`, but the comment's premise ("with `maxDelay>0` the cap triggers first,
so this branch is only reachable when `maxDelay==0`") doesn't hold. The cap check
`delay > maxDelay` needs `delay` to still be **positive**, but `int64(base) << rc`
wraps negative once `base·2^rc ≥ 2^63`. For the default `base=30` that's `rc=59` —
three counts *below* the guard:
```
calculateRestartDelay(30, 600, 59) = 0   // want 600
calculateRestartDelay(30, 600, 60) = 0   // want 600
calculateRestartDelay(30, 600, 61) = 0   // want 600
calculateRestartDelay(30, 600, 62) = 600 // guard kicks in here
calculateRestartDelay(10,   0, 60) = 0   // uncapped path, want saturate
```
A `0` return makes `checkRestartBackoff` treat it as "no backoff", so a long-lived
crashloop that reaches ~59 restarts loses backoff. The guard only covers `base≤3`;
any `base≥4` has a live window. Suggest capping on `restartCount` (or testing the
multiply for overflow) **before** the shift, e.g.:
```go
// once base*2^rc would exceed maxDelay/int32max, we're already saturated
if maxDelaySeconds > 0 && (restartCount >= 31 || int64(base)<<restartCount > int64(maxDelaySeconds)) {
    return maxDelaySeconds
}
```

### ❌ B2 (stable-period reset) — still reproduces, and I traced why
The `isReset` guard in `updateStatus` does fire correctly for the reconcile that
performs the reset — the counter *does* briefly reset `5→1` and persist. But it's then
clobbered back by a **stale-cache reconcile ~6 ms later**. Instrumented envtest trace
(seed `RestartCount=5`, `LastRestartTime` 11 min ago, then crash):
```
DEBUG-RECREATE guarded=true count=5
DEBUG-URT enter count=5 lrt=…16:36:41       # 11 min ago
DEBUG-URT exit  count=1                       # reset works
DEBUG-US newStatus=1 instance=5 liveClone=5 isReset=true  -> final=1   # persisted OK
DEBUG-US newStatus=5 instance=5 liveClone=1 isReset=false -> final=5   # <-- clobbered
DEBUG-US newStatus=5 instance=5 liveClone=5 isReset=false -> final=5   # stuck at 5
```
The second reconcile's `newStatus.RestartCount` is still `5` (computed from a lagging
informer), while `liveClone` read fresh from the API is already `1`. Then:
```go
isReset := newStatus.RestartCount < instance.Status.RestartCount  // 5 < 5 => false
clone.Status = *newStatus                                          // copies stale 5 over fresh 1
if !isReset && liveRestartCount > newStatus.RestartCount { … }     // 1 > 5 => false, no rescue
```
So the reset isn't durable: any reconcile firing inside the informer-sync window
re-persists the pre-reset count. `isReset` guards only the rollback direction
(stale-low over fresh-high); it doesn't protect a fresh-low (reset) from a stale-high
`newStatus`. Result: `RestartCount` stays `5` for the whole 60 s window (120/120 polls).
A robust fix probably needs to (a) base the reset decision on the fresh API value, and
(b) only overwrite `RestartCount` when it actually changed this reconcile, rather than
blindly `clone.Status = *newStatus`.

### ❌ B5 (first backoff is `2×base`) — unchanged
`updateRestartTracking` still increments `RestartCount` to ≥1 *before* the first
backoff check, so the smallest realized delay is `calculateRestartDelay(base,max,1) =
2×base`; the documented "first retry after `base`" never occurs at runtime. Either use
`1 << (restartCount-1)`, or update the doc/PR table to say `2×base`.

---
Verification harness (tests + scripts + this trace) lives on my fork branch
`cheyang/rbg@verify/pr394-restart-backoff` under
`docs/verification/pr394-restart-backoff/`; `scripts/re-verify.sh` reproduces all of
the above against any ref.
