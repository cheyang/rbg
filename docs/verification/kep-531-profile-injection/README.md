# kep-531-profile-injection — design-doc verification

Reproducible evidence for the findings raised while reviewing
[PR #548](https://github.com/sgl-project/rbg/pull/548) (KEP-531: Engine Runtime
Profile Injection — **documentation-only**; production code is identical to
base `3612013e`/`73e05db4`).

**Round 3** (frozen head `d84004a2`; the PR was force-pushed with a condensed
document). Round 2 reviewed `7a9827bc`; round 1 reviewed `12693d02`.
Layers run against the code under review:

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `SidecarBuilder`/`PodReconciler` injection behavior that the KEP documents and pins compatibility against | `go test ./pkg/discovery/ ./pkg/reconciler/ -run 'TestKEP531_Verify' -json` |
| 1b. Doc | 18 doc-text checks (round 3): 11 CONTRACT (fixed state of prior findings; fail = regression) + 7 CANARY (open wording defects; flip = fixed) | `bash docs/verification/kep-531-profile-injection/scripts/check_doc_contracts.sh [--json]` |
| 2. Integration | — (skipped: docs-only PR, no runtime change) | — |
| 3. Live | — (not applicable) | — |

> Test polarity: the **unit** tests and the doc **CONTRACT** checks pass on
> the current head and MUST keep passing after the KEP implementation lands —
> a red result is a compatibility break or a doc regression. The doc **CANARY**
> checks pass *while the defective wording is present* and flip to fail when
> the author fixes it (then invert the check into a contract).

## Problem premise (P0)

Answered before the findings, run against the **base** branch without the
patch, because the question is whether the problem exists today.

| | |
|---|---|
| Claimed symptom | issue #531: `engineRuntimes[].injectContainers` "is accepted by the API but never consumed"; "LeaderWorkerPattern workers receive no profile injection at all"; "CustomComponentsPattern injects the full profile into every component"; `updateStrategy: RollingUpdate` "is declared in the CRD but unimplemented" |
| Linked issue | #531, OPEN, created 2026-10-30, still valid (no injection-semantics change landed since) |
| Reported component | `pkg/discovery/sidecar_builder.go`, `pkg/reconciler/*` (engine-runtime injection) |
| Patched component | `keps/531-engine-runtime-profile-injection/` — design doc; implementation follows in separate PRs |
| Component match | Yes (a KEP is the right artifact for a design decision over that component) |
| **Verdict** | **Confirmed** — gaps 1–3 reproduced on base; gap 4 (updateStrategy) confirmed unconsumed by code search, but the KEP does not address it (finding F2) |
| Evidence | `results/unit-l1.txt`; code refs below |

Premise evidence (base `3612013e`):

- Gap 1 (injectContainers inert): `pkg/discovery/sidecar_builder.go` never
  reads `runtime.InjectContainers`; the only references are API type
  declarations and v1alpha1↔v1alpha2 conversion copies
  (`rolebasedgroup_conversion.go:297,478`). Reproduced by
  `TestKEP531_Verify_InjectContainersIsInert`.
- Gap 2 (LWP worker skip): `roleinstanceset_reconciler.go:426` and
  `lws_reconciler.go:282` both `SetInjectors` the worker pod reconciler
  without `sidecarInjector` ("workerTemplate do not need to inject sidecar").
  Reproduced at the wiring level by `TestKEP531_Verify_WorkerChainSkipsSidecarInjection`.
- Gap 3 (CCP broadcast): `constructRoleInstanceTemplateByCustomComponentsPattern`
  (`roleinstanceset_reconciler.go:318-320`) builds every component with the
  default injector set (config, sidecar, commonEnv) — full profile into each
  component. Verified in code (wiring identical to the reproduced leader chain).
- Gap 4 (updateStrategy unconsumed): no consumer of
  `ClusterEngineRuntimeProfileSpec.UpdateStrategy` in `pkg/` (grep). The KEP
  does not address it anywhere — see finding F2.

## Summary of results — round 3 (head `d84004a2`)

The PR was force-pushed again: one commit, "docs(kep): condense 531 README and
address review rounds 2-3" (README only; kep.yaml unchanged), 1096 insertions /
1349 deletions. The condense reworded most round-2 fixed-state sentences
(all 7 affected contracts re-pinned to the new wording — see
`results/doc-bites-r3.txt` for the bite check on the new phrases) and
redesigned two areas:

