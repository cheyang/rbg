# PR #509 verification — v1alpha1 deprecation & compat gate (Reviewer A: Claude)

- PR: https://github.com/sgl-project/rbg/pull/509
- Head reviewed: `2c9cce6a73e34e337c9d0ab82a7472a3c910c406`
- Base: `ecdfd6e601de64adc0d0892c7ad05f75e85873a8`
- Harness branch: `verify/pr509-v1alpha1-deprecation-claude` (pushed to the reviewer fork `cheyang/rbg`)
- Production code is untouched by this harness — the diff to the PR head is tests + docs only.

## Premise (P0) — Confirmed

The PR claims 0.9.0 must warn on every v1alpha1 write and gate *new* deprecated
workload uses while keeping *existing* ones reconcilable. On the base branch
there is no warning code at all (`api/workloads/v1alpha1/deprecation_webhook.go`
is new in this PR) and the only gate is the flag-off cutoff
(`if !v.EnableDeprecatedWorkloadTypes` in `rolebasedgroup_admission.go`), i.e.
v1alpha1 writes succeed silently and there is no compat-mode middle ground. The
premise is a real gap; the component matches the patch.

## Observed vs expected

| ID | Claim | Layer | Polarity | Result on head | Verdict |
| --- | --- | --- | --- | --- | --- |
| P0 | base lacks warnings + compat gate | code-path | — | warning log lines emitted on every v1alpha1 write (unit-head.out, integration-head.out) | Confirmed |
| F1 | an existing RBGSet with a legacy template role cannot scale up in compat mode: the controller-issued child CREATE is rejected and the reconcile requeues forever | integration (envtest) | canary | **canary passes = bug present**. `failed to create RoleBasedGroup legacy-set-1: admission webhook "vrolebasedgroup.kb.io" denied the request: ... "apps/v1/StatefulSet" is deprecated; new uses are not accepted` (results/integration-head.out) | **Confirmed** |
| F2 | the v1alpha1 validator's supplementary cached client.Get can reject an otherwise-admitted grandfathered update (read failure) or override the request's authoritative oldObj with a stale cache copy | unit | contract | **both tests red = bug reproduced** (results/unit-head.out) | **Confirmed** |
| G1 | the gate itself behaves as designed: new deprecated use rejected, grandfathered update admitted without a client | unit | contract | green | Control |
| G2 | grandfathered set/child updates admitted; direct create rejected; v1alpha1 writes hit the deprecation webhook; non-legacy set scales under live admission | integration | contract | all green | Control |

All green is **not** "no bug" here: F1 is a bug-canary (asserts the broken
state; flips red when fixed) and F2's contract tests are red on the head.

## How to run

```bash
# unit layer (F2) — expects 2 FAIL on the PR head (the reproduction)
go test ./api/workloads/v1alpha1/ -run 'TestPR509' -v

# integration layer (F1 + contracts) — envtest assets required
export KUBEBUILDER_ASSETS="$(ls -d ~/.local/share/kubebuilder-envtest/k8s/1.31.0-* | head -1)"
go test ./test/envtest/testcase/deprecation/ -timeout 10m -v
```

The envtest suite reproduces the production admission topology: the v1alpha2
validating/mutating webhooks, all six new v1alpha1 deprecation webhooks, and the
RBGSet controller. "Legacy" objects are seeded by temporarily pointing every
validating webhook's rules at a never-matching resource (`relaxAdmission` in
suite_test.go) — the envtest stand-in for "this object existed before the
0.9.0 upgrade" — then restoring the production rules.

## Harness-bites check (done, then reverted)

For F2, the proposed fix (trust the request's oldObj; drop the persisted-object
read) was applied temporarily to `deprecation_webhook.go`: both F2 contract
tests went green. The fix was reverted; `git diff` on production code is empty.

For F1, the control spec (`control: scales a non-legacy RoleBasedGroupSet...`)
creates `ctl-set-0` and `ctl-set-1` through live admission, proving the
controller, admission chain, and seeding path all work; the only difference in
the F1 spec is the template's legacy workload annotation.

## Continuing after the fix

`bash docs/verification/pr509-v1alpha1-deprecation-claude/scripts/re-verify.sh`
(no argument) resolves the current PR head, grafts this harness onto it, and
prints per finding Fixed / Still-broken / Partial:

- F1 (canary) is fixed only when it **flips to fail** — child `legacy-set-1`
  gets created. Then invert the canary (or promote it to a contract test
  asserting the child appears).
- F2 (contract) is fixed when both `TestPR509F2*` tests pass.
- G1/G2 must stay green; a red control means a harness update.

Fresh-agent kickoff: clone the PR repo, fetch this branch
(`verify/pr509-v1alpha1-deprecation-claude` from the reviewer fork), check it
out, read this README, run the two `go test` commands above, then
`scripts/re-verify.sh` for the delta since `.last-reviewed`.

## F2 proposed fix

In `deprecatedWorkloadValidator.ValidateUpdate` (api/workloads/v1alpha1/deprecation_webhook.go),
the request's `oldObj` already is the persisted object as of this update and is
authoritative. Either drop the `v.Client.Get` block entirely, or — if the
decoded-oldObj defaults concern is real — read through a **direct API reader**
(not the informer cache) and treat read failure as "fall back to oldObj", never
as a rejection.
