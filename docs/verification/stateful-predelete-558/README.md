# Verification — PR #558 "fix(stateful): honor pre-delete lifecycle hooks" (Reviewer A, Claude)

- PR: https://github.com/sgl-project/rbg/pull/558
- Head: `4d82f841585459509f0d5d68e545678e38747864`
- Base: `fd160cc964b0e1438bead4ad7324d0a8f0bf5eac`
- Branch: `verify/stateful-predelete-558-claude` (production code identical to
  the PR head; only the harness below is additive)

## Premise (P0) — CONFIRMED

Claim: stateful rolling updates and scale-down delete a RoleInstance without
waiting for `spec.lifecycle.preDelete`.

Evidence (differential): the PR's own
`TestStatefulRollingUpdateAndScaleDownWaitForPreDeleteHook` grafted onto the
base commit fails both subtests — the instance is deleted in the first
reconcile while the consumer hold label is present
(`results/premise-base-differential.txt`). On the head the same test passes.
The component matches (`statefulmode` owns both delete paths), and the base
has zero PreDelete handling in `statefulmode` (the hook exists only for the
stateless mode).

## What the harness runs

Unit layer only (per round policy — no live/KUBECONFIG layer):

```bash
go test -mod=vendor ./pkg/reconciler/roleinstanceset/statefulmode/ ./pkg/inplace/instance/lifecycle/ \
  -run 'TestVerify558|TestStatefulRollingUpdateAndScaleDownWaitForPreDeleteHook' -count=1
```

`pkg/reconciler/roleinstanceset/statefulmode/verify_predelete_558_test.go`
drives the real `updateStatefulInstanceSet` loop (Phase A–D) with a
fake-client-backed `lifecycleControl` (the fake client enforces
resourceVersion conflicts like the API server), recording instance
create/delete calls.
`pkg/inplace/instance/lifecycle/lifecycle_announce_conflict_test.go` tests
`UpdateRoleInstanceLifecycle` in isolation.

## Observed vs expected

