# Verification: PR #568 — docs(agents): add rbg-conventions skill, AGENTS.md and verification kit

- PR: https://github.com/sgl-project/rbg/pull/568
- Reviewed head: 9ff493817cc3b60387565b9f3d8a2799b4eeb1fe (base 7e377a06fe3845f38bf9fc1ce363198d632107e7)
- Reviewer: Reviewer B (Codex). Production code untouched — this branch adds only this directory and
  `test/envtest/testcase/deprecationv1a1/vc5_roundtrip_verify_test.go`.

## P0 — premise

Docs/tooling PR, so the formal premise step is the carve-out case. The load-bearing empirical claims
the PR makes about `main` were verified instead:

| Claim | Verdict | Evidence |
|---|---|---|
| VC-1: a new CRD default applies on read, parent present; nothing materialized under an absent optional parent; no rewrite | Confirmed on kube-apiserver 1.31.0 (envtest binaries): A1–A4 PASS | results/integration-apiserver.txt |
| VC-2: a controller restart changes nothing | Confirmed on this head: `PASS: nothing changed (302 records)` across a stop/start of a controller built from the head, fixtures applied, 11 pods | results/integration-apiserver.txt |
| VC-4: role label removal propagates; single field manager `rbg/Apply` | Confirmed: both PASS lines from `ssa-key-removal.sh` against the real controller | results/integration-apiserver.txt |
| VC-5: a no-op v1alpha1 round trip rewrites the stored v1alpha2 object (annotation added, `restartPolicy` string replaced by a defaulted `restartPolicyConfig`, configured `baseDelaySeconds: 10` reset to 30) and customComponentsPattern roles are rejected | Confirmed by new envtest specs running the production conversion + admission webhooks (3/3 pass as canaries of the current behavior) | results/vc5-envtest.txt, test/envtest/testcase/deprecationv1a1/vc5_roundtrip_verify_test.go |
| Every code citation in the docs resolves on this head | Confirmed: 39/39 | results/citation-check.txt (scripts/citation-check.sh) |
| markdownlint / AutoCorrect clean; selftest 22/22 | Confirmed | results/static-lint.txt, results/selftest.txt |

## Findings and their harness

| ID | Severity | Claim | Polarity | Where | Status on 9ff49381 |
|---|---|---|---|---|---|
| F1 | minor | `rbg-snapshot.sh --settle` prints a valid-looking snapshot on stdout even when it exits 3 (not settled), so `rbg-snapshot.sh --settle 20 > before.txt` leaves a file `rbg-compare.sh` accepts as authoritative | contract (red = reproduced) + canary | scripts/script-units.sh: `contract-settle-timeout-quiet` (FAIL), `canary-settle-timeout-prints-snapshot` (PASS) | Reproduced |
| F3 | nit | `rbg-compare.sh`'s "What it means" explainer has no hint for removed or changed ControllerRevision lines (only added ones) | canary | scripts/script-units.sh: `canary-removed-cr-unexplained` (PASS) | Documented |
| F4 | minor | `rbg-fixtures.sh apply`: if the namespace-create response is lost, the namespace may exist with an unrecorded owner token; the error is a bare `cannot create namespace` with no recovery hint (crd-default-on-read.sh does have such a hint) | contract + canary | scripts/script-units.sh: `contract-apply-lost-create-guides-cleanup` (FAIL), `canary-apply-lost-create-bare-message` (PASS) | Reproduced |
| F5 | minor | `crd-default-on-read.sh` (VC-1) flakes on a busy/slow apiserver: `kubectl wait --for=condition=Established` aborts with `.status.conditions accessor error` and `set -e` exits 1. Right after installing the RBG CRDs it failed 2 of 3 runs; observed again in the full integration run | evidence + live observation (NOTE line) | results/f5-wait-flake.txt, results/integration-apiserver.txt | Reproduced |

The guard checks that PASS on the reviewed head (contract tests, must keep passing):
`contract-kc-retry-budget`, `contract-kc-wait-is-read`, `contract-delete-unknown-no-blind-retry`,
`contract-owner-no-record`, `contract-owner-tampered-refused`, `contract-owner-match-accepted`,
`contract-state-file-per-cluster`, `contract-snapshot-rendering`, `contract-snapshot-deterministic`,
`contract-compare-hints`, `contract-compare-refuses-empty`, `contract-compare-refuses-no-rbg`,
`contract-roundtrip-requires-ownership`, `contract-roundtrip-refuses-none`,
`contract-roundtrip-conflict-not-retried`, `contract-fixture-image-substitution`,
integration: `contract-vc1-default-on-read`, `contract-vc1-cleanup`, `contract-fixtures-apply`,
`contract-fixtures-marker-label`, `contract-fixtures-reapply`, `contract-foreign-statedir-refused`,
`contract-foreign-statedir-ns-intact`, `contract-foreign-namespace-refused`,
`contract-compare-real-identical`, `contract-roundtrip-refuses-none-live`,
`contract-vc2-restart-noop`, `contract-vc4-label-removal`, `contract-fixtures-delete-accepted`,
`contract-delete-keeps-state-while-terminating`.

## Harness-bites check

The candidate fixes were applied temporarily and reverted:
- F1: dropping the `printf '%s\n' "$cur"` on the timeout path flips `contract-settle-timeout-quiet`
  green and the canary red.
- F4: extending the die message flips `contract-apply-lost-create-guides-cleanup` green and the
  canary red.
- F5: wrapping `kc wait --for=condition=Established` in a retry loop made VC-1 pass on the first
  run right after CRD churn (previously 2/3 failures).
All three were reverted; `git status` shows only this harness.

## How to run

Unit (no cluster, needs bash + jq):

```sh
bash docs/verification/rbg-conventions-skill/scripts/script-units.sh
bash .agents/skills/rbg-conventions/scripts/selftest.sh     # the PR's own selftest
bash docs/verification/rbg-conventions-skill/scripts/citation-check.sh
```

Integration (needs `kubectl` and envtest binaries; set KUBEBUILDER_ASSETS or have
~/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64):

```sh
go build -o /tmp/rbgs ./cmd/rbgs
bash docs/verification/rbg-conventions-skill/scripts/integration-apiserver.sh --with-controller /tmp/rbgs
KUBEBUILDER_ASSETS=... go test ./test/envtest/testcase/deprecationv1a1/ \
  -run TestV1alpha1DeprecationContract -ginkgo.focus VC-5 -timeout 8m
```

The integration script starts its own etcd + kube-apiserver on 127.0.0.1 (ports 2399/18443) with a
throwaway data dir and token auth, installs the CRDs from `config/crd/bases`, and cleans up on exit.
It never touches a real cluster. `--with-controller` additionally runs a controller binary built
from the checkout for the VC-2/VC-4 cases.

## Continuing after the fix

From a checkout of this branch: `bash docs/verification/rbg-conventions-skill/scripts/re-verify.sh`
(resolves the current PR head from `manifest.pr`, grafts the harness, runs unit + integration).
Polarity: when F1/F4 are fixed, their contract tests go green and the canaries flip red — invert the
canaries then. F5 needs a load-timed condition; `results/f5-wait-flake.txt` is the deterministic
reproduction recipe (run VC-1 immediately after applying `config/crd/bases`).

Kickoff prompt for a fresh machine: "Check out branch verify/rbg-conventions-skill-codex of
https://github.com/cheyang/rbg.git, read docs/verification/rbg-conventions-skill/README.md, run
scripts/re-verify.sh with the envtest assets under ~/.local/share/kubebuilder-envtest."
