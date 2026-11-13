# KEP-576 compact discovery ConfigMap (PR #578) — review verification (Codex)

Reproducible evidence for the review of https://github.com/sgl-project/rbg/pull/578
(KEP-576, docs-only KEP; head `b83bff8c285ebc8beff8d208524eb7c4b72429c1`,
base `3abe58a5aac1c2ab1381af337514e9e8f27f901b`, merge-base `ea5e82ae59169e0e954fb1c7068f9480fb4b7375`).

Because the PR changes only `keps/576-compact-discovery-configmap/*`, the verification
targets are the KEP's factual claims about the **shipped codebase** — its "Shipped
Baseline", rollout/downgrade matrix, and membership semantics. The tests pin down what
the shipped code actually does; they are contract tests on the code, and where the
observed behavior contradicts the KEP text, the KEP text is what must change.

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `pkg/discovery` ConfigBuilder output ordering, legacy injector target, initial membership planning | `go test ./pkg/discovery/ -run TestKEP576 -v` |
| 2. Integration | real API server via envtest: legacy-mode RBG end to end (child RIS pod template vs. ConfigMap existence) | `KUBEBUILDER_ASSETS=$(bin/setup-envtest use 1.31.0 --bin-dir bin -p path) go test ./test/envtest/testcase/rbg/ -run TestRBGController -ginkgo.focus 'KEP-576 baseline: legacy discovery mode' -v` |
| 3. Live | skipped (docs-only PR; no runtime change to exercise) | — |

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "The shipped `refine` mode (#134, released v0.7.0/v0.8.0) stores a full enumeration of every instance address in a single RBG-level ConfigMap — ~40-120 bytes per serialized entry; a 4000-replica role with 5 pods per instance is ~2.4 MB, beyond the 1 MiB ConfigMap limit, and every scale event rewrites the whole object to every mounting node." (PR body) |
| Linked issue | #576 (OPEN, 2026-11-13, "Compact declarative discovery ConfigMap (supersedes KEP-133)") — valid, matches |
| Reported component | `pkg/discovery` builder + RBG-level discovery ConfigMap (refine mode) |
| Patched component | docs only: `keps/576-compact-discovery-configmap/` (the design that would change that component) |
| Component match | Yes — the KEP's target component is exactly the component the symptom lives in |
| **Verdict** | **Confirmed (mechanism)** — the enumeration is `O(instances)` and written by the controller; see below |
| Evidence | `reconcileRefinedDiscoveryConfigMap` builds one `<rbg>` ConfigMap from `ConfigBuilder.Build()` with one entry per pod (`pkg/discovery/config_builder.go`). Arithmetic: 4000 instances x 5 pods x ~118 B/entry = ~2.36 MB > 1 MiB. Not load-tested at 4000 replicas (docs PR; the mechanism + arithmetic are the claim). |

One arithmetic nit inside the premise: the PR summary says "Measured at roughly 43 bytes
per pod, a 4000-replica role with 5 pods per instance crosses the 1 MiB ConfigMap limit".
43 B x 20,000 pods = ~860 KB < 1 MiB; the crossing needs the KEP's own ~118 B/entry
figure (which yields ~2.4 MB). The summary sentence pairs the wrong per-pod number with
the conclusion (finding F4).

## Summary of results

| ID | Claim under test | Layer | Verdict | Evidence |
|----|------------------|-------|---------|----------|
| F1 | KEP baseline #6 "roles in spec order" vs. shipped `roles:` section order | 1 | **KEP text refuted by code**: `group.roles` is spec order, but the `roles:` mapping is alphabetical (Go map via sigs.k8s.io/yaml -> JSON) | results/unit-initial-run.txt (`TestKEP576_Baseline_RolesSectionIsAlphabeticalNotSpecOrder`) |
| F2 | KEP rollout/downgrade model: "legacy = per-role ConfigMaps, direct mount (unchanged)" | 1+2 | **KEP text refuted by code**: since #188 (shipped v0.7.0/v0.8.0) the injector mounts the RBG-level `<rbg>` ConfigMap for every stateful role regardless of mode, and no path creates that object for `legacy` RBGs | results/unit-initial-run.txt + results/integration-legacy-mode-envtest.txt |
| F3 | KEP "does not project the user-facing excluded-ordinals annotation" | 1 | **KEP text overbroad**: before the child RIS exists, the shipped builder projects valid exclusion input (`replicas=3, excluded="1"` -> members `[0 2 3]`, incl. ordinal >= replicas) | results/unit-initial-run.txt (`TestKEP576_Baseline_InitialMembershipProjectsExclusionInput`) |

Harness-bites check: each assertion was temporarily inverted (spec-order roles, per-role
legacy mount, dense `[0 1 2]` initial membership) — all three tests then FAIL
(results/unit-harness-bites-inverted.txt, 3 failures), and pass again after restore.
Production code untouched throughout.

