# Verification — PR #552: ship the 0.9.0 deprecation webhooks in the Helm chart

- PR: https://github.com/sgl-project/rbg/pull/552
- Reviewed heads: round 1 `a1ff903e` → round 2 `1a4f7ab0` → **round 3 `69fc06c1`**
  (base moved: `73e05db4` (#524) → **`ea5e82ae`**; see Round 3 below)
- Reviewer: Reviewer A (Claude). Harness branch: `verify/pr552-chart-deprecation-webhooks-claude`
- Layers run: L1 (unit, `helm template` + go test) and L2 (integration, envtest). No live layer was run.

## Round 3 (head `69fc06c1`) — the PR became docs-only; base moved past #556

**The ground shifted under this PR.** Between rounds the author rewrote history (dropped the
round-2 merge-endpoint refactor `1a4f7ab0`, kept its docs commit `02f0e06e`) and **removed the six
v1alpha1 deprecation entries from the chart template** (`1bbfdfb9`). Meanwhile upstream main
moved `73e05db4 → ea5e82ae`, merging:

- **#556** `refactor(deprecation): remove v1alpha1 admission webhooks` — deletes
  `api/workloads/v1alpha1/deprecation_webhook.go`, the `cmd/rbgs` wiring, the six entries from
  `config/webhook/manifests.yaml` / `deploy/kubectl/manifests.yaml`, and updates the six CRD
  `deprecationWarning` texts to carry the 0.10.0 removal timeline (it also lands the
  `test/envtest/testcase/v1a1gate` suite covering the post-removal composition).
- **#555** `chore(rbgs): update Helm chart and default image versions to 0.9.0-alpha.3` —
  `Chart.yaml` `0.9.0-alpha.3`, `values.yaml` controller+crdUpgrade pins `v0.9.0-73e05db4`.

The PR's own delta vs merge-base is now **docs-only** (`README.md`, `README-zh_CN.md`, chart
README, `NOTES.txt`, `values.yaml` comments) and the PR body says so: *"It does not ship
v1alpha1 admission webhooks: #556 removes those handlers at the source because the CRD
deprecatedVersion warning already covers every v1alpha1 request and the v1alpha2 validators
continue to enforce the compatibility gate through conversion."*

Consequences for this harness:

- **Harness-update applied (large)**: the round-2 contract (chart renders 3 v1alpha1 entries in
  parity with the shipped 6) is inverted — the round-3 contract is **chart renders zero v1alpha1
  deprecation entries, in parity with the post-#556 shipped config**. `chart_contract_setup.go`
  now registers the post-#556 handler set (the four v1alpha2 webhooks) and defines its own
  `ContractWarningRecorder` (the old one lived in `v1alpha1_contract_setup.go`, deleted by #556);
  the `deprecationchartlegacy` suite is **retired** (its premise — chart renders v1alpha1
  entries a pre-#524 image does not serve — no longer exists). The L2 specs were rewritten to
  the post-#556 composition (gate through conversion, CRD-level warning) and gained an
  inventory-command spec running the chart README's documented jq verbatim.
- **The shipping tree is the merge, not the branch**: the PR branch itself still carries the
  pre-#556 code (its tree has the six v1alpha1 webhook entries in `config/webhook/manifests.yaml`
  and a chart that renders none — parity *fails* on the branch in isolation). `re-verify.sh` now
  merges the PR head into the current base before grafting (verified: the merge is clean, and
  the merged tree differs from the base by exactly the PR's five docs files).
- Round-3 verification ran against merge `69fc06c1` + base `ea5e82ae`:
  `results/reverify-69fc06c1-merge-l1.txt`, `results/reverify-69fc06c1-merge-l2.txt`.

### Round-3 observed vs expected

| ID | Finding | Layer / test | Observed on the merge of `69fc06c1` into `ea5e82ae` | Verdict |
| --- | --- | --- | --- | --- |
| F1 | Chart default image serves every rendered webhook path | L1 `TestChartDefaultImageServesRenderedWebhookPaths` | PASS: pin `v0.9.0-73e05db4` (from #555) registers all 5 rendered paths; chart renders no v1alpha1 paths | **Fixed** (via base #555 + this PR's chart-entry removal) |
| F2 | Chart↔shipped-manifest parity (now: v1alpha2 entries) | L1 `TestChartWebhookConfigurationsMatchShippedManifests` | PASS on merge tree (both sides carry only the v1alpha2 entries); bites on injected v1alpha1 entry (see bites record) | **Parity holds; the committed-guard gap is now pre-existing repo hygiene, not PR-introduced** |
| F8 | Cutoff guidance requires completed reconciliation | docs (README decision bullets, Upgrading, NOTES.txt, values.yaml comments) + `deleteOrphanRoles` gating re-checked in round 2 | `ecca7885` + `69fc06c1` add "reconciliation has finished / old RBG-owned workloads are gone" to all four places; NOTES.txt WARNING moved to apply in both modes | **Fixed** |
| F9 | NEW: pinned crdUpgrade image predates #556 — default-values installs emit the old CRD warning text (no 0.10.0 timeline) | L1 `TestPinnedCrdUpgraderImageCarriesDocumentedWarningText` | RED: `git show 73e05db4:config/crd/bases/*` warning texts carry no "0.10.0"; `tools/crd-upgrade/Dockerfile` COPYs `config/crd/bases` into the image, so the CRDs a default-values install applies come from the pin | **Open (minor)** — release-pinning, not PR-introduced |
| F10 | NEW: chart must render zero v1alpha1 deprecation entries (post-#556) | L1 `TestChartRendersNoV1alpha1DeprecationWebhookEntries` | PASS on merge tree; bites on injected entry | **Contract green** |
| F11 | NEW: every v1alpha1 CRD version is deprecated with a 0.10.0-timeline warning (the layer the docs now lean on) | L1 `TestCRDDeprecationWarningsCarryDocumentedTimeline` | PASS on merge tree (six kinds, `deprecated: true` + warning contains "0.10.0") | **Contract green** |
| — | Chart-rendered webhooks drive the documented gate in the post-#556 composition | L2 `deprecationchart`, 8 specs | 8/8 PASS: v1alpha1 new-use rejected through conversion; schema-absent-workload default → StatefulSet → rejected; explicit RoleInstanceSet accepted with exactly one CRD warning carrying 0.10.0; v1alpha2 write unwarned; type-exact grandfathering; deprecated-type switch rejected; set-template rejection; inventory jq lists exactly the deprecated roles | **Confirmed** (the PR's premise holds for the docs it ships) |
| F6 | helm-upgrade fail-closed rollout window (v1alpha1 Fail entries) | n/a | No v1alpha1 entries ship in the chart or the shipped config anymore; no released chart ever carried them (the entries only ever existed on PR branches) | **Obsoleted by removal** |
| F7 | Dead `DeprecationWarningValidator` | n/a | #556 deleted `deprecation_webhook.go` and its test file entirely | **Obsoleted** |
| F5 | Chart version not bumped | n/a | #555 (base) bumped chart to `0.9.0-alpha.3` / appVersion `v0.9.0-73e05db4` | **Fixed on base** |

### Round-3 residual risk recorded for the ledger

The PR branch (as pushed) is **7 commits behind its base** and still carries the pre-#556 code in
its tree, including the six v1alpha1 webhook entries in `config/webhook/manifests.yaml` and
`deploy/kubectl/manifests.yaml`. The merge into the current base is clean and the merged tree is
exactly base + this PR's five docs files (verified with `git merge-tree`), so nothing stale ships
— but per-commit history review (e.g. `git log -p` on the branch) shows chart entries being added
(`a1ff903e`) and later removed (`1bbfdfb9`). A rebase would drop the churn; not required for
correctness.

## Round 2 (head `1a4f7ab0`) — delta `a1ff903e..1a4f7ab0`

The author pushed two commits: `02f0e06e` (docs: toggle by installation type) and `1a4f7ab0`
(refactor: merge the four warning-only webhooks into one endpoint
`/validate-workloads-v1alpha1-deprecation` + marker listing the four resources). Consequences for
this harness:

- **Harness-update applied**: the six-entry model (four Ignore + two Fail) became three entries
  (two Fail + one merged Ignore). `v1alpha1DeprecationWebhooks`/`v1alpha1DeprecationPaths`/
  `v1alpha1WarningResources` in `test/chart/webhook_config_test.go` were updated accordingly; the
  path assertions are now exact per-entry (marker path == rendered path == registered path).
- `make manifests` at head is **idempotent** (zero diff) — controller-gen consumes the merged
  marker and regenerates both `config/webhook/manifests.yaml` and `deploy/kubectl/manifests.yaml`
  exactly as committed.
- The PR's own `test/envtest/testcase/deprecationv1a1` suite (shipped manifests + head handlers)
  passes 8/8 at head, including "returns the on-the-wire warning for every warning-only kind"
  through the merged endpoint.
- Per-finding outcomes at `1a4f7ab0`: **F1 still broken** (L1 tracker RED: the default image
  registers 3 of the 9 rendered paths unserved — both `Fail` entries and the merged warning
  path; L2 legacy witness 3/3 still reproduces the admission denial), **F2 parity holds**
  (updated guard PASS, bites on injected path drift) but the *committed*-guard gap in the PR is
  unchanged, **F3 fixed** (README EN+zh paragraphs rewritten; new schema-default claims verified
  against the CRD), **F4 fixed** (guide references now point at tracking issue
  sgl-project/rbg#454, which is OPEN), **F5 unchanged** (Chart.yaml still `0.9.0-alpha.2` /
  appVersion `0.9.0-d88b3186`).
- New round-2 observation (nit): `DeprecationWarningValidator` is now production-dead — no
  production code path wires it (the merged handler replaces it); only its own unit test
  references it, and `deprecationv1a1/contract_test.go:56` still describes the old wiring.

## Premise (P0) — CONFIRMED (round 1); SUPERSEDED round 3

Round 3 note: the premise the PR originally solved ("chart lacks the gate webhooks") was closed by
round 1 and then **dissolved**: #556 removed the v1alpha1 admission webhooks at the source, and
this PR now removes them from the chart too. The round-3 premise is the PR body's new claim — the
CRD deprecatedVersion warning covers every v1alpha1 request and the v1alpha2 validators enforce
the gate through conversion — which is verified above (F11, L2 suite 8/8). The original
differential evidence is retained below for provenance.

The PR claims to close the chart↔manifests intermediate state left by #524: the kubectl-rendered
`config/webhook/manifests.yaml` ships the v1alpha1 deprecation webhooks, while the Helm chart
still rendered the pre-gate webhook configuration, so a Helm install got no admission gate.

Differential evidence on the **base** branch (`73e05db4`):

- v1alpha1-deprecation entries in the chart template: **0**
- v1alpha1-deprecation entries in `config/webhook/manifests.yaml`: **6**

See `results/premise-and-static-evidence.txt`. The gap is real, and the PR closes it: the chart at
head renders the deprecation entries (six at `a1ff903e`, three after the round-2 merge), in
parity with the shipped manifests (verified below).

## Observed vs expected

| ID | Finding | Layer / test | Polarity | Observed on head `1a4f7ab0` | Verdict |
| --- | --- | --- | --- | --- | --- |
| P0 | Premise: chart lacks the gate webhooks on base | `results/premise-and-static-evidence.txt` | differential | base: 0 entries vs shipped 6 | **Confirmed** |
| F1 | Chart default image `v0.9.0-d88b3186` predates #524 and registers none of the v1alpha1 webhook paths the chart renders (both `Fail` entries + the merged warning path) | L1 `TestChartDefaultImageServesRenderedWebhookPaths`; L2 `deprecationchartlegacy` suite | contract (L1, RED = reproduction); mechanism witness (L2) | L1 RED: image commit `d88b3186` registers 0/3 deprecation paths (3 of 9 rendered paths unserved — `results/reverify-1a4f7ab0-l1.txt`). L2: v1alpha1 RBG/RBGSet create rejected with `failed calling webhook "vrolebasedgroup-v1alpha1-deprecation.kb.io": ... the server could not find the requested resource`; InstanceSet create succeeds (Ignore absorbs it) | **Still broken** (blocker) |
| F2 | Nothing in the repo guards chart↔shipped-manifest webhook parity (the PR's core claim) | L1 `TestChartWebhookConfigurationsMatchShippedManifests` + `TestChartV1alpha1DeprecationWebhookFields` | contract | PASS on head (parity holds after the merge-endpoint refactor; `make manifests` idempotent, zero diff); bites on injected path drift (round-2 bites check) | **Gap unchanged** — parity holds, the committed guard is still missing (major) |
| — | Chart-rendered webhooks behave like the shipped manifests | L2 `deprecationchart` suite, 5 specs | contract | 5/5 PASS: warning on v1alpha1 write, new-use rejection, type-exact grandfathering, deprecated-type switch rejection, set-template rejection | **Confirmed** (PR's functional claim holds through the merged endpoint) |
| — | Merged warning endpoint serves all four warning-only kinds (shipped-manifest path) | L2 PR suite `deprecationv1a1` (not harness — head's own tests) | contract | 8/8 PASS at head, incl. "returns the on-the-wire warning for every warning-only kind" | **Confirmed** |

## How to run

```bash
# L1 — unit (helm + go test). KUBECONFIG must point at a missing path or a 0600 file,
# otherwise helm's kubeconfig-permission warnings (stderr) pollute CombinedOutput
# in the chart tests and every render parse fails.
export KUBECONFIG=/tmp/rbgs-missing-kubeconfig
go test ./test/chart/ -count=1 -v

# L2 — integration (envtest). Assets: setup-envtest use 1.36.2 (any 1.3x works).
export KUBEBUILDER_ASSETS=$(ls -d ~/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64)
go test ./test/envtest/testcase/deprecationchart/ -count=1 -v -timeout 10m
```

Or run everything at once: `bash docs/verification/pr552-chart-deprecation-webhooks/scripts/re-verify.sh`

## Harness-bites record

- Parity tests: temporarily flipped `vinstance` entry's `failurePolicy: Ignore` → `Fail` in the
  chart template → both parity tests went RED; reverted → GREEN. (Use `go test -count=1`: go's
  test cache does not key on chart files read via exec.)
- Round 2 (after the Harness-update): injected path drift (`/validate-workloads-v1alpha1-deprecation`
  → `/validate-workloads-v1alpha1-drift`) into the chart template → both
  `TestChartV1alpha1DeprecationWebhookFields` and `TestChartWebhookConfigurationsMatchShippedManifests`
  went RED; reverted → GREEN.
- F1 contract test: RED on head (the reproduction); temporarily set
  `controller.image.tag: "v0.9.0-a1ff903e"` (a post-#524 commit) → GREEN; reverted → RED.
  The test therefore flips exactly when the default image is bumped past the gate.
- Production code was never modified; `git status` on the branch shows only additive test/doc files.
- Round 3: injected a v1alpha1 deprecation entry into the chart template (on the merge tree) →
  `TestChartRendersNoV1alpha1DeprecationWebhookEntries` AND
  `TestChartWebhookConfigurationsMatchShippedManifests` went RED (the parity test also reported
  "chart renders 4 validating webhooks, shipped manifests carry 3"); reverted → GREEN. This is
  the regression guard for the round-3 contract (chart must not grow entries the binary does not
  serve).
- Round 3 (logic bite for F9's tracker): `git show 73e05db4:config/crd/bases/…` returns the
  pre-#556 warning text (no "0.10.0") while `git show 47b347fd:…` carries the timeline — the
  tracker fails on the first and would pass on the second, i.e. it flips exactly when the
  crdUpgrade pin moves past #556.

## Interpreting results after a fix

| Test | Polarity | Round-3 meaning |
| --- | --- | --- |
| `TestChartDefaultImageServesRenderedWebhookPaths` | contract | GREEN on the merge tree — F1 tracker (was RED rounds 1–2) |
| `TestChartRendersNoV1alpha1DeprecationWebhookEntries` | contract | GREEN; RED if the chart ever re-adds v1alpha1 deprecation entries |
| `TestChartWebhookConfigurationsMatchShippedManifests` | contract | GREEN; RED on any chart↔config drift (committed-guard gap F2 residual) |
| `TestCRDDeprecationWarningsCarryDocumentedTimeline` | contract | GREEN; RED if a CRD loses its v1alpha1 deprecation marker/timeline |
| `TestPinnedCrdUpgraderImageCarriesDocumentedWarningText` | contract | **RED by design until the crdUpgrade pin moves past #556** — the F9 tracker |
| `deprecationchart` suite | contract | stays GREEN (gate through conversion, CRD warning, inventory command) |
| `deprecationchartlegacy` suite | retired in round 3 | its premise (chart renders v1alpha1 entries a pre-#524 image does not serve) no longer exists |

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
