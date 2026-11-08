# Verification — PR #548 (KEP-531: Engine Runtime Profile Injection)

Reviewer: Codex (debate Reviewer B). Latest head reviewed: `d84004a29c80b7e284a0e9662dd5d4c3b23a2da9` (round 3; round-1 head `12693d02`, round-2 head `7a9827bc`)
(base `3612013e6f779401831f5947dc124cc4313faddb`). The PR is **docs-only** (one KEP),
so the harness pins the KEP's *premises about main's renderer* — the compatibility
surface the follow-up implementation PRs must preserve — instead of exercising new code.

## P0 — premise verdict: CONFIRMED (with one contradiction)

The KEP's description of main's renderer was verified claim by claim against the base
code and by 16 additive tests (16/16 PASS on the PR head, whose code is identical to
base). Issue #531 (OPEN, 2026-10-30) names the same components the KEP designs against
(`pkg/discovery/sidecar_builder.go`, `pkg/reconciler/pod_reconciler.go`) — component
match OK, and the KEP is `Refs #531`, not an auto-close.

One sentence in the KEP contradicts the code: Compatibility #6 says v1alpha1's new
`roles` MaxItems=16 matches "the v1alpha2 cap" — **no MaxItems exists on `roles` or
`engineRuntimes` in either served version** (see F1). The premise is otherwise accurate,
including the subtle leader-only sidecar default under leaderWorkerPattern.

## Per-claim results (observed vs expected)

| claim | layer | test | expected (KEP) | observed | verdict |
|---|---|---|---|---|---|
| Legacy merge is env/args-only | unit | TestKEP531_LegacyOverrideMergesEnvArgsOnly | command/image/volumeMounts ignored | only Env+Args appended | confirmed |
| Legacy override no-ops for init containers | unit | TestKEP531_LegacyOverrideInitContainerIsNoop | silent no-op | no-op, no error | confirmed |
| Unknown override container skipped (V(1) log) | unit | TestKEP531_LegacyOverrideUnknownContainerSkipped | skipped, render succeeds | skipped | confirmed |
| Append-if-missing, role's container wins | unit | TestKEP531_AppendIfMissingRoleWins | role wins, no dup | role image kept, 1 container | confirmed |
| Duplicate refs: pod-level dedup, legacy appends per ref | unit | TestKEP531_DuplicateReferencesDedupPodLevelLegacyAppendsTwice | 1 container, env x2 | exactly that | confirmed |
| Cross-profile name collision: earlier ref wins silently | unit | TestKEP531_CrossProfileCollisionFirstReferenceWins | first wins | first image kept | confirmed |
| Env stage runs after sidecar, covers injected containers | unit | TestKEP531_InjectEnvCoversProfileInjectedContainer | RBG_* on sidecar | RBG_ROLE_NAME present | confirmed |
| mergeEnvVars last-wins + reorder | unit | TestKEP531_MergeEnvVarsLastWinsAndReorders | [base, batch, lwp, user] | exact order, last wins | confirmed |
| LWP worker skips sidecar stage (both reconcilers) | integration | TestKEP531_S1_WorkerTemplatesSkipSidecarInjection | leader-only default | confirmed in source | confirmed |
| injectContainers inert | integration | TestKEP531_S2_InjectContainersInert | no consumers | zero consumers | confirmed |
| No MaxItems caps on roles/engineRuntimes (either version) | integration | TestKEP531_S3_NoMaxItemsCapsExistOnMain | KEP says v1alpha2 cap exists | **none exists** | **KEP text refuted (F1)** |
| customComponents names unconstrained | integration | TestKEP531_S4_CustomComponentNamesUnconstrained | (KEP silent) | no Pattern on InstanceComponent.Name | confirmed (F3) |
| CERP cluster-scoped | integration | TestKEP531_S5_CERPClusterScoped | cluster scope | marker present | confirmed |
| No CERP watch; updateStrategy unconsumed | integration | TestKEP531_S6_ProfileEditsDoNotPropagate | KEP silent | sole consumer is sidecar_builder | confirmed (F2) |
| templateRef prohibited for LWS | integration | TestKEP531_S7_TemplateRefProhibitedForLWS | prohibited | validation present | confirmed |
| Duplicate mountPath rejected by apiserver | integration | TestKEP531_S8_ApiserverRejectsDuplicateMountPath | KEP says kubelet last-wins | apiserver "must be unique" | **KEP mechanism wrong (F4)** |

Harness-bites: see `results/harness-bites.txt` — four inverted assertions all failed as
expected; production code was never modified (`git diff` vs PR head: harness files only).

