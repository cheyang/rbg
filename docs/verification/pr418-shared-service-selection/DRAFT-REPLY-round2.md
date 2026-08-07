# Draft replies — round 2 (NOT POSTED)

Three comments, all follow-ups on threads already open. Nothing here is published until
explicitly confirmed.

Suggested order: **Reply 3 first** (it settles the default decision), then Reply 2 (time-sensitive,
the author is about to rewrite the KEP), then Reply 1.

---

## Reply 1 — thread on `api/workloads/v1alpha2/helper.go:279`

Answers my own reachability question and converts it into a concrete proposal.

```text
Answering my own question first: I don't think `apps/v1/StatefulSet` + `leaderWorkerPattern` is
reachable in practice. `convertRoleV1alpha1ToV2` only builds a `LeaderWorkerPattern` when
`src.LeaderWorkerSet != nil`, and StatefulSet falls through to `StandalonePattern`, so the v1alpha1
conversion can't produce the combination. That same function also says "New v1alpha2 RBGs should
NOT set this annotation". It takes a hand-written deprecated annotation to get there, so on its own
this is a TODO rather than something to block on.

It stops being optional if you want the default recorded on the object, which is what I'd argue
for. I've changed my mind on the earlier "keep `nil -> All`" ask. The KEP's own motivation
convinced me that `LeaderOnly` is the right default, and it makes the case better than the PR
description does: worker Pods may only run a dummy API server, so the old all-Pods default was
routing requests at non-functional endpoints. All six `leaderWorkerPattern` examples in the repo
leave the field unset, so the topology that was mis-served is the one everybody actually deploys.
That's a correction rather than a preference, and I'd keep it.

What I'd still like to change is that the default is invisible. A CRD default and the restored CEL
rule are mutually exclusive, since defaulting runs before validation, and you showed that yourself
with the rejection error. Moving the default into the controller keeps the rule but gives up the
operational half: nothing in the stored object says leader-only selection is in force, so there is
no `kubectl get -o yaml` evidence and nothing to diff before an upgrade.

I'd rather give up the CEL rule than the visibility, and this thread is why. The rule only refuses
the explicit spelling of a policy the controller applies anyway through the unset path, so it isn't
protecting anyone, and it's the reason the StatefulSet case above misbehaves while looking guarded.
Concretely:

- restore `+kubebuilder:default=LeaderOnly`
- drop the `RoleSpec` CEL rule
- add the scope check to `constructServiceApplyConfiguration`:

      if role.IsLeaderWorkerPattern() &&
          role.GetWorkloadType() == constants.RoleInstanceSetWorkloadType &&
          role.LeaderWorkerPattern.GetSharedServiceSelection() == workloadsv1alpha2.SharedServiceSelectionLeaderOnly {

That's not a revert to the earlier commit. That one dropped the rule without the scope check, which
is what left the StatefulSet path narrowing to a label its Pods never carry. With the check the
controller and the documented scope agree, and the default becomes safe to store.

The cost is that `LeaderOnly` becomes writable on LWS roles where it does nothing. A line in the
KEP's scope section seems enough. A status condition exposing the effective policy would be more
rigorous, but I don't think it earns the surface.
```

---

## Reply 2 — thread on `keps/260-leaderonly-service/README.md:179`

Time-sensitive: the author has already said they will update the docs, and the correct direction
depends on the default decision. Posting after that work lands means asking for a second revision.

```text
Before you start on this: the direction depends on how the default question in the `helper.go`
thread lands, and I'd rather not send you through the KEP twice.

If the default moves back into the CRD, most of what I flagged here becomes correct again rather
than needing deletion. The `+kubebuilder:default=LeaderOnly` marker in the snippet and the "the API
server defaults it to `LeaderOnly`" line on 197 would both be accurate, and what needs removing is
instead the "deliberately not a CRD default" comment this PR added to `rolebasedgroup_types.go`.
The `### Validation` section only needs restoring if the CEL rule stays, so that one is on the same
hinge.

Either way the PR description still needs its own pass. It currently explains that the CEL rule had
to be removed because the default required it, which is the opposite of what f8f2a59 does, and that
text is what lands in the squash commit message.
```

---

## Reply 3 — thread on `pkg/reconciler/svc_reconciler.go:111` (primary)

Closes out the two-option question raised in that thread. Post this one first: it settles the
default decision that Reply 1 and Reply 2 both depend on.

```text
Option two, and I don't think it costs much.

