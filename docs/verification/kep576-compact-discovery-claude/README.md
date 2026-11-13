# KEP-576 compact discovery ConfigMap (PR #578) — verification harness, Reviewer A (Claude)

- **PR:** https://github.com/sgl-project/rbg/pull/578 (docs-only: adds
  `keps/576-compact-discovery-configmap/` — README.md + kep.yaml; no code changes)
- **Head:** `b83bff8c285ebc8beff8d208524eb7c4b72429c1` · **Base (merge-base):** `ea5e82ae59169e0e954fb1c7068f9480fb4b7375`
- **Harness branch:** `verify/kep576-compact-discovery-claude` (this branch) —
  production code untouched; only additive test files + this directory.

Because the PR is documentation-only, base and head **production code are
identical**. The harness therefore pins the *codebase facts* the KEP's design
and compatibility gate are written against (canaries), and adds
**doc-contract tests** on the KEP text itself whose red-on-head is the
reproduction of each doc finding (vacuous pass on the merge-base, where the
KEP does not exist — see `results/unit-l1-base.txt`).

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (`pkg/discovery`, `internal/controller/workloads`) | shipped-builder facts + KEP doc-contract checks | `go test -count=1 -v -run 'TestKEP576Claude' ./pkg/discovery/ ./internal/controller/workloads/` |
| 2. Integration | **not applicable** — the PR changes no controller/builder code; every claim is decided at L1 (fact canaries) or is a doc-content claim | — |
| 3. Live | skipped per round policy (no KUBECONFIG layer this round) | — |

> Polarity: the three `TestKEP576Claude_Doc*` tests are **contract** tests on the
> KEP document — red on the PR head is the reproduction, green once the KEP text
> is corrected. All other tests are **canaries of current code behavior** — green
> means the codebase fact is pinned; they are the ground truth the KEP must model.

## Problem premise (P0)

Issue https://github.com/sgl-project/rbg/issues/576 (OPEN, 2026-11-13, same
author; referenced from the PR body, no closing keyword).

| | |
|---|---|
| Claimed symptom | "The RBG discovery ConfigMap (`/etc/rbg/config.yaml`) stores a full enumeration of every instance address. Measured at ~43 bytes/pod, a 4000-replica role with 5 pods per instance crosses the 1 MiB ConfigMap limit, and every scale event rewrites the whole object to every mounting node." |
| Linked issue | #576, OPEN, created 2026-11-13 (day before the PR), still valid |
| Reported component | shipped `refine` discovery ConfigMap (`pkg/discovery/config_builder.go` + `reconcileRefinedDiscoveryConfigMap`) |
| Patched component | a KEP proposing the replacement design for exactly that component |
| Component match | Yes |
| **Verdict** | **Confirmed** — the enumeration really is O(pods); measured ~129 bytes/entry for the KEP's own example shape (CCP leader 1 + worker 4, ports http+grpc) ⇒ 4000 × 5 entries ≈ 2.6 MB > 1 MiB. (`TestKEP576Claude_P0_EnumerationSizeGrowsWithPodCount`, `results/unit-l1-head.txt`) |
| Caveat | the *Summary's* "~43 bytes per pod ... crosses the 1 MiB limit" arithmetic is wrong (20000 × 43 B = 860 KB < 1 MiB) — see F4. The Motivation's ~118 B/entry analysis (~2.4 MB) is the correct one; measured 129 B. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | premise: refine enumeration is O(pods) and blows the 1 MiB limit | 1 | **Confirmed** | ~129 B/entry × 20000 ≈ 2.6 MB; `results/unit-l1-head.txt` |
| F1 | KEP Shipped Baseline says today's output has "roles in spec order"; actually the `roles` map is serialized with **sorted keys** (only `group.roles` is spec order) — the order-sensitive equality gate is self-contradictory | 1 | **Confirmed (doc defect)** | doc-contract test FAIL on head, canary PASS; `results/unit-l1-head.txt` |
| F2 | compat-rendering output schema underspecified (group object, per-role `size`, port-key normalization) | 1 | Confirmed by code reading (no mechanical test — design gap, not a false statement) | `pkg/discovery/config_builder.go:58-96` |
| F3 | pre-child-creation + exclusion input: shipped builder **projects the annotation**, KEP says dense — divergence window | 1 | **Confirmed (doc defect)** | doc-contract FAIL on head; canary `TestKEP576Claude_PreChildExclusionWindowProjectsAnnotation` PASS |
| F4 | Summary size arithmetic internally inconsistent (43 B/pod does not cross 1 MiB) | 1 | **Confirmed (doc defect)** | doc-contract FAIL on head; measured 129 B/entry |
| F5 | migration scope for `legacy` RBGs unspecified | — | open question (code-path) | `keps/576-.../README.md` Rollout section |
| F6 | legacy-object deletion readiness signal unspecified | — | open question (code-path) | same |
| F7 | alpha default-on (`refine-compact` for every new RBG) with no feature gate | — | open risk (hypothesis) | same |

## Per-finding detail

