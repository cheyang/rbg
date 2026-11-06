# pr546-kep-roleinstance-lifecycle — verification harness

Reproducible evidence for the findings raised while reviewing
[PR #546](https://github.com/sgl-project/rbg/pull/546) (KEP-527: RoleInstance
Lifecycle Hooks — a **documentation-only** PR: `keps/527-roleinstance-lifecycle/{README.md,README.zh-CN.md,kep.yaml}`).

Harness branch: `verify/pr546-kep-roleinstance-lifecycle-claude`, rebased on the
reviewed PR head `fb6c2b36` (round 1 reviewed `6e8419ab`, round 2 `71dd5e87`).
**Production code is touched only by the PR itself** — the branch adds only test
files and this harness on top of the PR head.

Layers run against the code under review (PR head `fb6c2b36`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (Go) | pins the KEP's factual claims about *existing* code (premise + Ready-Gating prerequisite) | `go test ./pkg/inplace/instance/readiness/ ./pkg/inplace/instance/lifecycle/ -run 'TestKep527' -v` |
| 2. Docs | markdownlint (CI config + default), whitespace, kep.yaml see-also resolution, en/zh parity | `bash docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_kep_docs.sh` |
| 2b. Round-3 contracts (**gating**) | pins every round-2 open finding the head claims fixed + substance carry-pins for the round-2 fixes the removal pivot rewrote (required/forbidden text, both languages) | `bash docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_round3_contracts.sh <repo-root>` |
| 2c. Round-2 contracts (reference) | round-2 wording pins — fail on head **by design** (the round-3 removal pivot deleted the coexistence sentences they matched); kept for history, not gating | `bash docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_round2_contracts.sh <repo-root>` |
| 3. Live | **not run this round** (round policy: unit + integration only) | see `verify-manifest.json` `liveNote` |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code /
> PASS when fixed. Bug-canary tests (assert current behavior) PASS now / FLIP to
> red when fixed.

## Problem premise (P0)

Docs-only PR, so the *runtime* premise check does not apply; the premise is
validated as a **claim about the existing codebase** against base (the PR adds
no code, so head == base for all code paths).

| | |
|---|---|
| Claimed symptom | "RBG's existing RIS lifecycle provides coordination through labels and finalizers for `PreDelete` / `InPlaceUpdate`, but this needs to be extended to support instance-level application actions across RI creation, updates, readiness, and deletion" (issue #527) |
| Linked issue | #527, OPEN, created 2026-10-26 (9 days old at review) — still valid |
| Reported component | RIS/RI lifecycle coordination via `labelsHandler` + `finalizersHandler` |
| Patched component | `keps/527-roleinstance-lifecycle/` design document only |
| Component match | Yes — the mechanism the issue names exists exactly as described (`roleinstanceset_types.go:90-103`, `lifecycle_utils.go`) |
| **Verdict** | **Confirmed** (the problem space and the existing-mechanism description are real) |
| Evidence | `results/premise-code-evidence.txt`; pinned green by `TestKep527ExistingCoordinationModel` and `TestKep527ReadinessGatePrerequisite` |

Note on scope: issue #527's expected-capabilities table lists **five** hooks
(`preCreate`, `preUpdate`, `postReady`, `preDelete`, `postDelete`); the KEP
proposes **four** (`preUpdate`, `preRecover`, `postReady`, `preDelete`),
dropping `preCreate`/`postDelete` without an explicit rationale and adding
`preRecover`. See finding F-SCOPE-PRECREATE.

## Round 3 (head fb6c2b36) — delta reviewed: f627bcc2, c4dd645d, fb6c2b36

The author responded to the round-2 debate with a **design pivot**: the legacy
`RoleInstanceSetSpec.lifecycle` protocol is **replaced/removed** (admission
rejects any use of the field) instead of coexisting with mutual exclusion, and a
normative **Wire Names and Constants** section is added
(`rbg.workloads.x-k8s.io/role-instance-hook-state`, the preDelete finalizer
`rbg.workloads.x-k8s.io/role-instance-pre-delete`, feature gate
`RoleInstanceLifecycleHooks`, KEP-502 phase values, Go constant names). Because
the pivot rewrote the wording the round-2 pins matched, those pins fail on head
**by design** — the round-3 contract set re-pins the substance under the new
wording plus carry-pins proving the round-2 fixes survived the rewrite.

Round-2 open findings vs the new head:

- **FIXED** `F-SCOPE-PRECREATE`: Non-Goals now states the preCreate/postDelete
  exclusion rationale (EN+ZH) — preCreate has no established workload/executor
  before Pod creation; postDelete concerns external cleanup after the instance
  is gone and needs a separate protocol.
- **FIXED** `F-PROPAGATED-VS-LEGACY` (by redesign): the legacy field is rejected
  outright and the new entry points (`RoleBasedGroup.spec.roles[].lifecycle`,
  `RoleInstanceSet.spec.roleInstanceTemplate.lifecycle` →
  `RoleInstance.spec.lifecycle`) are distinct field paths, so no dual-protocol
  routing is possible. Residue nit: `F-PROPAGATED-SNAPSHOT-EDIT`.
- **FIXED** `F-FINALIZER-BOOTSTRAP-RACE`: new RIs are created with the reserved
  finalizer; enablement on an existing stable RI adds it *before* exposing the
  config; enablement is rejected/deferred once a `deletionTimestamp` exists
  (EN+ZH + test plan).
- **FIXED** `F-NS-TEARDOWN-BYPASS`: namespace teardown is scoped out of the
  live-engine guarantee with a break-glass pointer (EN+ZH + test plan).
- **FIXED** `F-RBGSET-FOREGROUND-BOUNDARY`: RBGSet is in the foreground-delete
  admission boundary in both sections (EN+ZH + test plan).
- **FIXED** `F-INFLIGHT-ADMISSION-MUST` (should→must, EN+ZH),
  `F-DISABLE-SUPPORTED-CONTRADICTION` (gate named, `disable-supported: true`,
  semantics clarified), `F-NOPOSTREADY-CLEANUP-CLARIFICATION` (alt/else branches
  in both diagrams + prose + test plan for the omitted-postReady completion
  path), `F-ZH-RIS-RI-PARITY` (dissolved — the 同一 RI sentence is gone).
- **STILL OPEN** `F-SEEALSO-502`: `/keps/502-workload-status-model` still does
  not exist in the repo at head or on `origin/main`; PR #528 (KEP-502) is still
  OPEN at round-3 time, so the dangle persists (auto-resolves when #528 merges).
- **CARRIED FIXED (survived the rewrite)**: `F-PREDELETE-LIVENESS`,
  `F-PREDELETE-GATE-LIFETIME`, `F-ESCAPE-HATCH`, `F-DEAD-ENGINE-PREUPDATE`,
  `F-PHASE-STORAGE`, `F-KEPYAML-NOISE`, `F-MARKNOTREADY-NAME`,
  `F-READYBLOCK-NAME`, `F-LIFECYCLE-STATE-PREFIX` (strengthened to the exact
  key), `F-CREATING-REPLACE-FINALIZER` (moot), `F-OLDNEW-ISOLATION`
  (superseded/strengthened by removal), `F-INFLIGHT-CONFIG-ADMISSION` (now
  "must").
- **NEW (round 3)** `F-LEGACY-REMOVAL-COMPAT` (question: removal vs issue #527's
  "remain compatible with existing lifecycle mechanisms" + whether rejection of
  the removed fields applies when the feature gate is off — "the new controller
  neither writes nor interprets them" reads as unconditional, which would break
  existing `inPlaceUpdate` behavior on controller upgrade independent of the
  gate), `F-PROPAGATED-SNAPSHOT-EDIT` (nit: no stated rule for user edits of the
  controller-written template snapshot), `F-ZH-ENGLISH-TERMS` (nit:
  untranslated "source of truth"/"desired snapshot" in zh).

Harness-bites (differential) evidence for the round-3 fixes:
`results/round3-contracts-fb6c2b36.txt` (head: 67 PASS / 0 FAIL, exit 0) vs
`results/round3-contracts-71dd5e87.txt` (previous head: 51 FAIL / 16 PASS) and
`results/round3-contracts-09accf63.txt` (base: 58 FAIL / 9 PASS). A synthetic
bites check (removing the preCreate rationale from a scratch copy of the head)
flips the corresponding pin red and exits 1; the copy was restored.

## Round 2 (head 71dd5e87) — delta reviewed: 589e43cb, a08526d8, 71dd5e87

The author revised the KEP in response to the round-1 debate. The full harness
(`re-verify.sh`) was re-run on the new head: premise pins green, markdownlint
(repo + default config) clean, whitespace clean, en/zh structural parity green,
`F-SEEALSO-502` still reproducing (dangling `/keps/502-workload-status-model`),
and the pre-existing `F-CUSTOMREADY-STATUS-EMPTY` canary still green (bug present,
non-gating).

Round-1 findings vs the new head:

- **FIXED** `F-OLDNEW-ISOLATION` (the round-1 major): the KEP now scopes the
  "sole user-facing definition point" to the new four-hook protocol, states the
  Alpha admission mutual-exclusion rule between the new Role-level lifecycle and
  a legacy user-configured RIS lifecycle, deprecates `inPlaceUpdate`, and requires
  `finalizersHandler` to be empty on the new path (mirrored in zh).
- **FIXED** `F-PREDELETE-LIVENESS`: the barrier is redesigned around a reserved RI
  finalizer with the invariant stated correctly — an owner with a
  `deletionTimestamp` but *without* the foreground finalizer is classified
  **solid** by the K8s garbage collector, so background GC does not collect the
  member Pods (re-verified against `kubernetes/kubernetes@release-1.32`
  `garbagecollector.go` `classifyReferences` / `attemptToDeleteItem`).
  Foreground cascading deletion is now rejected by admission at every affected
  ancestor; direct Pod deletion stays scoped to Recovery.
- **FIXED** `F-CREATING-REPLACE-FINALIZER` (dissolved: no per-Pod finalizers),
  `F-PHASE-STORAGE` (phases persisted in `RoleInstance.status.phase` per
  KEP-502, with a pre-implementable blocker), `F-PREDELETE-GATE-LIFETIME`,
  `F-ESCAPE-HATCH` (break-glass procedure), `F-DEAD-ENGINE-PREUPDATE`,
  `F-INFLIGHT-CONFIG-ADMISSION` (admission rejects in-flight lifecycle edits),
  `F-KEPYAML-NOISE`, `F-MARKNOTREADY-NAME` (`markPodNotReady`), `F-READYBLOCK-NAME`
  (`ready-notify`), `F-LIFECYCLE-STATE-PREFIX` (fully qualified key required).
- **STILL OPEN** `F-SEEALSO-502` (dangling see-also, now more load-bearing since
  the KEP hard-references KEP-502 for the phase model) and `F-SCOPE-PRECREATE`
  (no preCreate/postDelete exclusion rationale in Non-Goals).
- **NEW (round 2)** `F-NS-TEARDOWN-BYPASS` (namespace deletion deletes member
  Pods directly, outside the enumerated supported/rejected paths), 
  `F-PROPAGATED-VS-LEGACY` (no mechanism given for admission to distinguish
  propagated from user-configured `RIS.spec.lifecycle`), and
  `F-FINALIZER-BOOTSTRAP-RACE` (enable-on-existing finalizer bootstrap race).
  All questions-level, doc design gaps.

Harness-bites (differential) evidence for the fixed findings:
`results/round2-contracts.txt` (head `71dd5e87`: 43/43 contract assertions PASS)
vs `results/round2-contracts-prevhead.txt` (previous reviewed head `6e8419ab`:
40 FAIL / 3 PASS). The pins were red before the round-2 delta and green after.

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Existing RIS lifecycle coordination exists as the issue/KEP describes | 1 | Confirmed | `TestKep527ExistingCoordinationModel` PASS; `results/premise-code-evidence.txt` |
| F-SEEALSO-502 | `kep.yaml` `see-also` references `/keps/502-workload-status-model`, which does not exist in the repo | 2 | **Confirmed (reproduced)** | `check_kep_docs.sh` section 4 → `see-also: FAIL`; `results/docchecks.txt` |
| F-KEP-CLAIMS-ACCURATE | KEP's "ReadinessGate prerequisite" claim about the existing helper is accurate | 1 | Confirmed (claim true) | `TestKep527ReadinessGatePrerequisite` PASS |
| F-CUSTOMREADY-STATUS-EMPTY | **Pre-existing** (not this PR): first `RoleInstanceCustomReady` write omits `Status` | 1 | Confirmed (canary green) | `TestKep527CanaryCustomReadyFirstWriteOmitsStatus` PASS = bug present |
| F-PREDELETE-LIVENESS | preDelete live-engine barrier is unachievable on the GC/owner-delete path | — | Not-reproduced at runtime; hypothesis grounded in K8s semantics + KEP's own admission | `liveNote` in manifest; no kubelet/GC layer this round |
| F-ESCAPE-HATCH / F-SCOPE-PRECREATE / F-PHASE-STORAGE / F-OLDNEW-ISOLATION / F-KEPYAML-NOISE / F-MARKNOTREADY-NAME | design/doc-level findings | — | Reasoning only (no runnable layer) | findings document |

## Per-finding detail

- **F-SEEALSO-502** (contract, docs layer): `kep.yaml` lists
  `/keps/502-workload-status-model` under `see-also`. No such KEP exists in the
  repository (issue #502 "[Feature] Improve workload status reporting" is OPEN
  but has no KEP). Harness-bites check performed: temporarily removing the
  entry turns `check_kep_docs.sh` green; restoring it turns red. Fix: reference
  the issue, or drop the entry until that KEP lands.
- **F-PREDELETE-LIVENESS**: the KEP's own caveat ("A Pod finalizer retains the
  Pod API object; it does not guarantee that its containers remain running
  until `preDelete` completes") and open question 3 concede the point; the
  determinate part of the answer is already known — kubelet begins graceful
  termination as soon as a Pod's `deletionTimestamp` is set, regardless of
  finalizers, and GC deletes dependents once the owner has a `deletionTimestamp`
  (which is why the KEP itself mandates Pod finalizers). Therefore on the
  RI-delete path (user `kubectl delete ri`, RIS scale-down), the engine dies
  within `terminationGracePeriodSeconds` while the controller is still waiting
  for `preDelete`, and the "hand off resources that require a live engine" use
  case cannot be delivered by this design on that path. Needs live-layer proof
  (see `liveNote`) or a design revision that scopes the guarantee.
- **F-CUSTOMREADY-STATUS-EMPTY** (canary, pre-existing, non-gating):
  `addNotReadyKey` in `pkg/inplace/instance/readiness/instance_readiness_utils.go`
  creates the `RoleInstanceCustomReady` condition without `Status`, so the first
  write lands with `status: ""`. The CRD marks `status` as required (string, no
  enum), so the API server accepts it, but every consumer comparing against
  `True`/`False` sees a malformed condition. Not introduced by this
  documentation-only PR; file a tracking issue.

## Proposed fixes (NOT applied to production here)

- **F-SEEALSO-502**: point `see-also` at the issue (or drop the entry).
- **F-KEPYAML-NOISE**: delete the stray "Segment is data-driven…" comment;
  either define the feature gate or drop `disable-supported: true`; fill in a
  real author.
- **F-MARKNOTREADY-NAME**: align the proposed field name with the existing
  `markPodNotReady` json name or state the rename explicitly.
- **F-PREDELETE-LIVENESS**: pre-answer open question 3 in the negative and
  either scope `preDelete`'s guarantee to controller-initiated deletion paths,
  or add a real liveness barrier (e.g. an explicit controller-ordered Pod
  drain with a grace-period budget before the RI deletion is allowed to
  propagate).

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr546-kep-roleinstance-lifecycle-claude`
(production code untouched), so it grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg verify/pr546-kep-roleinstance-lifecycle-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/pr546-kep-roleinstance-lifecycle \
     pkg/inplace/instance/readiness/kep527_claims_test.go \
     pkg/inplace/instance/lifecycle/kep527_claims_test.go
   ```
2. Re-run everything with one command (fetches the current PR head itself):
   ```bash
   bash docs/verification/pr546-kep-roleinstance-lifecycle/scripts/re-verify.sh
   ```
   Exit 0 iff all machine-checkable *gating* findings are fixed and the premise
   pins are green.
3. Polarity table for interpreting results after fixes:

   | Test | Now (bug present) | After fix |
   |------|-------------------|-----------|
   | `TestKep527ReadinessGatePrerequisite` (contract) | PASS (claim accurate) | PASS; FAIL means the code changed and the KEP text is stale |
   | `TestKep527ExistingCoordinationModel` (contract) | PASS (premise accurate) | PASS; FAIL means the existing protocol changed shape |
   | `TestKep527CanaryCustomReadyFirstWriteOmitsStatus` (canary) | PASS (bug present) | **FLIPS to FAIL** — invert it and promote to a contract test |
   | `check_kep_docs.sh` see-also (contract) | **FAIL** (dangling ref) | PASS once the reference is fixed |

4. Manual-review findings (`F-LEGACY-REMOVAL-COMPAT`,
   `F-PROPAGATED-SNAPSHOT-EDIT`, `F-ZH-ENGLISH-TERMS`) have no automated
   check — re-read the KEP sections listed in the findings document each round.
   The former manual findings (F-SCOPE-PRECREATE, F-PROPAGATED-VS-LEGACY,
   F-FINALIZER-BOOTSTRAP-RACE, F-NS-TEARDOWN-BYPASS,
   F-RBGSET-FOREGROUND-BOUNDARY) gained round-3 contract pins.
5. Cross-machine notes: `npx` (markdownlint) needs network; without it the doc
   script marks lint NOT-RUN. Go tests need the repo toolchain (go1.27).
   Machine-local state does not travel; everything durable is on this branch.
