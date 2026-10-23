# PR #509 verification — 0.9.0 v1alpha1 deprecation window

PR: https://github.com/sgl-project/rbg/pull/509
Reviewed head: `2c9cce6a73e34e337c9d0ab82a7472a3c910c406` (base `ecdfd6e601de64adc0d0892c7ad05f75e85873a8`)
Reviewer: Codex (debate Reviewer B). Production code untouched; this branch adds tests + docs only.

## Premise (P0) — CONFIRMED

The PR claims: v1alpha1 writes today are silently accepted, deprecated workload
types (Deployment/StatefulSet/LeaderWorkerSet) are fully accepted in the default
mode, and 0.9.0 needs a warning + new-use gate with a compatibility window.

Verified against the **base** branch: `RoleBasedGroupValidator` with
`enableDeprecatedWorkloadTypes=true` admits a brand-new deprecated role with no
warning and no error (results/premise-base.out), and the base registers no
v1alpha1 admission webhook at all. The premise is real and the patched
component (admission webhooks) matches the problem.

## Findings and observed-vs-expected

| ID | Severity | Claim | Layer | Expected (PR's own contract) | Observed on head | Verdict |
|----|----------|-------|-------|------------------------------|------------------|---------|
| F1 | blocker | Compat mode breaks scale-up / child re-creation of pre-existing RoleBasedGroupSets that use deprecated workload types | unit + envtest | "Existing roles continue to reconcile" → child RBG create admitted | `admission webhook "vrolebasedgroup.kb.io" denied the request: ... new uses are not accepted` | **Confirmed** |
| F2 | major | Warning-only v1alpha1 webhooks intercept DELETE with failurePolicy=Fail; webhook outage blocks v1alpha1 deletes | envtest (outage simulated) | warning-only gate must not block deletes | `failed calling webhook ... connection refused` → delete rejected | **Confirmed** |
| F3 | minor | v1alpha1 validator overrides the authoritative request oldObj with a cache-backed persisted read; stale cache grants the exemption (policy bypass window) / errors fail the update | unit (fake client, deterministic) | reintroducing a deprecated type into a migrated role is rejected | admitted when persisted read is stale; rejected when fresh (control) | **Confirmed (mechanism), race-window bounded** |
| F4 | minor | Compat-mode rejection error tells users to "re-enable ... =true" — the flag value already in effect | unit | hint must not advise the current value | hint present in the rejection | **Confirmed** |

Supporting (green) evidence on head:
- E1 control: updating an existing legacy child RBG is admitted (exemption works through the real API server).
- E3: a defaulted-workload v1alpha1 object stays writable in compat mode end-to-end (through the real conversion webhook), and the probe webhook captured the admission `oldObject` **carrying** the schema default (`"kind":"StatefulSet"`) — contradicting the `deprecation_webhook.go` comment that justifies the persisted read ("oldObj ... may not carry v1alpha1 schema defaults").
- E4: v1alpha1 create returns the deprecation admission warning; new defaulted-workload creates are rejected. The shipped mechanism works as documented for direct user writes.

## Harness-bites check

Each contract test was flipped green by a temporary fix-direction patch to
production code, then reverted (results/bites-check.out):
- F1/F3: permissive-gate patch / trust-request-oldObj patch → green.
- F4: drop the shared hint from the compat-mode rejection → green.

## How to run

Unit layer (fast, no cluster):

```bash
go test ./api/workloads/v1alpha1/ ./api/workloads/v1alpha2/ -run 'TestPR509' -v
```

Integration layer (envtest; needs `bin/setup-envtest`, see `make setup-envtest`):

```bash
KUBEBUILDER_ASSETS="$(bin/setup-envtest use 1.31.0 --bin-dir bin -p path)" \
  go test ./test/envtest/testcase/verifypr509/ -v -timeout 10m
```

The integration suite uses `ContinueOnFailure` so a red contract spec does not
hide the green control specs.

## Continuing after the fix

```bash
bash docs/verification/pr-509-deprecation-window-codex/scripts/re-verify.sh
```

(no ref needed: it fetches the current PR head from `manifest.pr`; the delta
since the last round comes from `.last-reviewed`). All findings are contract
polarity: a finding is **Fixed** when its tests go green. If a fix reshapes the
code (e.g. replaces the persisted read with an APIReader), treat a compile
break as Harness-update, not proof of fix.

## Proposed fixes (reviewer suggestion, not applied)

- F1: exempt controller-created children of an existing RoleBasedGroupSet whose
  template already carries the same deprecated type (owner-reference-aware
  exemption), or have the RBGSet controller stop creating legacy-typed children
  while keeping the set writable (document the semantic break explicitly if
  intentional).
- F2: drop `delete` from the six v1alpha1 deprecation webhooks' operations (the
  handler never rejects deletes), or use `failurePolicy: Ignore` for them.
- F3: trust the admission request's oldObj (E3 shows it carries schema
  defaults); if belt-and-braces is wanted, use `mgr.GetAPIReader()` (direct
  read) instead of the cache.
- F4: only append `deprecatedWorkloadTypeHint`'s re-enable advice in the
  flag=false path; the compat-mode message should point at RoleInstanceSet
  migration only.
