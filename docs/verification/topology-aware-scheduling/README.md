# topology-aware-scheduling — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/473 (Reviewer A / Claude, first round).

Layers run against the **code under review** (`beabf09cc0f00ad89f8d39db27db79749a39a475` = `origin/pr/473`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `placementPodGroupName` / `boundedPlacementPodGroupName`, `buildTopologySubGroups`, `ResolvePlacementPlan` | `go test ./pkg/scheduler/volcano/ -run 'TestVerify' -v` |
| 2. Integration | real API server (envtest): repo's own in-tree envtest suite against the PR code | `KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64 go test ./test/envtest/testcase/... -timeout 20m` |
| 3. Live | skipped per debate instructions (unit + integration only) | — |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. Bug-canary tests (assert current behavior) PASS now / FLIP to red when fixed.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "RBG workloads are increasingly topology-sensitive … pods must land inside one high-performance network domain (rack/block)" — no way to declare topology intent today |
| Linked issue | `ref #473` (KEP tracking issue); no `fixes #N` closer |
| Reported component | RBG scheduling API + scheduler dialects (Volcano/Koordinator/KAI) |
| Patched component | api/workloads/v1alpha2, pkg/scheduler/common, pkg/scheduler/volcano, internal/controller/workloads, CRDs, RBAC |
| Component match | Yes (Phase 1 = Volcano, per KEP phase plan) |
| **Verdict** | **Confirmed** — base `7ed1860c` has zero topology-aware scheduling capability (see `results/premise-base-check.txt`) |
| Evidence | `results/premise-base-check.txt` |

Side-note on premise integrity: the PR body states **"No runtime code is added in this
PR"** and "No runtime code is added… The KEP includes a test plan". The diff adds
**4,161 insertions** of runtime code (new API fields, admission validation, controller
gating, Volcano compiler, CRD schemas, ClusterRole `hypernodes` RBAC). The claim is
positively refuted by `git diff --stat` (see `results/` and the PR files list).

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| B1 (F1) | Sibling placement groups whose readable names share the first 22 chars render to the SAME physical PodGroup name; second apply overwrites the first group's spec while pods of both scopes share one membership | 1 | **Confirmed** | `results/unit-l1-verify-tests.txt` — both scopes produced `llama-serving-p-pd-disaggregation-gr-8b0716fdc827` |
| B2 (F3) | Whole-group gang + cross-role topology rule on a subset of roles (the PR's own PD-disaggregation motivation) is rejected by the Volcano compiler with `SchedulerUnsupported` | 1 | **Confirmed** (canary) | `results/unit-l1-verify-tests.txt` — plan builds the parent/child tree, `buildTopologySubGroups` then errors |

## Per-finding detail

### B1 / F1 — PodGroup name truncation collision (blocker)

Mechanism: `placementPodGroupName` (pkg/scheduler/volcano/placement.go:167) derives the
physical PodGroup name via `boundedPlacementPodGroupName(rbg.Name, placementName, string(rbg.UID))`
(placement.go:213). `placementName` (`"p-"+ruleName` / `"r-"+roleName`, or the first
sorted name in the subtree) is truncated to **22 characters**, and the only hash in the
name is of the **RBG UID**, which is identical for every scope in one RBG. Two sibling
scopes whose derived names share the first 22 characters therefore produce **identical**
PodGroup names. `scopeID`'s comment (topology_plan.go:250) claims the scope hash exists
"so disjoint scopes cannot collide in generated PodGroup names", but the hash is never
used in a generated name — it only selects between two code branches.

Impact: `ReconcilePlacement` builds both desired PodGroups, `applyPodGroup` patches the
same object twice (second spec overwrites first), `InjectPlacementSchedulingFields`
annotates pods of both logical scopes with the same group name, so pods of two different
topology domains are silently placed under one PodGroup carrying only the second scope's
networkTopology. Silent semantic loss — exactly what KEP §"never silent degradation"
promises cannot happen.

Reproduction (contract test, red on PR code):

```bash
go test ./pkg/scheduler/volcano/ -run 'TestVerifySiblingPlacementGroupsGetDistinctPodGroupNames' -v
```

Harness-bites: folding the scope hash into the hashed input
(`string(rbg.UID)+"-"+group.ID`) makes the test green; reverting the fix makes it red
again with an empty production diff — `results/unit-bite-check.txt`.

### B2 / F3 — gang + subset-scope topology composition unsupported on Volcano (major)

Mechanism: the planner correctly builds a parent/child tree for a contained scope
(gang root over all roles; topology rule over `{prefill, decode}` as a child), but
`buildTopologySubGroups` (placement.go:575) only knows how to render children that are
**single-role, per-RoleInstance** subgroups, and returns `SchedulerUnsupported` for a
cross-role child. The PR body claims "Gang and topology can compose on the same logical
scope" — true only when the gang scope and topology scope are **identical**. The natural
PD configuration (whole-group gang + prefill/decode topology rule) fails; the workaround
is scoping the gang rule to exactly the topology rule's roles.

Canary (green on PR code, must be inverted when a fix makes the composition renderable):

```bash
go test ./pkg/scheduler/volcano/ -run 'TestVerifyGangWholeGroupPlusSubsetTopologyRuleIsRejected' -v
```

## Live run notes

Skipped (unit + integration only, per debate setup). The envtest layer ran the repo's
existing integration suites against the PR code — `results/integration-envtest.txt` —
to establish that the PR does not regress existing behavior (KEP-430 equivalence at the
API-server level).

## Proposed fixes (NOT applied to production here)

- **B1**: include the scope hash in the bounded name, e.g. hash `rbgUID + group.ID`
  (as in the bite-check), or truncate the readable segment to 22 − 8 and append 8 hex
  chars of `group.ID`. Keep the DNS-label budget ≤ 63.
- **B2**: either render a cross-role child as a Volcano subgroup keyed on a label
  selector without `MatchLabelKeys` (needs Volcano semantics check), or document the
  restriction in the KEP + PR description and point users at equal-scope composition.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/topology-aware-scheduling-claude` (production code
untouched; the only tracked changes are this directory and
`pkg/scheduler/volcano/placement_verify_test.go`).

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/topology-aware-scheduling-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/topology-aware-scheduling pkg/scheduler/volcano/placement_verify_test.go
   ```
2. Prereqs: Layer 1 = Go toolchain only; Layer 2 = envtest binaries at
   `~/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64` (or `setup-envtest use 1.31.0`).
3. Re-run the two commands in the layer table.
4. Read results via the polarity table: `TestVerifySiblingPlacementGroupsGetDistinctPodGroupNames`
   (contract) should be GREEN; `TestVerifyGangWholeGroupPlusSubsetTopologyRuleIsRejected`
   (canary) will be RED when the composition becomes renderable — invert it into a
   contract test then.
5. Harness-bites: run Layer 1 once against pre-fix code to confirm it still goes red.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/topology-aware-scheduling-claude
(https://github.com/cheyang/rbg.git). Background: a review of PR
https://github.com/sgl-project/rbg/pull/473 produced findings B1 (PodGroup name
truncation collision) and B2 (gang+subset topology composition unsupported on
Volcano); a harness reproduced them. The PR is now fixed at <ref>. Read
docs/verification/topology-aware-scheduling/README.md ("Continuing after the fix")
and follow it: graft the harness onto the fixed code, re-run the unit layer
(go test ./pkg/scheduler/volcano/ -run TestVerify -v) and the envtest layer, mind the
polarity table (invert the B2 canary when it flips), run the harness-bites check, and
report an observed-vs-expected table. Do not run cluster-wide destructive actions.
```