## How to run

```bash
go test ./pkg/discovery/  -run 'TestKEP531_' -v   # behavior claims (fake client)
go test ./pkg/reconciler/ -run 'TestKEP531_' -v   # source-structure claims
```

Or re-run everything against the current PR head (auto-fetched via `manifest.pr`):

```bash
bash docs/verification/kep-531-profile-injection-codex/scripts/re-verify.sh
```

## Round 2 — head 7a9827bc (squash-rewrite on the same base; main moved to 73e05db4)

The head was force-pushed: `12693d02..7a9827bc` rewrites the KEP (+1093/-361) —
`patchContainers` → `targetContainers` + per-entry `extraPatches`, side-asymmetric
root allowlist (D11), positional env-duplicate model, and new Profile Mutability /
Upgrade-Downgrade / Feature Enablement sections. Base movement `3612013e..73e05db4`
touches nothing the KEP cites (pkg/discovery, injector chain, RBG/CERP CRD caps all
unchanged on the new main tip — re-verified by grep: zero maxItems on
roles/engineRuntimes at 73e05db4; only deprecation-admission work landed).

### Code-fact harness (16 Go tests)

`bash scripts/re-verify.sh 7a9827bc...` — **16/16 FIXED** (all contract tests green;
raw output in `results/reverify/`). Production code at the head is identical to the
merge-base, so every pinned main-behavior claim still holds.

### Doc-text contracts (new this round): `scripts/check-doc-contracts.sh`

Each check is a contract test over the KEP text at a ref (contract polarity:
PASS = criterion met). Raw outputs: `results/doc-contracts-{head-7a9827bc,
prev-12693d02,base-3612013e}.txt`.

| check | finding | prev 12693d02 | head 7a9827bc |
|---|---|---|---|
| DOC-CONSISTENCY | F-ENV-CONFLICT (r1 blocker) | FAIL | **PASS — fixed** |
| DOC-ISSUE-COVERAGE | F-COVERAGE-UPDATESTRATEGY (r1 major) | FAIL | **PASS — fixed** |
| DOC-CAPS | F-CAPS-MAXITEMS (r1 major) | FAIL | **PASS — fixed** |
| DOC-MAIN-ACCURACY | F-MOUNTPATH/PODSECURITY/INJECTOR-COUNT/ENV-VAR-DIRECTION (r1 nits) | FAIL | **FAIL — all 4 sentences retained** |
| DOC-COMPONENT-VOCAB | F-COMPONENT-VOCAB (r1 minor) | FAIL | **PASS — fixed** |
| DOC-CONVERSION | F-CONVERSION-ROUNDTRIP (r1 missing test) | FAIL | **PASS — fixed** |
| DOC-ENABLEMENT | F-ENABLEMENT-ROLLBACK (r1 minor) | FAIL | **PASS — fixed** |
| DOC-UNKNOWN-TARGET | F-UNKNOWN-TARGET-PROSE (r1 nit) | PASS | PASS |
| DOC-VMOUNT-EQUALITY | F-COPILOT-VOLUMEMOUNT-EQUALITY (r1 nit) | FAIL | **PASS — fixed** |
| DOC-NO-404-LINK | F-INFERENCE-KIT-404 (r1 nit) | FAIL | **PASS — fixed (see-also removed)** |
| DOC-STAGE3-LEGACY | **F-STAGE3-LEGACY-VISIBILITY (r2 major, OPEN)** | FAIL* | **FAIL — open** |
| DOC-PROPAGATION-TRIGGER | **F-PROPAGATION-TRIGGER (r2 minor, OPEN)** | PASS* | **FAIL — open** |

*DOC-STAGE3-LEGACY on prev: the contract targets the new positional model; the prev
doc's failure there is the old contradiction (F-ENV-CONFLICT), tracked separately.
DOC-PROPAGATION-TRIGGER on prev: the inaccurate sentence did not exist yet (vacuous).

### Round-2 findings evidence

- **F-STAGE3-LEGACY-VISIBILITY (major, open).** Application Order: "Each stage-3 pass
  resolves its role-owned checks against the role template and the pod-level content
  of earlier references only". Compatibility #3 promises the legacy merge's role-wins
  override "keeps its exact outcome under the new grammar". The role's OWN
  legacy-merge env on a profile-declared container (stage 2, same reference — the only
  reference allowed to override it) is role-authored but is in neither the template
  snapshot nor earlier references' pod-level content, so per the stated visibility a
  profile-side `/env/-` append of the same name does not error and lands after the
  role's entry — profile wins by position, contradicting both quotes. Harness:
  `TestKEP531_LegacyOverrideMergesEnvArgsOnly` pins the legacy append landing after
  the profile's declared env (role wins today); `TestKEP531_S6_ProfileEditsDoNotPropagate`
  and the D11 text pin that legacy overrides admit profile-declared names only.
