# Verification — PR #473 (KEP-473: Topology-Aware Scheduling) — Reviewer A (Claude)

PR: https://github.com/sgl-project/rbg/pull/473 — adds
`keps/473-topology-aware-scheduling/{README.md,kep.yaml}` only (the Go files in the
GitHub diff are a cherry-pick of main's already-merged #495, byte-identical — check C9).

**Round 3** (head `99b991c`, delta `6894a2e..99b991c`, force-push rebase onto `79f7e585`
plus 3 new KEP commits `d5d89dc9/877db572/99b991c5`): the KEP nearly doubled and answers
the round-3 request-changes. Ledger F1 (contract freeze vs cache-backed policy read) is
**fixed** and pinned by contract LF1 (uncached `apiReader` read before first acceptance,
explicit `topologyPolicyRequired` dependency, downgraded ordering claim; base controller
fact `mgr.GetAPIReader()` verified). Ledger F2/F4/F6/F10/F11/F12/F13 likewise fixed and
guarded by LF2/LF4/LF6/LF10/LF11/LF12/LF13. All round-1/2 fixed findings stay fixed —
their flipped canaries were inverted into fixed-guard contracts (F1–F12 now `contract`).
New evidence contracts C4–C8 verify the KEP's new upstream scheduler-internal citations
(Volcano v1.14.4 subjob selection / restart-domain recovery / BestEffort+backfill, KAI
v0.18.6 non-injective `calcDomainId` + level validation, Koordinator pinned-commit
no-Bound-members limitation) against `results/upstream/round3/` snapshots fetched this
round. G3/G4 needles updated for the rewritten KAI/Koordinator examples (harness update,
not a defect). **Still live**: F8 = ledger F9 (RBAC never enumerated) and F13 = ledger
F7 (DNS-1123 rule for "Role names" unscoped). Bite checks: reverting the LF1 guard, the
PRR heading, or reintroducing "controller resync" flips exactly the matching contract —
see `results/l1-bites-check-round3.txt`.

**Round 2** (head `6894a2e`, delta `2b65779b..6894a2e`, 3 commits clarifying review
feedback): F1–F6 and F9 are fixed (canaries flipped; F4 via a PR-body edit —
`results/pr-body.md` re-archived from the current body). New findings F11–F13 from the
delta review. F7/F8/F10 unchanged. `re-verify.sh` gained a repo-root `cd` (round-1
invocation from inside this directory broke git's relative pathspec after the
head checkout deleted the cwd).

## Premise (P0): CONFIRMED

Claimed problem (PR body / issue #473): RBG workloads are topology-sensitive (multi-node
instance packing, PD–decode co-location) and RBG cannot express topology intent today.

Verified against the **base** branch (`origin/main`): a `git grep` over first-party Go code
for `topologyConstraint|networkTopology|InstanceTopologyConstraint` finds nothing —
`pkg/scheduler` ships gang-only backends (volcano, k8s-scheduler-plugin). The only hits are
the vendored `volcano.sh/apis` types, which are a dependency, not RBG functionality.
A real gap; a design doc is the right first artifact. Component match: the KEP designs
changes for exactly the components that exist (RoleSpec, CoordinatedPolicy scheduling
strategy, the scheduler backends).

## Layers

| layer | what | how to run |
| --- | --- | --- |
| unit (python claim-check) | 13 canaries (F1–F13) + 4 contracts (P0, C1–C3) over the KEP text, the PR body, the repo, and upstream API snapshots | `python3 -I scripts/check_claims.py --repo <repo-root> --evidence results/` |
| integration (go test) | strict-decodes the KEP's embedded example YAML into the real vendored upstream API types (Volcano `scheduling.volcano.sh/v1beta1`; scheduler-plugins `scheduling.x-k8s.io/v1alpha1`), structural check of the KAI example | `go test ./docs/verification/473-topology-aware-scheduling-claude/harness/ -v` |
| live (L3) | **skipped** — the PR is documentation-only; nothing runs. (A cluster probe did find a reachable 2-node cluster; live coverage belongs to the implementation PRs of the KEP's phase plan.) | — |

Re-run everything against the current PR head (machine-independent, resolves the head from
`manifest.pr`):

```bash
bash scripts/re-verify.sh            # or: bash scripts/re-verify.sh <fixed-ref>
```

## Observed vs expected (round 3, head 99b991c)

| check | polarity | observed | verdict |
| --- | --- | --- | --- |
| LF1 ledger F1: contract freeze lacks a freeze-time guard / blessed ordering | contract (fixed guard) | PASS | **FIXED** — uncached `apiReader` read before first acceptance, `topologyPolicyRequired`, ordering claim downgraded with documented recovery; base controller exposes `mgr.GetAPIReader()` |
| LF2 ledger F2: 'controller resync' listed as drift trigger | contract | PASS | **FIXED** — event-driven requeue + five-second retries only; no resync claim |
| LF4 ledger F4: webhooks-disabled sentence contradicts the freeze | contract | PASS | **FIXED** — webhooks required, but controller still rejects mismatch and retains previous bindings |
| LF6 ledger F6: KAI subGroup names carry no length budget | contract | PASS | **FIXED** — kind-prefixed bounded forms + hash12, 63-char tests promised |
| LF10 ledger F10: minMember/minSubGroup zero-value wording contradiction | contract | PASS | **FIXED** — "Nonzero thresholds come only from gang configuration" |
| LF11 ledger F11: alpha KEP omits the PRR questionnaire | contract | PASS | **FIXED** — full PRR section + kep.yaml feature-gates/metrics |
| LF12 ledger F12: binding injection ordering after reconcileRoles | contract | PASS | **FIXED** — flow step 11 (inject bindings) precedes step 12 (create/update Roles); per-Pod handshake specified |
| LF13 ledger F13: Volcano example omits subGroupSize | contract | PASS | **FIXED** — `subGroupSize: 4` + `ComputeSubGroupSize` + threshold semantics |
| F8 ledger F9: new cluster-scoped reads, RBAC unaddressed | canary | DEFECT-PRESENT | **finding live** (permission surface grew: topology CR CRUD, Node watch, HyperNode/Topology CRUD, cross-namespace checks) |
| F13 ledger F7: DNS-1123 admission rule for 'Role names' unscoped | canary | DEFECT-PRESENT | **finding live** (RoleSpec.Name still has no pattern; scope the rule to instanceTopologyConstraint-bearing roles) |
| C4 Volcano v1.14.4 subjob mechanics (first-non-empty-match, default-subjob topology inheritance, subGroupSize→MinAvailable) | contract | PASS | facts hold (results/upstream/round3/volcano_*.go) |
| C5 Volcano restart domain recovery skips topology-less subjobs | contract | PASS | fact holds |
| C6 Volcano allocate skips BestEffort; backfill direct-allocates | contract | PASS | fact holds |
| C7 KAI v0.18.6 calcDomainId unescaped join; MaxItems=16; nodeLabel immutable | contract | PASS | fact holds |
| C8 Koordinator pinned commit: no-Bound-members-only + OnceSatisfied early return | contract | PASS | fact holds |
| C9 PR's Go delta byte-identical to main's #495 | contract | PASS | fact holds — net docs-only PR |
| F1–F12 (round-1/2 findings, inverted fixed guards) | contract | PASS | all remain fixed at 99b991c |
| P0, C1–C3, G1–G4 | contract | PASS | facts/examples hold; G3/G4 needles updated for the rewritten examples |

Raw output round 3: `results/reverify/{unit.out,integration.out,unit.parsed,integration.parsed}`;
bites: `results/l1-bites-check-round3.txt`.

## Observed vs expected (round 2, head 6894a2e)

| check | polarity | observed | verdict |
| --- | --- | --- | --- |
| F1 downgrade section ignores the topology finalizer | canary | DEFECT-ABSENT | **FIXED** — downgrade section now documents the finalizer escape hatch |
| F2 RBGSet guard described two contradictory ways | canary | DEFECT-ABSENT | **FIXED** — guard is now described once, self-contained |
| F3 PR body promises preferred≤required validation, KEP declines it | canary | DEFECT-ABSENT | **FIXED** — KEP now validates ordering (`InvalidLevelOrder`) |
| F4 promised in-tree Chinese translation absent | canary | DEFECT-ABSENT | **FIXED** — claim removed from the PR body |
| F5 Generated Group Names covers Volcano only | canary | DEFECT-ABSENT | **FIXED** — per-dialect naming rules + collision-safe PlacementGroup-ID hash |
| F6 examples partition unconstrained roles per instance | canary | DEFECT-ABSENT | **FIXED** — KAI renders `role-decode`; Koordinator renders one role-level decode member |
| F7 KEP cites `hasSubGroupPolicy`; code says `supportsSubGroupPolicy` | canary | DEFECT-PRESENT | **finding live** |
| F8 new cluster-scoped reads needed, RBAC unaddressed | canary | DEFECT-PRESENT | **finding live** |
| F9 `minMember: 0` on sigs PodGroup vs upstream CRD `Minimum=1` | canary | DEFECT-ABSENT | **FIXED** — dialect detection + `SchedulerUnsupported` documented; canary refined to guard the mitigation, G4 promoted to contract |
| F10 typo "upto" | canary | DEFECT-PRESENT | **finding live** |
| F11 Koordinator "cross-role-only" rendering shows per-instance prefill members the unrenderability claim excludes | canary | DEFECT-PRESENT | **finding live** (new in round 2) |
| F12 "Role declaration immutable from create onward" contradicts codebase and test item 7 | canary | DEFECT-PRESENT | **finding live** (new in round 2) |
| F13 DNS-1123 admission rule for "Role names" unscoped; RoleSpec.Name has no pattern today | canary | DEFECT-PRESENT | **finding live** (new in round 2) |
| P0 base has no topology scheduling | contract | PASS | fact holds |
| C1 KEP's codebase symbols exist | contract | PASS | facts hold |
| C2 vendored Volcano API matches dialect claims | contract | PASS | facts hold |
| C3 upstream snapshots back external API claims | contract | PASS | facts hold |
| G1 Volcano example strict-decodes | contract | PASS | example fields are real |
| G2 sigs PodGroup example strict-decodes | contract | PASS | idem |
| G3 KAI example structure (kind-prefixed tree-unique names, no unconstrained-role split) | contract | PASS | updated for the round-2 example |
| G4 Koordinator dialect rendering + documented detection | contract | PASS | promoted from canary after F9 fix |

F14 (does a never-accepted late policy receive the topology finalizer?) is an open
**question** to the author, not a canary — see the debate document.

Raw output: round 1 — `results/l1-claims.txt`, `results/l2-gotest.txt`;
round 2 — `results/reverify/{unit.out,integration.out,unit.parsed,integration.parsed}`.

Raw output: `results/l1-claims.txt`, `results/l2-gotest.txt`.

## What the contracts positively established (facts worth keeping)

The KEP's load-bearing factual claims about the world all check out:

- `ResolveGangStrategy`, `IncompatibleGangConfig`, `GangConfigured`, the
  `rbg.workloads.x-k8s.io/{group-name,role-name,role-instance-name}` labels,
  `SchedulingCoordinationStrategy`, `LeaderWorkerPattern`/`CustomComponentsPattern`,
  RBGSet — all exist on the base branch as the KEP describes.
- Volcano (vendored v1.14.4 + upstream master): `PodGroup.spec.networkTopology`
  (`mode` hard/soft, `highestTierAllowed`/`highestTierName` mutually exclusive),
  `subGroupPolicy[].networkTopology`, PodGroup `minMember` allows 0; HyperNode
  `spec.tier` (int) + optional `spec.tierName`.
- Volcano's subJob partitioning (upstream `pkg/scheduler/api/sub_job_info.go`): pods
  sharing `matchLabelKeys` values form one subJob and the hard topology constraint covers
  the whole subJob — so the KEP's per-instance rendering mechanism is sound, including
  with the default `subGroupSize` omitted (subGroupSize is dispatch readiness, not
  topology scope).
- Koordinator: `gang.scheduling.koordinator.sh/{groups,network-topology-spec}` annotations,
  `gatherStrategy` with `MustGather`/`PreferGather`, `ClusterNetworkTopology` layers
  (`topologyLayer` + node `labelKey`).
- KAI scheduler: PodGroup at `scheduling.run.ai/v2alpha2` with
  `topologyConstraint.{requiredTopologyLevel,preferredTopologyLevel,topology}`, nested
  `subGroups[].parent`, `minSubGroup`, `minMember` (0 = elastic, explicitly supported);
  `Topology` CRD with `spec.levels[].nodeLabel`.
- The KEP's Volcano and scheduler-plugins example YAML blocks strict-decode into the real
  vendored types (no misspelled fields).

## Harness bites (Step 4 check)

Round 1: applying the proposed fixes for F1/F2/F10 to a **temporary copy** of the KEP
flips exactly those three canaries to DEFECT-ABSENT while the others stay present —
see `results/l1-bites-check.txt`.

Round 2: applying the proposed fixes for F11 (render two role-level members instead of
per-instance prefill in the Koordinator partial example), F12 (justify "cannot arrive
late" via topology-declaration immutability instead of whole-Role immutability), and
F13 (scope the DNS-1123 rule to roles bearing `instanceTopologyConstraint`) to a
temporary checkout of the PR head flips exactly F11/F12/F13 to DEFECT-ABSENT — see
`results/l1-bites-check-round2.txt` (run against the head tree; the branch's KEP was
never modified, production diff empty).

## Continuing after the fix

When the author amends the KEP and pushes, from a checkout of this branch:

```bash
bash scripts/re-verify.sh          # resolves the current PR head from manifest.pr
```

Polarity: all F-checks are **canaries** — `DEFECT-PRESENT` means the finding still
applies; after a real fix the check flips to `DEFECT-ABSENT` (= fixed). Once flipped,
invert or retire the check (e.g. F10's typo check becomes pointless after the fix; F2's
contradiction check should become a contract asserting the guard is described exactly one
way). G1–G3 are contracts about the KEP's examples decoding into real upstream types —
they must stay green as the KEP evolves; a red G1/G2 after an edit means the edit
introduced a field the real APIs don't have.

Prerequisites: git, python3, go, jq; the repo's `vendor/` tree (already committed);
network for the initial upstream snapshots (archived under `results/upstream/`).

Copy-paste kickoff prompt for a fresh agent:

> You are resuming a review verification. Branch
> `verify/473-topology-aware-scheduling-claude` in the fork cheyang/rbg holds the harness
> under `docs/verification/473-topology-aware-scheduling-claude/`. Read its README.md,
> then run `bash scripts/re-verify.sh` from a checkout of that branch. Report per
> finding Fixed / Still-present / Partial, honoring canary polarity (fixed = flip to
> DEFECT-ABSENT). Review the `last-reviewed..head` delta printed at the end, then advance
> `.last-reviewed` and commit.
