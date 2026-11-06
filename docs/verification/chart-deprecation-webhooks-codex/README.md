# Verification — PR #552: ship the 0.9.0 deprecation webhooks and gate documentation (chart slice)

Reviewer: Codex (Reviewer B). Branch: `verify/chart-deprecation-webhooks-codex`.
Production code untouched; this branch adds only tests and this directory.

## Round 2 — head `1a4f7ab00cbb0be6021cca2690bacfef9a0e0278` (delta `a1ff903e..1a4f7ab0`)

The round-2 delta refactors the four per-kind warning-only v1alpha1 endpoints
into one merged endpoint (`/validate-workloads-v1alpha1-deprecation`,
`vv1alpha1-warning-deprecation.kb.io`, single rule covering instancesets,
instances, rolebasedgroupscalingadapters, clusterengineruntimeprofiles) served
by a static handler that never decodes the object; the two Fail enforcement
entries are unchanged. The v1alpha1 deprecation surface is now exactly **three**
entries. The delta also rewrites the root README sections (F3 fix), retargets
the migration-guide references to the open roadmap issue #454 (F4 fix), and
adds the "choose by installation type" guidance + inventory command.

### Round-2 harness changes (Harness-update from round 1, additively replaced)

- `setup_test.go` (envtest): the four removed per-kind registrations replaced by
  `workloadsv1alpha1.SetupV1alpha1WarningWebhook` — mirrors cmd/rbgs at head.
- `test/chart/webhook_manifests_test.go`: asserts the exact three-entry surface,
  per-entry failurePolicy polarity, the merged entry's single four-resource
  rule, and the absence of the retired per-kind names. Bites on base (base
  shipped config carries the six pre-refactor entries; base chart ships none)
  and on the round-1 shape.
- New `api/workloads/v1alpha1/deprecation_webhook_merged_verify_test.go`:
  the merged handler allows every request and returns the shared
  `v1alpha1DeprecationWarning` (naming the 0.10.0 removal) without decoding.
- New `docs_inventory_test.go` (envtest): runs the chart README's documented
  inventory command (`kubectl get rbg,rbgs -A -o json | jq ...`) verbatim
  against a live API server with a seeded deprecated RoleBasedGroup, a seeded
  deprecated RoleBasedGroupSet, and a modern RoleInstanceSet control; asserts
  discovery prefers v1alpha2 (the version assumption behind the documented jq
  paths) and that exactly the deprecated objects are listed.
- New `scripts/check-default-image-paths.sh`: F1 tracker — every v1alpha1
  deprecation path the chart renders vs. the paths registered at the default
  image pin's commit.
- `scripts/re-verify.sh`: results now persist after the worktree restore
  (the restore used to clobber them); new `script` layer kind.
- `scripts/render-notes/`: renders NOTES.txt in the install/upgrade x
  toggle-on/off matrix through Helm's own engine.

### Round-2 observed vs expected

| ID | Claim | Result at head |
| --- | --- | --- |
| A-PARITY | chart renders exactly the three shipped v1alpha1 deprecation entries, field-identical | PASS (`results/unit-chart-and-handler.out`); bites on base and on the round-1 six-entry shape |
| A-HANDLER | merged handler allows + warns with the shared constant, no decode | PASS (2/2) |
| A-GATE | chart-rendered three-entry config drives the full 0.9.0 gate in envtest (new-use rejection, defaulted-workload rejection, grandfathering, switch rejection, warnings for all four kinds via the merged path, Exact match, ungated deletes) | PASS (10/10 contract specs, 2 premise specs skip by design) — `results/reverify/integration.out` |
| A-INVENTORY | documented inventory command lists the seeded deprecated RBG and RBGSet and only them; discovery preferredVersion == v1alpha2 | PASS — `results/reverify-round2-inventory-excerpt.out` |
| A-IMAGE (F1 tracker) | the default image pin serves every rendered v1alpha1 deprecation path | **RED** — `results/tracker-head.out`: pin `v0.9.0-d88b3186` serves neither Fail path (fail-closed on all v1alpha1 RBG/RBGSet writes) nor the merged Ignore path (warnings silently missing). Base: GREEN vacuous (`results/tracker-base.out`); previous head: RED (`results/tracker-previous.out`) |
| P0 (premise, base) | base chart ships no v1alpha1 deprecation plane | still CONFIRMED at round 2 — `results/reverify-round2-premise.out` |
| — | `make manifests` idempotent at head; `deploy/kubectl/manifests.yaml` byte-identical to fresh kustomize build | PASS — `results/manifests-idempotency.out` (zero diff) |
| — | helm lint / yamllint / chart toggle suite | PASS — `results/helm-lint.out`, `results/yamllint.out`, `results/unit-chart-and-handler.out` |
| — | NOTES.txt renders in install/upgrade x enabled/disabled | PASS, no render errors — `results/notes-render.out` |

### Round-2 review verdicts on round-1 findings

- F1 (blocker, image pin): **still open** — tracker RED at head; verification
  {base: pass, previous: fail, head: fail}.
- F2 (major, no committed parity guard): **still open** — `test/chart/` at head
  still contains only the toggle test; the drop-in guard lives on this branch.
- F3 (README contradictions): **fixed** — both languages now say "choose by
  installation type"; the contradicting paragraphs/blockquotes are gone.
- F4 (dead migration-guide pointer): **fixed** — references now resolve to the
  open roadmap issue sgl-project/rbg#454, which tracks the migration guide.
- F5 (chart version unbumped): **still open** — Chart.yaml unchanged.
- F6 (rollout fail-closed window doc): still deferred (PR body slice-3
  decision); the new Upgrading section does not cover the write-outage window.
- Q1 (release sequencing): **still open** — the pin has not moved.
- N1 (pre-existing `cmd.CombinedOutput()` in test/chart): still open.

## Round 1 — head `a1ff903e3aab5fa7749b9a7ea67457db9972e8b6` (base `73e05db4`)

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