| finding | test | polarity | on head | verdict |
| --- | --- | --- | --- | --- |
| P0 premise | `TestStatefulRollingUpdateAndScaleDownWaitForPreDeleteHook` (PR's own) | contract | PASS | Confirmed (premise fixed by the PR) |
| F1 no PreparingDelete→Normal recovery | `TestVerify558RegretRevertsPreparingDeleteState` | contract | **FAIL** | **Confirmed (reproduced)** |
| F1 impact: regretted instance stuck NotReady | `TestVerify558RegretLeavesInstanceUnavailableCanary` | canary | PASS | Confirmed (documents wrong behavior) |
| F1 impact: OrderedReady scale-up frozen | `TestVerify558RegretOrderedReadyFreezesScaleUpCanary` | canary | PASS | Confirmed (documents wrong behavior) |
| F2 announcement self-conflict | `TestVerify558MarkNotReadyAnnounceSingleCall` | contract | **FAIL** | **Confirmed (reproduced)** |
| F4 guard: release resumes deletion (OrderedReady + MarkNotReady) | `TestVerify558OrderedReadyMarkNotReadyReleaseResumes` | contract | PASS | Confirmed OK (the core claim holds) |

Full-suite check: `go test ./pkg/reconciler/roleinstanceset/... ./pkg/reconciler/roleinstance/... ./pkg/inplace/...`
passes on the head except the two intentional contract reproductions above.

### F1 — no recovery from PreparingDelete ("regret")

The PR adds the PreparingDelete announcement for stateful scale-down and
recreate updates, but no code ever transitions an instance back to `Normal`.
The stateless mode has the counterpart
(`sync.managePreparingDelete`, gated on `!MarkNotReady`); stateful has
nothing. Reproduction: scale 2→1 with a held hook (instance-1 enters
PreparingDelete and the delete is correctly blocked), then scale back 1→2
before release — the instance stays in PreparingDelete forever
(`stateful_instance_set_control.go:1356-1369`, `prepareDelete`).

With `MarkNotReady: true` and the `RoleInstanceCustomReady` readiness gate
declared, the impact is user-visible and permanent:

- the instance's Ready condition is set False by the announcement
  (`readiness.addNotReadyKey` → `SetRoleInstanceNotReadyCondition`) and
  nothing ever clears the `preDeleteHook` gate key;
- `status.ReadyReplicas`/`AvailableReplicas` undercount by one forever;
- under `PodManagementPolicy: OrderedReady`, the stuck-unhealthy ordinal
  blocks creation of all higher ordinals — scale-up is frozen
  (`processReplica`'s health gate).

Note the last sub-impact heals only partially at this controller layer: even
with a Normal transition, the `RoleInstanceReady` condition is recomputed by
the RoleInstance controller (not run in this unit harness), so the OrderedReady
canary stays green here and flips only end-to-end.

### F2 — MarkNotReady announcement self-conflicts

`UpdateRoleInstanceLifecycle(instance, PreparingDelete, true)` first writes
the readiness-gate status (`addNotReadyKey` → status update bumps
resourceVersion) and then issues the lifecycle-label patch preconditioned on
the caller's now-stale resourceVersion (`lifecycle_utils.go:117-127`).
The first announcement therefore always returns a Conflict error; the label
only lands on the next reconcile after the informer catches up. Converging,
but it converts every MarkNotReady announcement into an error requeue plus a
warning event, and the state label is observably late relative to the
not-ready gate. Introduced by this PR (the base used an unconditional
RawPatch). The stateful harness's MarkNotReady tests tolerate exactly one
conflict reconcile for this reason.

### F4 — checked and NOT a bug

With MarkNotReady=true + gate, the blocked instance is Ready=False, but it is
the `firstUnhealthyInstance`, so `processCondemned`'s health gate lets the
deletion resume once the consumer releases the hook — the "OrderedReady stall
after release" concern does not reproduce on this head.

## Proposed fixes

1. F1: add a PreparingDelete→Normal revert in `statefulmode` for instances
   that are back in range / no longer update targets, mirroring stateless
   `managePreparingDelete`. Scope it to instances not pending deletion
   (not condemned, at updateRev, not terminating) so mid-rollout
   announcements are not clobbered.
2. F1 (prerequisite): `executeRoleInstanceNotReadyPolicy` must remove the
   `preDeleteHook` readiness key on the PreparingDelete→Normal transition
   (today `Normal` only removes `preparingUpdateHook`, and callers pass
   `markPodNotReady=false` so the policy is not invoked at all).
3. F2: in `UpdateRoleInstanceLifecycle`, re-read the object after the
   markPodNotReady status write (or issue the label patch before the status
   write) so a single call succeeds.
4. Documentation: state that the hook contract is pre-registration (the
   consumer holds the label/finalizer *before* deletion is attempted); a
   consumer that reacts to the PreparingDelete notification has roughly one
   reconcile to establish the hold. This differs from stateless mode, where
   PreparingDelete is only set once the hold already exists.

## Bite check

`results/bite-check.txt`: probe versions of fixes 1–3 flip both contract
tests green and the unavailable canary red (i.e. must be inverted when
fixed). Probes were reverted afterwards; `git diff 4d82f84` is empty apart
from the additive harness.

## Continuing after the fix

```bash
git clone -b verify/stateful-predelete-558-claude https://github.com/cheyang/rbg.git
cd rbg && bash docs/verification/stateful-predelete-558/scripts/re-verify.sh
```

Polarity table for the next round:

| test | polarity | when fixed |
| --- | --- | --- |
| TestVerify558RegretRevertsPreparingDeleteState | contract | goes green |
| TestVerify558MarkNotReadyAnnounceSingleCall | contract | goes green |
| TestVerify558RegretLeavesInstanceUnavailableCanary | canary | flips red → invert |
| TestVerify558RegretOrderedReadyFreezesScaleUpCanary | canary | flips red only end-to-end |
| TestVerify558OrderedReadyMarkNotReadyReleaseResumes | contract | must stay green |

Kickoff prompt for a fresh agent:

> You are resuming a PR review verification. The harness lives on branch
> verify/stateful-predelete-558-claude of https://github.com/cheyang/rbg.git
> (reviewer fork; PR under review: sgl-project/rbg#558). Read
> docs/verification/stateful-predelete-558/README.md and verify-manifest.json,
> run scripts/re-verify.sh against the current PR head, apply the polarity
> table above, and report Fixed / Still-broken / Partial / Harness-update per
> finding. Do not modify production code on the harness branch.
