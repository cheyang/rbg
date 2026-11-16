# Verification: PR #588 — fix exclusive topology scheduling (issue #518)

Reviewer B (Codex) harness. Additive only: two test files, no production code touched.

## Premise (P0) — CONFIRMED on base

Issue #518: with `rbg.workloads.x-k8s.io/group-exclusive-topology: kubernetes.io/hostname` on a
v1alpha2 RoleBasedGroup, the first Pod never schedules because the injected required podAffinity
selects `rbg.workloads.x-k8s.io/group-unique-hash`, a label no Pod carries (Pods carry the same
SHA1 hash under `rbg.workloads.x-k8s.io/group-uid`).

- Reproduced deterministically at the **unit layer** on base `1fc69f70`:
  `results/unit-base.txt` — the rendered pod template's affinity selector is
  `group-unique-hash in (<hash>)` while its labels carry only `group-uid: <hash>`, so the
  first Pod fails the kube-scheduler self-match rule and can never bind.
- Reproduced at the **integration layer** (envtest, real API server + real RBG/RIS/RI
  controllers): `results/envtest-base.txt` — the RoleInstanceSet pod template rendered by the
  base controller shows the same selector/label mismatch, and an annotation toggle is never
  reconciled (base `RBGPredicate` ignores annotation-only updates).
