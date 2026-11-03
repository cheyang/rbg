# pr546-kep-roleinstance-lifecycle — verification harness

Reproducible evidence for the findings raised while reviewing
[PR #546](https://github.com/sgl-project/rbg/pull/546) (KEP-527: RoleInstance
Lifecycle Hooks — a **documentation-only** PR: `keps/527-roleinstance-lifecycle/{README.md,README.zh-CN.md,kep.yaml}`).

Harness branch: `verify/pr546-kep-roleinstance-lifecycle-claude`, based on the
reviewed PR head `6e8419ab`. **Production code is untouched** — the diff
contains only additive test files and this harness.

Layers run against the code under review (PR head `6e8419ab`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (Go) | pins the KEP's factual claims about *existing* code (premise + Ready-Gating prerequisite) | `go test ./pkg/inplace/instance/readiness/ ./pkg/inplace/instance/lifecycle/ -run 'TestKep527' -v` |
| 2. Docs | markdownlint (CI config + default), whitespace, kep.yaml see-also resolution, en/zh parity | `bash docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_kep_docs.sh` |
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

4. Manual-review findings (F-PREDELETE-LIVENESS, F-ESCAPE-HATCH,
   F-SCOPE-PRECREATE, F-PHASE-STORAGE, F-OLDNEW-ISOLATION) have no automated
   check — re-read the KEP sections listed in the findings document each round.
5. Cross-machine notes: `npx` (markdownlint) needs network; without it the doc
   script marks lint NOT-RUN. Go tests need the repo toolchain (go1.27).
   Machine-local state does not travel; everything durable is on this branch.
