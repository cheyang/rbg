# Verification — KEP-543 "Segment as the Cross-Role Service Unit" (PR #569)

Reviewer: Codex (Reviewer B). Round: initial. Reviewed head: `bd378ebd262fb71a64dff70f7d52a90b7b5dce7d`, base `ea5e82ae59169e0e954fb1c7068f9480fb4b7375`.

This PR is documentation-only (KEP proposal: `keps/543-segment/{README.md,README.zh-CN.md,kep.yaml}`), so the harness targets the two things a KEP can actually get wrong:

1. claims about **existing behavior** of the controllers it reuses (checked against the base branch code), and
2. **internal consistency** of the normative design text.

## P0 — premise / claim validation (against base)

Scoped out as a runtime premise (docs-only PR); executed as a fact-check of the KEP's "existing behavior" statements against base `ea5e82ae`. Verdict: **Confirmed** — every referenced symbol and mechanism exists as described, with one contradiction (F1):

| KEP claim | Base evidence |
| --- | --- |
| `AdapterScaleTargetRef.role` is required today; making it optional is part of the contract | `api/workloads/v1alpha2/rolebasedgroupscalingadapter_types.go`: `Role string \`json:"role"\`` (no omitempty) |
| Existing `ScalingAdapter` type is reused (`enable` default false, managed labels win) | `api/workloads/v1alpha2/rolebasedgroup_types.go` L547+ |
| Non-Segment path raises scaling targets to the Gang minimum | `raiseScalingTargetsToGangMinimum`, `internal/controller/workloads/rolebasedgroup_controller.go:1246-1281` |
| Existing Volcano subGroup = one RoleInstance, `matchLabelKeys=[role-instance-name]`, minMember = Σ(minSubGroups × subGroupSize) | `pkg/scheduler/volcano/scheduler.go` `buildGangSpec` (L260-360) |
| scheduler-plugins has only a Pod-count threshold | `pkg/scheduler/k8s-scheduler-plugin/scheduler.go:85` |
| Current RIS surge sizing `activeSurge = min(maxSurge, max(healthyOld - maxUnavailable, 0))` (simplified) | `stateful_instance_set_utils.go:254` + `:590` (`surgeNeeded = max(0, healthyOldInBase - maxUnavailable)`) |
| `excluded-ordinals` (user) / `committed-skip-ordinals` (controller) exist | `api/workloads/constants/annotation.go:91-96` |
| Label prefix `rbg.workloads.x-k8s.io/`, `group-name` label | `api/workloads/constants/constants.go`, `label.go:33` |
| RBG computes dynamic partition from maxSkew and takes the stricter of it and the user partition; Policy `maxUnavailable` overrides the role value unchanged | `calculateRollingUpdateForPolicyRule`, `rolebasedgroup_controller.go:1473+` |

## Findings and harness

| ID | Claim | Layer | Polarity | Result on bd378ebd | Evidence |
| --- | --- | --- | --- | --- | --- |
| F1 | KEP requires Segment budget `B=0` ⇒ RIS executes with literal `maxUnavailable: 0`; current RIS floors any resolved maxUnavailable to ≥1 when maxSurge==0, so the rule is not implementable via the mechanism the KEP says stays unchanged | unit (`pkg/reconciler/roleinstanceset/statefulmode/verify_kep543_segment_floor_test.go`) | canary | **Reproduced** — explicit 0 with maxSurge 0 resolves to 1; control case with maxSurge 2 keeps 0 | `results/unit-kep543-floor.log` |
| F2 | Design-overview table and RIS-annotation paragraph still use `kv-cache-N` as the `segment` label value, contradicting the normative `<ruleID>-</<groupIndex>` scheme introduced in commit bd378ebd | unit (`keps/543-segment/segment_doc_consistency_test.go`, wrapper `scripts/check-doc-consistency.sh`) | contract | **Reproduced** (test RED; fixing the examples flips it green — bites-checked and reverted) | `results/unit-doc-consistency.log`, `results/doc-consistency.log` |
| F3 | At `B=0` the deterministic surge admission predicate (`\|U ∪ I_loss\| + 1 <= B` or `surgeEligible`) can never fire for a group without pre-existing surge, while the text also requires partition lowering so RIS can *prepare* surge ("candidate update scope"); the state machine relating candidate scope, admission, and budget occupancy is never pinned down | — | reasoning-only | Not verified by harness; doc-clarity question | — |

