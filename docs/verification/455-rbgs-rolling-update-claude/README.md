# Verification — PR #491 (KEP-455: RBGSet rolling update) — Reviewer A (Claude)

First round, 2026-10-11; re-run 2026-10-12 (added E6 check, PR-head guard, E4 unit canary `TestVerify491E4ControllerOwnedAnnotationWipe`, bite C); **round 2, 2026-10-14** (PR force-pushed: head `719851a6` → `b03a7e67`, a KEP amendment answering round 1; harness rebased onto the new head, E3c inverted after the F2 fix, R2-* contract checks added for every fix, new R2-F9 contract reproduction, bites D and E). Branch `verify/455-rbgs-rolling-update-claude` (fork `cheyang/rbg`).

- PR: https://github.com/sgl-project/rbg/pull/491 — **docs-only**: adds `keps/455-rbgs-rolling-update/` (README + kep.yaml), no code changes. Code at head `b03a7e67` is still byte-identical to merge-base `0821cb5b` for everything outside `keps/`.
- Round-2 delta: `keps/455-rbgs-rolling-update/README.md` +114/−5 (new *Child Rollout Barrier* section, scale-subresource paragraph, 4-key exclusion list, Appendix with five one-sentence answers, test-plan entries).
- Base movement since round 1 (main `0821cb5b` → `375fdfd5`, PRs #484/#488/#494/#495): **no finding-referenced file touched** (`rolebasedgroup_controller.go`, `rolebasedgroupset_controller.go`, `rolebasedgroupset_conversion.go`, `pkg/utils/revision_utils.go`, RBGSet CRD unchanged) — verified by `git diff --stat 0821cb5b origin/main`; round-1 code-path contracts remain valid.
- Live layer: **skipped by debate-round configuration** (unit + integration only).

## Premise (P0)

Issue #455 "[Feature] RBGS-level RollingUpdate implementation" — **OPEN**, requests set-level rolling update for RoleBasedGroupSet, citing #112/#18. Matches the KEP's subject and the only files the PR touches. The KEP also honestly discloses that the propagation fix (whole `groupTemplate.spec` incl. `roleTemplates`) changes the legacy path too — and that claim is real (see E-P0 below). Premise **valid**.

## Observed vs expected

| ID | Claim under test | Layer | Observed | Expected | Verdict |
|----|------------------|-------|----------|----------|---------|
| P0 | Set controller drops `groupTemplate.spec.roleTemplates` on children today (KEP's propagation-gap premise) | unit (`TestVerify491P0PropagationGap`) | `newRBGForSet` and `updateExistingRBGs` copy `Roles` only; child `Spec.RoleTemplates` stays empty | child lacks `roleTemplates` → `templateRef` unresolvable | **confirmed** |
| E1 | `RoleBasedGroupSpec` has exactly `Roles` + `RoleTemplates` (KEP's "blast radius bounded" claim) | unit (`TestVerify491E1SpecBlastRadiusBounded`, reflection) | exactly those two fields | bound holds today | **confirmed** |
| E2 | "RBG publishes `RollingUpdateInProgress` …" reads like existing behavior | script (`E2-no-writer`) | 0 non-test writers in `internal/`/`cmd/`/`pkg/`; condition type only *declared* (api v1alpha2 rolebasedgroup_types.go:580); RBG controller sets only `Ready`/`GangConfigured` | if the KEP means a barrier that exists, ≥1 writer | **refuted as existing behavior** → F2 |
| E3 | Scale subresource interaction with redefined `status.replicas` | script (`E3a/b/c`) | scale subresource served on `.status.replicas` in both API versions + CRD; KEP text mentions scale subresource/HPA **0 times** | design that changes `status.replicas` semantics must cover the surface that reads it | **gap confirmed** → F1 |
| E4 | Controller-owned annotation exclusion is load-bearing | unit + script (`TestVerify491E4ControllerOwnedAnnotationWipe`, `E4a/b`) | RBG controller writes `discovery-config-mode` onto children; `needsUpdate` reports drift from that annotation alone; `updateExistingRBGs`/`syncRBGMetadata` wipes it (annotations → nil); child then looks converged until the RBG controller re-adds it — oscillation | exclusion list must be explicit and shared by hash / comparison / sync | **confirmed (deterministic unit repro)** → F3 |
| E5 | Naming surface | script (`E5`) | role-level enum has legacy `Recreate` spelling + `InPlaceUpdateStrategy` field | set-level `Recreate`/`InPlaceUpdate` coexists with them | **confirmed** → F4 |
| E6 | Revision-hash exclusion of controller-injected template keys | script (`E6`) | v1alpha1→v2 conversion injects `conversion.workloads.x-k8s.io/v1alpha1-coordination` into `groupTemplate.annotations` (rolebasedgroupset_conversion.go:61); KEP's exclusion example names only `discovery-config-mode` | conversion-injected keys must be enumerated as excluded, or a v1alpha1 round-trip of an unchanged template changes revision identity | **gap confirmed** → F6 |

## Test layers

- **L1 unit**: `go test ./internal/controller/workloads/` — green (3.6 s), incl. the two harness tests above.
- **L2 integration (envtest)**: `go test ./test/envtest/testcase/...` with `KUBEBUILDER_ASSETS=~/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64` — green: `rbg` 47 s, `restart_policy` 201 s, `webhook` 32 s. Confirms the PR changes no behavior (docs-only) and the touched component's suites pass at head.

## Harness bites

`results/bite-check.txt` + `results/bite-a-with-propagation-fix.txt`:

- **Bite A**: implementing the KEP's propagation fix in `newRBGForSet` (copy `RoleTemplates`) flips `TestVerify491P0` red — the harness genuinely detects the fact it pins. Code reverted after (`git diff` clean).
- **Bite B**: inserting "scale subresource" into the KEP's Status heading flips E3c's grep 0→1 (FAIL) — the silence check genuinely detects KEP text changes.
- **Bite C** (2026-10-12): mutating `needsTemplateAnnotationUpdate` to skip the controller-owned `discovery-config-mode` key (the exclusion F3 asks for) flips `TestVerify491E4ControllerOwnedAnnotationWipe` red (`results/bite-b-annotation-fix.txt`). Code reverted after; production diff clean.

## Round 2 (2026-10-14, head `b03a7e67`) — observed vs expected

The amendment answers every round-1 MUST/SHOULD. Status per prior finding:

| ID | Round-2 check | Observed | Verdict |
|----|---------------|----------|---------|
| F1 (child barrier) | `R2-F1-barrier-section`, `R2-F1b-ordering-matches-code`, `R2-F1c-api-reader-exists` | New *Child Rollout Barrier* section: producer (RBG controller, uncached `apiReader` reads, per-role workload checks), ordering invariant (status written Step 4 before Step 8 mutates roles), consumer (`groupSetChildReady`, absent-or-False@ObservedGeneration≥Generation). The Step 4/Step 8 claim **maps onto the real controller**: `Step 4: Construct role statuses` (rolebasedgroup_controller.go:221) calls `updateRBGStatus` inside `constructAndUpdateRoleStatuses` (line 689), before `Step 8: Reconcile roles` (line 276); `apiReader` exists (line 121) | **fixed** (design; implementation PR still owes E2's writer) |
| F2 (scale subresource) | `E3c-kep-addresses-scale` (inverted round-1 canary) | New *Scale subresource interaction* paragraph: surge visible in `.status.replicas`, HPA-during-surge declared **not a supported configuration**, scoped to manual/set-aware scaling | **fixed** (scoped out with reason, as asked) |
| F3+F4 (controller-owned keys) | `R2-F3-key-enumeration` | One enumerated 4-key list (`discovery-config-mode` + three `conversion.workloads.x-k8s.io/*` keys) governing revision hash, drift comparison, and metadata sync; `syncRBGMetadata` carries the keys over | **fixed** at design level; the E4 unit canary stays green (code unchanged — docs-only PR), flips when implementation lands |
| F5 (checkpoint storage) | `R2-F5-checkpoint-storage` | Appendix names the annotation `rbg.workloads.x-k8s.io/groupset-observed-replicas` on the RBGSet | **fixed** |
| F6 (numeric revision) | `R2-F6-numeric-informational` | Appendix: field is informational only; name equality decides A→B→A | **fixed** |
| F7 (partition timing) | `R2-F7-partition-timing` | Appendix: resolved against current `spec.replicas` at reconcile time; completed batches not re-evaluated | **fixed** |
| Q1 (maxUnavailable 0) | `R2-Q1-zero-verdict` | Appendix: the webhook rejects it | **fixed** |
| Q2 (downgrade stance) | `R2-Q2-downgrade-stance` | Appendix: downgrade stance written down (legacy path resumes, strategy preserved by webhook) | **fixed** |
| **F9 (NEW)** | `R2-F9-bootstrap-window` (contract, expected-FAIL repro) | Absent-as-ready is accepted for *all* children, including ones the set has just spec-bumped whose RBG controller has not reconciled yet (post-upgrade bootstrap window). The producer invariant does not cover that window: nothing has published the barrier yet on a child the set touched first. Fix = one sentence restricting absence-acceptance to children not yet updated in the current rollout | **open** (minor/question; reproduction at `results/R2-F9-bootstrap-window.txt`; bite D proves the check detects the fix) |
| M1 (test-plan gaps) | diff + residual scan | Entries added for barrier set/clear, bootstrap semantics, scale-subresource behavior, controller-owned annotation survival — the four round-1 gaps. The two Reviewer C wording catches remain (line 434 "old template, not the current one"; the :401 in-place ordering sentence still gates the higher ordinal on its own completion) | **mostly fixed**; two advisory wording nits open |
| F8 / O1 / C4238820879 (nits) | residual-open scan | No vocabulary cross-reference, no Graduation/Release Signoff sections, lowercase "rbg" at line 478 | **open** (nits, non-gating) |

## Round-2 harness bites

- **Bite D** (`results/bite-d-f9-simulated-fix.txt`): appending the one-sentence F9 restriction to the KEP text (temp commit via commit-tree; ref restored) flips `R2-F9` 0→1. The check detects the fix it asks for.
- **Bite E** (`results/bite-e-barrier-section-removed.txt`): stripping the *Child Rollout Barrier* section (round-1 state simulated) makes `R2-F1` fail (no matches). The fix-pinning checks are real regression tests.
- Round-1 bites A/B/C remain recorded in `results/`.

## Re-verify

```bash
bash docs/verification/455-rbgs-rolling-update-claude/scripts/re-verify.sh
```

Resolves head from `origin/pr/491`; prints PASS/FAIL per check; full logs in `results/`.

## Continuing after the fix (polarity)

When the KEP's implementation lands, graft this harness onto the new head (`git checkout <new-head> -- .` is *not* the way: rebase or cherry-pick this branch's harness commit onto the PR head, or fetch `refs/remotes/origin/pr/491` and re-run the script) and read:

- `TestVerify491P0PropagationGap`, `E2`–`E6`, `E1` are **contract checks pinning base facts**: they keep passing while those facts hold. `TestVerify491P0` flips red the moment `roleTemplates` propagation lands — then invert it into the fixed-behavior contract.
- `TestVerify491E4ControllerOwnedAnnotationWipe` is a **bug-canary**: it passes while the annotation wipe exists. When propagation preserves controller-owned keys, it flips red — invert the three assertions (drift=false, annotation survives, convergence without deletion) and promote it to a contract test.
- E3c was a **text canary** in round 1 (passed while the KEP was silent on the scale subresource). The round-2 amendment covered it, the canary flipped (as predicted), and it has been **inverted into a contract check** (`E3c-kep-addresses-scale`) pinning the F2 fix.

## Round 3 (2026-10-23, head `f711893c`) — observed vs expected

Single KEP-only commit (`kep: clarify child rollout acknowledgement and replica-change budgets`, +193/−81 in README.md; kep.yaml untouched; headings/TOC unchanged; code at head still byte-identical to merge-base `0821cb5b` outside `keps/`). Base movement since round 2 (`fd84f3b3` → `a0b11e6a`, PRs #497/#487/#501/#504/#506/#505/#500/#508) touches set-controller orphan adoption/UID filtering (#500) and RBG scaling-adapter ownership (#487/#497) — **no KEP-referenced code fact changed**; all E-checks and R2-* guards re-run green at head. Issue #455 still **OPEN**.

The amendment rewrites the Child Rollout Barrier around a persistent **request/acknowledgement protocol** and answers the round-2 major (N1, filed by Claude as F9):

| ID | Round-3 check | Observed | Verdict |
|----|---------------|----------|---------|
| N1 / F9 (absent-condition exception not generation-gated — round-2 major) | `R3-N1a`…`R3-N1h` + updated `R2-F9` (disjunction) | The set now writes `rbg.workloads.x-k8s.io/rollout-request` (per-child decimal counter) **in the same resource-version-protected Update** as the spec/metadata/revision label; the RBG controller acknowledges via `status.observedRolloutRequest` only after applying workloads and re-verifying every role; `groupSetChildReady` demands request==ack; pending requests exclude the child from `readyReplicas`/`updatedReadyReplicas`; a missing condition can never bypass an issued request; both values persist on the object (restart-safe); A→B→A gets a fresh counter. This is **stronger than the round-2 exemplar clause**: it also closes the metadata-only window (a child write that does not bump `metadata.generation`), which a generation gate alone would *not* have covered — the amendment's own opening paragraph identifies that case | **fixed** (design-level; mechanism pinned by 8 contract checks, all PASS; bite F proves they bite) |
| New design surface: role hashes fold Pod-shaping annotations | `R3-C2`, `R3-C3` | `group-exclusive-topology` / `group-gang-scheduling` constants exist (`api/workloads/constants/annotation.go:25,43`); per-role FNV-32a hashing exists (`pkg/utils/revision_utils.go`) — the "fold in two Pod-shaping annotations" design composes with real machinery | **consistent** with code |
| New design surface: "shared SSA serializer" | `R3-C1` | `utils.PatchObjectApplyConfiguration` (pkg/utils/utils.go:48, field manager `rbg`, force) really is a shared SSA status/spec patch path used by the RBG controller today | **consistent** with code |
| F3/F4 (exclusion list) | `R2-F3` + new `R3-F3b` | List grew to 6 keys (adds `rollout-request`, `v1alpha1-pod-group-policy`); sync semantics refined: child-side values preserved, template annotations applied over them, rollout-request never copied from template | **still fixed** (refinement, not regression: keys stay excluded from hash+drift, so no spec-level churn; value-level convergence is template-authoritative) |
| **F10 (NEW, minor)** | `R3-F10-request-strip-fallback` (doc-gap contract, EXPECTED-FAIL at head) | A replace-style full-object write (kubectl replace, Argo `Replace=true`) on a child RBG can strip the live `rollout-request` annotation; the set keeps no independent record of issued requests (the key is drift-excluded, so no re-issue), and a stripped pending request reopens exactly the N1 window (stale `Ready` + no request → legacy fallback → budget released). Same hazard class as the round-2 F5 checkpoint nit; children are controller-owned objects an operator rarely replace-writes, so **minor, non-gating** | **open** (hardening: one KEP sentence — detection or re-issue of a lost request) |
| **Q3 (NEW, question)** | residual scan + code fact | Compatibility-annotation convergence is explicitly ungated ("independently of revision changes and rollout budgets", :206) while coordination data drives the RBG controller's role-replica application (:590 "Apply coordination scaling target replicas if available") — an ungated write that can change serving capacity | **question** (confirm intended; state the rationale) |
| M1 (test-plan residual) | residual scan | Child-level "Conversion: request annotations and acknowledgements round-trip" entry added + manual-validation paragraph; **set-level** v1alpha1 strategy/status round-trip entry still missing; wording catches unchanged at :494 (in-place ordering) and :530 ("old template, not the current one") | **partially addressed** (advisory) |
| F8 / O1 / C4238820879 (nits) | residual scan | Unchanged: no vocabulary cross-reference, no Graduation/Release Signoff sections, lowercase "rbg" now at :590 | **open** (nits, non-gating) |

### Round-3 harness update

`R2-F9` was remedy-shaped (grep for the round-2 exemplar clause) and missed the actual fix shape — the disclosed round-2 harness note. It is now a **disjunction** (either remedy closes the window) and the mechanism is pinned by `R3-N1a`–`R3-N1h`. `R3-F10` is the reproduction for the new minor finding (flips green when the KEP states a strip fallback/re-issue).

### Round-3 harness bites

- **Bite F** (`results/bite-f-n1-fix-removed.txt`): removing the request-gate sentences from a temp copy of the KEP makes `R3-N1a`, `R3-N1c`, `R3-N1f` fail and returns the `R2-F9` disjunction to EXPECTED-FAIL — the fix-pinning checks bite.
- **Bite G** (`results/bite-g-f10-simulated-fix.txt`): adding one hardening sentence (re-issue on stripped request) to a temp copy flips `R3-F10` 0→1.

### Round-3 layers

- **L1 unit**: `go test ./internal/controller/workloads/` — green (3.6 s full suite).
- **L2 envtest**: `rbg` 49 s, `restart_policy` 201 s, `webhook` 32 s — green at the grafted head.
- **P0-net**: issue #455 OPEN (`results/P0-issue-455.txt`).

## Round 4 (2026-10-29, head `5f14daaf`) — observed vs expected

Single KEP-only commit (`kep: correct the description related to Round 3`, +71/−22 in README.md; code at head still byte-identical to merge-base `0821cb5b` outside `keps/`). Base movement since round 3 (`a0b11e6a` → `a2e797c6` at round freeze; `81971432` now) touches only warmup CRD/rampup (#514/#478/#526), RoleInstance reconciler (#512), and CI/docs — **no KEP-referenced code fact changed**; all prior guards re-run green at the new head (see `results/`).

The amendment answers W1 and the M1 Reviewer-C extension, unbounds `partition` from `replicas`, adds the canonical-payload design (with the orphan-cascade rationale), orphan adoption, live-budget surge sizing, and the requeue scope:

| ID | Round-4 check | Observed | Verdict |
|----|---------------|----------|---------|
| W1 ('admission check' wording overload) | `R4-W1` + `R4-W1b` | 0 remaining "admission check" instances; the runtime floor check is now "the same availability gate" (:312) | **fixed** |
| M1 / Reviewer C extension ('lossless' v1alpha1 round-trip overstated) | `R4-V1`, `R4-V2`, `R4-V3` + code fact `R4-V4` | The round-trip is now declared **not lossless**: a v1alpha1 full-object write drops `groupTemplate.labels`/`annotations` (apart from compatibility keys restored from set annotations), which changes revision identity and rolls every child — stated consistently in the risk row (:97), the scenario table (:289), and the downgrade section (:628-633), with a release-notes ask to write rolling sets through v1alpha2. Code-verified: the v1→v2 converter injects only `annotationV1alpha1Coordination` into `GroupTemplate.Annotations` (rolebasedgroupset_conversion.go:57-63); v1alpha1 has no template ObjectMeta at all | **fixed** (by narrowing, as asked) |
| New: canonical payload design | `R4-C1`–`R4-C3` + **E7 envtest** | The KEP claims `ControllerRevision.data` is byte-wise immutable and a strategic-merge patch re-serializes with sorted keys, so a struct-ordered payload fails validation — which would make `kubectl delete --cascade=orphan` block on the orphan finalizer. **E7 (envtest, real kube-apiserver)**: creating a ControllerRevision with struct-ordered data (`{"zz":…,"mm":{…},"aa":…}`) then applying an SMP to metadata is rejected with `data: Invalid value: {sorted form}: field is immutable` — the error message itself displays the sorted re-serialization, confirming both the re-serialization and the byte-wise check. The sorted (canonical) payload survives the same patch byte-identically (`results/integration-e7-canonical.out`). Code facts: the repo's stateful revision path already stores canonical sorted-key data (`getPatch` round-trips through `map[string]interface{}` before `json.Marshal`, stateful_instance_set_helper.go:118) and `EqualRevision` compares `Data.Raw` byte-wise (revision_utils.go:99) | **confirmed** — the design claim is accurate and the canonicalization is load-bearing |
| New: partition unbounded by replicas | `R4-P1`–`R4-P3` | Field table now says `min(partition, replicas)`, StatefulSet parity; scenario row explains the scale-subresource bypass. Code fact: the validating webhook rule matches `resources=rolebasedgroupsets` only (rolebasedgroupset_admission.go:31) — a `rolebasedgroupsets/scale` write skips admission, so the premise of the scenario row holds | **consistent** with code; one consistency wart found → **F11** |
| New: orphan adoption ("matching the legacy path") | `R4-O1` + code fact `R4-O2` | The scenario row's claim that the legacy path considers children "owned or adoptable" and adopts them is accurate against **main** since #500 (`isOwnedOrAdoptable`, rolebasedgroupset_controller.go:231 on main) — the PR branch's merge-base predates it. The uncached re-read on create collision is new rolling-path design | **consistent** with main |
| New: surge sized from the live budget | `R4-S1`, `R4-S2` | Only serving in-scope groups need cover; headroom = ready base count above the floor; existing surge subtracted. The two worked numbers check out (4/0 → 4 surge; 4/1 → 3), and the failure-below-partition case now cannot zero the batch | **consistent** (internal arithmetic verified) |
| New: requeue scope | `R4-R1` | An unacknowledged request is the barrier's only periodic requeue (2 s); `RollingUpdateInProgress=True` alone requeues nothing; the set requeues only after writing. Watch-driven completion is coherent with the informer design | **consistent** |
| F9 (repair-write request scope) | `R4-Q1` + `R4-F9` (EXPECTED-FAIL) | Generalized to "every budgeted child write" with a three-class enumeration (template update, user-metadata-only, Recreate replicas-only) — an improvement, but drift-repair and the one-shot upgrade rewrite are still neither named as request-advancing nor scoped out | **partially addressed** (still open, minor) |
| **F11 (NEW, minor)** | `R4-F11` (EXPECTED-FAIL) | `expectedUpdatedReplicas = replicas - partition` (:423) can now go **negative**: partition is no longer bounded by replicas, and the autoscaler scenario row makes reaching that state normal (scale `replicas` below an absolute `partition`). The Status table has no clamp; a negative "target batch size" is meaningless for status consumers. Fix = one clause (`max(0, replicas - partition)`) | **open** (minor) |
| F10 (replace-strip fallback) | `R3-F10` (EXPECTED-FAIL) | No stated fallback or re-issue for a replace-write stripping the live rollout-request annotation | **open** (minor) |
| Q3 (unbudgeted compatibility convergence) | residual scan | :206 unchanged; coordination data still drives role replicas unbudgeted (base #526 even fixed the SSA rejection on that path, confirming it is live) | **open** (question) |
| M1 residuals | residual scan | (a) set-level v1alpha1 stash/restore still has no automated test-plan entry (the lossy-drop statement itself is now testable); (b) :532 in-place-ordering wording; (c) :571 "old template, not the current one" | **open** (advisory) |
| F8 / O1 / C4238820879 (nits) | residual scan | Unchanged: no vocabulary cross-reference, no Graduation/Release Signoff sections, lowercase "rbg" at :639 | **open** (nits, non-gating) |

### Round-4 harness additions

- **E7** (`test/envtest/verify491r4/`): the canonical-payload integration test against a real kube-apiserver. E7a is the contract (canonical data survives an SMP byte-identically — what orphan cascading needs); E7b is the mechanism (struct-ordered data is rejected by the SMP validation, error text showing the sorted re-serialization). E7b doubles as E7a's bite check: the same patch path behaves differently exactly when the payload is non-canonical, proving the test exercises the real validation path.
- `R4-*` grep/contract checks pin every round-4 amendment sentence and its code facts (webhook rule vs scale, conversion injection, stateful-path canonicalization, byte-wise equality, main's adoption logic).
- `R4-F9` / `R4-F11` are the reproductions for the two open minors (flip green when the KEP states the repair/one-shot request semantics, resp. the expectedUpdatedReplicas clamp).

### Round-4 layers

- **L1 unit**: `go test ./internal/controller/workloads/` — green.
- **L2 envtest**: repo suites green (`rbg` 49 s, `restart_policy` ~200 s, `webhook` ~31 s) + E7 green (`verify491r4`, 2.6 s).
- **P0-net**: issue #455 OPEN.

### Round-4 harness bites

- E7b is the bite for the E7a contract (same patch path, non-canonical payload → rejected).
- The R4 checks are remedy-shaped on the round-4 text: removing the canonical-payload paragraph / the min(partition, replicas) clause / the "not lossless" declaration from the KEP makes `R4-C1` / `R4-P1` / `R4-V1` fail respectively (grep checks on the PR-head text; observed by construction, same mechanism as bites B/E/F).

### Continuing after the fix (round 5)

`bash docs/verification/455-rbgs-rolling-update-claude/scripts/re-verify.sh` from a graft of this harness onto the new PR head. `R4-F9` and `R4-F11` (plus the round-3 `R3-F10`) flip to PASS when the one-sentence fixes land; everything else is a regression suite for the settled rounds. Polarity: all new checks are **contract** checks except the EXPECTED-FAIL repro rows, which are silence-canaries that flip green on the fix.
