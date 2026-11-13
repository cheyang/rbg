# KEP-531 Engine Runtime Profile Injection — verification harness (Reviewer A, Claude)

- PR under review: https://github.com/sgl-project/rbg/pull/571
- Head reviewed: `1810f5c6ed28a48addcc3e348699c3f4ae170a6c` (base `ea5e82ae59169e0e954fb1c7068f9480fb4b7375`)
- Harness branch: `verify/kep531-engine-runtime-claude` (reviewer fork `cheyang/rbg`)
- Production code is untouched: the diff of this branch against the PR head is
  only the two harness test files and this `docs/verification/` tree.

## Premise (P0) — verdict: CONFIRMED

The PR implements merged KEP-531. The claimed problem is the legacy
engine-runtime grammar's inventory (issue #531):

1. "Referencing a profile injects wherever the pattern happens to; a role
   cannot say 'this runtime belongs to the worker component.'"
2. "No way to touch role-declared containers. A profile author cannot add an
   env var or a volumeMount to the role's own engine container."
3. "Role customization of profile containers is env/args-only."

Confirmed on **base** (`ea5e82ae`) by code inspection of
`pkg/discovery/sidecar_builder.go::injectRuntime` — `profileName` is the only
targeting knob, the legacy override loop merges `env`/`args` only and silently
ignores every other `corev1.Container` field — plus
`results/base-differential-sidecar.out`, which pins base's unconditional
fragment injection (the differential premise for F2). Component match: the PR
touches exactly the render pipeline, API surfaces and webhooks the KEP names.

## Observed vs expected

| finding | layer | polarity | observed on head | expected | evidence |
|---|---|---|---|---|---|
| **F1** profile-side env ownership hole across stacked profiles | unit (deterministic) | contract (RED = reproduction) | Two profile-side passes on a role-declared env name: profile A defaults at `/env/0`, then profile B **appends** at `/env/-` — no render error. Rendered env: `[A, role, B]` → B (profile content) wins the name under kubelet last-occurrence. | KEP-531 Patch Grammar: "a `-` append or an insert at any other index is a render error" for a role-owned name, whatever another profile did earlier in the render ("profile content never wins a same-name contest with role content"). | `results/unit-harness-red.out` |
| **F2** LWS + non-leaderWorkerPattern roles silently drop pod-level fragments | unit (deterministic) + base differential | contract (RED = reproduction) | A standalonePattern role with the LWS workload-type annotation and a bare `profileName`-only reference: the resolver keys `FragmentApplications` by the pattern vocabulary (`role.Name`), the LWS construct path reads position `"leader"` → nothing injected, no event, no error (`containers=[nginx] volumes=[]`). Base injects the sidecar+volume into the leader template. | KEP-531 compatibility guarantee #1: "references that set only profileName render identically to main", and the default-delivery table: a deprecated-path render "keeps main's behavior verbatim for the references it can carry". | `results/unit-harness-red.out`, `results/base-differential-sidecar.out` |

Controls in both harness tests (single-profile rejection, leaderWorkerPattern
LWS leader injection, fresh-name cross-profile stacking) pass on the head, so
the red results are the findings, not fixture noise.

## Harness-bites check (proposed fixes verified, then reverted)

- **F1 fix** — record into `envCreationTracker` only names the container did
  not already carry (a default onto a role-owned name is not a creation):
  applied temporarily to `pkg/discovery/container_patch.go`; the F1 contract
  test goes green and the PR's own env-grammar suite stays green
  (`results/harness-bites-f1-fix-green.out`). Reverted.
- **F2 fix** — `positionVocabulary` gains an LWS case collapsing to the
  `{leader, worker}` vocabulary with `leader` as the unscoped default (where
  main injected), instead of the pattern vocabulary the LWS construct path
  never reads: applied temporarily to `pkg/discovery/runtime_resolver.go`;
  the F2 contract test goes green
  (`results/harness-bites-f2-fix-green.out`). Reverted.

Both are minimal, review-suggested fixes for the author to adapt, not part of
the harness.

## Full-suite re-run (this round, independent of the PR's CI)

- `go build ./...`, `go vet ./...`: clean.
- All unit packages: green.
- Full envtest suite against a real kube-apiserver 1.31
  (`KUBEBUILDER_ASSETS=… go test ./test/envtest/testcase/...`): **8/8 packages
  ok** (`results/envtest-full-suite-green.out`) — the PR's "green envtest"
  claim independently reproduced.

## How to re-run

From a checkout of **this branch**:

```bash
# unit layer only (deterministic, no cluster):
bash docs/verification/kep531-engine-runtime/scripts/re-verify.sh <fixed-ref>   # or no arg: current PR head
# exit 0 iff every finding is Fixed (contract tests green)

# integration layer (needs envtest assets):
export KUBEBUILDER_ASSETS="$(setup-envtest use 1.31.0 --bin-dir bin -p path)"
go test ./test/envtest/testcase/rbg/ -run TestRBGController -ginkgo.focus 'engine runtime'
```

The manifest (`verify-manifest.json`) maps each finding to its test and
polarity; `re-verify.sh` fetches the PR head from the manifest's `pr` URL,
grafts this harness onto it, and prints per finding Fixed / Still-broken /
Partial / Harness-update.

## Continuing after the fix

| test | on buggy head | after a correct fix |
|---|---|---|
| `TestReview_EnvOwnershipAfterDefault` | FAIL (main sub-case) | PASS (both controls must stay green) |
| `TestReview_LWSNonLWPPatternFragmentRouting` | FAIL (standalone sub-case) | PASS (leaderWorker control must stay green) |

A one-line note on the dedupe corner (not a filed finding): plan
`ShouldWarnUnaimed` is keyed by profileName only, so two references of one
profile on different components each hitting the render-side unaimed warning
share one warning event; harmless, noted for completeness.

## Kickoff prompt (fresh agent, another machine)

```
Checkout branch verify/kep531-engine-runtime-claude from
https://github.com/cheyang/rbg.git (fork of sgl-project/rbg). Read
docs/verification/kep531-engine-runtime/README.md and verify-manifest.json.
Run bash docs/verification/kep531-engine-runtime/scripts/re-verify.sh (no
argument — it resolves the current PR #571 head). Report the per-finding
verdict table; F1 and F2 are contract tests, so "STILL-BROKEN" means the
PR head still carries the defect and "FIXED" means the author landed a fix.
Do not touch production code; do not publish anything to GitHub.
```
