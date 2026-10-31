# pr-524-compat-gate — PR #524 compatibility admission gate verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/524.

## Round 2 (head `22acc57039523bf4fc7f28c47c93bd29961930ba`, base `a2e797c6aa5702059b4e3341f3d8837972df130f`)

The PR was rebased (base moved `885d655` → `a2e797c6`, which brings #514 —
test/e2e infra and vendor only, no admission/production code, so the round-1
premise evidence carries over) and force-pushed with two substantive changes:

1. **F2 fixed**: the four warning-only v1alpha1 webhooks now register
   `failurePolicy=ignore` (markers + both shipped manifests); the two enforcing
   v1alpha1 RBG/RBGSet webhooks keep `Fail`.
2. **F1 fixed**: the PR now ships in-repo integration coverage of the v1alpha1
   admission chain at `test/envtest/testcase/deprecationv1a1` +
   `test/envtest/testutil/v1alpha1_contract_setup.go`, grafted from this
   harness's round-1 suite (production `config/webhook/manifests.yaml` +
   production manager registrations + on-the-wire warning assertions + a
   gate-readiness dry-run probe).

The three Copilot e2e advisories from round 1 (C4381195150, C4381195073,
C4381195209) are also addressed in this revision (see the round-2 ledger).

This round's harness therefore shrinks to what the PR does **not** carry:

- `test/envtest/testcase/deprecationv1a1outage` — F2 fail-open contract: with
  the shipped warning-only webhooks pointed at a dead port, writes to the four
  still-supported v1alpha1 kinds must be admitted (plus a Fail-policy control
  proving the dead endpoint is really consulted).
- `test/envtest/testcase/deprecationv1a1spoof` — F3 bug-canary (carried).

Reviewer: Claude (reviewer A in the two-reviewer debate). Harness branch:
`verify/pr-524-compat-gate-claude`. Production code is untouched — the diff vs
the PR head is only this harness (test files + docs).