I've changed my mind on the "keep `nil -> All`" half. The KEP's own motivation argues for
`LeaderOnly` better than the PR description does: worker Pods may only run a dummy API server, so
the old all-Pods default was routing requests at non-functional endpoints. All six
`leaderWorkerPattern` examples in this repo leave the field unset, so the topology that was
mis-served is the one everybody deploys. That's a correction and I'd keep it. What I still want is
for it not to ride along under a `fix:` title.

The split is cheap because after f8f2a59 the API surface is already identical to base: the CEL rule
is back, there is no `default: LeaderOnly` in the generated CRDs, and the entire `config/crd` plus
`deploy/kubectl` diff is description text. The breaking change is one condition.

    // svc_reconciler.go:111
    - SharedServiceSelection != nil && *... == LeaderOnly    // nil does not narrow
    + GetSharedServiceSelection() == LeaderOnly              // nil narrows

The worker-DNS fix doesn't depend on it. `GetSharedServiceSelection()` is inert in
`roleinstanceset_reconciler.go`, because `nil` resolving to `LeaderOnly` is still not `All`, so
unset roles behave there exactly as they do today.

So `roleinstanceset_reconciler.go`, `helper.go`, the `ComponentServiceName` test, the envtest `All`
context, the e2e `LeaderOnly -> All` case and the KEP's `All` semantics can go in as the fix. The
`svc_reconciler.go` condition, all three `svc_reconciler_test.go` hunks, the envtest "policy is not
set" and LWS-reject contexts, and the CRD-default question from the `helper.go` thread would be the
second PR, with an upgrade note.

A useful check on that boundary: every test edit here that replaces `nil` with an explicit `All`
exists only to accommodate the new default. The two hunks in
`TestServiceReconciler_reconcileHeadlessService_UpdatesSelectorInPlace` and `..._Reverse` need no
change if the condition stays as it is.

One addition for the fix half. @NoobDream2568 already raised the rollout for existing `All` users
and I agree it's expected: recreating the worker Pods is what makes the requested behaviour take
effect, given `hostname` and `subdomain` are immutable. It belongs in the KEP and the release notes
together with the fact that it's bounded. `limitUpdateIndexes` caps concurrent updates at
`maxUnavailable` in `pkg/reconciler/roleinstanceset/statelessmode/sync/update.go` and `statefulmode`
carries an equivalent budget, so it rolls rather than taking every worker down at once. I'd avoid
saying those workloads were already broken, though. Anyone consuming only the Service-level A record
saw nothing wrong and still gets their Pods replaced.

Separately, for whichever PR ends up touching `svc_reconciler_test.go`: the expected selector is now
computed by calling `GetSharedServiceSelection()`, the same helper the code under test uses, so that
assertion can no longer fail if the default is wrong. Worth writing the expected labels out
literally.
```

---

## Notes for the reviewer (not for GitHub)

- Reply 1 walks back the "keep `nil -> All`" option from the `svc_reconciler.go:111` thread. That
  thread offered two choices, so this narrows rather than contradicts, but the reversal is stated
  plainly rather than left for the author to notice.
- The `maxUnavailable` bound lands in Reply 3, alongside the release-note ask, rather than in
  Reply 1 where it would be off-topic.
- Reply 3 deliberately does not restate the CRD-default / drop-the-CEL-rule proposal, which already
  has its own thread on `helper.go:279` (Reply 1). It only cross-references it, so the two comments
  don't duplicate each other.
- @NoobDream2568's "the inference service may not have been functioning correctly anyway" framing
  is deliberately not repeated. Some users only consume the Service-level A record and saw nothing
  wrong, and their Pods still get replaced.
- Fact-checked before drafting: `convertRoleV1alpha1ToV2` exists at
  `api/workloads/v1alpha1/rolebasedgroup_conversion.go:125`; 6 of 6 `leaderWorkerPattern` examples
  leave `sharedServiceSelection` unset; the rejection error referenced as "you showed that
  yourself" is the one quoted in the PR description.