Full `statefulmode` package suite also run as corroboration that the harness is additive and existing behavior is intact: `results/unit-statefulmode-full.log` (ok).

## How to run

```sh
# unit layer (F1 canary + F2 contract)
go test ./pkg/reconciler/roleinstanceset/statefulmode/ ./keps/543-segment/ -run 'TestVerifyKEP543' -v

# integration layer (whole package the KEP's reused mechanism lives in)
go test ./pkg/reconciler/roleinstanceset/statefulmode/

# re-verify after the doc is amended
bash docs/verification/kep-543-segment-codex/scripts/re-verify.sh        # resolves PR head itself
```

## Continuing after a revision

- F2 is a contract test: it goes **green** when the stale `kv-cache-N` label examples are rewritten to the `<ruleID>-</<groupIndex>` form.
- F1 is a canary: it **flips red** if `computeMaxUnavailable` stops flooring explicit 0 when maxSurge==0 (i.e. the implementation chose to change RIS resolution for the override path) — then invert it or replace it with a contract test for the documented B=0 semantics. If instead the KEP text is amended to acknowledge the floor (or to always pair B=0 with surge), update this note and keep the canary as a regression guard on the floor itself.
- F3 needs only a doc clarification (define the candidate-scope/admission state machine at B=0); no harness.

Production code is untouched by this branch; the diff vs `bd378ebd` is harness-only.

## Handoff prompt

```
Fetch https://github.com/cheyang/rbg.git branch verify/kep-543-segment-codex, check it out,
then run: bash docs/verification/kep-543-segment-codex/scripts/re-verify.sh
It fetches the current head of https://github.com/sgl-project/rbg/pull/569, grafts this
harness onto it, and prints Fixed/Still-broken per finding (F1 canary flips = semantics
changed; F2 contract green = stale examples fixed). Advance
docs/verification/kep-543-segment-codex/.last-reviewed to the new head after review.
```

## Round 2 — incremental (head d8d53364, previous bd378ebd)

Re-verified after the author pushed 3 doc commits addressing round-1 findings.

| ID | Round-2 status | Evidence |
| --- | --- | --- |
| F1 (B=0 floor) | **Fixed by doc redesign** — B=0 now requires positive effective maxSurge (admission rejects explicit 0 without surge; reconciliation reports round-down-to-zero without surge); the doc explicitly acknowledges the RIS floor. Canary pair stays green (code unchanged) and now guards the doc's descriptive claims about the floor. | `results/reverify/unit.parsed` (pass=2) |
| F2 (stale kv-cache-N) | **Fixed** — examples now use `e7652f82cdee-{0,1}`; SHA-256("kv-cache")[:12] independently recomputed. Contract test flipped green; kept as regression guard. | `results/reverify/unit.parsed` |
| F3 **new** | **Reproduced at d8d53364** — test-plan bullet (README.md:1083 / zh-CN:1080) still requires *accepting* a zero-budget-without-surge config under paused-update semantics, contradicting the same revision's new reject/report rule. Revision regression (bullet predates the delta but was consistent with the old text). Bites-checked: rewriting the bullet flips the test green. | `results/reverify/unit.parsed` (contract FAIL) |
| F4 **new** | **Reproduced at d8d53364** — the new discovery-owner paragraph claims enumeration derives "otherwise [from] the child RIS committed ordinals"; refuted for Segment members by ground-truth tests: the child-committed path requires excluded/committed-skip annotations Segment membership forbids, so the only reachable source is dense enumeration from RBG **spec** replicas. The misaligned-window guard is the new fail-fast ordering (validation before the ConfigMap update), which the doc *does* specify — the enumeration-source clause is the inaccurate part. | `results/reverify/unit.parsed` (doc contract FAIL; ground-truth pair PASS) |

Round-1 F3 (B=0 admission state machine) stays reasoning-only and **open**: the B=0+surge
candidate-scope/admission/partition-lowering relation is unchanged by the delta and, with B=0
now requiring surge, is the only B=0 mode.

Harness additions this round (additive, production code untouched):
`pkg/discovery/verify_kep543_discovery_source_test.go` (F4 ground truth),
two contract tests in `keps/543-segment/segment_doc_consistency_test.go` (F3, F4 doc side),
and `scripts/check-doc-consistency.sh` extended to cover F2+F3+F4.
