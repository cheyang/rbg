# Verification — PR #473 (KEP-473: Topology-Aware Scheduling) — Reviewer A (Claude)

PR: https://github.com/sgl-project/rbg/pull/473 — adds
`keps/473-topology-aware-scheduling/{README.md,kep.yaml}` only (656 insertions, no runtime code).

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
| unit (python claim-check) | 10 canaries (F1–F10) + 4 contracts (P0, C1–C3) over the KEP text, the PR body, the repo, and upstream API snapshots | `python3 -I scripts/check_claims.py --repo <repo-root> --evidence results/` |
| integration (go test) | strict-decodes the KEP's embedded example YAML into the real vendored upstream API types (Volcano `scheduling.volcano.sh/v1beta1`; scheduler-plugins `scheduling.x-k8s.io/v1alpha1`), structural check of the KAI example | `go test ./docs/verification/473-topology-aware-scheduling-claude/harness/ -v` |
| live (L3) | **skipped** — the PR is documentation-only; nothing runs. (A cluster probe did find a reachable 2-node cluster; live coverage belongs to the implementation PRs of the KEP's phase plan.) | — |

Re-run everything against the current PR head (machine-independent, resolves the head from
`manifest.pr`):

```bash
bash scripts/re-verify.sh            # or: bash scripts/re-verify.sh <fixed-ref>
```

## Observed vs expected

| check | polarity | observed | expected | verdict |
| --- | --- | --- | --- | --- |
| F1 downgrade section ignores the topology finalizer | canary | DEFECT-PRESENT | present while unfixed | **finding live** |
| F2 RBGSet guard described two contradictory ways | canary | DEFECT-PRESENT | idem | **finding live** |
| F3 PR body promises preferred≤required validation, KEP declines it | canary | DEFECT-PRESENT | idem | **finding live** |
| F4 promised in-tree Chinese translation absent | canary | DEFECT-PRESENT | idem | **finding live** |
| F5 Generated Group Names covers Volcano only | canary | DEFECT-PRESENT | idem | **finding live** |
| F6 examples partition unconstrained roles per instance | canary | DEFECT-PRESENT | idem | **finding live** |
| F7 KEP cites `hasSubGroupPolicy`; code says `supportsSubGroupPolicy` | canary | DEFECT-PRESENT | idem | **finding live** |
| F8 new cluster-scoped reads needed, RBAC unaddressed | canary | DEFECT-PRESENT | idem | **finding live** |
| F9 `minMember: 0` on sigs PodGroup vs upstream CRD `Minimum=1` | canary | DEFECT-PRESENT | idem | **finding live** |
| F10 typo "upto" | canary | DEFECT-PRESENT | idem | **finding live** |
| P0 base has no topology scheduling | contract | PASS | PASS | fact holds |
| C1 KEP's codebase symbols exist | contract | PASS | PASS | facts hold |
| C2 vendored Volcano API matches dialect claims | contract | PASS | PASS | facts hold |
| C3 upstream snapshots back external API claims | contract | PASS | PASS | facts hold |
| G1 Volcano example strict-decodes | contract | PASS | PASS | example fields are real |
| G2 sigs PodGroup example strict-decodes | contract | PASS | PASS | idem |
| G3 KAI example structure | contract | PASS | PASS | idem |
| G4 Koordinator example renders `minMember: 0` | canary | PASS (renders 0) | present while unfixed | backs F9 |

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

Applying the proposed fixes for F1/F2/F10 to a **temporary copy** of the KEP
(`/tmp/kep473-fixed.md`, never committed) flips exactly those three canaries to
DEFECT-ABSENT while the other seven stay present — see `results/l1-bites-check.txt`.
The repo's KEP file was never modified (`git status` clean of tracked changes).

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