### F1 — roles-map ordering (major)

Mechanism: `ClusterConfig.Roles` is `map[string]RoleInstances`
(`pkg/discovery/config_builder.go:69`). `sigs.k8s.io/yaml` marshals Go maps
through JSON, which sorts keys, so today's `roles:` block is **alphabetically
key-sorted**, while `group.roles` (a slice, `config_builder.go:98-104`) is
spec order. The KEP's Shipped Baseline item 6 ("... and roles in spec order"),
the Compact Format table ("Role entries in spec declaration order (rendering
preserves the shipped enumeration order)"), and Goal 2 ("order-sensitive ...
spec ordering") all describe an output ordering the shipped builder does not
produce. A renderer that emits the roles map in spec order produces a
different file than today for every RBG whose role names are not already
alphabetical (e.g. roles `[prefill, decode]` → today emits `decode:` first),
so the KEP's own e2e gate ("field-by-field, order-sensitive equality") fails
against its own instructions — or the implementers quietly reinterpret the
gate, leaving the spec wrong.

- Reproduction: `TestKEP576Claude_DocMustNotClaimRolesMapIsSpecOrder` (FAIL on
  head = reproduction; vacuous pass on base).
- Ground truth: `TestKEP576Claude_ShippedRolesMapOrderIsSortedNotSpecOrder`
  (roles map = `[decode prefill]` while spec order and `group.roles` =
  `[prefill decode]`); also visible in the pre-existing repo fixture
  `pkg/discovery/config_builder_test.go` ("simple cluster config": spec order
  `[worker leader]`, serialized `leader:` before `worker:`).
- Fix: reword the Shipped Baseline (roles map is key-sorted; `group.roles` is
  spec order), and specify which ordering the renderer must emit for the
  `roles` mapping + whether the equality gate compares parsed structures
  (map order irrelevant) or serialized text (order load-bearing).

### F2 — rendered-output schema underspecified (minor)

The shipped file is `group: {name, size, roles}` + `roles.<name>:
{size: <entry count>, instances: [...]}` with port keys normalized by
`generatePortKey` (lowercase, `-`→`_`, `port<N>` for unnamed ports,
`config_builder.go:194-199`). The compact source carries `group` as a plain
string, per-role `replicas` (instance count, not entry count), and ports as
declared. All of it is derivable, but the KEP never states the rendered
file's schema, and "field-by-field equality" silently depends on these
fields. An implementer following the doc literally emits `group:
pd-inference` (string) — not equal, field-by-field, to today's group object.
Fix: one paragraph in "Compatibility Rendering Layer" defining the rendered
schema (group object incl. `size`/`roles`, per-role `size` = rendered entry
count, port-key normalization rule).

### F3 — pre-child exclusion window (minor)

`memberOrdinals` (`config_builder.go:253-266`): when the RoleInstanceSet does
not exist yet **and** the role carries a valid `excluded-ordinals`
annotation, the shipped builder publishes `ordinals.Select(nil, replicas,
exclusions)` — i.e. it projects the **user annotation** (canary:
`[0 2 3]` for replicas 3, exclusion [1]). The KEP's Committed Membership
section says the compact format "does not project the user-facing
excluded-ordinals annotation" and lists the pre-child source of truth as
dense ("dense `[0, replicas)` when no exclusion input is present"), leaving
the exclusion-present window undefined. In that window a faithful renderer
per the KEP diverges from the shipped output the KEP promises to reproduce.
Fix: specify what the compact source publishes between an exclusion-annotation
edit and the child's first commit (mirroring `memberOrdinals`, including the
`invalid input → empty` case at `config_builder.go:256-258`).

### F4 — Summary arithmetic (minor)

Summary: "Measured at roughly 43 bytes per pod, a 4000-replica role with 5
pods per instance crosses the 1 MiB ConfigMap limit." 20000 × 43 B = 860 KB —
under the limit. The claim only holds at the with-ports entry size (KEP's own
Motivation: ~118 B; measured 129 B ⇒ ~2.6 MB). Copied verbatim from issue
#576's flawed sentence. Fix: use the with-ports number in the Summary (or
state both and let the arithmetic speak).

### F5 — legacy-RBG opt-in unspecified (minor, question)

