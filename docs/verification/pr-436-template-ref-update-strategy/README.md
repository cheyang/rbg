# Verification — PR #436 (template ref & update strategy validation)

PR: https://github.com/sgl-project/rbg/pull/436
Head: `3ec86313` (fix-template-ref-update-strategy) · Base: `a747e804` (main) · Reviewed: 2026-09-07

Layers: **unit** (`go test`) · **envtest** (real apiserver+etcd, PR-head controller in-process) · **live-kubectl** (real ACK cluster, CRDs applied/restored via kubectl).

## Observed vs expected

| ID | Finding | Severity | Layer | Expected | Observed | Verdict |
|----|---------|----------|-------|----------|-----------|---------|
| F1 | Enum vs default-omitted `type` create path | — | live-kubectl | omitting `type` still admitted (relies on runtime default) | A1: RBG omit `rollingUpdate.type` → admitted, stored empty | **safe** |
| F2 | Upgrade hazard: stored invalid `type` wedges updates | major→ | live-kubectl | pre-existing bogus value blocks updates | T4 non-touching patch ✔, T5 re-apply same value ✔, T6 change-to-different-invalid ✘, T7 invalid→valid ✔ | **disproven** |
| F3 | Intended enum rejections at admission | — | live+envtest | invalid `type` rejected at create/update | A3/T1/T3 rejected w/ enum msg; envtest 2 cases PASS | **pass** |
| F4 | templateRef-without-patch → Ready | major→ | envtest+live | RBG reaches Ready | envtest `Should allow v1alpha2 templateRef without patch` PASS; live deployed controller already lacks the check → Ready=True | **pass** |
| F4-side | explicit `type:""` now rejected | minor | live-kubectl | empty string rejected by enum | A4 rejected; use omission (A1) instead | **minor (ok)** |
| F5 | v1alpha1 still requires patch | minor | source | alignment claim covers v1alpha1 | v1alpha1 validation.go:134 still rejects patch-less templateRef | **open** |
| F6 | no e2e; envtest unverified by author | minor | unit+envtest | tests run green | `go test` ok; envtest 3/3 PASS | **closed** |

## Verdict
**COMMENT** — the PR's CRD enum validation works as intended (F3), does not regress the default-omitted path (F1) or existing data (F2 disproven), and the templateRef relaxation is correct (F4). One open consistency question (F5: v1alpha1 left strict) and a minor side-effect (F4-side: explicit `""` rejected). No blocker/major survives verification → not a request-changes.

## Evidence log

### Live cluster (kubectl, ACK cn-hongkong) — CRDs applied from PR `deploy/kubectl/manifests.yaml`
- **A1** omit type → `rolebasedgroup/rbg-omit-type created`; stored `rollingUpdate.type` empty.
- **A2** valid `InPlaceIfPossible` → created.
- **A3** `type: NotARealStrategy` → `invalid: spec.roles[0].rolloutStrategy.rollingUpdate.type: Unsupported value: "NotARealStrategy": supported values: "RecreatePod", "InPlaceIfPossible", "InPlaceOnly"`.
- **A4** `type: ""` → rejected (empty not in enum).
- **T1** fresh RIS `type: DefinitelyBogusStrategy` → rejected.
- **T2** RIS `type: InPlaceOnly` → created.
- **T3** patch valid RIS → `BogusViaUpdate` → rejected.
- **T4** non-touching patch (replicas) on stored-bogus RIS → `patched` (succeeds).
- **T5** full `kubectl apply` re-sending same stored bogus value → `configured` (succeeds).
- **T6** patch stored-bogus → `AnotherBogus` → rejected.
- **T7** patch stored-bogus → `InPlaceOnly` (invalid→valid) → `patched` (self-heal works).

### Unit
`go test ./api/workloads/v1alpha2 ./pkg/reconciler -count=1` → `ok` (both packages).

### Envtest (PR-head controller in-process; real apiserver/etcd 1.31.0)
`Ran 3 of 11 Specs … SUCCESS! -- 3 Passed | 0 Failed`:
- Should allow v1alpha2 templateRef without patch — PASS (3.96s)
- Should reject v1alpha2 role rolloutStrategy with invalid update type — PASS (3.08s)
- Should reject RoleInstanceSet with invalid update strategy type — PASS (3.07s)

## Cleanup
Test namespace `pr436-verify` deleted; all three CRDs restored to pre-PR (no-enum) state via server-side apply of backed-up definitions. No leftover test objects. Production controller image untouched (deployed controller already lacked the removed check).
