# PR #524 verification — 0.9.0 compatibility admission gate (core gate)

PR: https://github.com/sgl-project/rbg/pull/524
Reviewed head (round 2): `22acc57039523bf4fc7f28c47c93bd29961930ba` (base `a2e797c6aa5702059b4e3341f3d8837972df130f`)
Round-1 head: `993dcf10c618f0b8f143ce2471d72d820126e260` (base `885d655bd1b4c26e6d14837709eeda675f3ae886`)
Reviewer: Codex (debate Reviewer B). Production code untouched; this branch adds tests + docs only.
This PR is the first slice split out of the closed PR #509; findings F1–F4 from
the #509 review (branch `verify/pr-509-deprecation-window-codex`) are carried
forward and re-verified here against the new code shape.

## Premise (P0) — CONFIRMED

The PR claims the default mode (`--enable-deprecated-workload-types=true`)
needs a compatibility gate: existing deprecated workload uses keep working,
new uses are rejected, and v1alpha1 writes must return deprecation warnings.

Verified against the **base** branch: `RoleBasedGroupValidator` with
`enableDeprecatedWorkloadTypes=true` admits a brand-new deprecated role with no
warning and no error (`results/premise-base.out`), and the base registers no
v1alpha1 admission webhook at all. The same premise test FAILS on the PR head
(`results/premise-head.out`) because the gate now rejects — polarity control.
Component match: the problem and the patch both live in the admission webhooks.


## Round 2 (head 22acc570, 2026-10-31) — re-verification

The head was force-pushed: rebase onto `a2e797c` (#514; the only base delta,
which already contained the `inactive_pod.go` rewrite, so the old fixture
hunk was dropped unchanged in effect) plus one new commit `22acc570`
("fail-open warning-only webhooks and pin the v1alpha1 contract").

