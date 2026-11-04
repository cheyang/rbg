# Verification — PR #552: ship the 0.9.0 deprecation webhooks and gate documentation (chart slice)

Reviewer: Codex (Reviewer B). Branch: `verify/chart-deprecation-webhooks-codex`.
Head reviewed: `a1ff903e3aab5fa7749b9a7ea67457db9972e8b6` (base `73e05db4` — post-#524 main).
Production code untouched; this branch adds only tests and this directory.

## P0 — Premise (runs against the BASE branch)

Claim: the Helm chart shipped the pre-gate webhook configuration, so Helm installs
lacked the 0.9.0 v1alpha1 deprecation admission behavior that the
kubectl-rendered `config/webhook/manifests.yaml` (merged in #524) provides.

**Verdict: CONFIRMED (with a nuance).** Against the base chart render installed
into envtest with production webhook handlers:

| Probe | Base chart (observed) | Head chart (observed) |
| --- | --- | --- |
| v1alpha1 InstanceSet create | admitted, **no deprecation warning** | admitted, `Warning: 299` "v1alpha1 is deprecated … removed in 0.10.0" |
| v1alpha1 RBG create, new StatefulSet use | rejected ("new uses are not accepted") — via the **Equivalent**-matching v1alpha2 validator, still **no warning** | rejected by the Exact v1alpha1 entry, with warning |
| v1alpha1 RBG/RBGSet/Instance/ScalingAdapter/ClusterEngineRuntimeProfile writes | never warned | all warned |

So the "no gate at all" framing is imprecise for RBG/RBGSet new-use enforcement
(the chart's v1alpha2 webhooks match v1alpha1 requests under the default
`matchPolicy: Equivalent`); what was genuinely missing is the **deprecation
warning plane for all six v1alpha1 kinds** plus the Exact v1alpha1 enforcement
entries. The PR closes exactly that. Evidence: `results/premise-base-chart.out`.

## Claims and how they were verified

| ID | Claim | Layer | Polarity | Result |
| --- | --- | --- | --- | --- |
| A-PARITY | Rendered chart's six `*-v1alpha1-deprecation.kb.io` entries match `config/webhook/manifests.yaml` (name, path, rules, failurePolicy, matchPolicy, sideEffects, admissionReviewVersions) | unit (`test/chart/webhook_manifests_test.go`) | contract | PASS at head; FAILS on the base worktree (all six missing) → harness bites |
| A-GATE | The chart-rendered webhook configuration drives the real admission chain: new-use rejection (explicit + schema-defaulted workload), type-exact grandfathering of a seeded pre-gate object, per-kind warnings for the four Ignore entries, Exact match policy (v1alpha2 writes unwarned), ungated deletes | integration (envtest, `test/envtest/testcase/deprecationv1a1helm/`) | contract | PASS (8/8 specs) at head |
| — | `helm lint`, `yamllint -c .yamllint.yml deploy/`, existing `test/chart` toggle suite | unit | contract | PASS |
| — | NOTES.txt renders in enabled/disabled/unset modes (helm engine render) | unit (manual) | — | PASS |

## How to run

Prereqs: `helm`, `go`, envtest assets (`bin/k8s/1.31.0-linux-amd64` or
`bin/setup-envtest use 1.31.0 --bin-dir ./bin -p path`). Set `KUBECONFIG` to a
0600 file (or leave it absent) — `helm` kubeconfig permission warnings on stderr
otherwise leak into any tool that merges stderr into the rendered stream.

```bash
# unit layer (chart parity + existing toggle tests)
go test ./test/chart/ -count=1 -v

# integration layer (envtest, chart-rendered webhook configs, head chart)
KUBEBUILDER_ASSETS=$PWD/bin/k8s/1.31.0-linux-amd64 \
  go test ./test/envtest/testcase/deprecationv1a1helm/ -count=1 -v

# premise evidence against the BASE chart (P0)
bash docs/verification/chart-deprecation-webhooks-codex/scripts/run-premise-base.sh

# byte-level comparison script (same check as the Go test, standalone)
python3 docs/verification/chart-deprecation-webhooks-codex/scripts/compare_webhooks.py

# full re-verification after a fix round (auto-discovers the PR head)
bash docs/verification/chart-deprecation-webhooks-codex/scripts/re-verify.sh
```

## Observed vs expected

- A-PARITY: expected six entries equal to the shipped config — observed equal
  (both the Go test and `results/compare_webhooks.out`).
- A-GATE: expected the full 0.9.0 compat-mode admission behavior through the
  chart render — observed, all specs green (`results/integration-head-contract.out`).
- P0: expected the base chart to lack the v1alpha1 deprecation plane — observed
  (`results/premise-base-chart.out`).

## Findings fed back to the review (see findings-codex.md)

- F1 (minor): root README.md / README-zh_CN.md — updated flag table contradicts
  the unchanged paragraph + blockquote below it ("only for a first-time
  installation", "no supported way to turn the toggle off on an existing
  installation" vs the table's "set it only after every role has migrated").
- F2 (minor): chart README references "the project migration guide" twice; the
  guide is slice 3 and does not exist in the repo at this head.
- F3 (missing test): no committed test pins chart↔shipped-config webhook parity;
  `test/chart/webhook_manifests_test.go` on this branch is a drop-in guard.
- F4 (minor risk, deferred by author to slice 3 "Q1"): during `helm upgrade` the
  updated ValidatingWebhookConfiguration goes live while old controller pods
  still serve the webhook Service; the fail-closed v1alpha1 RBG/RBGSet entries
  reject all such writes (including grandfathered updates) until the new binary
  serves the six paths. caBundle is not a blocker (the running controller
  patches every entry of the named configuration). Needs the rollout-ordering
  note in the upgrade docs.

## Continuing after a fix round

All tests above are contract polarity: green = correct. If a later commit
renames the suite or the chart layout changes, expect `Harness-update` from
`re-verify.sh`; adjust paths and re-run. Kickoff prompt for a fresh machine:

> Check out `verify/chart-deprecation-webhooks-codex` (fork `cheyang/rbg`),
> read `docs/verification/chart-deprecation-webhooks-codex/README.md`, then run
> `bash docs/verification/chart-deprecation-webhooks-codex/scripts/re-verify.sh`.