Layers run against the **code under review** (`22acc570`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | gate decision logic, conversion of v1alpha1 workload to the v1alpha2 annotation, controller wiring | `go test ./api/workloads/v1alpha1 ./api/workloads/v1alpha2 ./cmd/rbgs ./internal/controller/workloads` |
| 2. Integration (envtest) | the PR's own deprecation + deprecationv1a1 suites + this harness's outage and spoof suites | `KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64 go test -count=1 ./test/envtest/testcase/deprecation/... ./test/envtest/testcase/deprecationv1a1/... ./test/envtest/testcase/deprecationv1a1outage/... ./test/envtest/testcase/deprecationv1a1spoof/...` |
| 3. Live | not run (round policy: unit + integration only, no KUBECONFIG) | see `liveNote` in verify-manifest.json |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. Bug-canary tests (assert current behavior) PASS now / FLIP to red when fixed.

## What this harness adds beyond the PR's own tests

**Round 2 note:** the PR now ships `test/envtest/testcase/deprecationv1a1` in-repo
(grafted from this harness's round-1 suite), so the v1alpha1-side coverage described
below is part of the PR itself and runs in its CI. What remains reviewer-only are the
two round-2 packages: `deprecationv1a1outage` (F2 fail-open contract) and
`deprecationv1a1spoof` (F3 canary). The original gap, for the record:

The PR's envtest suite (`test/envtest/testcase/deprecation`) installs **hand-written
v1alpha2-only** webhook configurations. It never installs the v1alpha1 deprecation
webhooks and never applies the shipped manifests. The round-1 harness (now in-repo as
`test/envtest/testcase/deprecationv1a1`):

- registers **exactly what `cmd/rbgs/main.go` registers** for a 0.9.0 default upgrade
  (v1alpha2 admission webhooks + all six v1alpha1 deprecation webhooks, compat flag on);
- seeds the pre-upgrade state, then installs the **production
  `config/webhook/manifests.yaml`** (both Mutating and Validating configurations,
  clientConfig rewritten to the envtest server) — so every generated webhook path,
  rule, matchPolicy and failurePolicy is exercised against the real handlers;
- captures AdmissionWarnings through a `rest.Config.WarningHandler` recorder, so the
  warning contract is asserted, not just assumed;
- carries the bug-canary for finding F3 (replacement-child exemption spoofability).

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "existing deprecated workload roles remain reconcilable, while new deprecated workload uses are rejected" + "an Admission Warning for every v1alpha1 create/update" (PR body) |
| Linked issue | none closing; #509 (closed, the superseded 2,400-line PR this was split from) and #512 are referenced only |
| Reported component | admission validation layer (v1alpha2 validators, v1alpha1 webhooks) |
| Patched component | same — `api/workloads/v1alpha1/deprecation_webhook.go`, `api/workloads/v1alpha2/*_admission.go`, `cmd/rbgs/main.go`, `config/webhook` + `deploy/kubectl` manifests |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | On base, the validator's own unit test passes asserting *"deprecated workload types enabled, StatefulSet - no error"* — i.e. with the default flag a **new** deprecated use is silently admitted, no compat window exists. `api/workloads/v1alpha1/` on base has zero webhook files / markers, so no v1alpha1 admission warning is produced. See `results/premise-base.txt`. |

## Summary of results

### Round-2 observed vs expected

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| F2-WARNING-ONLY-OUTAGE | With the four shipped warning-only v1alpha1 webhooks unreachable, a v1alpha1 InstanceSet write is admitted (Ignore policy); the same dead endpoint with Fail rejects it | 2 | **Fixed** — contract green on head, red when the manifest is reverted to the pre-fix Fail (`results/bite-outage-policy-fail.txt`) | `results/outage-r2.txt` |
| GATE-V1A1-WIRING | The v1alpha1 admission chain works end-to-end as shipped — **now covered in-repo by the PR itself** | 2 | **Fixed (coverage landed)** — 7/7 green; bite checks: neutered warning → spec red, neutered gate → BeforeSuite red | `results/envtest-head-r2.txt`, `results/bite-warning-removed-r2.txt`, `results/bite-gate-neutered-r2.txt` |
| F3-SPOOF-EXEMPTION | The replacement-child exemption is claimable by any writer that can read the parent set's name+UID, not just the set controller | 2 | Confirmed — canary still green on `22acc570` (docstring unchanged, no caller-identity check) | `results/spoof-r2.txt` |
| MANIFESTS-IDEMPOTENT | `make manifests` regenerates `config/webhook` / `deploy/kubectl` byte-identically (PR claim) | n/a | Confirmed | `results/manifests-idempotent-r2.txt` |
| PR-SUITES | The PR's own unit + envtest suites pass on the head | 1+2 | Confirmed | `results/unit-head-r2.txt`, `results/envtest-head-r2.txt` |
| BASE-CARRY | The rebase onto a2e797c6 (#514) does not break the e2e tree | compile | Confirmed — `go vet ./test/...` clean; the PR's full CI matrix (incl. all E2E jobs) is green on `22acc570` | round-2 run |

### Round-1 results (head `993dcf10`) — superseded where marked

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| GATE-V1A1-WIRING | The six new v1alpha1 deprecation webhooks work end-to-end as shipped: new deprecated uses rejected (explicit and schema-defaulted), RoleInstanceSet creates admitted, warning returned, grandfathered v1alpha1 RBG updates admitted, new deprecated role injection rejected | 2 | Confirmed (7/7 specs green) | `results/envtest-head.txt` |
| F3-SPOOF-EXEMPTION | The replacement-child exemption is claimable by any writer that can read the parent set's name+UID, not just the set controller | 2 | Confirmed (canary green = current permissive behavior holds) | spec "grants the exemption to an ordinary writer…" |
| MANIFESTS-IDEMPOTENT | `make manifests` regenerates `config/webhook` / `deploy/kubectl` byte-identically (PR claim) | n/a | Confirmed | `git diff --quiet` clean after `make manifests` (controller-gen v0.17.2) |
| PR-SUITES | The PR's own unit + envtest suites pass on the head | 1+2 | Confirmed | `results/unit-head.txt`, `results/envtest-head.txt` |

## Per-finding detail

### GATE-V1A1-WIRING (contract, integration)

Mechanism: envtest + the production `config/webhook/manifests.yaml` installed against a
manager running the production setup functions. The suite's BeforeSuite has a dry-run
readiness barrier (a deprecated v1alpha1 create must be rejected before any spec runs),
which is what makes "gate inactive" a BeforeSuite failure rather than a silent pass.

Observed (all green on `993dcf10`):

- new v1alpha1 create with explicit `apps/v1 StatefulSet` → 403 "new uses are not accepted";
- new v1alpha1 create with `workload` key absent (raw JSON, kubectl-like) → same rejection,
  proving the v1alpha1 schema default makes the role deprecated by admission time;
- new v1alpha1 create naming `workloads.x-k8s.io/v1alpha2/RoleInstanceSet` → admitted, and
  the webhook's own warning ("is deprecated and will be removed in 0.10.0") is captured —
  distinct from the CRD-level deprecationWarning the API server also emits;
- grandfathered v1alpha1 RBG (seeded pre-gate) update (replicas bump) → admitted + warning;
- appending a new deprecated role to the grandfathered RBG → rejected;
- new v1alpha1 RoleBasedGroupSet with a deprecated template role → rejected;
- a write to `rolebasedgroupscalingadapters` (one of the four warning-only endpoints)
  round-trips and returns the webhook warning — every generated path resolves to a
  registered handler (failurePolicy=Fail would hard-fail otherwise).

Bite check (Step 4 of the verifier skill): with `validateV1alpha1DeprecatedRoles`
temporarily neutered, the BeforeSuite readiness barrier fails (gate detected inactive);
with only `warnV1alpha1` neutered, exactly the three warning specs go red
(`results/bite-warning-removed.txt`); with the v1alpha2 replacement-child exemption
removed, both this suite's canary and the PR's deprecation suite go red
(`results/bite-exemption-removed.txt`). Production code was restored afterwards
(`git diff --quiet -- api/` clean).

Envtest caveat, documented in the suite: CRDs from `config/crd/bases` use conversion
strategy None, so v1alpha1-only storage round-trips are lossy (a v1alpha1-seeded
RoleBasedGroupSet loses `spec.template.roles` on read-back). Set-template grandfathering
is therefore covered through the v1alpha2 storage shape (the PR's own suite), not through
a v1alpha1 set update. In production the conversion webhook round-trips both directions.

### F3-SPOOF-EXEMPTION (canary, integration)

Mechanism: `legacySetTemplateRoles` grants the replacement-child exemption from the
child's controller ownerReference plus a live parent read (name + UID + template role
name/workload-type match). The webhook cannot tell who issued the create.

Observed: an ordinary client that reads a live grandfathered set's name and UID — both
readable with standard RBAC — creates a deprecated-type child that is **admitted**
(canary green). The PR's stated security property "a forged owner reference … grants
nothing" holds only for fabricated parents; claiming a *real* parent grants the
exemption to any writer. Filed as a non-blocking question/risk (the gate is a migration
aid, not a security boundary; the created child is still subject to RBAC and will be
adopted by the set controller).

## Live run notes

Not run this round (no KUBECONFIG; round policy is unit + integration). The manifest's
`liveNote` records the observable signal for a future live pass.

## Proposed fixes (NOT applied to production here)

- **F2 (warning-only webhooks, failurePolicy=Fail)**: set `failurePolicy: Ignore` for the
  four v1alpha1 endpoints that only emit warnings (`instances`, `instancesets`,
  `rolebasedgroupscalingadapters`, `clusterengineruntimeprofiles`) — they can never
  reject, so Fail only turns webhook outages into hard rejections of v1alpha1 writes,
  the exact tradeoff the PR itself cites for skipping DELETE.
- **F3 (spoofable exemption)**: if the gate is meant to be an enforcement boundary,
  require something the controller provably sets and a plain user cannot copy (e.g. a
  controller-owned immutable label checked in the same live parent read), or accept and
  document the exemption as best-effort.

## Continuing after the fix (possibly on another machine)

The harness lives on branch `verify/pr-524-compat-gate-claude` (production code
untouched), so it grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/pr-524-compat-gate-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/pr-524-compat-gate test/envtest/testcase/deprecationv1a1
   ```
2. Run it:
   ```bash
   export KUBEBUILDER_ASSETS="$(setup-envtest use 1.36.2 -p path)"
   go test -count=1 ./test/envtest/testcase/deprecation/... ./test/envtest/testcase/deprecationv1a1/...
   ```
3. Or use the one-liner (auto-resolves the current PR head from the manifest):
   ```bash
   bash docs/verification/pr-524-compat-gate/scripts/re-verify.sh
   ```
   Exit 0 iff all findings are Fixed (canary counted fixed only when it flips red).

Polarity table for post-fix interpretation:

| Test | Type | Buggy code | Fixed code |
|------|------|-----------|------------|
| all GATE-V1A1-WIRING specs | contract | red | green |
| "grants the exemption to an ordinary writer…" | canary | green | **red — invert it** |

Copy-paste kickoff prompt for a fresh agent:

> You are re-verifying PR sgl-project/rbg#524. Checkout the fixed branch, graft
> `docs/verification/pr-524-compat-gate` and `test/envtest/testcase/deprecationv1a1`
> from branch `verify/pr-524-compat-gate-claude` of
> https://github.com/cheyang/rbg.git, run
> `bash docs/verification/pr-524-compat-gate/scripts/re-verify.sh` and report each
> manifest finding as Fixed / Still-broken / Partial / Harness-update, honoring
> polarity (the F3 canary is fixed only when it flips to red).