| Ledger ID | Harness | Round-1 verdict | Round-2 observed | Verdict |
|-----------|---------|-----------------|------------------|---------|
| F2 (warning-only webhooks ship Fail) | harness F5 canary + check-shipped-webhook-policy.sh | Confirmed (major) | shipped bytes now: 4 warning-only `Ignore`, 2 enforcing `Fail`, no DELETE (`results/shipped-webhook-policy-22acc570.out`); new contract `TestShippedWarningOnlyPolicy` red on 993dcf10 bytes, green on head; outage mechanism demo green | **Fixed** |
| F1/missingTests (no in-repo v1alpha1 admission coverage) | PR's own new `test/envtest/testcase/deprecationv1a1` | open (major) | PR now ships a 7-spec envtest suite that installs the shipped `config/webhook/manifests.yaml` against the production manager registrations and asserts on-the-wire warnings, new-use rejection (RBG + set template), Exact routing, ungated deletes: 7/7 green on head (`results/pr-suite-deprecationv1a1-22acc570.txt`); bite-checked: neutered warning fails spec 1 (`...-bite-warning.txt`), neutered gate fails the BeforeSuite readiness probe (`...-bite-gate.txt`); wired into CI (`go test ./test/envtest/testcase/...`, envtest job green) | **Fixed** |
| F3 (exemption forgeable / docstring overclaims) | harness F6 canary | Confirmed (minor) | exemption code unchanged (range-diff: only commit 3's fixture hunk + new commit); canary still green = forged ownerRef with known UID still admitted | **Still open (minor)** |
| F4/F7/O1 (nits: fixture names, dead objectKey, hint spelling) | static | open | `objectKey` still present (deprecation_webhook.go:181); hint and fixture names untouched by 22acc570 | **Still open (nit)** |
| Copilot advisories C4381195150 / C4381195073 / C4381195209 | code inspection | unconfirmed | all three addressed in 22acc570 (debug.go checks RoleInstanceSet first; compat checker asserts per-component template metadata — component names `leader`/`worker`/`role.Name` cross-checked against pkg/reconciler/roleinstanceset_reconciler.go:442-498; upgrade fixtures pin StatefulSet/LeaderWorkerSet explicitly) | **Fixed (by inspection; e2e layer not run locally)** |
| Regression sweep | re-verify.sh unit+integration | — | all carried contracts green on 22acc570 (F1-F4 harness, E1-E3); PR unit tests + envtest deprecation/webhook suites green (`results/pr-unit-22acc570.txt`, `results/pr-envtest-22acc570.txt`); `make manifests` byte-idempotent | **No regression** |
| CI | gh pr checks | success (previous head) | all checks pass on 22acc570 (unit-test, envtest, lint, 12 e2e jobs incl. v1alpha1 + manifest-installation) | **success** |

## Findings and observed-vs-expected

| ID | Severity | Claim | Layer | Expected (contract) | Observed on head | Verdict |
|----|----------|-------|-------|---------------------|------------------|---------|
| F1 (carry #509-F1) | blocker on #509 | Compat gate must admit the set controller's replacement children of un-migrated legacy templates | unit + envtest | child create admitted | admitted (owner-ref + live parent UID + template name/type match) | **Fixed** |
| F2 (carry #509-F2) | major on #509 | Warning-only v1alpha1 webhooks must not intercept DELETE with failurePolicy=Fail | static + envtest | deletes never intercepted | shipped webhooks register CREATE;UPDATE only; delete succeeds during simulated outage | **Fixed** |
| F3 (carry #509-F3) | minor on #509 | Exemption must be decided from the admission request's oldObj, not a cache re-read | unit + envtest | re-introduction of a deprecated type after migration rejected | rejected; v1alpha1 validator carries no client at all | **Fixed** |
| F4 (carry #509-F4) | minor on #509 | Compat-mode rejection must not advise re-enabling the already-enabled flag | unit | no re-enable advice in compat hint | `deprecatedWorkloadTypeCompatHint` points at RoleInstanceSet migration; flag advice kept only in the flag=false path | **Fixed** |
| F5 (new) | minor | The four warning-only v1alpha1 webhooks (Instance, InstanceSet, ScalingAdapter, ClusterEngineRuntimeProfile) ship `failurePolicy: Fail` on CREATE+UPDATE; a webhook outage blocks every write to those legacy types with zero enforcement value — the exact rationale the PR itself uses for not registering DELETE | static + envtest (canary) | warning-only webhooks should not be able to block writes | shipped policy is Fail (`results/shipped-webhook-policy.out`); dead-webhook demo: create rejected with `failed calling webhook ... connection refused`, Ignore-policy control admits | **Confirmed** |
| F6 (new) | minor | The replacement-child exemption is forgeable: any user who can read the parent set's UID (non-secret metadata) and create RBGs in the namespace gets the exemption; the `legacySetTemplateRoles` docstring claims "a forged owner reference ... grants nothing", which holds only for an *unknown* UID | envtest (canary) | unrelated creates stay rejected | create with forged controller ownerRef + correct UID + matching role name/type admitted, with an arbitrary pod template | **Confirmed (soft-gate bypass; documented behavior pinned)** |
| F7 (new) | nit | `objectKey` in `api/workloads/v1alpha1/deprecation_webhook.go` is dead production code kept alive only by its own unit test | static | no dead helpers | only referenced by `deprecation_webhook_test.go` | **Confirmed** |

Supporting (green) acceptance evidence on head:
- E1: grandfathered v1alpha1 RBG (schema-defaulted StatefulSet workload) stays
  writable in compat mode through the *real* API-server path — Exact-match
  v1alpha1 webhook + Equivalent-match v1alpha2 webhook + real conversion
  webhook round-trip — and the client receives the `v1alpha1 is deprecated`
  admission warning.
- E2: a new v1alpha1 create relying on the StatefulSet schema default is
  rejected; a renamed role on a grandfathered object is rejected.
- E3: stale-owner isolation — after the parent set's template migrates to
  RoleInstanceSet, an old-shape child carrying the (still valid) owner
  reference is rejected.
- E4: `make manifests` is byte-idempotent on the PR head (claim from the PR
  body; verified locally, clean `git status` afterwards).
- E5: repo's own `test/envtest/testcase/webhook` and `test/envtest/testcase/deprecation`
  suites pass on the head with k8s 1.31 envtest assets.

## Harness-bites check

- P0: premise test passes on base, fails on head (results/premise-*.out) — the
  gate is what changes the verdict.
- F1 envtest: the identical child *without* an owner reference is rejected
  (control subtest), proving the exemption path — not a gate hole — admitted
  the replacement child.
- F1 unit: wrong-UID / missing-parent / name-mismatch / type-mismatch /
  migrated-parent subtests all reject (zz_verify_pr524_test.go), proving the
  admitted case goes through every guard.
- F5: the Ignore-policy control admits during the same dead-webhook outage,
  proving the Fail policy is what blocks.
- F6 canary: pins current (bypass) behavior; flips red when the exemption is
  hardened (e.g. by checking the request user belongs to the set controller's
  service account — not available through the CustomValidator API today).
- F4: control asserts the flag=false path still carries the re-enable advice,
  proving the assertion discriminates.

## How to run

Unit layer (fast, no cluster):

```bash
go test ./api/workloads/v1alpha1/ ./api/workloads/v1alpha2/ -run 'TestPR524' -v
```

Integration layer (envtest; needs `bin/setup-envtest`, see `make setup-envtest`):

```bash
KUBEBUILDER_ASSETS="$(bin/setup-envtest use 1.31.0 --bin-dir bin -p path)" \
  go test ./test/verifycodex/ -v -count=1 -timeout 10m
```

Static shipped-policy evidence:

```bash
bash docs/verification/deprecation-admission-gate-codex/scripts/check-shipped-webhook-policy.sh
```

Premise re-check (base branch):

```bash
cp docs/verification/deprecation-admission-gate-codex/premise/zz_premise_base_test.go \
   api/workloads/v1alpha2/ && go test ./api/workloads/v1alpha2/ -run TestP0 -v
```

## Continuing after the fix

```bash
bash docs/verification/deprecation-admission-gate-codex/scripts/re-verify.sh
```

(no ref needed: it fetches the current PR head from `manifest.pr`; the delta
since the last round comes from `.last-reviewed`). Polarity: F1–F5 + E1–E3 are
contract tests (green = good; F5 was inverted from a canary to the
shipped-bytes contract `TestShippedWarningOnlyPolicy` after the 22acc570 fix).
Only F6 remains a canary pinning the current exemption behavior — it FLIPS red
when the exemption is hardened; invert or promote it then.

## Proposed fixes (reviewer suggestions, not applied)

- F5: set `failurePolicy: Ignore` on the four warning-only v1alpha1 deprecation
  webhooks (Instance, InstanceSet, ScalingAdapter, ClusterEngineRuntimeProfile).
  A lost warning during an outage is strictly better than blocked writes; this
  is the same tradeoff the PR already makes by not registering DELETE.
- F6: either soften the docstring (the guard bounds *stale-read* abuse, not
  deliberate forgery) or move the exemption to a handler that can see
  `admission.Request.UserInfo` and require the set controller's service
  account.
- F7: drop `objectKey` (and its test) — leftover from the #509 persisted-read
  design.
