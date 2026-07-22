# PR #394 round-3 review comment — POSTED

Target: https://github.com/sgl-project/rbg/pull/394 (head `245884dd`)
Posted: 2026-07-22 as https://github.com/sgl-project/rbg/pull/394#issuecomment-5045974131

---

Thanks for the quick turnaround on `245884dd` — I re-ran the verification harness
against it. **3 of the 4 open issues are resolved (B1, B5, B4b); B2 still
reproduces.**

### ✅ B1 (int64 overflow) — fixed
Checking the overflow *before* the shift is exactly right. The old guard only
covered `rc≥62`, but `base·2^rc` wrapped negative below that; the new
`rc>=62 || base > 1<<(62-rc)` closes the whole window. Verified:
```
calculateRestartDelay(30, 600, 59) = 600   # was 0
calculateRestartDelay(30, 600, 60) = 600   # was 0
calculateRestartDelay(30, 600, 61) = 600   # was 0
calculateRestartDelay(10,   0, 60) = 2147483647   # uncapped saturates, was 0
```

### ✅ B5 (first backoff = base, not 2×base) — fixed
`base·2^(restartCount-1)` with the `restartCount<=0 => 0` guard makes the first
realized backoff equal `baseDelaySeconds` (30s), matching the docs. I flipped my
canary into a contract test asserting `first delay == base`, and it's green on
`245884dd`.

### ✅ B4b (negative delay via API) — still fixed
RoleInstance continues to reject negative `baseDelaySeconds` through the shared
`RestartPolicyConfig` `Minimum=0`. (B4a note: `checkRestartBackoff` still returns
`0s` for a negative base rather than clamping — pure defense-in-depth now that the
CRD gate makes it unreachable via the API; fine to leave, or add a clamp.)

### ❌ B2 (stable-period reset) — still reproduces
The `isReset` → timestamp-based rewrite fixed the round-2 symptom (a stale-*low*
count clobbering a fresh-*high* one), but the reset-to-1 is still not durable.
Instrumented envtest trace (seed `RestartCount=5`, `LastRestartTime` 11 min ago,
then crash):
```
[22] newStatus.RC=1 informer.RC=5 clone.RC=5 changed=true  -> final.RC=1   # reset persisted, API=1
[24] newStatus.RC=5 informer.RC=5 clone.RC=1 changed=true  -> final.RC=5   # CLOBBER
[26] newStatus.RC=1 informer.RC=1 clone.RC=5 changed=false -> final.RC=5   # preserve-live re-cements 5
```
Line 24 is the clobber: a racing reconcile carries `RestartCount=5` (a stale-high
value — `syncRestartTrackingFromAPI` only ever pulls the count *upward*,
`if fresh.RC > instance.RC`) together with a *fresh* `LastRestartTime` (re-stamped by
`updateRestartTracking`, which didn't reset because its synced timestamp was already
recent). `restartTrackingChanged` only inspects the timestamp, so it concludes "trust
newStatus" and writes 5 over the API's 1. Then at line 26 a reconcile that *does* have
the correct `newStatus.RC=1` takes the `changed=false` "preserve the live value"
branch — but the live value it reads back is the clobbered 5, so it re-cements it.
Net: `RestartCount` stays 5 for the whole window (120/120 polls).

The underlying tension is that "reset 5→1" contradicts two preserve-the-maximum
mechanisms that are still in place: (1) `syncRestartTrackingFromAPI` is monotonic-up
only, so it structurally cannot carry a reset downward; and (2) the `updateStatus`
fallback preserves the (cache-backed) live count. A fresh timestamp is being used as a
proxy for "this count is authoritative," but the count and the timestamp can come from
different sources within a reconcile.

Suggestion: treat `(RestartCount, LastRestartTime)` as one timestamp-versioned pair —
**newest timestamp wins, not highest count wins** — in both `syncRestartTrackingFromAPI`
(adopt the fresh count *and* time together when the fresh timestamp is newer, even if
the count is lower) and `updateStatus` (only overwrite RestartCount when the value
paired with the newer timestamp changed; never restore a count whose paired timestamp
is older than newStatus's).

---
Verification harness (tests + scripts + this trace) lives on my fork branch
`cheyang/rbg@verify/pr394-restart-backoff` under
`docs/verification/pr394-restart-backoff/`; `scripts/re-verify.sh` reproduces all of
the above against any ref (`results/b2-clobber-round3-trace.log` for the B2 trace).
