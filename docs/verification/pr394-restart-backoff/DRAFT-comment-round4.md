# PR #394 round-4 review comment — POSTED

Target: https://github.com/sgl-project/rbg/pull/394 (head `422b9394`)
Posted: 2026-07-23 as https://github.com/sgl-project/rbg/pull/394#issuecomment-5054029052

---

Verified the B2 fix — it works. I re-ran the harness against `422b9394`; the
stable-period reset now survives (seed `RestartCount=5`, `LastRestartTime` 11 min
ago, crash → count resets to 1 and stays 1 for the whole window, where it was
previously stuck at 5).

The two-part fix matches the failure mode exactly:
- `syncRestartTrackingFromAPI` now adopts `(RestartCount, LastRestartTime)` as a
  versioned pair — newest timestamp wins — so a reset is carried downward instead of
  being blocked by the old monotonic-up guard.
- `updateStatus` now requires the fresh timestamp to differ from *both* the informer
  and the live value, so a `syncRestartTrackingFromAPI` pass-through is no longer
  mistaken for a real `updateRestartTracking` and can't re-clobber the reset.

Status of the four issues I raised: **B1, B5, B2, B4b all resolved.** The only
leftover is B4a — `checkRestartBackoff` still returns `0s` for a negative
`baseDelaySeconds` rather than clamping — but that's pure defense-in-depth now that
B4b rejects negatives at the CRD, so it's unreachable through the API. Fine to leave
as-is, or add a one-line clamp if you want belt-and-suspenders.

Nothing else in the delta stands out. Thanks for working through all of these.

(One micro-note, non-blocking: `syncRestartTrackingFromAPI` compares timestamps with
`After`, and `metav1.Time` is second-granularity — so a hypothetical reset landing in
the *same second* as the previously-known `LastRestartTime` wouldn't be adopted by the
pair branch. In practice the reset threshold is ≥10 min, so the timestamps are always
well separated; noting it only for completeness.)

---
Verification harness (tests + scripts + traces) lives on my fork branch
`cheyang/rbg@verify/pr394-restart-backoff` under
`docs/verification/pr394-restart-backoff/`; `scripts/re-verify.sh` reproduces the
current all-green-except-B4a state against any ref.
