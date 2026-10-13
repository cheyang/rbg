# Verification — PR #473 (KEP-473: Topology-Aware Scheduling) — Reviewer A (Claude)

PR: https://github.com/sgl-project/rbg/pull/473 — adds
`keps/473-topology-aware-scheduling/{README.md,kep.yaml}` only (676 insertions, no runtime code).

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
