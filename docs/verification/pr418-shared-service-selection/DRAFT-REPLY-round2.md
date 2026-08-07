# Draft replies — round 2 (NOT POSTED)

Two comments, both follow-ups on threads already open. Nothing here is published until
explicitly confirmed.

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

## Notes for the reviewer (not for GitHub)

- Reply 1 walks back the "keep `nil -> All`" option from the `svc_reconciler.go:111` thread. That
  thread offered two choices, so this narrows rather than contradicts, but the reversal is stated
  plainly rather than left for the author to notice.
- The claim that the rollout is bounded by `maxUnavailable` is held back from these two replies. It
  belongs in the `svc_reconciler.go:111` thread alongside the release-note ask, or in a reply to
  @NoobDream2568's upgrade-disruption comment.
- @NoobDream2568's "the inference service may not have been functioning correctly anyway" framing
  is deliberately not repeated. Some users only consume the Service-level A record and saw nothing
  wrong, and their Pods still get replaced.
- Fact-checked before drafting: `convertRoleV1alpha1ToV2` exists at
  `api/workloads/v1alpha1/rolebasedgroup_conversion.go:125`; 6 of 6 `leaderWorkerPattern` examples
  leave `sharedServiceSelection` unset; the rejection error referenced as "you showed that
  yourself" is the one quoted in the PR description.
