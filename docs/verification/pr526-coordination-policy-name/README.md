# Verification — PR #526: name the policies applied from the v1alpha1 coordination annotation

Reviewer: B (Codex). Round: initial. Production code untouched by this branch
(diff vs PR head is harness + docs only).

- Base (merge-base): `8a7bcfcb5dbf104788b35e8cb143d6090a3f6c37`
- Reviewed head: `a9f998878b0767746d2ffff2b3f316723ef34d79`
- PR: https://github.com/sgl-project/rbg/pull/526 (fixes #517)

## Premise (P0) — CONFIRMED

Issue #517 claims that on `main` a v1alpha1 RoleBasedGroup with
`spec.coordination` never gets workloads: the conversion preserves coordination
in the `conversion.workloads.x-k8s.io/v1alpha1-coordination` annotation, and the
RBG reconcile (step 5, `EnsureV1alpha1CoordinatedPolicy`) server-side-applies a
CoordinatedPolicy whose `spec.policies[]` entries omit `name`. `spec.policies`
is `+listType=map`/`+listMapKey=name`, so the apiserver rejects the apply and the
reconcile returns before step 8 creates any role workload.

Verified against the BASE branch by grafting the PR's own envtest onto the
merge-base (production code at base, tests at head):

```
cd <base worktree>   # 8a7bcfcb, with the PR's two test files checked out
export KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64
go test ./test/envtest/testcase/rbg/ -count=1 -v -args --ginkgo.focus "converted from v1alpha1"
```

Observed on base: the spec FAILS after ~35s; the controller log repeats
`apply CoordinatedPolicy …: failed to create typed patch object … .spec.policies:
element 0: associative list with keys has an element that omits key field "name"`
(13 Reconciler errors in one run, no CoordinatedPolicy created). This is the
exact error string quoted in issue #517, reproduced against a real (envtest)
apiserver. Symptom, mechanism and component all match the report. Component match:
the report names `buildCoordinatedPolicyApplyConfiguration` in
`internal/controller/workloads/coordinatedpolicy_migration_controller.go`, which
is exactly the file the PR patches. Evidence: `results/envtest-base.log`.

## Observed vs expected

| Finding | Layer | Polarity | Base | Head | Verdict |
| --- | --- | --- | --- | --- | --- |
| P0 premise (#517 reproduces on main) | integration (envtest) | n/a (runs against base) | spec FAILS with the exact SSA list-key error | spec PASSES | Confirmed |
| F1 fix works: named CoordinatedPolicy applied, both role workloads created | integration (envtest) | contract | red | green | Fixed by PR |
| F1-unit: applied policy carries `policies[0].name == "c1"` | unit | contract | red (actual: "") | green | Fixed by PR |
| E1 (minor): coordination entry with empty `name` still unfixable post-merge — `WithName("")` serializes `"name":""`, rejected by CRD `minLength: 1` | unit | canary (documents current behavior) | n/a | green (payload = `{"name":"","roles":[...]}`) | Confirmed residual gap; pre-existing class, NOT introduced by this PR |

Regression sweep on head: full `go test ./internal/controller/workloads/` and the
full `./test/envtest/testcase/rbg/` suite (14 specs) both pass. CI on the PR was
green at round start (`statusCheckRollup: success`).

## How to re-run

Unit layer (no cluster needed):

```
go test ./internal/controller/workloads/ -run 'TestEnsureV1alpha1CoordinatedPolicy|TestVerifyPR526' -count=1 -v
```

Integration layer (needs envtest assets; 1.31.0 was used here):

```
export KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path)   # or the cached path above
go test ./test/envtest/testcase/rbg/ -count=1 -v -args --ginkgo.focus "converted from v1alpha1"
```

One-command re-verification of a future fix round (from a checkout of this branch):

```
bash docs/verification/pr526-coordination-policy-name/scripts/re-verify.sh
# resolves the current PR head from manifest.pr, grafts the harness, runs L1+L2.
# Exit 0 iff all contract findings pass (canary E1 is informational).
```

## Continuing after a fix

All findings are contracts except E1 (canary documenting current behavior for a
degenerate input). If a later round makes the controller skip or default empty
coordination names, invert `TestVerifyPR526_EmptyCoordinationName_StillSerializedEmpty`
or delete it and assert the new behavior in the migration controller tests.

Handoff prompt for a fresh machine:

> Review PR https://github.com/sgl-project/rbg/pull/526. Fetch
> `verify/pr526-coordination-policy-name-codex` from https://github.com/cheyang/rbg.git,
> read `docs/verification/pr526-coordination-policy-name/README.md` and
> `verify-manifest.json`, then run `scripts/re-verify.sh` against the current PR
> head. KUBEBUILDER_ASSETS must point at envtest 1.31.0 binaries.
