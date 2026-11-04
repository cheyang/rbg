# Verification — PR #552: ship the 0.9.0 deprecation webhooks in the Helm chart

- PR: https://github.com/sgl-project/rbg/pull/552
- Reviewed head: `a1ff903e3aab5fa7749b9a7ea67457db9972e8b6` (base `73e05db4213950a5dd1f230279e9344a4ffe8435`, the #524 merge)
- Reviewer: Reviewer A (Claude). Harness branch: `verify/pr552-chart-deprecation-webhooks-claude`
- Layers run: L1 (unit, `helm template` + go test) and L2 (integration, envtest). No live layer was run.

## Premise (P0) — CONFIRMED

The PR claims to close the chart↔manifests intermediate state left by #524: the kubectl-rendered
`config/webhook/manifests.yaml` ships the six v1alpha1 deprecation webhooks, while the Helm chart
still rendered the pre-gate webhook configuration, so a Helm install got no admission gate.

Differential evidence on the **base** branch (`73e05db4`):

- v1alpha1-deprecation entries in the chart template: **0**
- v1alpha1-deprecation entries in `config/webhook/manifests.yaml`: **6**

See `results/premise-and-static-evidence.txt`. The gap is real, and the PR closes it: the chart at
head renders all six entries, byte-parity with the shipped manifests (verified below).

## Observed vs expected

| ID | Finding | Layer / test | Polarity | Observed on head | Verdict |
| --- | --- | --- | --- | --- | --- |
| P0 | Premise: chart lacks the gate webhooks on base | `results/premise-and-static-evidence.txt` | differential | base: 0 entries vs shipped 6 | **Confirmed** |
| F1 | Chart default image `v0.9.0-d88b3186` predates #524 and registers none of the six v1alpha1 webhook paths the chart now renders | L1 `TestChartDefaultImageServesRenderedWebhookPaths`; L2 `deprecationchartlegacy` suite | contract (L1, RED = reproduction); mechanism witness (L2) | L1 RED: image commit `d88b3186` registers 0/6 paths. L2: v1alpha1 RBG/RBGSet create rejected with `failed calling webhook "vrolebasedgroup-v1alpha1-deprecation.kb.io": ... the server could not find the requested resource`; InstanceSet create succeeds (Ignore absorbs it) | **Confirmed** (major) |
| F2 | Nothing in the repo guards chart↔shipped-manifest webhook parity (the PR's core claim) | L1 `TestChartWebhookConfigurationsMatchShippedManifests` + `TestChartV1alpha1DeprecationWebhookFields` | contract | PASS on head (parity holds today); bites when the template drifts (checked by corrupting one failurePolicy) | **Confirmed** (the guard is missing, not the parity) |
| — | Chart-rendered webhooks behave like the shipped manifests | L2 `deprecationchart` suite, 5 specs | contract | 5/5 PASS: warning on v1alpha1 write, new-use rejection, type-exact grandfathering, deprecated-type switch rejection, set-template rejection | **Confirmed** (PR's functional claim holds) |

## How to run

```bash
# L1 — unit (helm + go test). KUBECONFIG must point at a missing path or a 0600 file,
# otherwise helm's kubeconfig-permission warnings (stderr) pollute CombinedOutput
# in the chart tests and every render parse fails.
export KUBECONFIG=/tmp/rbgs-missing-kubeconfig
go test ./test/chart/ -count=1 -v

# L2 — integration (envtest). Assets: setup-envtest use 1.36.2 (any 1.3x works).
export KUBEBUILDER_ASSETS=$(ls -d ~/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64)
go test ./test/envtest/testcase/deprecationchart/ -count=1 -v -timeout 10m        # full handlers + chart config
go test ./test/envtest/testcase/deprecationchartlegacy/ -count=1 -v --ginkgo.v -timeout 10m  # pre-#524 handlers + chart config
```

Or run everything at once: `bash docs/verification/pr552-chart-deprecation-webhooks/scripts/re-verify.sh`

## Harness-bites record

- Parity tests: temporarily flipped `vinstance` entry's `failurePolicy: Ignore` → `Fail` in the
  chart template → both parity tests went RED; reverted → GREEN. (Use `go test -count=1`: go's
  test cache does not key on chart files read via exec.)
- F1 contract test: RED on head (the reproduction); temporarily set
  `controller.image.tag: "v0.9.0-a1ff903e"` (a post-#524 commit) → GREEN; reverted → RED.
  The test therefore flips exactly when the default image is bumped past the gate.
- Production code was never modified; `git status` on the branch shows only additive test/doc files.

## Interpreting results after a fix

| Test | Polarity | When F1 is fixed (image bumped past #524) |
| --- | --- | --- |
| `TestChartDefaultImageServesRenderedWebhookPaths` | contract | goes GREEN — this is the F1 tracker |
| `deprecationchartlegacy` suite | mechanism witness (canary-like) | still PASSES by design: it proves the causal chain (unregistered path → 404 → admission error → failurePolicy applied) and does not depend on the deployment state |
| `deprecationchart` suite | contract | stays GREEN |
| Parity tests | contract | stay GREEN unless the chart drifts from `config/webhook/manifests.yaml` |

## Findings summary (full detail in the review debate document)

- **F1 [major]** `deploy/helm/rbgs/values.yaml:13` — the default image tag predates #524. Until the
  0.9.0 image-bump commit lands, `helm install/upgrade` from the chart source with default values
  fail-closes every v1alpha1 RoleBasedGroup/RoleBasedGroupSet create and update (the chart README's
  own "Installing" instructions hit this). CI's helm-mode e2e overrides `controller.image.tag`
  (`.github/actions/setup-e2e-cluster/action.yaml:198`), and the one job that installs with default
  images (`release-test.yml` e2e-test-helm) is command-gated and did not run on this PR — so CI
  green does not cover this state. Fix: bump the default image to a post-#524 build (verified
  green with tag `v0.9.0-a1ff903e`), or gate the new webhook entries on a value the release sets.
- **F2 [major]** no committed assertion that the chart's webhook configuration matches
  `config/webhook/manifests.yaml`. `make manifests` regenerates the shipped file (e.g. at the
  0.10.0 v1alpha1 removal) and nothing would flag the chart drifting back into the intermediate
  state this PR closes. Fix: land the L1 tests from this harness (uplift candidates).
- **F3 [minor]** root `README.md`/`README-zh_CN.md`: the flag table was corrected but the unchanged
  surrounding paragraphs still contradict it ("only for a first-time installation", "There is
  currently no supported way to turn the toggle off on an existing installation", "A migration
  guide ... will be provided in a later release").
- **F4 [minor]** chart README points readers to "the project migration guide", which does not
  exist yet (slice 3 per the PR body).
- **F5 [minor]** `Chart.yaml` `version` stays `0.9.0-alpha.2` despite a 126-line template change;
  a chart packaged from main before the release bump would collide with the published
  0.9.0-alpha.2.

## Continuing after the fix

Graft this harness onto any ref (the fixed PR head, or a post-merge commit):

```bash
git fetch https://github.com/sgl-project/rbg.git pull/552/head
git checkout verify/pr552-chart-deprecation-webhooks-claude
bash docs/verification/pr552-chart-deprecation-webhooks/scripts/re-verify.sh <ref-or-empty>
```

`re-verify.sh` prints per finding **Fixed / Still-broken / Partial / Harness-update** and exits 0
iff all contract tests pass. Prerequisites: go, helm on PATH, envtest assets (setup-envtest),
`KUBECONFIG` unset or pointing at a 0600/missing file.

### Kickoff prompt for a fresh agent

> You are resuming a review verification for https://github.com/sgl-project/rbg/pull/552.
> Checkout the branch `verify/pr552-chart-deprecation-webhooks-claude` (pushed to the fork
> `cheyang/rbg`). Read `docs/verification/pr552-chart-deprecation-webhooks/README.md` and
> `verify-manifest.json`, then run `scripts/re-verify.sh` with no argument (it resolves the
> current PR head itself). Report per finding Fixed / Still-broken / Partial / Harness-update,
> honoring the polarity table above (the legacy-binary suite is a mechanism witness and stays
> green; the F1 tracker is `TestChartDefaultImageServesRenderedWebhookPaths`).