- **F-PROPAGATION-TRIGGER (minor, open).** Profile Mutability: "a profile change …
  lands through whatever update path exists, today only deliberate workload updates".
  Refuted in code: `reconcileRoleInstanceSet` re-renders and SSA-applies
  unconditionally every reconcile (`roleinstanceset_reconciler.go` —
  `revisionHashEqual` gates logging only; `utils.PatchObjectApplyConfiguration` always
  runs); the LWS path applies whenever `semanticallyEqualLeaderWorkerSet` (which
  compares the rendered `LeaderWorkerTemplate`) is false. Reconcile triggers include
  RBG spec changes, owned-child spec updates, ScalingAdapter/CoordinatedPolicy/Service
  events, and controller restarts (RBGPredicate CreateFunc admits the initial list).
  Compatibility #2's "one warning event per consent-less reference is emitted" has
  the same unstated trigger (no CERP watch — `TestKEP531_S6_ProfileEditsDoNotPropagate`).

## Round 3 — head d84004a2 (single commit "condense 531 README and address review rounds 2-3"; base unchanged at 73e05db4)

The README was condensed and restructured (1591 -> 1338 lines; kep.yaml untouched).
Biggest design change: **profiles are now immutable while referenced** (CERP
validating webhook warn->reject, in-use finalizer, new controller CERP watch, alpha
escape-hatch flag `--disable-profile-immutability`), and `updateStrategy` is
deprecated in both served versions. This subsumes round-2's F-PROPAGATION-TRIGGER
and Q-CERP-V1ALPHA1-NONE-CONVERSION (a v1alpha1 mirror of `containerPatches` was
added) and the missing-test F-PROFILE-EVOLUTION-TEST (immutability test battery).

### Code-fact harness

