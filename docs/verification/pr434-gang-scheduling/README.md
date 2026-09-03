# Verification — sgl-project/rbg#434 (Implement KEP-430: two-level gang scheduling)

Reviewer-side evidence harness for [PR #434](https://github.com/sgl-project/rbg/pull/434).
Reviewed head: `acb43d010b6f39086cc093517f6c7cb3384e2f3c` (round 3.1 — one
incremental commit on top of round 3's `bd9ee4dd`; round 2's pre-rework head was
`9d3f4b19`).

**Production code is untouched.** This branch adds only test files and this
directory, so a red test is unambiguously the PR's behavior and not a patched
variant of it.

## Round 3.1 — incremental head `acb43d0` (current)

The PR advanced by a single commit, `acb43d0` "make manifests", and this time
`bd9ee4dd` **is an ancestor** of the new head, so it is a delta re-review rather
than another full re-sweep. The delta touches four files and **no production gang
logic**: a godoc reword on `SchedulingCoordinationStrategy.Gang`, the CRD
descriptions regenerated from it (`config/crd/bases` and `deploy/kubectl/manifests.yaml`
stay in sync, so `make manifests` was run completely), and one e2e test value
(`Scaling.MaxSkew` `Int32(1)` → `String("50%")`). Every path verified in round 3
(`pkg/scheduler/*`, `internal/controller/*`, `pkg/reconciler/*`) is byte-identical
between the two heads.

L1 contract harness re-ran against the merged head: **18/18 `TestVerifyR3*` PASS,
`HARNESS_RC=0`** — no regression (`results/L1-round3.1-delta-acb43d0.txt`). CI on
`acb43d0` is **all green**, including `e2e-test-volcano-gang` and
`e2e-test-scheduler-plugins-gang`.

This head also closes out the 2026-09-01 reviewer round (cheyang R3 + Copilot,
both filed against the earlier `e4f9449a`). The substantive logic comments —
`gang_strategy.go:283/284` (an all-or-nothing rule absorbing other rules'
per-role minimums) and `scheduler.go:162` (every covered role at `replicas: 0`) —
were already fixed in `bd9ee4dd` (asserted by `TestVerifyR3_N1` and
`TestVerifyR3_F2b`); `helper.go:103` (omitted component size counted as zero) and
the helm `README:189` snippet were fixed in `bd9ee4dd` too; and the godoc
contradiction at `coordinatedpolicy_types.go:74/75` is exactly what `acb43d0`
reworded. **Verdict stays APPROVE-leaning.**

## Round 3 — the reworked head fixes everything

The PR was force-pushed; round 2's reviewed sha `9d3f4b19` is **not an ancestor**
of the new head `bd9ee4dd`, so this was a full re-review plus a full test sweep.
The gang design was rewritten end to end — `MergeGangStrategies` (union of roles
across rules, max-minimum wins, all-or-nothing rules subsume only their own
roles), `ResolveGangStrategy` (resolve-once, carried on the reconcile context),
`GangSize` / `GangMinimumReplicas` / `UnknownGangRoles` / `RoleInGang`,
`IncompatibleGangConfigError` (non-retryable, 5-minute requeue), a `GangConfigured`
status condition, a `CoordinatedPolicy` admission webhook, a `Watches` on
`CoordinatedPolicy`, `raiseScalingTargetsToGangMinimum`, and RoleInstance
gang-annotation derivation in the RoleInstanceSet reconciler. **All ten round-2
findings (F1–F10) are addressed at the code level**, as are the four review
comments posted on 2026-09-01.

Round-3 polarity flipped: the harness now asserts the **fixed** behavior, so
every test must PASS (a failure would mean a regression). The two round-2 harness
files were deleted (they target removed APIs and collide with the author's new
helper names) and replaced by three contract-test files keyed to the same
findings.

| Layer | What ran | Result |
|-------|----------|--------|
| L1 unit (macOS + Linux sandbox) | 18 `TestVerifyR3*` across `pkg/scheduler/{common,volcano}` + `api/workloads/v1alpha2` | **18/18 PASS**, `HARNESS_RC=0` |
| L2 full sweep (Linux sandbox) | `go test ./pkg/... ./api/... ./internal/...` with harness in place | **`FULL_RC=0`**, zero failures (incl. `internal/controller/workloads` 10.7s) |
| L3 live (3-node ACK + Volcano 1.14.4) | 6 policy+RBG pairs + 2 admission negatives, controller image `v434-r3` | **8/8 as predicted** — see table below |

Round-3 live results (namespace `v434-r3`, controller `v434-r3` = verify branch
`ce931129`, production code byte-identical to `bd9ee4dd`, plus a test-only
env-gated namespace field selector):

| case | intent (round-2 finding) | observed | verdict |
|------|--------------------------|----------|---------|
| r3-min | baseline per-role minimums + F10 derivation | PodGroup `minMember=3`, `subGroupPolicy[prefill(2/1),decode(1/1)]` with `matchLabelKeys=[role-instance-name]`, 4/4 Running, both RIS annotated `role-instance-gang-scheduling=true`, `GangConfigured=True` | fixed |
| r3-merge | F7 (two rules must merge) | `minMember=2`, `subGroupPolicy` for both prefill+decode, 2/2 Running | fixed |
| r3-scope | F3/F6 (scope honored) | `minMember=1`, `subGroupPolicy` prefill-only; excluded sidecar NOT enrolled in the gang (gets its own Volcano default group), prefill RIS annotated / sidecar not, both Running | fixed |
| r3-typo | F2 (unknown role rejected) | `GangConfigured=False` / `IncompatibleGangConfig`, message names `[prefil]`, no PodGroup/RIS/pods, quiet 5-min requeue (0 log mentions in a 45s window) | fixed |
| r3-exceeds | F4 (minimum > replicas rejected) | `GangConfigured=False`, message "can never be satisfied", no PodGroup/RIS/pods | fixed |
| r3-watch | F9 (policy edit propagates) | after patching `prefill` 1→2, PodGroup `minMember` 1→2 within 30s **with the RBG untouched** (resourceVersion unchanged) | fixed |
| r3-adm-zero | F5 at admission | webhook DENIED: `minReplicas[prefill]: must be at least 1, got 0` | fixed |
| r3-adm-oos | F6 at admission | webhook DENIED: `minReplicas[decode]: role is not listed in spec.policies[0].roles [prefill], so the minimum would be silently ignored` | fixed |

F1 (non-RIS backing rejected) and F8 (policy read error propagates) are decidable
at L1 only and pass there; they are not exercisable live without deprecated
workload types / fault injection.

One setup caveat worth recording: on the first pass the two admission negatives
were **accepted**, because the cluster's `rbgs-validating-webhook-configuration`
is the old v0.8.0 chart config that routes only `rolebasedgroups` — swapping just
the controller image does not update the VWC, so the API server never called the
(present, running) `ValidateCoordinatedPolicyGang` handler. This is a **setup
artifact, not a product gap**: the PR ships the `coordinatedpolicies` rule in
`config/webhook/manifests.yaml` and registers it in `main.go`. After adding the
PR's rule to the live VWC, both negatives were correctly denied; the VWC was
restored at teardown. The practical note for reviewers: the webhook only enforces
on installs that apply the PR's updated VWC/chart.

The cluster was fully restored and verified afterward: `v434-r3` deleted, image
back to `v0.8.0-0c00546d`, env removed, VWC back to one webhook, no stray
PodGroups, and the two pre-existing live RBGs (`default/nginx-cluster`,
`pr433-test/test-rbg`) untouched at `READY=True`. Full transcript in
`l3/L3-results-r3.txt`; cases in `l3/cases-r3.yaml`; raw observation snapshot in
`results/L3-round3-observations.txt`; L1/L2 sandbox log in
`results/L1-L2-round3-sandbox.txt`.

**Verdict: APPROVE-leaning.** The reworked head fixes every round-2 finding, with
L1 (18/18), the full suite (rc=0) and L3 (8/8 live) all green and no new defects.
The only open items are documentation-level: the scheduler-plugins path returns a
hard runtime error for per-role `minReplicas` (the KEP goal is half-delivered off
Volcano) — reasonable as a first step but should be called out — and the admission
webhook only enforces on installs that ship the updated VWC.

The round-2 detail below is retained as the historical record of what the defects
were before the rework.

## Premise (P0) — Confirmed

The PR's own problem statement holds. Checked against the **base** branch, where
both gang scheduler implementations hardcode `MinMember = rbg.GetGroupSize()`
with no `subGroupPolicy` and no per-role knob; the only user-facing switch is the
boolean `workloads.x-k8s.io/group-gang-scheduling` annotation. There is genuinely
no way to express "start once 2 of 4 prefill instances can be placed", which is
what KEP-430 asks for, and the KEP itself (#430) is merged, so the design is
accepted upstream.

One thing the premise does **not** cover, and which the PR should say out loud:
role-level gang is only reachable on Volcano >= 1.14, because it is implemented
entirely through `spec.subGroupPolicy`. On scheduler-plugins the same
`minReplicas` config returns a hard error. The KEP goal is therefore half
delivered — reasonable as a first step, but users on the default scheduler get
an error rather than a documented limitation.

## Observed vs expected

Polarity matters when reading this table. A **contract** test asserts the
intended behavior, so RED means the defect reproduces. A **canary** asserts the
current wrong behavior, so GREEN means the defect reproduces and the test must be
inverted once the code is fixed.

| # | Sev | Finding | Layer | Polarity | Result | Verdict |
|---|-----|---------|-------|----------|--------|---------|
| F1 | minor | `matchLabelKeys` uses a label only the RoleInstanceSet path writes | L1 | canary | GREEN | Confirmed (see note) |
| F2 | major | typo'd role name in `minReplicas` silently yields `minMember=0` | L1 + L3 | canary | GREEN | Confirmed live |
| F2b | major | same defect as a contract: `minMember` must never be 0 when gang is on | L1 | contract | RED | Confirmed |
| F3 | moderate | roles excluded from `minReplicas` are still joined to the PodGroup | L1 + L3 | canary | GREEN | Confirmed live |
| F3b | minor | the new `role` parameter is dead in both implementations | L1 | canary | GREEN | Confirmed |
| F4 | moderate | `minReplicas > role.replicas` makes the gang permanently unsatisfiable | L1 + L3 | contract | RED | Confirmed live |
| F5 | minor | zero and negative `minReplicas` accepted unvalidated | L1 + L3 | contract | RED | Confirmed live |
| F6 | moderate | `GetGangStrategy` discards `CoordinatedPolicyRule.Roles` | L1 + L3 | canary | GREEN | Confirmed live |
| F7 | moderate | only the first gang rule wins; later ones silently dropped | L1 + L3 | contract | RED | Confirmed live |
| F8 | minor | a `CoordinatedPolicy` read error is indistinguishable from absence | L1 | contract | RED | Confirmed |
| F9 | moderate | nothing watches `CoordinatedPolicy`; edits do not trigger reconcile | code-read + L3 | n/a | — | Confirmed live |
| F10 | major | the CoordinatedPolicy path never enables RoleInstance-level gang enforcement | code-read | n/a | — | Confirmed by reading |
| BASELINE | — | happy path still works (harness-bites check) | L1 + L3 | contract | GREEN | Harness is sound |

F1 is downgraded to **minor**: RBG no longer supports Deployment / StatefulSet
roles, so the missing `role-instance-name` label only affects deprecated
workload types that fresh installs will not use. The defect is real but its
blast radius is limited to a path the project is retiring.

The full pre-existing suite (`go test ./pkg/... ./api/... ./internal/...`) was run
with the harness in place: **zero pre-existing failures**, every red is one of the
contract tests above. Raw output in `results/L2-full-suite-with-harness.txt`.

F9 and F10 are deliberately not encoded as harness tests, because both are
properties of wiring rather than of a computable value. F9 is the absence of a
watch in `SetupWithManager`, and the PR's own e2e file already concedes it with
the comment `controller doesn't watch CoordinatedPolicy` next to a `rbg.Spec` poke
that exists only to force a reconcile. F10 is the absence of a write, established
by enumerating every non-test reference to the two annotation keys; the grep
transcripts are in `results/`.

## Detail on the majors

**F1 — the partitioning label doesn't exist for every workload type.** *(now
minor — see the downgrade note above; retained here for completeness.)*
`buildSubGroupPolicy` unconditionally emits
`matchLabelKeys: [rolebasedgroup.workloads.x-k8s.io/role-instance-name]`. The
code comment claims the label "exists in both stateful and stateless modes",
which is true *within* the RoleInstanceSet path — but a role backed by a
Deployment or a StatefulSet never gets it. Volcano then cannot split the role
into per-instance subGroups, so `subGroupSize` and `minSubGroups` describe a
partition that does not exist and the pods stay Pending. The canary shows the key
being emitted for exactly those workload types.

**F2 — a typo turns gang scheduling off instead of failing.**
`calculateGangMinimum` iterates `rbg.Spec.Roles` and only adds roles that appear
in the `minReplicas` map. If the map keys don't match any role (rename, typo),
the sum is 0, `buildSubGroupPolicy` returns an empty slice, and the PodGroup is
created with `minMember: 0` — which Volcano will happily admit, dispatching pods
one at a time with no gang guarantee at all. Silent downgrade of a scheduling
guarantee is worse than a rejected config; there is no webhook validation, no
event, and no status condition covering this.

**F10 — the new path skips the instance-level half of gang scheduling.**
RBG already has a second, orthogonal gang mechanism:
`rbg.workloads.x-k8s.io/role-instance-gang-scheduling`, whose doc comment
promises two guarantees — fail pod creation while an orphan pod lingers rather
than starting the group partially, and recreate *all* pods of an instance
atomically when an in-place update cannot be applied, "so the PodGroup minimum
member requirement is met". Enabling gang through the new CoordinatedPolicy API
does not turn any of that on. `GetGangStrategy` feeds the PodGroup builder and
nothing else; no code path writes the RoleInstance flag that
`instance_scale.go:767` reads.

That is a bigger deal under `subGroupPolicy` than it was before, because a Volcano
subGroup is defined here as exactly one RoleInstance. Recreating an instance's
pods non-atomically is precisely the event that drops a subGroup below
`subGroupSize` — so the feature most in need of the instance-level guarantee is
the one that silently runs without it.

While verifying this I also found the annotation's documented auto-derivation is
simply absent: the comment says it "is derived automatically from the RBG-level
`GangSchedulingAnnotationKey` annotation during RoleInstanceSet reconciliation",
but the only two write sites propagate an already-present value from
RoleInstanceSet down to RoleInstance. Nothing derives it from the group
annotation. That part predates this PR, so it is context rather than a
regression — but the PR is the natural place to reconcile the two mechanisms
instead of adding a third entry point that bypasses one of them.

## How to run

L1 is the only layer needed — every finding above is decidable deterministically.

```bash
go test ./pkg/scheduler/volcano/... ./pkg/scheduler/common/... -run TestVerify -v
```

L2 (`make test`, envtest) adds nothing for these claims but was run as a
regression sweep: the pre-existing suite is unaffected by the harness.

L3 (live) **was run this round** on a 3-node ACK cluster (k8s v1.36.1) with
Volcano 1.14.4 and `--scheduler-name=volcano`. Because the original PR branch
predates the `restartPolicy` structuring in the cluster's installed CRD, the raw
PR binary failed every typed LIST with a decode error (a version-skew artifact of
the setup, not a gang finding). The branch was therefore **rebased onto current
`upstream/main` — which merged with zero conflicts**, itself a useful signal that
the PR is cleanly mergeable — and deployed with a test-only, env-gated field
selector (`metadata.namespace=v434-verify`) on the RBG/RoleInstanceSet/RoleInstance
informers, so the controller reconciled only the test namespace and never touched
the live workload. The full transcript is in `l3/L3-results.txt`; the manifests
are `l3/cases-rebased.yaml`.

Observed PodGroups (`kubectl get podgroups.scheduling.volcano.sh`) matched every
prediction exactly:

| case | policy intent | PodGroup `minMember` | pods | finding |
|------|---------------|----------------------|------|---------|
| baseline-ok | prefill:2 | 2 | 2 Running | sanity OK |
| f2-typo | `prefil`:2 (typo) | 0 (none) | Running, no gang | F2 |
| f3-excluded | prefill:1, sidecar omitted | 1 | prefill + **sidecar both `schedulerName=volcano`**, both in group `f3-excluded` | F3 |
| f4-exceeds | prefill:5 (replicas 2) | 5 | 2 **Pending forever** | F4 |
| f5-zero | prefill:0 | 0 (none) | Running | F5 |
| f6-scope | scope=[prefill], minReplicas prefill:1 + decode:1 | 2 | decode joined despite scope | F6 |
| f7-tworules | two rules prefill:1 + decode:1 | 1 | second rule dropped | F7 |

F9 was also exercised live: editing `baseline-ok`'s CoordinatedPolicy from
`prefill:2` to `3` left the PodGroup `minMember` at 2, and even forcing an RBG
reconcile afterward did not propagate the new value — the policy→PodGroup linkage
is effectively one-shot.

After the run the cluster was restored to its original controller image
(`v0.8.0-0c00546d`), the test env var was removed, the `v434-verify` namespace was
deleted, and `nginx-cluster` was confirmed still `READY` (4/4 Running, untouched).

## Re-verifying after a fix

```bash
bash docs/verification/pr434-gang-scheduling/scripts/re-verify.sh
```

With no argument it resolves the current PR head from `verify-manifest.json`
(`pr` / `prHeadFetch`), grafts this harness onto it, runs L1, and prints per
finding Fixed / Still-broken / Partial / Harness-update. Canaries are reported as
fixed only when they *flip to failing*; invert them (or promote them to contract
tests) at that point so they keep guarding against regressions.

The incremental review delta comes from `.last-reviewed` next to this README.
Advance and commit it after each round so the next session resumes without
anyone typing a sha.

## Suggested fixes (not applied here)

For F1, derive `matchLabelKeys` from the role's workload pattern and reject
`minReplicas` on roles whose backing workload cannot supply a per-instance label,
rather than emitting a key that silently never matches. For F2/F4/F5, validate
`minReplicas` in the CoordinatedPolicy webhook: keys must name existing roles,
values must be in `[1, role.replicas]`. For F6/F7, either honor
`CoordinatedPolicyRule.Roles` and merge all gang rules, or reject a
CoordinatedPolicy that declares more than one. For F8, propagate the read error
so a transient failure retries instead of degrading. For F9, add a watch on
CoordinatedPolicy mapping to the same-named RBG, which also lets the e2e test
drop its `rbg.Spec` poke. For F10, have the CoordinatedPolicy gang path set the
RoleInstance-level flag on the roles it covers (and fix or drop the "derived
automatically" claim in the annotation's doc comment), so both entry points give
the same guarantees.