- Archaeology: the original v1alpha1 implementation (#32, 26df2e70) selected
  `group-unique-key`, which v1alpha1 pods did carry; the mismatch was introduced by the
  v1alpha2-storage migration (#188, 415f5869) which pointed the selector at
  `constants.GroupUniqueHashLabelKey` while v1alpha2 common labels carry `group-uid`.
- Component match: report names `pkg/reconciler/pod_reconciler.go`; that is exactly what the
  PR patches. ComponentMatch = true.

## Claims verified (all CONTRACT polarity — red on base, green at head 29fa5faa)

| ID | Claim | Layer | Test | Base | Head |
|----|-------|-------|------|------|------|
| C1 | Rendered pod template's required podAffinity selector matches the pod's own labels (self-match → first pod schedulable); anti-affinity matches other groups but not own | unit | `TestVerifyExclusiveTopologyPodSelfMatch` | FAIL | PASS |
| C2 | Workloads whose persisted selector lacks `group-uid` (legacy deploy/sts selector reuse) still get the label injected into the pod template | unit | `TestVerifyExclusiveTopologyLegacySelectorGetsLabel` | FAIL | PASS |
| C3 | A workload selector pinning a conflicting `group-uid` is reported as an error | unit | `TestVerifyExclusiveTopologyConflictReported` | FAIL | PASS |
| C4 | End-to-end through the real controller: RIS pod template carries `group-uid` + self-matching affinity + pod annotation | integration (envtest) | `renders a self-matching group-uid affinity on the RoleInstanceSet template` | FAIL | PASS |
| C5 | Toggling the annotation (off, then on with a different key) reconciles the RIS template — exercises `RBGPredicate` through the real watch | integration (envtest) | `reconciles the RoleInstanceSet template when the annotation is toggled` | FAIL | PASS |
| C6 | Round-2 (S1 fix): a stored pod template with the legacy `group-unique-hash` affinity is judged NOT equal so deploy/sts/lws reconciles repair it on upgrade; stable after repair; gate scoped to annotated templates | unit | `TestVerifyExclusiveTopologyAffinityGateRepairsLegacy` | FAIL (prev head 3ae9d70b) | PASS (29fa5faa) |

Raw output: `results/unit-{head,base}.txt`, `results/envtest-{head,base}.txt`, `results/reverify/`.

Round 2 (head 29fa5faa, delta 3ae9d70b..29fa5faa): all six contracts re-verified green via
`scripts/re-verify.sh` (results under `results/reverify/`). C6 was confirmed differential
(red on 3ae9d70b, green on 29fa5faa). The PR's own round-2 tests were also run at head:
`TestDeploymentReconciler_RepairsLegacyExclusiveTopology` (unit, PASS) and the envtest spec
`rewrites RoleInstanceSet affinity when only the exclusive topology annotation changes`
(PASS, 11.7s). The new upgrade e2e specs (`test/e2e/upgrade/exclusive_topology.go`,
specs.go phases 1/3/4) compile clean (`go vet`) but have NOT executed: the
`e2e-test-upgrade` job is release-gated (`/release-test` comment) and was not run for this
head — the live-layer guard for the S1 repair is therefore still pending.

The PR's own new unit tests (`TestPodTemplateExclusiveTopology*`,
`TestRBGPredicateExclusiveTopologyAnnotation`) also pass at head, and its four new e2e specs
ran and passed in the PR's E2E lifecycle CI slice at head 3ae9d70b (kind cluster).

Round 3 (head fc8cfc48, delta 29fa5faa..fc8cfc48): docs-only delta — one paragraph added to
`doc/features/exclusive-topology.md` ("Before upgrading, verify that replacement Pods can satisfy
both the same-group affinity and the cross-group anti-affinity ..."). All six contracts re-verified
green via `scripts/re-verify.sh` (unit + integration; results under
`results/reverify-round3-fc8cfc48/`). The paragraph's scheduler claims were verified against
kube-scheduler source (`interpodaffinity/filtering.go` satisfyPodAffinity/satisfyPodAntiAffinity,
identical logic in v1.29.15 and v1.34.2): the self-match escape requires
`len(state.affinityCounts) == 0`, so while same-group Pods remain in a shared domain the required
affinity pins replacements to that domain, and `antiAffinityCounts` blocks any domain holding
other groups' `group-uid` Pods — the documented Pending/deadlock hazard is accurate. S10 update:
`/release-test` ran on 29fa5faa (run 43791276444) and failed ONLY in the new exclusive-topology
specs because the workflow deploys the chart-pinned pre-fix controller image
(`v0.9.0-73e05db4`), never a build of the PR — maintainer cheyang diagnosed this on 2026-11-16
and explicitly accepted that the upgrade specs first execute post-merge after the image bump
(issue-comment, non-gating). Live-layer validation recipe documented by maintainer:
build the head image, `RBGS_TO_TAG=<tag> make test-e2e-upgrade` on a dedicated cluster.

## How to run

```bash
# unit layer
go test ./pkg/reconciler/ -run 'TestVerifyExclusiveTopology' -count=1 -v

# integration layer (needs envtest binaries; repo pins 1.31.0)
KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64 \
  go test ./test/envtest/testcase/rbg -count=1 -timeout=8m \
  -args -ginkgo.focus='Exclusive topology verification' -ginkgo.v
```

## Continuing after the fix / re-verification

From a checkout of this branch:

```bash
bash docs/verification/exclusive-topology-uid/scripts/re-verify.sh          # uses current PR head
bash docs/verification/exclusive-topology-uid/scripts/re-verify.sh <sha>    # explicit ref
```

All findings are contract tests: a fix keeps them green; a regression turns them red.
`.last-reviewed` records the reviewed head; advance it after each round.

## Notes / non-blocking observations

- The widened anti-affinity (now `group-uid Exists + NotIn`) deliberately excludes *all* other
  RBGs' pods in the namespace from the exclusive group's topology domain, including groups that
  never opted in — documented in `doc/features/exclusive-topology.md` and the PR body.
- Pre-existing, out of scope: for a **v1alpha1 RoleBasedGroupSet**, the exclusive-topology
  annotation set on the RBGSet metadata is translated onto the converted v1alpha2 RBGSet's own
  annotations but never into `spec.groupTemplate.annotations`, so child RBGs do not receive it
  (only the coordination annotation is copied). The v1alpha1 RBGS e2e that would catch this
  discards the `ExpectRbgAnnotation` return value and is not selected by any CI label slice.