Facts independently verified by code reading (no test needed): address templates and the
`component_index = offset + j` rule match `pkg/discovery/config_builder.go:buildInstances`
and `pkg/reconciler/roleinstance/utils/instance_utils.go:FormatComponentPodName` for
Standalone / CustomComponents / LeaderWorker(RIS); the deprecated LWS vendor naming is
indeed not reproduced by the shipped builder (KEP's stated reason for excluding it is
accurate); `GetWorkloadName`/`GetServiceName` own the 63-char truncation + `s-` prefix;
`GetCompatibleHeadlessServiceName` implements the legacy-Service compat path and the
per-component `ServiceName` override; `ensureDiscoveryConfigMode` re-pins an unknown mode
to `legacy` when status exists, else `refine` (matches the downgrade matrix); deprecated
workload types are rejected at admission (`rolebasedgroup_admission.go`, with an
`EnableDeprecatedWorkloadTypes` escape hatch).

## Per-finding detail

### F1 — roles-section ordering (design-spec contradiction)

`RolesInfo` is `map[string]RoleInstances`; `ConfigBuilder.Build()` marshals with
`sigs.k8s.io/yaml` (JSON round-trip), so the `roles:` section is emitted with
alphabetically sorted keys. Observed for spec roles `[zz-role, aa-role]`:

```yaml
group:
  name: demo
  roles:          # spec declaration order: zz-role, aa-role
  - zz-role
  - aa-role
roles:            # alphabetical: aa-role first
  aa-role: ...
  zz-role: ...
```

The KEP stores compact roles as an array "in spec declaration order" and gates e2e on
"field-by-field, order-sensitive equality" with the shipped output. For non-alphabetical
role names those two requirements are mutually exclusive unless the renderer marshals the
`roles:` section through the same map shape (in which case "preserving declaration order"
only applies to `group.roles` and the `instances` arrays, and the KEP should say so).

### F2 — legacy population mis-modeled

- v0.6.0: injector created + mounted per-role ConfigMaps (`rbg.GetWorkloadName(role)`).
- #134: mode-aware injector (legacy -> per-role CM).
- #188 (shipped in v0.7.0/v0.8.0 and on main): injector always mounts `<rbg>`;
  `reconcileRefinedDiscoveryConfigMap` creates `<rbg>` only when mode == `refine`.
- envtest: legacy-pinned RBG -> child RIS pod template contains `rbg-cluster-config`
  referencing ConfigMap `legacy-demo`; that ConfigMap never exists; refine control
  RBG gets its ConfigMap (results/integration-legacy-mode-envtest.txt).

So the KEP's "legacy: Untouched: per-role ConfigMaps, direct mount" row and the
"legacy | Supported (native mode of old controller)" downgrade cell describe the
#134-era design intent, not the behavior of the controller versions the matrix names
(v0.7/v0.8): under those, a legacy RBG's recreated pods mount a missing ConfigMap and
wedge on the volume (pre-existing product gap, not caused by this PR — but the KEP's
three-population model should not certify it as "Supported"/"unchanged").

### F3 — initial membership is exclusion-projected, not committed

`memberOrdinals` with no child RIS, `replicas=3`, `excluded-ordinals="1"` returns
`[0 2 3]` (observed). The compact builder must replicate this initial-planning path;
writing only "committed" values on first reconcile would diverge from the enumeration
builder for RBGs created with a valid exclusion annotation. The KEP's source-of-truth
list half-covers this ("the same initial dense planner ... when no exclusion input is
present") but the headline sentence and the field reference ("committed replica count")
need to admit the pre-child-creation projection path explicitly.

## Proposed fixes (KEP text only; production untouched)

- F1: correct baseline #6 (roles section alphabetical; `group.roles` + `instances` are
  the order-sensitive parts); make the renderer emit the `roles:` section through the
  same map/struct shape as `ClusterConfig`, and scope the "order-sensitive" e2e gate
  accordingly.
- F2: correct the legacy row to describe current-main behavior (injector mounts `<rbg>`
  regardless of mode; `<rbg>` never created for legacy), and mark the legacy downgrade
  cell accordingly (or fix the injector's legacy branch as a separate product bug).
- F3: reword "does not project the user-facing annotation" -> "does not project it once
  the committed child target exists; before child creation it mirrors the same initial
  planner as `memberOrdinals`".
- F4: fix the summary arithmetic (use ~118 B/entry, or drop the 43 B figure).

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/kep-576-compact-discovery-codex` (production code
untouched). These are codebase-fact contract tests: they PASS while the shipped code
keeps the observed behavior. If a future PR changes the builder/injector/membership
code, a failure here means *the code moved* — re-check the KEP text against the new
behavior rather than assuming the test is stale.

```bash
git fetch https://github.com/cheyang/rbg.git verify/kep-576-compact-discovery-codex
git checkout <ref-to-check>
git checkout verify/kep-576-compact-discovery-codex -- \
  docs/verification/kep-576-compact-discovery-codex \
  pkg/discovery/kep576_baseline_verify_test.go \
  test/envtest/testcase/rbg/kep576_legacy_mode_verify_test.go
bash docs/verification/kep-576-compact-discovery-codex/scripts/re-verify.sh   # auto-discovers PR head
```

Kickoff prompt for a fresh agent: "Check out the verify branch above, read
docs/verification/kep-576-compact-discovery-codex/README.md and verify-manifest.json,
run scripts/re-verify.sh, and report Fixed/Still-broken per finding."