1. **Firing rules narrowed** — profile-side `containerPatches` now fire at
   **role-declared** `targetContainers` entries only (a profile-declared entry
   fires just its own `extraPatches`). This closes the round-2
   F-STAGE3-LEGACY-VISIBILITY publisher-wins geometry at the firing level.
2. **A final env normalization** after stage 4 keeps the rendered template
   duplicate-free, with author precedence ("the role-authored entry winning
   over any profile-authored entry of the same name") — which closes the same
   geometry at the outcome level. Its stated *justification* is false, though:
   see F-SSA-ENV-PREMISE below.
3. **Profile Mutability redesigned** — profiles are immutable while
   referenced (CERP webhook alpha-warning→beta-reject, in-use finalizer,
   controller CERP watch, `--disable-profile-immutability` escape hatch);
   `updateStrategy` deprecated. This closes round-2's F-PROPAGATION-TRIGGER
   by design change (its misstated sentence is gone) and answers round-2's
   Q-CERP-V1ALPHA1-NONE-CONVERSION (`containerPatches` mirrored into the
   v1alpha1 CERP schema, round trip pinned by envtest).

Run: `bash scripts/re-verify.sh --layers unit,doc` (raw output
`results/reverify/`, doc table `results/doc-contracts-r3.txt`, bites
`results/doc-bites-r3.txt`).

| ID (ledger) | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | issue #531 gaps 1–4 exist on base | unit | Confirmed (round 1; re-run green) | `results/reverify/unit.out` |
| F-ENV-CONFLICT | round-1 blocker (fixed r2) | doc | **STAYS FIXED** — one positional rule set, re-pinned wording | contract pass |
| F-COVERAGE-UPDATESTRATEGY | round-1 major (fixed r2) | doc | **STAYS FIXED** — now via deprecation + immutability Non-Goal | contract pass |
| F-CAPS-MAXITEMS | round-1 major (fixed r2) | doc | **STAYS FIXED** — "no new caps" + stored-object rationale | contract pass |
| F-COMPONENT-VOCAB | round-1 minor (fixed r2) | doc | **STAYS FIXED** — "The lack of a pattern … is deliberate" | contract pass |
| F-ENABLEMENT-ROLLBACK | round-1 minor (fixed r2) | doc | **STAYS FIXED** — both sections present | contract pass |
| F-CONVERSION-ROUNDTRIP | round-1 item (fixed r2) | doc | **STAYS FIXED** — plus the CERP v1alpha1 mirror round trip | contract pass |
| F-UNKNOWN-TARGET-PROSE | round-1 nit (fixed r2) | doc | **STAYS FIXED** | contract pass |
| F-COPILOT-SECRET-CHANNEL | round-1 nit (fixed r2) | doc | **STAYS FIXED** — re-pinned phrase | contract pass |
| F-COPILOT-VOLUMEMOUNT-EQUALITY | round-1 nit (fixed r2) | doc | **STAYS FIXED** — re-pinned phrase | contract pass |
| F-INFERENCE-KIT-404 | round-1 nit (fixed r2) | doc | **STAYS FIXED** (kep.yaml unchanged) | contract pass |
| F-ENV-VAR-DIRECTION | round-1 nit | doc | **FIXED at d84004a2** — absolute "cannot" claim gone; positional rule stated; canary inverted into a contract | contract pass |
| F-PROPAGATION-TRIGGER | round-2 minor | — | **FIXED by design change** (immutability); residual nit `:1194` "deliberate workload update or re-render" | reasoning + code (see findings) |
| Q-CERP-V1ALPHA1-NONE-CONVERSION | round-2 question | — | **RESOLVED** — v1alpha1 mirror + envtest round trip (`README:954-970`) | doc read |
| F-STAGE3-LEGACY-VISIBILITY | round-2 consensus **major** | doc | **DOWNGRADED to minor residual** — outcome closed by firing-rule narrowing + author-precedence normalization; the clause itself ("earlier references only", own legacy env omitted) survives verbatim | canary pass + code-path analysis |
| F-SSA-ENV-PREMISE | **NEW minor (round 3)** | doc/unit | **CONFIRMED** — "SSA … keyed by name and rejects duplicates" is false: `roleInstanceTemplate` is `x-kubernetes-preserve-unknown-fields: true`, no map-keyed list in the CRD; contradicted by the same paragraph's "role template's own duplicates pass through" | canary pass + `TestKEP531_Verify_RISTTemplateEnvIsNotMapKeyed` |
| F-MOUNTPATH-MECHANISM | nit (open) | doc | **STILL-PRESENT, worse** — the correct apiserver "must be unique" citation was dropped in the condense; only the wrong kubelet one remains | canary pass |
| F-PODSECURITY-STAGE | nit (open) | doc | **STILL-PRESENT** ("fails at workload creation", `:1011`) | canary pass |
| F-INJECTOR-COUNT-WORDING | nit (open) | doc | **STILL-PRESENT** ("four-injector pass", `:771` + mermaid) | canary pass |
| F-KEPYAML-SEEALSO-REMOVED | nit (open) | — | **STILL-OPEN** — kep.yaml unchanged (19 lines, no links) | `git diff 7a9827bc..d84004a2 -- kep.yaml` empty |
| F-GRADUATION-EVENT-INVENTORY | nit (open) | doc | **STILL-PRESENT** — shadowing event unnamed, absent from Graduation checklist | canary pass |
| F-PROFILE-EVOLUTION-TEST | minor (open, reframed) | doc | **STILL-OPEN** — no disable-flag landing-semantics case | canary pass |
| F-STALE-PR-BODY | minor (open) | — | **STILL-OPEN, worse** — body still describes `patchContainers`/five-root symmetric grammar and cites D6/D9/D10, which the condensed doc no longer defines | PR body read (pr-snapshot) |
| F3 | KEP's factual claims about main's renderer are accurate | unit | **Confirmed** — 9/9 harness tests green on head (incl. 2 new pins) | `results/reverify/unit.out` |

Round-3 code spot-checks against base `73e05db4` (all accurate unless listed
above): the legacy override still appends env unconditionally
(`pkg/discovery/sidecar_builder.go:146-160`); `roleInstanceTemplate` is
opaque in the served RIS CRD
(`config/crd/bases/workloads.x-k8s.io_roleinstancesets.yaml`, no
`x-kubernetes-list-type: map` anywhere in the file); every injector set
includes the common-env injector (RIS leader 4 / RIS worker 3 / LWS worker 2
— `roleinstanceset_reconciler.go:407,426`, `lws_reconciler.go:282`), and
`mergeEnvVars` dedupes the whole existing container env list last-wins
(`pkg/discovery/injector.go:183-216`) — so main's rendered *containers* are
already duplicate-free before apply, while init-container env is never
deduped (`InjectEnv` scans `containers` only, injector.go:135-141);
`deepCopyRoles` exists (`internal/controller/workloads/rolebasedgroupset_controller.go:438,470`);
`StandalonePattern` is inline in `RoleSpec`
(`api/workloads/v1alpha2/rolebasedgroup_types.go:348`), so the CEL rule's
`self.standalonePattern` access is expressible.

## Summary of results — round 2 (head `7a9827bc`)

Run: `bash scripts/re-verify.sh 7a9827bc --layers unit,doc`
(raw output `results/reverify/`, doc table `results/doc-contracts-r2.txt`).

| ID (ledger) | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | issue #531 gaps 1–4 exist on base | unit | Confirmed (round 1; re-run green) | `results/reverify/unit.out` |
| F-ENV-CONFLICT | round-1 blocker: env-name conflict check specified 3–4 inconsistent ways | doc | **FIXED** — one positional rule set; "legacy-only duplicates never error" | contract check pass |
| F-COVERAGE-UPDATESTRATEGY | round-1 major: updateStrategy claimed as answered, never addressed | doc | **FIXED** — Non-Goals + Profile Mutability section; kep.yaml see-also removed | contract check pass |
| F-CAPS-MAXITEMS | round-1 major: invented caps on published lists | doc | **FIXED** — "published lists carry **no new caps**; neither has `maxItems` on main" | contract check pass |
| F-COMPONENT-VOCAB | round-1 minor: component pattern stricter than source vocabulary | doc | **FIXED** — no pattern/length bound beyond `MinLength=1` | contract check pass |
| F-ENABLEMENT-ROLLBACK | round-1 minor: missing Feature Enablement and Rollback section | doc | **FIXED** — section + Upgrade/Downgrade Strategy + drills | contract check pass |
| F-CONVERSION-ROUNDTRIP | round-1 minor: Test Plan lacked conversion round-trip | doc | **FIXED** — "pinned by envtest", in graduation criteria | contract check pass |
| F-UNKNOWN-TARGET-PROSE | round-1 nit: unknown-target semantics unstated | doc | **FIXED** — fail-closed render error stated | contract check pass |
| F-COPILOT-SECRET-CHANNEL | round-1 nit: D9 scope on retained channels | doc | **FIXED** — D9 documents main's retained channels | contract check pass |
| F-COPILOT-VOLUMEMOUNT-EQUALITY | round-1 nit: equality ignored subPath/readOnly | doc | **FIXED** — full semantic equality + partial match = error | contract check pass |
| F-INFERENCE-KIT-404 | round-1 nit: dead see-also link | doc | **FIXED** — see-also block removed from kep.yaml | contract check pass |
| F-MOUNTPATH-MECHANISM | nit (open): "kubelet last-wins" citation beside the correct apiserver one | doc | **STILL-PRESENT** (canary) | canary check pass |
| F-PODSECURITY-STAGE | nit (open): risk table "fails at workload creation" | doc | **STILL-PRESENT** (canary) | canary check pass |
| F-ENV-VAR-DIRECTION | nit (open): absolute `$(VAR)` "cannot" claim | doc | **STILL-PRESENT** (canary) | canary check pass |
| F-INJECTOR-COUNT-WORDING | nit (open): "four-injector pass" overgeneralization | doc | **STILL-PRESENT** (canary) | canary check pass |
| F-STAGE3-CHECK-TIMING | **new minor (round 2)**: stage-3 check-timing clause contradicts the phased order | doc | **PRESENT** (canary) | canary check pass |
| F-PROFILE-EVOLUTION-TEST | minor (open): Test Plan lacks consented profile-evolution case | doc | **STILL-PRESENT** (canary) | canary check pass |
| F3 | KEP's factual claims about main's renderer are accurate | unit | **Confirmed** — 7/7 contract tests green on head | `results/reverify/unit.out` |

Factual spot-checks against base `73e05db4` this round (all accurate):
`convertEngineRuntimes{V1alpha1ToV2,V2ToV1alpha1}` helpers exist
(`api/workloads/v1alpha1/rolebasedgroup_conversion.go:289,470`); the RBG
validating webhook exists
(`api/workloads/v1alpha2/rolebasedgroup_admission.go`, `failurePolicy: Fail`,
and it does read objects — `ValidateScalingAdapterReplicas` at :202); CERP CRD
serves v1alpha1 (deprecated) + v1alpha2 (storage) with no conversion strategy
(None) and no `containerPatches`; standalonePattern renders
`WithName(role.Name)` and KEP-173 defines it as "1 component named after the
role"; `mergeEnvVars`→`dedupeEnvVarsPreserveLast` exist
(`pkg/discovery/injector.go:173-226`); the controller's config volume is
`rbg-cluster-config` (stateful roles only, injector.go:65); the apiserver
performs no capability-name validation (vendored
k8s core validation carries only the Windows-pod and
`allowPrivilegeEscalation=false`+`CAP_SYS_ADMIN` rules — validation.go:4806,8093);
duplicate mountPaths are apiserver-rejected ("must be unique",
validation.go:3046); kubernetes#119569 is real; Kubernetes >= 1.28 is the
repo's supported floor; and — checked against upstream sources — **both
containerd (`internal/cri/opts/spec_opts.go` `WithProcessArgs`) and CRI-O
(`internal/factory/container/container.go` `SpecSetProcessArgs`) ignore the
image ENTRYPOINT *and* CMD when the container sets `command`**, confirming the
KEP's `/args`-creation rule and its entrypoint-merge citation.

Round-1 history (head `12693d02`): see `results/unit-l1.txt`,
`results/f1-doc-contract.txt`, `results/harness-bites-check.txt`, and the
round-1 findings below. The round-1 doc check grepped phrases that the rewrite
removed, so it passed vacuously — the Harness-update that motivated
`check_doc_contracts.sh`.

## Per-finding detail (round 1 — historical)

### F1 [blocker] — the env-name conflict check's scope is specified three inconsistent ways

The KEP states the same check as:

- "the check spans **both passes**" (Role-Side Patches; Application Order —
  profile-side pass 3 + role-side pass 4);
- "the shared env-name conflict check **spanning all three passes**"
  (Compatibility item 3 — legacy merge + 3 + 4);
- "the legacy merge appends once per covering reference — **the same output
  main produces for duplicate references**, with the shared env-name conflict
  check **still guarding the result**" (Duplicate Delivery, mixed case).

Main has **no** conflict check in the legacy merge
(`sidecar_builder.go:144-155` appends env unconditionally per reference;
reproduced by `TestKEP531_Verify_LegacyMergeAppendsPerReferenceWithoutConflictCheck`
— two duplicate references → the override env appears twice) and no check
against the role template's own env
(`...AppendsToRoleDeclaredContainer`). If the implementation follows
Compatibility item 3, a pre-existing RBG with duplicate same-profile
references sharing a legacy override env name (valid and rendering on main)
becomes a **render error after upgrade** — a break of the headline guarantee
"pre-existing references render byte-for-byte identically to main". If it
follows "both passes", Compatibility item 3 and the Duplicate Delivery
sentence are wrong, and there is a silent-duplicate hole between the legacy
merge and the patch passes (legacy-merged env + role-side patch env of the
same name on one profile-declared container → kubelet last-wins, no event).

Demonstrated failure mode: `results/harness-bites-check.txt` — temporarily
implementing the "spanning all three passes" reading in
`sidecar_builder.go` makes exactly the two contract tests go red.

**Fix (doc-only):** state once, precisely, what the check compares: a
patch `add` of env name N errors iff N exists on the target at that point
(from the role template, the legacy merge, or an earlier patch, either
side), while the legacy merge's own appends stay unchecked exactly as on
main; then delete/align the "all three passes" and "still guarding"
wording, and add the duplicate-reference + role-template-pre-set cases to
the Test Plan's byte-for-byte envtest matrix.

### F2 [major] — updateStrategy (issue #531 gap 4) claimed as answered, never addressed

`kep.yaml` see-also comment: the issue is "a defect inventory of the existing
injection (inert injectContainers, unreachable leaderWorker worker template,
unscoped customComponents broadcast, **unconsumed updateStrategy**) that this
KEP answers point by point." The README never mentions `updateStrategy` —
not in Goals, Non-Goals, Future Work, or the Test Plan (grep: zero hits).
The Motivation re-numbers the issue's four gaps into three plus the dormant
`injectContainers`, silently dropping the fourth. A maintainer reading the
kep.yaml believes #531 is fully dispositioned by this KEP.

**Fix (doc-only):** add an explicit Non-Goal ("profile-change rollout /
`spec.updateStrategy` semantics — out of scope; to be aligned with the KEP-455
rollout machinery") and correct the kep.yaml comment.

### F3 [minor] — factual claims about main: verified accurate

Every renderer fact the KEP rests on was verified on base:
leader-only absent-component default under LWP (both RIS and LWS worker
chains omit `sidecarInjector`); `InjectEnv`/`InjectLeaderWorkerSetEnv` run
after the sidecar stage over every container with last-wins dedupe/reorder
(`pod_reconciler.go:104-118`, `injector.go:125-163`); stages 1+2 interleaved
per reference in `sidecar_builder.go`; init-container legacy override silent
no-op; per-reference override skip; append-if-missing precedence;
`injectContainers` inert; CERP cluster-scoped; templateRef prohibited for
LWS. No finding — this is the KEP's strongest aspect. The three contract
tests in the manifest pin these behaviors as the compatibility surface.

### F4 [minor] — present-tense claims of caps that do not exist on main

"Together with `roles=16` and `engineRuntimes=16`, these caps keep the schema
inside the kube-apiserver 1.31 CEL cost budget" (Patch Grammar) and "The
v1alpha1 RoleBasedGroup `roles` list **now carries** MaxItems=16, matching
the v1alpha2 cap" (Compatibility item 6). Neither `Roles` nor
`EngineRuntimes` has any `MaxItems` in either API version today (types +
generated CRDs checked). The doc reads as describing existing state; these
are new tightenings the alpha introduces, and the item-6 compat risk
(rejection of stored >16-role v1alpha1 objects on update) applies equally
to the published v1alpha2 lists the KEP will tighten. Mark them as
introduced-by-this-effort and acknowledge both versions.

### F5 [minor] — `component` pattern stricter than the component-name vocabulary

`component` is validated as a DNS-1123 label (≤63, lowercase). main's
`CustomComponentsPattern` component names (`InstanceComponent.Name`) are
unvalidated strings (types: bare string; CRD: `type: string`, no pattern, no
maxLength). A role that today declares a component named e.g. `Router` or a
70-character name can never be named by a component-scoped reference — the
only new surface that reaches it is unwriteable. Align the pattern with the
declared-name vocabulary (or state the restriction and tighten component-name
validation in the same PR).

### F6 [minor/missingTests] — Test Plan lacks conversion round-trip coverage

RBG is served in v1alpha1+v1alpha2 with a field-by-field conversion
(`rolebasedgroup_conversion.go` copies `EngineRuntime`, incl.
`InjectContainers` at :297/:478); CERP v1alpha1 is still served (deprecated).
The new reference fields and the profile-side `containerPatches` need
v1alpha1 mirrors + conversion updates; the Test Plan's unit/envtest/e2e
matrix has no conversion round-trip case. Add one (write v1alpha2, read
v1alpha1, round-trip, assert no loss of the new fields).

## Live run notes

Not applicable — docs-only PR.

## Proposed fixes (NOT applied to production here)

- **F1**: one precise paragraph defining the conflict check's comparison
  base (see above); align the three contradicting sentences; extend the
  byte-for-byte envtest matrix with the duplicate-reference and
  role-template-pre-set env cases.
- **F2**: Non-Goals entry for `updateStrategy` + kep.yaml comment fix.
- **F4**: mark MaxItems caps as new; acknowledge v1alpha2 tightening risk.
- **F5**: align `component` validation with the component-name vocabulary.
- **F6**: add conversion round-trip tests to the Test Plan.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/kep-531-profile-injection-claude`
(fork `cheyang/rbg`; production code untouched — only additive test files
and this docs tree), so it grafts onto whatever the implemented code is.

1. Get it onto the implemented code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/kep-531-profile-injection-claude
   git checkout <impl-branch>
   git checkout FETCH_HEAD -- docs/verification/kep-531-profile-injection \
     pkg/discovery/sidecar_builder_kep531_verify_test.go \
     pkg/reconciler/pod_reconciler_kep531_verify_test.go
   ```
2. Prereqs: Layer 1 = Go toolchain + vendored deps only.
3. Re-run: `bash docs/verification/kep-531-profile-injection/scripts/re-verify.sh`
   (no sha — resolves the current PR head from `manifest.pr`;
   `--layers unit,doc` runs both).
4. Read results via the polarity table: the **Go tests are contract tests** —
   the KEP implementation must keep them GREEN. Any red = compatibility
   break (F-ENV-CONFLICT's failure scenario) or a regression of a documented
   preserved-verbatim behavior (F3's surface). The **doc CONTRACT checks**
   likewise must stay GREEN (a red = the fixed wording regressed). The **doc
   CANARY checks** pass while the open wording defects are present; a flip to
   red means the author fixed one — invert that check into a contract then.
5. Harness-bites: demonstrated in both directions
   (`results/doc-bites-check.txt`, `results/harness-bites-check.txt`);
   re-run only if the checks are edited.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/kep-531-profile-injection-claude
(https://github.com/cheyang/rbg.git). Background: a review of PR #548 (KEP-531,
docs-only) produced findings F1–F6; a harness pinned main's engine-runtime
injection behavior as the KEP's byte-for-byte compatibility contract. The KEP
implementation has now landed at <ref>. Read
docs/verification/kep-531-profile-injection/README.md ("Continuing after the
fix") and follow it: graft the harness onto the implemented code, re-run the
unit layer (all contract tests must stay green; any red is a compatibility
break), confirm production code contains no harness edits, and report an
observed-vs-expected table. Do not run cluster-wide destructive actions.
```