`scripts/re-verify.sh` on d84004a2: **16/16 contract tests FIXED** (all green — the
PR is docs-only so main's renderer is unchanged) plus one new pin:
`TestKEP531_S9_RISTemplateSchemalessSoSSACannotRejectDuplicateEnv` — the RIS CRD's
`spec.roleInstanceTemplate` is `x-kubernetes-preserve-unknown-fields: true`
(config/crd/bases/workloads.x-k8s.io_roleinstancesets.yaml:146-149 at 73e05db4), so
SSA has no map-keyed `env` on the one apply path carrying the new fields.

### Doc-text contracts (`scripts/check-doc-contracts.sh`)

String-drift updates (harness-update, substance re-verified by full re-read):
DOC-CONSISTENCY ('legacy-only duplicates never error'), DOC-ISSUE-COVERAGE
('Profile Mutability'), DOC-COMPONENT-VOCAB ('lack of a pattern ... is deliberate').
DOC-PROPAGATION-TRIGGER strengthened (pins the immutability redesign). Three checks
added. Raw outputs: `results/doc-contracts-r3-{73e05db4,7a9827bc,d84004a2}.txt`.

| check | finding | prev 7a9827bc | head d84004a2 |
|---|---|---|---|
| DOC-CONSISTENCY | F-ENV-CONFLICT (r1 blocker) | PASS | PASS — stays fixed |
| DOC-ISSUE-COVERAGE | F-COVERAGE-UPDATESTRATEGY (r1 major) | PASS | PASS — stays fixed |
| DOC-CAPS | F-CAPS-MAXITEMS (r1 major) | PASS | PASS — stays fixed |
| DOC-MAIN-ACCURACY | 4 mechanism-citation nits | FAIL | FAIL — 3 remain; $(VAR) direction fixed |
| DOC-COMPONENT-VOCAB | F-COMPONENT-VOCAB | PASS | PASS |
| DOC-CONVERSION | F-CONVERSION-ROUNDTRIP | PASS | PASS |
| DOC-ENABLEMENT | F-ENABLEMENT-ROLLBACK | PASS | PASS |
| DOC-UNKNOWN-TARGET | F-UNKNOWN-TARGET-PROSE | PASS | PASS |
| DOC-VMOUNT-EQUALITY | F-COPILOT-VOLUMEMOUNT-EQUALITY | PASS | PASS |
| DOC-NO-404-LINK | F-INFERENCE-KIT-404 | PASS | PASS |
| DOC-STAGE3-LEGACY | **F-STAGE3-LEGACY-VISIBILITY (r2 major)** | FAIL | **FAIL — still open** (sentence retained verbatim ~:745-747; new normalization clause :647-655 is ambiguous for exactly this shape) |
| DOC-PROPAGATION-TRIGGER | F-PROPAGATION-TRIGGER (r2 minor) | FAIL | **PASS — fixed by redesign** (immutable-while-referenced) |
| DOC-SSA-ENV-KEYED | **F-SSA-DUPLICATE-ENV-PREMISE (r3 minor, NEW)** | PASS (claim absent) | **FAIL — new false claim** (:652-654 "SSA ... env keyed by name, rejects duplicates" vs schemaless template, S9) |
| DOC-FLAG-FINALIZER | **F-FLAG-FINALIZER-TRANSITION (r3 minor, NEW)** | PASS (flag absent) | **FAIL** — flag "neither adds nor honors" the finalizer; existing finalizers never removed -> stranded |
| DOC-DELETE-RACE | **F-DELETE-FINALIZER-RACE (r3 nit, NEW)** | PASS (no race claim) | **FAIL** — "One race exists and is accepted" omits the delete-vs-finalizer-add window |

Still-open carried nits re-verified in the head text: F-MOUNTPATH-MECHANISM
(:665 'kubelet resolves overlapping mount paths last-wins'), F-PODSECURITY-STAGE
(:1011 'fails at workload creation'), F-INJECTOR-COUNT-WORDING (:771 'four-injector
pass'), F-KEPYAML-SEEALSO-REMOVED (kep.yaml unchanged), F-GRADUATION-EVENT-INVENTORY
(shadowing event still unnamed in the Graduation list). Fixed this round:
F-ENV-VAR-DIRECTION (absolute claim removed), C-COPILOT-GHOST-COMPONENT (plan-time
vocabulary check + story 7 + Test Plan), C-COPILOT-SH-C-ARGS ($0 hazard documented +
test), C-COPILOT-ENV-PRECEDENCE-FLIP (author precedence "whatever index").
C-COPILOT-SSA-MAP-LIST: the disputed premise is now verified FALSE by S9 — and the
KEP newly asserts it (tracked as F-SSA-DUPLICATE-ENV-PREMISE).


## Continuing after KEP revisions / implementation PRs

- All tests are **contract** polarity: they PASS while main's renderer matches the KEP's
  compatibility premises. If an implementation PR intentionally changes renderer behavior
  (e.g. adds worker-side injection by default), the corresponding test flips red — that is
  the signal to update the KEP's compatibility section, not to blindly fix the test.
- The debate findings (F1..Fn) are **doc-text** defects; a KEP text fix is judged by
  re-reading `keps/531-engine-runtime-profile-injection/README.md` at the new head —
  re-verify green does not prove the text changed. `verify-manifest.json`'s
  `_docFindings` maps each F-number to its evidence test.
- Handoff prompt: "Check out branch verify/kep-531-profile-injection-codex of the
  reviewer's fork (https://github.com/cheyang/rbg.git), read
  docs/verification/kep-531-profile-injection-codex/README.md, run scripts/re-verify.sh."

## Round 4 — head 0c51bc0e (positional env redesign; the round-2/3 open set mostly lands)

Two commits over d84004a2 (`947eb2aa` Copilot-round dedupe, `0c51bc0e` round-4/5
findings): the env model is redesigned — author precedence and the final
normalization are **gone**; patches land verbatim and kubelet last-occurrence
decides, with profile-side entries on role-owned names legal only at `/env/0`,
a `Two races` delete-side window named, the escape hatch now *removing* existing
finalizers, DELETE admission added, the RBGSet/uncached-lookup/v1alpha1-CEL
premises folded in, and kep.yaml regaining its issue-#531 see-also.

### Code-fact harness (17 Go tests)

`bash scripts/re-verify.sh 0c51bc0e...` — **17/17 FIXED** (all green at head;
raw output in `results/reverify/`). Production code is still identical to the
merge-base (the diff is `keps/` only), so every pinned main-behavior claim holds.
New-codebase pins re-verified this round: apiserver `ValidateEnv` has **no**
duplicate-name check (vendor validation.go:2686-2710) while duplicate mountPath
is rejected (:3046) — the two platform facts the positional model now rests on —
and v1alpha1 `RoleSpec` has no `standalonePattern` (conversion default at
rolebasedgroup_conversion.go:199-209), matching the doc's new v1alpha1 CEL
predicate.

### Doc-text contracts: 21 pass / 5 fail at head

`scripts/check-doc-contracts.sh` re-pinned for the round-4 wording (the r3
DOC-STAGE3-LEGACY/DOC-FLAG-FINALIZER/DOC-DELETE-RACE pins were Harness-updates —
the sentences they pinned were rewritten; the re-pinned contracts FAIL at prev
d84004a2 and PASS at head, which is also their bite proof). Raw outputs:
`results/doc-contracts-r4-{0c51bc0e,d84004a2,3612013e}.txt`.

| check | finding | prev d84004a2 | head 0c51bc0e |
|---|---|---|---|
| DOC-CONSISTENCY | F-ENV-CONFLICT (r1 blocker) | PASS | PASS |
| DOC-ISSUE-COVERAGE | F-COVERAGE-UPDATESTRATEGY (r1 major) | PASS | PASS |
| DOC-CAPS | F-CAPS-MAXITEMS (r1 major) | PASS | PASS |
| DOC-MAIN-ACCURACY | F-MOUNTPATH-MECHANISM fixed; F-PODSECURITY-STAGE + F-INJECTOR-COUNT-WORDING open (nits) | FAIL | **FAIL** (2 terms remain) |
| DOC-STAGE3-LEGACY | F-STAGE3-LEGACY-VISIBILITY | FAIL | **PASS (fixed)** |
| DOC-SSA-ENV-KEYED | F-SSA-ENV-PREMISE | FAIL | **PASS (fixed — claim and normalization removed)** |
| DOC-ENV-POSITIONAL-MODEL | round-4 model pin (new regression contract) | FAIL | PASS |
| DOC-FLAG-FINALIZER | F-FLAG-FINALIZER-TRANSITION | FAIL | **PASS (fixed — 'neither added nor kept', active removal + transition test)** |
| DOC-DELETE-RACE | F-DELETE-FINALIZER-RACE | FAIL | **PASS (fixed — 'Two races of the same shape')** |
| DOC-COMPAT-CERP-LIFECYCLE | F-CERP-LIFECYCLE-COMPAT-PLACEMENT | FAIL | **PASS (fixed — Compatibility #6 + #2 alpha qualifier)** |
| DOC-GRADUATION-EVENTS | F-GRADUATION-EVENT-INVENTORY | FAIL | **PASS (fixed — EnvCollision + WrapShadowed inventoried)** |
| DOC-KEPYAML-SEEALSO | F-KEPYAML-SEEALSO-REMOVED | FAIL | **PASS (fixed — see-also: issues/531)** |
| DOC-DOWNGRADE-WORDING | F-PROPAGATION-DOWNGRADE-WORDING | FAIL | **PASS (fixed)** |
| DOC-V1ALPHA1-CEL | C-COPILOT-V1ALPHA1-CEL-RULE | FAIL | **PASS (addressed — v1alpha1 predicate specified)** |
| DOC-PROFILE-EVOLUTION-TEST | F-PROFILE-EVOLUTION-TEST | FAIL | **PASS (fixed via the alpha-warning-window landing case)** |
| DOC-CAPABILITY-FORM-REGEX | **F-CAPABILITY-FORM-REGEX (new minor)** | FAIL | **FAIL (open)** |
| DOC-DEPRECATED-PATH-MECHANISM | **F-DEPRECATED-PATH-MECHANISM (new minor)** | PASS (vacuous — r4 wording) | **FAIL (open)** |
| DOC-FINALIZER-REMOVAL-OVERCLAIM | **F-FINALIZER-REMOVAL-OVERCLAIM (new nit)** | PASS (sentence absent) | **FAIL (open)** |
| DOC-WEBHOOK-ROLLBACK | **F-WEBHOOK-ROLLBACK (new minor)** | FAIL | **FAIL (open — predates this round, discovered now)** |

Harness-bites for the new open-finding contracts: `results/doc-bites-r4.txt` —
all five failing checks flip to PASS (26/26 green) on a scratch copy of the head
text with the proposed one-sentence fixes applied; no production code touched.

Also fixed this round without a dedicated check: F-STALE-PR-BODY (the PR
description was refreshed to the positional model + immutability subsystem;
verified read-only via `gh pr view`), C-COPILOT-NORMALIZATION-ORDER (the
normalization it worried about is removed; the $(VAR) consumer rule now
specifies the ordering edge), C-COPILOT-PREPROTECTION-WINDOW (DELETE admission
covers the pre-backfill window; the residual is accepted race #2),
C-COPILOT-RBGSET-SCOPE (zero-replica group templates counted, uncached
lookup), C-COPILOT-UPDATE-ADOPT-RACE (uncached lookup removes the stale-cache
variant; the genuine in-flight race is named and accepted).
