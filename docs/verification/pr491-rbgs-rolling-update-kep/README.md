# Verification — PR #491 (KEP-455: rbgset rolling update) — Reviewer B (Codex)

PR under review: https://github.com/sgl-project/rbg/pull/491
Head reviewed: `b03a7e67cf681599fb1c69537be2a2078ec35cf0` (round 2; round-1 head `719851a65959ebc91157ce90651aaa911c26f30c`)
Base: `79f7e5858fdc30ffaeadef4aea12c3809dcb348d` (merge-base `0821cb5b`)

The PR is **docs-only**: it adds `keps/455-rbgs-rolling-update/{README.md,kep.yaml}`.
There is no runtime behavior to reproduce, so this harness verifies the two
things that decide whether the KEP is mergeable:

1. **The KEP's factual claims about the current codebase** (its premises) —
   contract tests `TestVerify_C*`, expected to PASS on the PR head. If a future
   code change flips one of them, the KEP text has gone stale.
2. **The review findings F1/F2** — contract tests `TestVerify_F*` that FAIL on
   the PR head (the failure *is* the reproduction) and must PASS once the KEP
   is amended.

## Premise (P0)

Claim (KEP "Child Template Propagation"): children currently receive only
`spec.roles`; a set template defining `roleTemplates` produces children that
cannot resolve a `templateRef`. **Confirmed** against the base/head code:
`newRBGForSet` and `updateExistingRBGs` in
`internal/controller/workloads/rolebasedgroupset_controller.go` handle only
`Spec.Roles`; `RoleBasedGroupSpec` (v1alpha2) holds exactly `roles` +
`roleTemplates` (C1, C2 pass; see `results/unit.out`).

