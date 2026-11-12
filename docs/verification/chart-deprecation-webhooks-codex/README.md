# Verification — PR #552 (codex) — round 3 (docs/chart-value slice)

Round 3 rescoped the PR completely: the v1alpha1 admission webhooks were removed at
the source by #556 (merged into base `ea5e82ae`) and the chart/image pins moved to
`0.9.0-alpha.3` / `v0.9.0-73e05db4` by #555. PR #552 now touches only documentation
(`README.md`, `README-zh_CN.md`, chart `README.md`, `NOTES.txt`) and `values.yaml`
comments. The round-1/2 harness (branch tip `e039c903`) is retired where the code it
exercised no longer exists (`Harness-update`, see `verify-manifest.json` →
`retiredHarness`); this tree replaces it additively.

## Verification target

The PR's claims describe the **post-#556 composition**, so every layer runs against
`merge(69fc06c1, ea5e82ae)` — a synthetic merge commit built by `scripts/re-verify.sh`
— not against the raw PR head tree (whose pre-#556 base still contains the removed
webhooks). The merge is clean.

## Premise (P0) — confirmed

The docs claim: after #556 the compatibility gate holds through the v1alpha2
validators via conversion; every v1alpha1 request returns the CRD deprecation
warning naming the 0.10.0 removal; the chart renders no v1alpha1 deprecation
endpoints. All three are verified by the layers below (envtest compat/strict
suites, warning-header assertions, `results/no-v1alpha1-render.out`).

## Observed vs expected

| claim | expected | observed | evidence |
|---|---|---|---|
| Chart renders no v1alpha1 webhook entries | 0 entries | 0 entries; full parity with `config/webhook/manifests.yaml` (6/6) | `results/unit-chart.out`, `results/no-v1alpha1-render.out` |
| Bites: guard detects the round-2 chart | red | red, names all 3 removed entries | `results/bites-oldhead-1a4f7ab0.out` |
| Bites: parity guard detects pre-#556 drift | red | red: shipped 9 vs chart 3 | `results/bites-rawhead-69fc06c1.out` |
| Schema-defaulted v1alpha1 create rejected in BOTH modes (genuinely absent `workload` key, kubectl-style) | rejected | rejected, "deprecated"/"not enabled" | `results/envtest-compat.out`, `results/envtest-strict.out` |
| Control: without the gate the same create is admitted AND stored as `apps/v1 StatefulSet` (proves the default fires and conversion stamps the annotation) | admitted + stamped | admitted + stamped | `results/envtest-nowebhook-control.out` |
| Explicit-RoleInstanceSet v1alpha1 admitted; CRD warning names 0.10.0 on create AND list; v1alpha2 unwarned | warned/unwarned | as expected | `results/envtest-compat.out` |
| New deprecated use rejected (v1alpha2 annotation); type-exact grandfathering; type-switch rejected; RBGSet legacy-template child exempt; forged-UID and no-owner children rejected | per docs | as expected | `results/envtest-compat.out` |
| Strict mode: unchanged re-apply of legacy object rejected; RoleInstanceSet admitted | per docs | as expected | `results/envtest-strict.out` |
| Inventory command verbatim lists exactly the deprecated seeds (RBG + RBGSet template), excludes modern control | both listed | as expected; fixtures cover v1alpha1 direct-workload, v1alpha1 `.spec.template.roles`, v1alpha2 annotation shapes, edge cases | `results/envtest-compat.out`, `results/unit-chart.out` |
| Default image pin serves every rendered webhook path | served | GREEN (pin `v0.9.0-73e05db4`, 6/6) | `results/tracker-merged.out` |
| NOTES.txt guidance matrix (toggle x install/upgrade) incl. F8 cutoff text | renders + correct branches | 11/11 string assertions | `results/notes-render.out` via `scripts/check-notes.sh` |
| helm lint / yamllint / existing toggle suite | clean | clean | `results/helm-lint.out`, `results/yamllint.out`, `results/unit-chart.out` |
| `make manifests` idempotent on merged tree | zero diff | zero diff | `results/manifests-idempotency.out` |

Note: the schema-defaulted rejection required an **unstructured** workload-free
object — the typed v1alpha1 client always serializes the non-pointer `Workload`
struct, so it can never exercise the genuinely-absent-key path (this is the gap
#556's own suite comments leave open: "that path is not covered in-repo"). The
first harness revision here falsely "failed" because the v1alpha1 wrapper
pre-fills `workload: RoleInstanceSet`; fixed by building the object unstructured.

## Carried ledger outcomes (round 3)

| finding | status | evidence |
|---|---|---|
| F1 (blocker, chart rendered unserved paths) | **fixed** | PR no longer ships the entries; tracker GREEN at merged state; pin moved to `v0.9.0-73e05db4` via #555. base=pass(vacuous), previous=fail, head=pass |
| F2 (major, no committed parity guard) | **fixed** (scope) | the PR no longer establishes any parity surface; generalized guard kept here as drop-in; residual pre-existing gap filed as non-blocking |
| F3, F4 | **fixed** (still) | head text re-read; #454 OPEN and linked twice |
| F5 (minor, chart version) | **open** | `Chart.yaml` still `0.9.0-alpha.3` while NOTES.txt/values change and `rbgs-0.9.0-alpha.3.tgz` is a published release asset |
| F6 (minor, rollout window, deferred) | **fixed** (moot) | no webhook entries shipped → no fail-closed window |
| F7 (nit, dead validator) | **fixed** | `deprecation_webhook.go` deleted by #556; absent on merged tree |
| F8 (minor, cutoff guidance) | **fixed** | README "empty inventory is necessary but not sufficient" + NOTES upgrade branch; asserted by `check-notes.sh` |
| Q1 | answered | pin bump landed via #555; residual: pinned CRD-upgrader image installs pre-#556 warning text (no 0.10.0 timeline) until the release-cut bump |
| N1 (pre-existing nit) | **open** | `test/chart/deprecated_workload_types_test.go:96` still `CombinedOutput` |

## How to re-run

```bash
bash docs/verification/chart-deprecation-webhooks-codex/scripts/re-verify.sh
```

Fetches the current PR head + `origin/main`, merges, grafts the harness, runs
unit (chart render/parity/jq fixtures), integration (envtest compat/strict +
nowebhook control), and both trackers. Exit 0 iff all pass. Advance
`.last-reviewed` afterwards. Live layer (L3) intentionally skipped in this
debate round; see `verify-manifest.json` → `liveNote`.

## Continuing after changes

All tests are contract polarity: green = good. If the docs' jq program or
NOTES.txt wording changes, the extractors fail loudly (by design) — update the
fixtures/assertions with the doc. If the PR is rebased onto main, the merge in
`re-verify.sh` degenerates to the head tree and everything still applies.
