# kep-531-profile-injection — design-doc verification

Reproducible evidence for the findings raised while reviewing
[PR #548](https://github.com/sgl-project/rbg/pull/548) (KEP-531: Engine Runtime
Profile Injection — **documentation-only**; production code is identical to
base `3612013e`).

Layers run against the **code under review** (`12693d02`, PR head):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `SidecarBuilder`/`PodReconciler` injection behavior that the KEP documents and pins compatibility against | `go test ./pkg/discovery/ ./pkg/reconciler/ -run 'TestKEP531_Verify' -v` |
| 2. Integration | — (skipped: docs-only PR, no runtime change) | — |
| 3. Live | — (not applicable) | — |

> Test polarity: all harness tests are **contract** tests for the KEP's
> byte-for-byte compatibility guarantee — they pass on main and MUST still
> pass after the KEP implementation lands. A red result then is positive
> evidence of a compatibility break.

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

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | issue #531 gaps 1–4 exist on base | 1 | **Confirmed** | `results/unit-l1.txt` + code refs |
| F1 | KEP's env-name conflict check scope is self-contradictory; one reading breaks the byte-for-byte guarantee | 1 | **Confirmed** (textual contradiction; main's unchecked legacy append reproduced; break demonstrated in bites check) | `results/harness-bites-check.txt` |
| F2 | kep.yaml/Motivation claim point-by-point coverage of #531 incl. "unconsumed updateStrategy"; KEP never mentions it | — | **Confirmed** (grep: zero `updateStrategy` occurrences in `keps/531-engine-runtime-profile-injection/`) | text |
| F3 | KEP's factual claims about main's renderer are accurate (leader-only default, legacy merge semantics, init no-op, override skip, append-if-missing) | 1 | **Confirmed** — all claims verified | `results/unit-l1.txt` |
| F4 | "roles=16 / engineRuntimes=16" caps stated in present tense but absent from main | — | **Confirmed** (no MaxItems on either API version or generated CRD) | text |
| F5 | `component` DNS-label pattern stricter than the component-name vocabulary it addresses | — | **Confirmed** (`InstanceComponent.Name` is an unvalidated string in types and CRD) | text |
| F6 | Test Plan lacks conversion round-trip coverage for the new fields | — | **Confirmed** (RBG v1alpha1/v1alpha2 both served, field-by-field conversion exists) | text |

## Per-finding detail

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
   (no sha — resolves the current PR head from `manifest.pr`).
4. Read results via the polarity table: **all tests are contract tests** —
   the KEP implementation must keep them GREEN. Any red = compatibility
   break (F1's failure scenario) or a regression of a documented
   preserved-verbatim behavior (F3's surface).
5. Harness-bites: already demonstrated (see
   `results/harness-bites-check.txt`); re-run only if the tests are edited.

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
