# Verification — sgl-project/rbg#434 (Implement KEP-430: two-level gang scheduling)

Reviewer-side evidence harness for [PR #434](https://github.com/sgl-project/rbg/pull/434).
Reviewed head: `9d3f4b193e82240e51878b0837dccb1cea8ab4ab`.

**Production code is untouched.** This branch adds only test files and this
directory, so a red test is unambiguously the PR's behavior and not a patched
variant of it.

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
