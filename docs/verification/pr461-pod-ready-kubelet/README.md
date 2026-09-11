# pr461-pod-ready-kubelet — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/461
("fix: leave Pod Ready condition to kubelet; controller writes only readiness gates", fixes #460).

Branch: `verify/pr461-pod-ready-kubelet` (based on PR head `a0a4fa39`, production code **untouched** —
the diff to `pr461` is empty; only this harness is added).

Layers run against the **code under review** (PR head `a0a4fa39`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `addNotReadyKey` / `removeNotReadyKey` single-writer contract (gate-only writes) | `go test ./pkg/inplace/pod/readiness/... -run 'TestAddNotReadyKey\|TestRemoveNotReadyKey' -v` |
| 2. Integration | — (not needed; the readiness utils are pure status-transition logic over an in-memory Adapter; L1 fully decides every finding) | — |
| 3. Live | real kubelet stale-republish flap | blocked by pre-existing cluster state — see "Live run notes" |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when fixed.
> There are **no bug-canaries** here — the PR *is* the fix, so every test is a contract test that
> fails on base and passes on the PR head.

## Problem premise (P0)

Answered before the findings, run against the **base** branch (`upstream/main` = `f8b4417e`)
without the patch, because the question is whether the problem exists today.

| | |
|---|---|
| Claimed symptom (issue #460) | "Pod Ready flaps because the controller writes a kubelet-owned condition … kubelet's status manager cache then diverges from the API server. On its next periodic sync kubelet republishes the stale value it has cached, overwriting what the controller wrote." (25–35 ms True→False→True flap, cascading Pod → RI → RIS → RBG.) |
| Linked issue | #460, OPEN, still valid, symptom + component match the PR. |
| Reported component | `pkg/inplace/pod/readiness` (`addNotReadyKey`/`removeNotReadyKey` → `util.UpdatePodReadyCondition`) |
| Patched component | same — PR removes the two `UpdatePodReadyCondition` calls in exactly these functions |
| Component match | **Yes** |
| **Verdict** | **Confirmed** (mechanism, unit layer). Live *symptom*: **Not-reproduced** (environment blocked, not refuted). |
| Evidence | Base branch writes Ready via `UpdatePodReadyCondition` at `pod_readiness_utils.go:65` and `:100` (a second writer of the kubelet-owned condition). Harness-bites: the PR's contract tests **FAIL** on buggy base (controller rewrites Ready) and **PASS** on the PR head. `results/L1-bites-buggy-base-fails.txt`, `results/L1-prhead-pass.txt`. Live flap not triggered — see Live run notes. |

> Refuted and Not-reproduced are not interchangeable. The live flap was not triggered, but the
> mechanism (second-writer) is positively proven, so this is **Confirmed**, not Refuted, and not a
> blocker. Only the live *symptom* portion is Not-reproduced (environment, not absence).

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Base writes kubelet-owned Ready (second writer) → flap | 1 (mech) | **Confirmed** (mechanism); live symptom Not-reproduced | base `:65`/`:100` call `UpdatePodReadyCondition`; tests fail on base / pass on PR — `results/L1-*.txt`; live blocked — `results/L3-blocked-legacy-data.txt` |
| B1 | `addNotReadyKey` writes only the gate, never Ready | 1 | Confirmed (fix) | `TestAddNotReadyKey_LeavesReadyConditionToKubelet` + `TestAddNotReadyKey_GateCondition` pass on PR, fail on base |
| B2 | `removeNotReadyKey` writes only the gate, never Ready | 1 | Confirmed (fix) | `TestRemoveNotReadyKey_LeavesReadyConditionToKubelet` + `TestRemoveNotReadyKey_GateCondition` pass on PR, fail on base |
| B3 | Remaining inplace callers' "safe, spec/label write refreshes kubelet cache" justification | static | Confirmed sound (minor residual) | `onlyUpdateRevision` sets revision-hash label after `updateInstanceReadyCondition`; `Update()` calls `updatePodInPlace` after `updateCondition` — same function. Residual = sub-second status↔spec API gap |
| B4 | KWOK `pod-ready-blocked` + gate-aware `pod-ready` emulate kubelet | static | Favorable (live KWOK not run) | jq syntax matches existing stage; logic emulates derivation; documented missing-gate gap acceptable. Recommend author run stress/e2e |
| B5 | Aggregation latency for Pod-Ready readers (~1s on return-to-rotation) | static | Acceptable (documented cost, not a regression) | `node_binding.go isPodRunningAndReady` reads Pod Ready; flap (which fed RIS readyReplicas 1→0) is removed |

**No blockers, no majors.** The premise is confirmed (mechanism), the fix is proven correct at
the unit layer, and the remaining items are minor validation gaps / acceptable design notes. The
suggested review verdict is therefore **COMMENT**, not REQUEST_CHANGES.

## Per-finding detail

### P0 — premise
- Mechanism: `pkg/inplace/pod/pod_util.go UpdatePodReadyCondition` recomputes `Ready` from
  `ContainersReady` + all readiness gates — i.e. it duplicates exactly what kubelet derives. The
  base branch calls it from `addNotReadyKey` and `removeNotReadyKey`, making the controller a
  second writer of the kubelet-owned `Ready` condition. Kubelet's status-manager cache then holds
  the old value and republishes it on its 10 s sync — the flap.
- Proof (unit / harness-bites): `git checkout upstream/main -- pkg/inplace/pod/readiness/pod_readiness_utils.go`
  (restore buggy prod, keep PR's tests) → `go test ./pkg/inplace/pod/readiness/...` → **FAIL**
  (4 test functions: the controller rewrites `Ready`). `git checkout pr461 -- …` → **PASS**.
  Production diff restored to empty.

### B1 / B2 — the fix
- The PR removes the `util.UpdatePodReadyCondition(newPod)` call from both `addNotReadyKey` and
  `removeNotReadyKey` (and the now-unused `util` import). The controller now writes only the
  readiness gate; `Ready` is left to kubelet.
- The rewritten tests are a real improvement: `recordingAdapter` records writes without
  recomputing Ready (the old `fakeAdapter` recomputed Ready and thus passed both with and without
  the bug). `kubeletManagedReady` carries a distinctive `Reason/Message` so any controller write
  to `Ready` shows up in a `reflect.DeepEqual` even when the `Status` happens to match.
- Bites: confirmed above (fail on base, pass on PR).

### B3 — remaining inplace callers
- Two callers of `UpdatePodReadyCondition` remain: `pkg/inplace/pod/inplaceupdate/inplace_update.go`
  `updateCondition` (when a gate is set False at the start of an update) and
  `pkg/reconciler/roleinstance/inplaceupdate/inplaceupdate.go` `updateInstanceReadyCondition`.
  Both have the existing comment *"We only update the ready condition to False, and let Kubelet
  update it to True"* — they intentionally write `Ready=False` only, to pull a pod out of rotation.
- The PR body claims these are safe because a spec/label write follows in the same reconcile,
  forcing kubelet to re-sync and refresh its cache. Static trace **confirms**:
  `onlyUpdateRevision` (line 240) calls `updateInstanceReadyCondition` (line 241) **then** sets the
  `controller-revision-hash` label and `UpdatePod` in the same function; `Update()` calls
  `updateCondition` then `updatePodInPlace` (spec image patch) in the same function. So the
  stale-republish window is only the sub-second gap between the status and spec API calls — far
  smaller than the readiness-utils case (which had **no** accompanying spec write, so the stale
  value persisted for a full 10 s tick). Not a blocker; the scoping is defensible.

### B4 — KWOK stages
- `test/stress/templates/kwok-stage.yaml`: the `pod-ready` stage gains a `NotIn "False"` gate
  expression so it only promotes to `Ready=True` while no gate is False; a new `pod-ready-blocked`
  stage flips `Ready=False` when any gate is False (status-only patch; strategic-merge preserves
  the controller-owned gates). The jq expressions use the same `[]?`/`select` shape as the
  existing `pod-ready` stage. Logic emulates kubelet's gate-aware derivation.
- Known/accepted fidelity gap (documented in the file header): a missing gate condition is treated
  as satisfied (kubelet treats it as not-ready), but the controller injects both gates at Pod
  creation so the window is transient.
- Validation gap: live KWOK verification (stress suite) was not run. Recommend the author confirm
  the stress/e2e suite is green with the new stages.

### B5 — aggregation latency
- `pkg/reconciler/roleinstance/sync/node_binding.go` `isPodRunningAndReady` reads the `Ready`
  condition. After the fix, `Ready` flips to True ~1 s after a pod returns to rotation (kubelet's
  derivation cadence on a settled/new pod — 17–36 ms / ~992 ms per the PR body's measurements).
  This is the documented cost of the fix, not a regression. The actual flap (which fed RIS
  `readyReplicas` 1→0→1 and RBG `Ready` True→False→True) is **removed**.

## Live run notes

- Cluster: ACK `cn-hongkong`, k8s `v1.36.1-aliyun.1`, 3 nodes. Controller:
  `rbgs-controller-manager` in `rbg-system`, image `rolebasedgroup/rbgs-controller:v0.8.0-0c00546d`
  (v0.8.0 tag carries the same bug at `pod_readiness_utils.go:65`/`:100` — confirmed).
- **The live flap repro was blocked by a pre-existing cluster-state issue, unrelated to this PR.**
  The controller cannot list `*v1alpha2.RoleInstance`:
  `failed to list *v1alpha2.RoleInstance: json: cannot unmarshal string into Go struct field
   RoleInstanceSpec.items.spec.restartPolicy of type v1alpha2.RestartPolicyConfig`.
  5 leftover **test** RoleInstances carry `restartPolicy: "None"` (string form), which v0.8.0's
  struct (expects object `RestartPolicyConfig`) cannot unmarshal. This breaks the RoleInstance
  watch cluster-wide, so the freshly applied `RBG flap` in `pr461-repro` never got a status or a
  Pod.
- Offending leftover RIs: `default/nginx-cluster-backend-{0,1,2}`,
  `default/nginx-cluster-frontend-0`, `pr433-test/test-rbg-worker-0` (all leftover test workloads).
- This is the same legacy-data class noted for prior reviews (see `rbg-ack-cluster-state` memory),
  in the inverse direction (string→object for v0.8.0).
- Per guardrails, these resources were **not** deleted/patched (they are not this session's and
  live in shared namespaces). See the user question below.

## Proposed fixes (NOT applied to production here)
- No production change needed — the PR's fix is correct and complete for the readiness-utils path.
- **B4 (validation gap):** ask the author to confirm the stress/e2e suite passes with the new
  `pod-ready-blocked` / gate-aware `pod-ready` KWOK stages (the only item not statically settled).
- **B3 (optional, minor):** if the team wants zero second-writer, the inplace callers could later
  drop the `Ready=False` write too (gate False alone pulls the pod out of rotation once kubelet
  re-derives) — but that is a separate, larger change and the current scoping is defensible.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr461-pod-ready-kubelet` (production code untouched), so it
grafts onto whatever the fixed code is. `re-verify.sh` auto-discovers the current PR head from
`manifest.pr`.

```bash
git clone <fork> rbg && cd rbg
git fetch origin verify/pr461-pod-ready-kubelet
git checkout verify/pr461-pod-ready-kubelet
bash docs/verification/pr461-pod-ready-kubelet/scripts/re-verify.sh
# no ref → fetches current PR head from manifest.pr; runs the unit layer; prints
# Fixed / Still-broken / Partial per finding. Exit 0 iff all findings fixed.
```

After a round, advance the marker: `echo <head-sha> > docs/verification/pr461-pod-ready-kubelet/.last-reviewed`
then commit + push.

### Polarity table (what goes green / what flips)
All findings are **contract** tests. On the (already-fixed) PR head they PASS. On buggy base they
FAIL (that is the reproduction). There are no canaries to invert.

| Finding | Test(s) | On PR head | On buggy base |
|---------|---------|-----------|---------------|
| B1 | `TestAddNotReadyKey_LeavesReadyConditionToKubelet`, `TestAddNotReadyKey_GateCondition` | PASS | FAIL |
| B2 | `TestRemoveNotReadyKey_LeavesReadyConditionToKubelet`, `TestRemoveNotReadyKey_GateCondition` | PASS | FAIL |
