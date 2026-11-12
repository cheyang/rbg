# Verification harness — PR #568 (rbg-conventions skill, AGENTS.md, verification kit)

Reviewer: Claude (Reviewer A). Branch `verify/rbg-conventions-skill-claude`, based on the PR head
`9ff493817cc3b60387565b9f3d8a2799b4eeb1fe` (base `7e377a06fe3845f38bf9fc1ce363198d632107e7`).
The PR adds only markdown and shell scripts; production Go code is untouched by the PR and by this
harness (the Go checker lives in its own nested module so the repo `go.mod` is unchanged).

## Premise (P0)

Claim: RBG reviews repeatedly find the same problem classes (revision-hash churn on upgrade,
CRD defaults applied on read, v1alpha1 conversion drift, mixed writer styles on SSA fields,
chart/manifest drift), the knowledge is spread across code comments, and the upgrade e2e suite
that proves it only runs on `/release-test`, so coding and review agents never apply it.

Verdict: **Confirmed** (documented-process premise; supporting facts verified):

- `/release-test` exists and is restricted to OWNER/MEMBER/COLLABORATOR
  (`.github/workflows/release-test.yml`); `make test-e2e-upgrade` runs only through it
  (Makefile + `references/build-test-release.md`).
- The problem classes cited in the docs exist in the repo: `restartPolicyConfig` upgrade pattern
  (`api/workloads/v1alpha2/helper.go`, `pkg/reconciler/roleinstanceset_reconciler.go`), SSA writer
  discipline (`PatchObjectApplyConfiguration` in `pkg/utils/utils.go`), cache `group-name` label
  filter (`cmd/rbgs/main.go` `cacheOptions()`), v1alpha1 conversion
  (`api/workloads/v1alpha1/rolebasedgroup_conversion.go`).
- ~25 doc citations (functions, files, flags, make targets, env vars, label keys, conditions)
  spot-checked against the PR head — all accurate, including the subtle ones (webhook
  `matchPolicy: Exact` only on the v1alpha1 deprecation webhooks; the nonexistent
  `make update-crd` mentioned by CI's error message).

Docs-only PR, so the premise carve-out applies; no runtime symptom to reproduce on the base branch.

## Findings and observed-vs-expected

This PR's review found **no blockers and no majors**. Everything verified below ran on this
machine on 2026-11-13.

| ID | claim | layer / test | result |
|---|---|---|---|
| V1 | `kc` write retry never replays a request that may have reached the API server (partial apply, sent-but-lost, webhook errors all break the retry loop); read retries do retry transient transport errors and give up after 5 | L1: `selftest.sh` (PR's own, 22 checks) + `edge_tests.sh` | PASS — see `results/selftest.txt`, `results/edge-tests.txt` |
| V2 | namespace ownership guard fails closed: no record → refuse, foreign token → refuse, tampered token → refuse; `rbg-fixtures.sh apply/delete` refuse foreign namespaces and send no writes/deletes | L1: `edge_tests.sh` (fake kubectl) | PASS — 31/31 |
| V3 | `delete_with_uid` sends the recorded UID as a delete precondition and deletes only that object; cleanup of the state file happens only after a successful delete | L1: `selftest.sh` + `edge_tests.sh` | PASS |
| V4 | the jsonpath templates the scripts use (slash-containing label keys in dot notation, space-separated multi-expression, fail-closed behavior when the label is missing) parse and evaluate with client-go's jsonpath, the library kubectl uses | L2: `harness` `jsonpath` subcommand | PASS — 7/7, `results/jsonpath-check.txt` |
| V5 | the six fixtures are realistic: they pass the v1alpha2 CRD's JSON-schema validation, its CEL `x-kubernetes-validations`, and contain no fields a real apply would prune | L2: `harness` `fixtures` subcommand against `config/crd/bases/workloads.x-k8s.io_rolebasedgroups.yaml` | PASS — 18/18, `results/fixture-validation.txt` |
| V6 | scripts are shellcheck-clean (info-level notes only: the trap-invoked `cleanup`, benign `A && B \|\| C`) and `bash -n` clean | static | PASS — `results/static-checks.txt` (ShellCheck 0.9.0) |
| V7 | the three Copilot review threads (name-only DELETE, replayed writes, init-container blind spot) are fixed in 9ff49381 | L1: selftest + edge tests cover all three | PASS, independently reproduced |
| V8 | live cluster cases VC-1..VC-5 | **not run this round** (live layer skipped) | unverified; the author's recorded results are in `references/verification-cases.md` |

Harness-bites check (Step 4 of the verifier method): injecting `replicas: "notanumber"` into
`fx-standalone` makes the fixture validation FAIL with a type error, and the file was restored
afterwards (`git status` clean). Injecting an unknown field under `spec.roles` is *not* detected
— correct, because the CRD declares `x-kubernetes-preserve-unknown-fields: true` on
`spec/properties/roles`, so the API server keeps such fields; the harness matches server
behavior, not a stricter rule.

## How to run

From any directory inside this checkout:

```sh
bash .agents/skills/rbg-conventions/scripts/selftest.sh          # 22 checks, needs bash+jq
bash docs/verification/rbg-conventions/scripts/edge_tests.sh     # 31 checks, needs bash+jq
cd docs/verification/rbg-conventions/harness                     # nested Go module
go run . jsonpath   # 7 checks
go run . fixtures   # 18 checks
```

Or everything at once: `bash docs/verification/rbg-conventions/scripts/re-verify.sh`.

The Go harness is a nested module (`harness/go.mod`) so it never touches the repo's `go.mod`;
its `go mod tidy` needs network access once (or a warm module cache).

## Continuing after the fix / next round

- Re-run: `bash docs/verification/rbg-conventions/scripts/re-verify.sh` (no sha — it fetches the
  current PR head via the manifest's `pr` URL and re-runs L1+L2).
- All tests are **contract tests** (they assert the intended correct behavior); there are no
  bug-canaries, so a green run means fixed and a red run means broken. If the author renames a
  function a fake dispatches on, the affected test fails — treat that as `Harness-update`, adjust,
  re-run.
- The live layer (V8, VC-1..VC-5) still needs a cluster; follow
  `references/verification-cases.md` and record the run there.

Kickoff prompt for a fresh agent:

> You are continuing the verification of PR #568 (rbg-conventions skill) on branch
> `verify/rbg-conventions-skill-claude`. Read `docs/verification/rbg-conventions/README.md` and
> `verify-manifest.json`, run `bash docs/verification/rbg-conventions/scripts/re-verify.sh`,
> report per-finding Fixed / Still-broken / Partial / Harness-update, then review
> `last-reviewed..PR-head` incrementally and update this README and the manifest.