Docs-only carve-out applies: no runtime symptom is claimed, so nothing is
reproduced against a cluster. The linked tracking issue #455 (open feature
request for "RBGS-level RollingUpdate implementation") matches the KEP's
subject. (`closesIssues: #15` reported by automation is the PR-template's
example text inside an HTML comment; #15 is long closed — no auto-close risk.)

## Observed vs expected (PR head 719851a)

| Test | Polarity | Expected now | Observed now | Meaning |
|------|----------|--------------|--------------|---------|
| C1 children receive only roles | contract | pass | PASS | KEP propagation premise true |
| C2 RoleBasedGroupSpec has 2 fields | contract | pass | PASS | "blast radius bounded" claim true |
| C3 RollingUpdateInProgress never published | contract | pass | PASS | **F1's code fact: no producer exists** |
| C4 conversion webhook in CRD manifests | contract | pass | PASS | KEP conversion claim true |
| C5 scalingAdapter rejection message | contract | pass | PASS | KEP conflict description matches code |
| C6 v1alpha2 label prefix | contract | pass | PASS | proposed label names fit convention |
| C7 kep.yaml milestone == Makefile VERSION | contract | pass | PASS | v0.9.0 consistent |
| C8 no set rolloutStrategy yet | contract | pass | PASS | feature is a proposal, not shipped |
| F1 KEP states RollingUpdateInProgress is new work | contract | **fail** | **FAIL** | finding reproduced |
| F2 KEP states checkpoint persistence | contract | **fail** | **FAIL** | finding reproduced |

## Findings

- **F1 [major]** — The KEP's InPlaceUpdate availability gating depends on the
  RBG condition `RollingUpdateInProgress` ("RBGSet requires that barrier to be
  false before spending another budget slot"), but no controller sets that
  condition today (C3: it exists only as an API constant, declared at
  `api/workloads/v1alpha2/rolebasedgroup_types.go:580`). The KEP never states
  that publishing it is new, in-scope RBG-controller work, nor when it is
  set/cleared. An implementation written literally from this KEP would find the
  barrier permanently absent (trivially "false") and could start the next
  ordinal before the downstream workload finished — the exact failure the KEP's
  risk table says the barrier prevents.
- **F2 [minor]** — The KEP requires a restart-safe `groupset-observed-replicas`
  checkpoint ("Controller restarts must not lose that reference or the
  observed-replicas checkpoint") but never says where it is persisted; it is
  absent from the Status table. If it is an annotation on the set, it interacts
  with the v1alpha1 conversion stash and GitOps diffs; if status, it must be
  specified there.

## Round 2 (head b03a7e6, delta 719851a..b03a7e6)

The round-2 revision (+114/-5 in the KEP README) addresses round-1 findings:
new "Child Rollout Barrier" design section (F1), "Scale subresource
interaction" paragraph (F2), enumerated controller-owned-key list covering
hash+drift+sync plus syncRBGMetadata carry-over (F3/F4), an Appendix pinning
checkpoint storage (F5), informational-only numeric revision (F6),
percentage-partition timing (F7), the InPlaceUpdate explicit-zero admission
verdict (Q1), and the operator-downgrade stance (Q2); plus test-plan additions
(M1 partial).

Harness changes this round:
- `TestVerify_F1_KEPStatesRollingUpdateInProgressIsNewWork` contract updated
  (harness-update): the round-1 regex expected an explicit "this is new work"
  acknowledgment; the round-2 head instead carries the full design section,
  which is the round-1 ask's first option. The contract now asserts the design
  section (producer/ordering/consumer) exists. Verified: FAIL on 719851a, PASS
  on b03a7e6.
- New guard tests `TestVerify_R2*` for every fixed finding — each FAILS on
  719851a and PASSES on b03a7e6 (both runs captured under results/reverify/),
  so they double as regression guards for future rounds.
- New finding N1 contract test + two discrete-event simulation exhibits.
- re-verify.sh: results are now persisted after the tree restore (previously
  `git checkout -f` clobbered fresh results back to the committed ones).

### Observed vs expected (head b03a7e6 / old head 719851a)

| Test | Polarity | b03a7e6 | 719851a | Meaning |
|------|----------|---------|---------|---------|
| C1-C8 premise/code-fact contracts | contract | PASS | PASS | KEP premises still true; code unchanged outside keps/ |
| F1 barrier designed (updated contract) | contract | PASS | FAIL | round-1 F1 FIXED by the design section |
| R2F2 scale-subresource interaction | contract | PASS | FAIL | round-1 F2 FIXED |
| R2F3F4 enumerated keys + sync carry-over | contract | PASS | FAIL | round-1 F3/F4 FIXED |
| F2(old id)/R2F5 checkpoint storage named | contract | PASS | FAIL | consensus F5 FIXED |
| R2F6 numeric revision informational-only | contract | PASS | FAIL | round-1 F6 FIXED |
| R2F7 partition resolution timing | contract | PASS | FAIL | round-1 F7 FIXED |
| R2Q1 explicit-zero admission verdict | contract | PASS | FAIL | round-1 Q1 ANSWERED |
| R2Q2 downgrade stance | contract | PASS | FAIL | round-1 Q2 ANSWERED |
| R2M1 v1alpha1 round-trip test-plan entry | contract | **FAIL** | FAIL | M1 residual reproduced (round-trip entry still missing; two Reviewer C wording catches also unchanged) |
| R2N1 absent-exception generation-gated | contract | **FAIL** | FAIL | **N1 reproduced** (new this round) |
| N1Exhibit concurrent rolls (simulation) | exhibit | PASS | PASS | specified rule: 2 children mid-roll with maxUnavailable=1; gated rule: 1 |
| N1Exhibit silent stale completion (simulation) | exhibit | PASS | PASS | specified rule: child controller down -> set declares complete with 0/4 rolled; gated rule: fail-safe stall after first bump |

### N1 — the absent-condition exception is not generation-gated (major, new)

The round-2 consumer rule (`groupSetChildReady`, KEP "Child Rollout Barrier"):
ready when the barrier condition is **absent**, or `ObservedGeneration >= gen`
and `False`. The absent-exception exists for legacy children that predate the
feature. But it is unscoped: it also fires in the window between the set
bumping a legacy child's spec (generation N -> N+1, revision label written in
the same update) and the child RBG controller's first reconcile of N+1 (which
per the KEP's own producer invariant stamps `RollingUpdateInProgress=True`).
In that window the set observes matches-update + stale Ready=True + barrier
absent and spends the next budget slot on a child that has not started rolling
— the stale-capacity failure the barrier exists to prevent, on exactly the
legacy-bootstrap population the exception serves. With the child controller
down, the set marches through every ordinal and declares RolloutComplete with
zero children rolled (silent stale completion; exhibit B).

Fix (one clause): treat absent as ready only when the child's
`status.observedGeneration >= child.generation` (the field exists —
api/workloads/v1alpha2/rolebasedgroup_types.go:524 — and is stamped every
reconcile by updateRBGStatus). Legacy converged children have
observedGen == gen and still bootstrap; just-bumped children have
observedGen == gen-1 and are held until their controller reacts.

Simulation assumptions (kep_round2_test.go): set re-reconciles every tick (its
own child writes re-trigger it via Owns(&RoleBasedGroup{})) and acts before
child controllers within a tick (its reconcile is the cheap side of the race);
child controller latency 1 tick, roll duration 3 ticks, 4 children,
maxUnavailable=1.

## How to run

```bash
# unit layer (all claims + findings)
go test ./test/verification/pr491kep/ -run 'TestVerify_' -count=1 -v

# full re-verification against a revised PR head (from a checkout of this branch)
bash docs/verification/pr491-rbgs-rolling-update-kep/scripts/re-verify.sh
```

Integration (envtest) and live (L3) layers are intentionally omitted: the diff
contains no controller code. The unit layer's claims *are* the integration with
the repository (they read the actual source/manifests under review).

## Harness-bites check (done on this branch)

Temporarily appended to the KEP README: "`RollingUpdateInProgress` is not
currently published by any controller; this KEP adds it as new RBG controller
behavior. The `groupset-observed-replicas` checkpoint is persisted in the set
status." → F1/F2 tests went **green**; after reverting, red again. Production
and KEP diffs are empty (`git status` clean). The harness bites.

## Continuing after a KEP revision

1. Re-run `bash docs/verification/pr491-rbgs-rolling-update-kep/scripts/re-verify.sh`
   (no args — it fetches the current PR head from `manifest.pr`).
2. F1/F2 fixed ⇔ their tests flip to PASS. If the author rewords the statements
   completely differently, the tests may report Harness-update — loosen the
   regex in `test/verification/pr491kep/kep_claims_test.go`, then re-run.
3. C1–C8 must stay green; a flip means the code drifted from the KEP's claims
   (e.g. the implementation PR #474 landed first) and the KEP needs a re-read.
4. Advance `.last-reviewed` to the new head after the round.

Kickoff prompt for a fresh machine: "Check out branch
`verify/pr491-rbgs-rolling-update-kep-codex` of https://github.com/cheyang/rbg.git,
then run `bash docs/verification/pr491-rbgs-rolling-update-kep/scripts/re-verify.sh`."

