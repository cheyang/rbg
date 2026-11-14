# exclusive-topology — bug verification (Reviewer A, Claude)

Reproducible evidence for the review of PR https://github.com/sgl-project/rbg/pull/588
(fixes https://github.com/sgl-project/rbg/issues/518).

Harness branch: `verify/exclusive-topology-claude`, based on the reviewed PR head
`3ae9d70bf5e0dae747f0d8b512e6e9ba2349b5d1`. Production code is untouched — the diff is
only these docs, the scripts, and additive test files.

Layers run against the **code under review** (`3ae9d70b`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `ConstructPodTemplateSpecApplyConfiguration` + `setExclusiveAffinities` (`pkg/reconciler`) | `go test ./pkg/reconciler -run 'TestVerifyClaude' -v` |
| 2. Integration | real API server + real RBG/RIS controllers (envtest), incl. the annotation-toggle reconcile loop | `KUBEBUILDER_ASSETS=<envtest-bin> go test ./test/envtest/testcase/rbg/ -count=1 -timeout=8m` |
| 3. Live | real scheduler on a real cluster — **not run in this round** (live layer out of scope); the PR's own e2e (`lifecycle` CI group) covers it | see `liveNote` |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. Bug-canary tests (assert current behavior) PASS now / FLIP to red when fixed.

## Problem premise (P0)

Run against the **base** branch (`1fc69f703695bc9355f5730ee3fb3f75cd0331b6`) without the patch.

| | |
|---|---|
| Claimed symptom | "the group's first Pod never schedules, even when free untainted nodes exist: `0/4 nodes are available: 2 node(s) didn't match pod affinity rules, 2 node(s) had untolerated taint(s)`" (issue #518) |
| Linked issue | #518, OPEN, `bug` label, created 2026-10-26 |
| Reported component | `pkg/reconciler/pod_reconciler.go` (`ConstructPodTemplateSpecApplyConfiguration` → `setExclusiveAffinities`), v1alpha2 default RoleInstanceSet path |
| Patched component | `pkg/reconciler/pod_reconciler.go` (+ controller predicate) — same file |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | L1 and L2 harness grafted onto the merge-base both go RED: the injected required podAffinity selects `rbg.workloads.x-k8s.io/group-unique-hash`, while the Pod template labels (and every v1alpha2 Pod) carry `rbg.workloads.x-k8s.io/group-uid`; nothing on base sets `group-unique-hash` (verified by `git grep` at the base sha). See `results/L1-base-premise.log` and `results/L2-base-premise.log`. |

Mechanism (why the symptom is Pending): the Kubernetes scheduler's inter-pod affinity
plugin lets the first Pod of a self-affine group schedule only if the Pod matches its own
required affinity terms (`satisfyPodAffinity` in
`k8s.io/kubernetes@v1.34.2/pkg/scheduler/framework/plugins/interpodaffinity/filtering.go`:
"This pod may be the first pod in a series that have affinity to themselves … if the pod
matches its own terms … we allow the pod to pass"). On base the Pod never matches its own
term (mismatched label key), so `Filter` returns `UnschedulableAndUnresolvable` forever.

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Base produces an affinity selector key no v1alpha2 Pod carries → first Pod Pending | 1+2 | Confirmed | `results/L1-base-premise.log`, `results/L2-base-premise.log` |
| F1 | At head, the affinity selector matches the Pod template's own labels (group-uid), same-group Pods excluded by anti-affinity, other groups matched | 1+2 | Confirmed (fixed) | `results/L1-head.log`, `results/L2-head.log` |
| F2 | Toggling the annotation rewrites the workload template (predicate + rebuild from RBG spec) | 2 | Confirmed | `results/L2-head.log` (annotation removed → affinity dropped) |
| F3 | Legacy workload selectors without group-uid are augmented, not rejected | 1 | Confirmed | `results/L1-head.log` (`TestVerifyClaude_ExclusiveTopologyLegacySelectorIsAugmented`) |
| F4 | Conflicting group-uid in the workload selector errors out (canary: current intended behavior) | 1 | Confirmed (canary) | `results/L1-head.log` (`TestVerifyClaude_ExclusiveTopologyConflictSelectorErrors`) |
| F5 | Opt-out (`role-disable-exclusive`) skips injection but keeps the group-uid label | 1 | Confirmed | `results/L1-head.log` (`TestVerifyClaude_ExclusiveTopologyOptOut`) |

Harness bites: with the fix temporarily un-applied (selector key reverted to
`group-unique-hash`), L1 and L2 both go RED; production file restored afterwards.
See `results/harness-bites.log` (and `git status` clean for `pkg/reconciler/pod_reconciler.go`).

PR's own tests also pass at head: `results/pr-unit-tests.log`
(`go test ./pkg/reconciler ./internal/controller/workloads`).

## Per-finding detail

### P0 — premise (base)
Command (from a worktree of the base sha with the harness grafted on):
`go test ./pkg/reconciler -run 'TestVerifyClaude_ExclusiveTopologyPodMatchesItsOwnAffinity' -v`
Observed: FAIL — `affinity selector key "rbg.workloads.x-k8s.io/group-unique-hash" != "rbg.workloads.x-k8s.io/group-uid"`.
Expected (per the contract): the required affinity the controller injects must select a
label the Pod template actually carries. On base it does not → the reported symptom is real.

### F1 — fix contract (head)
`go test ./pkg/reconciler -run 'TestVerifyClaude' -v` → all green. The L2 run confirms the
same contract on the workload object the controllers actually persist
(`RoleInstanceSet.spec.roleInstanceTemplate.components[0].template`), not just the
constructed apply-configuration.

### F2 — annotation toggle (head)
envtest: create RBG with `rbg.workloads.x-k8s.io/group-exclusive-topology:
kubernetes.io/hostname` → RIS template gains the affinity; then remove the annotation →
the affinity disappears from the RIS template. This also demonstrates the upgrade-repair
mechanism: the Pod template is rebuilt from the RBG spec on every reconcile, so a workload
created by the old controller (affinity selecting `group-unique-hash`) is rewritten — not
left with the legacy terms.

### F3 — legacy selector augmentation (head)
A workload selector without `group-uid` (older controller) is kept verbatim while the
template gains the identity label, so the first Pod still matches its own affinity.

### F4 — conflict guard (head, canary)
A workload selector carrying a *different* `group-uid` makes
`ConstructPodTemplateSpecApplyConfiguration` return an error naming the label key, instead
of silently generating an unsatisfiable affinity. Canary polarity: if the maintainers
decide to auto-correct or warn instead, invert this test.

### F5 — opt-out (head)
`role-disable-exclusive: "true"` on the role's Pod template → no injected affinity, and the
Pod keeps `group-uid` (so it remains a valid affinity anchor for enabled roles).

## Live run notes

Not run this round (live layer skipped by policy). What the live layer should confirm on a
real cluster, and what the PR's own e2e (in the CI `lifecycle` group) already asserts:

- same-group Pods co-locate on one node ("schedules exclusive groups together …");
- another group (ordinary or exclusive) is blocked from that node with a
  `FailedScheduling`/anti-affinity event;
- the disabled-role anchor case and the annotation-change case.

## Proposed fixes (NOT applied to production here)

None required for the core fix — the PR's approach is correct and verified. Review-level
suggestions live in the findings document, not the harness:

- consider emitting an Event/Condition when the F4 conflict guard fires (it currently only
  fails the reconcile; roles in later dependency layers are skipped too);
- consider treating an empty `group-exclusive-topology: ""` value as absent instead of
  hard-erroring ("topology key can't be nil") — pre-existing behavior.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/exclusive-topology-claude` (production code untouched), so
it grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/exclusive-topology-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/exclusive-topology-claude \
     pkg/reconciler/verify_exclusive_topology_claude_test.go \
     test/envtest/testcase/rbg/verify_exclusive_topology_claude_test.go
   ```
2. Run the unit layer: `go test ./pkg/reconciler -run 'TestVerifyClaude' -count=1 -v`
3. Run the integration layer:
   `KUBEBUILDER_ASSETS=<envtest-1.31-bin> go test ./test/envtest/testcase/rbg/ -count=1 -timeout=8m`
4. Or run everything: `bash docs/verification/exclusive-topology-claude/scripts/re-verify.sh`

Polarity table (what changes when the bug is fixed):
- `TestVerifyClaude_ExclusiveTopologyPodMatchesItsOwnAffinity` — contract: red on base/buggy, green when fixed.
- `TestVerifyClaude_ExclusiveTopologyAntiAffinitySemantics` — contract: same.
- `TestVerifyClaude_ExclusiveTopologyLegacySelectorIsAugmented` — contract: same.
- `TestVerifyClaude_ExclusiveTopologyOptOut` — contract: same.
- `TestVerifyClaude_ExclusiveTopologyConflictSelectorErrors` — canary: green while erroring is the intended behavior; if the behavior changes to auto-correct, invert it.
- envtest "reviewer verify" It — contract: red on base, green when fixed.

Kickoff prompt for a fresh agent:

> You are re-verifying PR https://github.com/sgl-project/rbg/pull/588. Checkout the
> current PR head, graft the harness from branch `verify/exclusive-topology-claude`
> (fetch from https://github.com/cheyang/rbg.git) using step 1 above, then run
> `bash docs/verification/exclusive-topology-claude/scripts/re-verify.sh` and report
> Fixed / Still-broken / Partial / Harness-update per finding in
> `docs/verification/exclusive-topology-claude/README.md`.