The opt-in precondition ("must not contain stateful roles of a deprecated
workload type ... otherwise ... keeps the RBG on `refine`") is written for the
`refine` population. A `legacy` RBG (pre-#134) with RIS roles satisfies the
precondition as written, but the migration sequence is expressed entirely in
terms of the legacy `<rbg>` enumeration object — which legacy RBGs do not
have (they have per-role ConfigMaps), and whose cleanup path is then
undefined (orphaned per-role ConfigMaps). Fix: either reject mode changes on
`legacy` RBGs explicitly, or specify the legacy variant (per-role CMs never
rewritten; when/if deleted).

### F6 — legacy-object deletion readiness signal (minor, question)

"Once the rollout completes and no pod mounts the legacy volume, the
controller deletes the `<rbg>` enumeration object." How is "no pod mounts"
determined — child status (updatedReplicas/generation observed)? stuck
terminating pods? pods on the pre-migration template during a partial
rollout? Deleting a ConfigMap that a still-running pod mounts leaves kubelet
serving stale content (same as the disclosed stale behavior, but it should be
specified, with the readiness signal and any grace period).

### F7 — alpha default-on (minor, risk)

Alpha graduation criteria: "new RBGs default to `refine-compact`" — every new
RBG in every upgrading cluster takes the untested init-container path, with
the renderer image as a new release-pipeline dependency. Consider a
controller-level gate (default `refine` at alpha, flip to `refine-compact`
at beta) — the KEP already specifies the image flag, so the gate is cheap.
Judgment call, not a defect.

## Confirmed-positive design claims (verified, no finding)

These KEP claims were checked against the code and are **accurate** — worth
recording so the cross-review does not re-litigate them:

- `component_index = offset + j` with the documented per-pattern offsets
  reproduces the shipped address list exactly (values and order) for
  CustomComponentsPattern (incl. component `ServiceName` override) and
  LeaderWorkerPattern on RoleInstanceSet
  (`TestKEP576Claude_ComponentIndexRuleReproducesShippedAddresses`).
- Committed membership publishes members with ordinal ≥ replicas (replicas 3,
  committed skips [1,3] → [0,2,4]) — `CommittedOrdinals()` = `skips.Members(n)`
  (`api/workloads/v1alpha2/excluded_ordinals.go:112-126`).
- Downgrade re-pin mechanics: an old controller meeting the unknown
  `refine-compact` value re-pins to `legacy` when the RBG has status and to
  `refine` when it has none (`ensureDiscoveryConfigMode` /
  `shouldUseLegacyDiscoveryConfig`, `rolebasedgroup_controller.go:409-468`).
- The injector mounts the ConfigMap as a whole volume (no subPath) at
  `/etc/rbg` for stateful roles (`pkg/discovery/injector.go:60-123`) — the
  live-refresh claim in the Snapshot Contract is real.
- Relative (short-form) addresses, spec-order components, per-entry port
  copies, `group.size` = role count: all as described.
- Role-name uniqueness is CRD-enforced (`roles` is
  `x-kubernetes-list-type: map` keyed by name in
  `config/crd/bases/workloads.x-k8s.io_rolebasedgroups.yaml`).
- `test/e2e/testcase/v1alpha2/discovery_configmap.go` exists (the e2e gate the
  KEP plans to extend).
- Prior-round (PR #570) findings F2 (FQDN), F3 (committed membership), F4
  (component order), F5 (snapshot disclosure), F6 (size guardrail) are
  genuinely resolved in this KEP; F1's replacement baseline reintroduces one
  new inaccuracy (this round's F1).

## Harness-bites check

No production fix exists to apply/revert (docs-only PR), so the bite check is
done by mutation (`results/mutation-bites.txt`):

1. Inverting the F1 canary's expected order (spec order instead of sorted)
   makes it FAIL.
2. Relaxing the component-index rule (worker `offset: 0` instead of 1) makes
   the indexing contract test FAIL.
3. The doc-contract tests provably bite differentially: **PASS(vacuous) on the
   base** commit — KEP absent (`results/unit-l1-base.txt`: doc tests SKIP,
   everything else green, full suites ok) and **FAIL on the PR head**
   (`results/unit-l1-head.txt`) — same harness, different doc, opposite
   outcome.
4. Full pre-existing suites still pass with the harness grafted on both base
   (`go test ./pkg/discovery/ ./internal/controller/workloads/` → ok) and
   head (only the three doc-contract reproductions are red).

## Continuing after the fix

1. `git fetch` this branch, then from a checkout of the fixed code:
   `bash docs/verification/kep576-compact-discovery-claude/scripts/re-verify.sh`
   (no ref: it resolves the current PR head from `manifest.pr`).
2. Polarity table:
   - `TestKEP576Claude_Doc*` (contract): **Fixed = pass**. Red = the KEP still
     carries the defect.
   - Fact canaries: green means "codebase fact unchanged". If an
     implementation of KEP-576 lands, `TestKEP576Claude_ShippedRolesMapOrder…`
     and `TestKEP576Claude_PreChildExclusionWindowProjectsAnnotation` describe
     the *old* builder; keep them until the renderer contract is implemented,
     then invert/promote per the new compat contract (Harness-update).
3. Update `verify-manifest.json` per finding (`fixed` / `still-broken` /
   `harness-update`) and advance `.last-reviewed`.
4. F2/F5/F6/F7 have no mechanical test (design gaps/questions, not false
   statements): re-review them by reading the revised KEP sections.

Kickoff prompt for a fresh agent: "You are resuming the KEP-576 PR #578 review
harness. Read
docs/verification/kep576-compact-discovery-claude/README.md and
verify-manifest.json, run scripts/re-verify.sh against the current PR head,
and report per finding Fixed/Still-broken/Harness-update honoring the polarity
table. F2/F5/F6/F7 need a manual doc re-read."
