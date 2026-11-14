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

## Claims verified (all CONTRACT polarity — red on base, green at head 3ae9d70b)

| ID | Claim | Layer | Test | Base | Head |
|----|-------|-------|------|------|------|
| C1 | Rendered pod template's required podAffinity selector matches the pod's own labels (self-match → first pod schedulable); anti-affinity matches other groups but not own | unit | `TestVerifyExclusiveTopologyPodSelfMatch` | FAIL | PASS |
| C2 | Workloads whose persisted selector lacks `group-uid` (legacy deploy/sts selector reuse) still get the label injected into the pod template | unit | `TestVerifyExclusiveTopologyLegacySelectorGetsLabel` | FAIL | PASS |
| C3 | A workload selector pinning a conflicting `group-uid` is reported as an error | unit | `TestVerifyExclusiveTopologyConflictReported` | FAIL | PASS |
| C4 | End-to-end through the real controller: RIS pod template carries `group-uid` + self-matching affinity + pod annotation | integration (envtest) | `renders a self-matching group-uid affinity on the RoleInstanceSet template` | FAIL | PASS |
| C5 | Toggling the annotation (off, then on with a different key) reconciles the RIS template — exercises `RBGPredicate` through the real watch | integration (envtest) | `reconciles the RoleInstanceSet template when the annotation is toggled` | FAIL | PASS |

Raw output: `results/unit-{head,base}.txt`, `results/envtest-{head,base}.txt`.

The PR's own new unit tests (`TestPodTemplateExclusiveTopology*`,
`TestRBGPredicateExclusiveTopologyAnnotation`) also pass at head, and its four new e2e specs
ran and passed in the PR's E2E lifecycle CI slice at head 3ae9d70b (kind cluster).

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
