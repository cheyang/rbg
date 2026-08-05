# pr400-coordinated-policy-doc — documentation-claim verification

Reproducible evidence for the findings raised while reviewing
[sgl-project/rbg#400](https://github.com/sgl-project/rbg/pull/400)
(head `ab396534`), which adds
`doc/best-practice/{zh,en}/07-configuring-coordinated-policy{,-guide}.md`.

PR #400 is a **documentation-only** PR — it changes no production code. So every
finding here has the shape *"the doc asserts X; the implementation actually does
Y"*. The harness pins Y so the divergence is reproducible and re-checkable after
someone decides which side to change.

Implementation under examination (unmodified by this branch):

- `pkg/coordination/coordinationscaling/scaler.go`
- `internal/controller/workloads/rolebasedgroup_controller.go`
- `api/workloads/v1alpha2/coordinatedpolicy_types.go`

## Hypothesis

The coordinated-policy docs were written from the API's *intent* (and from the
Go doc comments, which carry the same claims) rather than from the implemented
behaviour. If so, the divergences should be mechanical and provable by calling
the scaling/rollout arithmetic directly — no cluster required except to show
that the CRD schema does not catch the one input that breaks reconciliation.

## Layers

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | the real scaling + rollout arithmetic: `NewCoordinationScalerFromPolicy`, `CalculateTargetReplicas`, `getProgressionType`, `canProceedToNextBatch`, `calculateCoordinationUpdatedReplicasBound` | `go test ./pkg/coordination/coordinationscaling/ ./internal/controller/workloads/ -run 'TestVerifyPR400' -count=1 -v` |
| 2. Cluster (F1 only) | real API server admission — does the CRD reject an absolute `maxSkew`? | `KUBECONFIG=... bash scripts/l2-crd-accepts-absolute-maxskew.sh` |

There is no L3. Every finding is deterministic arithmetic or schema shape;
nothing here depends on timing, a real kubelet, or a live rollout.

> **Test polarity.** Contract tests assert intended behaviour: they FAIL on buggy
> code and PASS when fixed. Bug-canaries assert *current* behaviour: they PASS
> now and FLIP TO RED when the behaviour changes.
>
> **Every test on this branch is a bug-canary.** That is deliberate: for each
> finding the "correct" resolution is genuinely open, because the Go doc comments
> and the generated CRD descriptions make the *same* claims as the new docs. A
> maintainer may reasonably fix the docs *or* the implementation. So the harness
> records what the code does today rather than pre-judging the target. See
> "Interpreting a re-run" below.

## Summary of results

All six findings **Confirmed**. Raw output in `results/`.

| ID | Sev | Doc says | Implementation actually does | Proven by | Verdict |
|----|-----|----------|------------------------------|-----------|---------|
| F1 | blocker | `scaling.maxSkew` may be an absolute number, e.g. `2` (zh:158 / en:165) | `parsePercentage` demands a `%` suffix. `maxSkew: 2` → `failed to parse maxSkew: percentage string must end with '%'`, returned up through `Reconcile` as `return ctrl.Result{}, err`, aborting reconciliation of the **entire RBG**. The CRD accepts it (`x-kubernetes-int-or-string: true`), so there is no admission-time warning. | `TestVerifyPR400_F1_ScalingMaxSkewRejectsAbsoluteValue`, `TestVerifyPR400_F1_AbsoluteMaxSkewErrorReachesCaller`, `scripts/l2-crd-accepts-absolute-maxskew.sh` | **Confirmed** |
| F2 | major | coordinated scaling affects only first deployment; "不影响后续的弹性伸缩行为" (zh:86, zh:94 / en:90, en:99) | No first-deployment gate exists. From steady state prefill 5/5 + decode 10/10, raising desired to 20/40 with `maxSkew: "10%"` yields `{prefill:7, decode:14}` — not `{20, 40}`. Same for 4/4 + 2/2 → 8/4, which yields `{prefill:5, decode:3}`. HPA- and ScalingAdapter-driven scale-up is throttled identically to initial rollout. | `TestVerifyPR400_F2_ScalingThrottlesScaleUpFromSteadyState` | **Confirmed** |
| F3 | major | `rollingUpdate.maxSkew: 1` = "两角色更新进度差最多 1 个实例" (guide zh:134/182/196 / en:136/184/198) | `rolebasedgroup_controller.go:1487` uses `intstr.GetScaledValueFromIntOrPercent(&maxSkew, 100, true)` — base is the literal **100**, so integer `N` is byte-identical to `"N%"`. Verified for `1/"1%"`, `10/"10%"`, `25/"25%"`, `100/"100%"` across 9 replica shapes. With the guide's own 4+4 topology, `maxSkew: 1` = 1% of 4 = 0.04 → rounds to a **±0 (lockstep)** window, far stricter than "±1 instance". | `TestVerifyPR400_F3_RollingUpdateMaxSkewIntegerIsPercentNotCount`, `TestVerifyPR400_F3_GuideScenarioMaxSkew1IsLockstepNotOneInstance` | **Confirmed** |
| F4 | major | with prefill 5 / decode 10 / `maxSkew "10%"`, batch 2 goes `{prefill:2, decode:3}` (zh:171 / en:178) | Batch 2 is `{prefill:1, decode:2}`. prefill stays at 1 because its progress (1/5 = 20%) already equals `minProgress(10%) + maxSkew(10%)`, so the `rp.progress >= maxAllowedProgress` branch pins it. Batch 1 (`{1,1}`) *is* correct as documented. Full measured sequence: `{0,0} → {1,1} → {1,2} → {2,4} → {3,5} → {3,6} → {4,7} → {4,8} → {5,9} → {5,10}`. | `TestVerifyPR400_F4_Batch2WalkthroughIsWrong` | **Confirmed** |
| F5 | major | guide's demo creates "每次创建 1 decode 和 2 prefill" per batch (guide zh:103 / en:105) | For the guide's own manifest (prefill 4, decode 2, `maxSkew "25%"`, `progression: OrderReady`) the measured cadence is `{0,0} → {1,1} → {2,1} → {3,2} → {4,2}`. Per-batch deltas are `(+1,+1) (+1,+0) (+1,+1) (+1,+0)`. **No batch adds 1 decode + 2 prefill**; the max prefill delta in any batch is 1. | `TestVerifyPR400_F5_GuideBatchCadenceIsWrong` | **Confirmed** |
| F6 | minor | `progression` defaults to `OrderScheduled` (zh:159 / en:166) | The field has `+optional` + `Enum` but **no `+kubebuilder:default`** — confirmed live: `progression has a schema default? False`. An omitted value is therefore `""`, and `canProceedToNextBatch`'s switch has cases only for `OrderScheduled`/`OrderReady`, so `""` matches neither and **the batch gate is skipped entirely**. Measured, with 1 of 2 prefill replicas unscheduled: `progression: ""` → `{prefill:2, decode:4}` (gate open) vs `OrderScheduled` → `{prefill:2, decode:2}` (gate held). `getProgressionType()`'s default only applies when `Strategy.Scaling` is nil — never when a user wrote a `scaling:` block and omitted `progression`. | `TestVerifyPR400_F6_OmittedProgressionIsNotOrderScheduled` | **Confirmed** |

### Corroboration from the live CRD

`results/l2-crd.txt` dumps the installed schema, which independently backs two
findings — and shows the API's own description repeating the F1 claim:

```
maxSkew = {"anyOf":[{"type":"integer"},{"type":"string"}],
           "description":"... Can be an absolute number (e.g., 5) or a percentage (e.g., \"10%\")",
           "x-kubernetes-int-or-string": true}
maxSkew is x-kubernetes-int-or-string? True      <- F1: nothing stops an absolute value
progression has a schema default? False          <- F6: no default exists
progression enum = ["OrderScheduled","OrderReady"]
```

## Harness-bites check

A canary is only evidence if it *discriminates*. `results/harness-bites.txt`
records temporarily applying the candidate fix to production code and confirming
each canary flips:

| Finding | Temporary fix applied | Result |
|---------|----------------------|--------|
| F1 | teach `parsePercentage` to accept a bare number | canary **flipped to RED** — all 4 absolute-value subtests plus the blast-radius test failed with `CANARY FLIPPED` |
| F3 | change the `maxSkew` base from `100` to the role's own `requestDesired` | canary **flipped to RED** — `maxSkew=10` gave `[0,4]` vs `"10%"` → `[0,1]`; the guide's `maxSkew: 1` window widened from `[0,0]` to `[0,1]`, exactly the documented ±1 instance |

Both patches were reverted with `git checkout --`, and production cleanliness is
asserted at four checkpoints (baseline, after each revert, final). The script
holds an exclusive `flock` and restores production files via an `EXIT` trap, so a
mid-run failure cannot leave the tree dirty.

Note that with the F3 fix, `int_100_equals_100%` correctly *stops* flipping: 100
instances clamps to the 4 available replicas, which is 100% — the two forms
legitimately coincide at that end of the range.

## Proposed fixes (NOT applied on this branch)

- **F1** — pick one and make all three sources agree (doc table, the Go comment
  at `coordinatedpolicy_types.go:86-87`, and the CRD description):
  - *implementation route*: parse via `intstr.GetScaledValueFromIntOrPercent`
    against the role's replica count, as the rollout path already does; or
  - *doc route*: state that `scaling.maxSkew` **must** be a percentage string,
    and add schema validation (a CEL rule or `pattern: '^[0-9]+%$'`) so a bad
    value is rejected at admission instead of silently wedging `Reconcile`.
  Either way, failing to parse `maxSkew` arguably should not abort the whole
  RBG's reconciliation — degrading to the 100% default would be more forgiving.
- **F2** — either drop the "first deployment only" claim and document that
  coordinated scaling throttles *all* replica changes including autoscaling, or
  add a genuine gate (e.g. skip coordination once every role has reached its
  desired count at least once).
- **F3** — if `maxSkew` is meant to be an instance count, scale against the
  role's replicas rather than the constant `100`; otherwise correct the guide to
  say "percentage of desired replicas" and use a `"N%"` string in the example so
  the intent is unambiguous.
- **F4 / F5** — recompute both walkthroughs from the implementation. The
  measured sequences in the table above (and in `results/l1-scaler.txt`) can be
  pasted in directly.
- **F6** — add `+kubebuilder:default=OrderScheduled` to the field (and regenerate
  the CRD) so the documented default is real; and/or give
  `canProceedToNextBatch`'s switch a `default:` branch that behaves like
  `OrderScheduled`, so an empty value cannot silently disable batch gating.

## Rough edges noticed in passing (not findings, not verified in depth)

- `calculateCoordinationUpdatedReplicasBound` does not clamp its upper bound to
  `requestDesired`: at `refUpdated=4, refDesired=4, requestDesired=4,
  maxSkew=25` it returns `[3,5]`, an upper bound above the role's replica count.
  Callers may well clamp downstream; flagged only because `results/l1-controller-f3.txt`
  makes it visible.

## Continuing after a fix (possibly on another machine)

The harness adds only test files and this directory — production code is
untouched — so it grafts onto whatever the fixed code turns out to be.

1. Graft it on:
   ```bash
   git fetch origin verify/pr400-coordinated-policy-doc
   git checkout <fixed-ref>
   git checkout origin/verify/pr400-coordinated-policy-doc -- \
     docs/verification/pr400-coordinated-policy-doc \
     pkg/coordination/coordinationscaling/zz_verify_pr400_doc_claims_test.go \
     internal/controller/workloads/zz_verify_pr400_maxskew_test.go
   ```
2. Prereqs: L1 needs only the Go toolchain. L2 needs a cluster with the
   `coordinatedpolicies.workloads.x-k8s.io` CRD installed and `$KUBECONFIG` set.
3. Re-run:
   ```bash
   go test ./pkg/coordination/coordinationscaling/ ./internal/controller/workloads/ \
     -run 'TestVerifyPR400' -count=1 -v
   KUBECONFIG=... bash docs/verification/pr400-coordinated-policy-doc/scripts/l2-crd-accepts-absolute-maxskew.sh
   ```
   Or drive it via the manifest: `bash scripts/re-verify.sh <fixed-ref> --layers unit`.

### Interpreting a re-run

All six findings are canaries, so **green does not mean fixed** — green means
nothing changed. Per finding:

| Observation | Meaning | Action |
|-------------|---------|--------|
| canary still GREEN | behaviour unchanged. If the docs were corrected, this is the intended end state. | Confirm the doc actually changed; then optionally promote the canary to a contract test so it guards the now-documented behaviour. |
| canary FLIPPED RED | the implementation changed | Read the `CANARY FLIPPED` message — it names the new value. Invert the assertion (promote the doc's claim to expected) and re-label the test `POLARITY: contract`. |
| `CANARY DRIFTED` message | behaviour changed but not to the documented value | Neither side matches; re-baseline against the new arithmetic and re-review. |

Each test carries per-finding guidance in its doc comment; the file headers
explain the inversion procedure.

Finally, re-run the harness-bites check against the pre-fix code to confirm the
tests still discriminate:
```bash
bash /root/bites.sh   # or re-derive from results/harness-bites.txt
```

### Kickoff prompt for a fresh agent

```text
Continue a verification task on branch verify/pr400-coordinated-policy-doc
(remote: origin = https://github.com/cheyang/rbg.git). Background: a review of
https://github.com/sgl-project/rbg/pull/400 — a DOCUMENTATION PR for
CoordinatedPolicy — produced six findings F1..F6, each a "doc claims X,
implementation does Y" divergence. A harness on that branch reproduces all six.
The PR head reviewed last round is recorded in
docs/verification/pr400-coordinated-policy-doc/.last-reviewed.

Read docs/verification/pr400-coordinated-policy-doc/README.md, section
"Continuing after a fix", and follow it: graft the harness onto the current PR
head, re-run Layer 1 (go test -run TestVerifyPR400) and Layer 2 (the L2 script,
with KUBECONFIG set).

CRITICAL: all six tests are BUG-CANARIES, so "all green" means "nothing changed",
NOT "fixed". Use the polarity table in the README to interpret each result, and
invert any canary that flipped. Re-run the harness-bites check to confirm the
tests still discriminate. Report an observed-vs-expected table.

Do not modify production code or the reviewed doc files. Cluster work must stay
on --dry-run=server; run no cluster-wide destructive actions. Then advance
.last-reviewed to the newly reviewed sha.
```

---

# Round 2 addendum — F1 confirmed on a live cluster

**Date:** 2026-08-05. **Reviewed head:** unchanged (`ab396534`) — this round adds
evidence, not a new review of new code.

Round 1 proved F1 (the blocker) at unit level: `parsePercentage` rejects an
absolute `scaling.maxSkew`, and the error propagates out of `Reconcile`. What it
could not show was the user-visible consequence, because the sandbox cluster's
controller was 23 commits stale (`v0.8.0-cea2a47`) and wrote `restartPolicy` as a
bare string while the CRDs required an object, so no RBG could create Pods at all.
After `helm upgrade rbgs deploy/helm/rbgs -n rbg-system` (chart `0.8.0-alpha.3`,
image `rolebasedgroup/rbgs-controller:v0.8.0-47cfe17`) the blast radius is
directly observable.

New script `scripts/l3-absolute-maxskew-bricks-rbg.sh`, output
`results/l3-absolute-maxskew.txt` (exit 0, **CANARY HOLDS**). It scopes everything
to a generated namespace and deletes it on exit.

## The measured blast radius

Applying the doc's own advertised form — `strategy.scaling.maxSkew: 2` — on a
two-role RBG with 2 replicas each:

```
--- 1. apply with maxSkew: 2 (absolute, as documented) ---
rolebasedgroup.workloads.x-k8s.io/c created
coordinatedpolicy.workloads.x-k8s.io/c created
    (accepted by the apiserver: the CRD is x-kubernetes-int-or-string,
     so nothing warns the user here)

--- 2. wait 50s, then observe ---
    RBG ready : False
    pods      : 0
    events    :
      Warning  FailedCalculateScaling  rolebasedgroup/c  Failed to calculate scaling
      targets for c: failed to create coordination scaler: failed to parse maxSkew:
      percentage string must end with '%'

--- 3. change ONLY maxSkew to "100%" and re-observe ---
    RBG ready : True
    pods      : 4
      c-decode-0    1/1   Running   0   29s
      c-decode-1    1/1   Running   0   29s
      c-prefill-0   1/1   Running   0   29s
      c-prefill-1   1/1   Running   0   29s
```

Changing that one value from `2` to `"100%"` is the only difference between zero
Pods and a healthy group. This closes the causal chain and sharpens the severity
in two ways the unit test could not:

1. **The failure is total, not partial.** It is not that coordinated scaling is
   skipped — the RBG never creates a single Pod. Round 1's README said the error
   "aborts the whole RBG reconcile"; this is that statement measured.
2. **The user gets no signal at apply time.** `kubectl apply` succeeds, the
   `CoordinatedPolicy` is stored, and the only clue is a `FailedCalculateScaling`
   event on the RBG. A user following the parameter table has nothing pointing at
   `maxSkew` unless they think to read events.

## Polarity

`scripts/l3-absolute-maxskew-bricks-rbg.sh` is a **canary**: it exits 0 while the
absolute form still bricks the RBG. If the implementation is changed to accept
absolute values (via `intstr.GetScaledValueFromIntOrPercent`, matching
`rollingUpdate.maxSkew`), or if a webhook starts rejecting them at admission, the
script exits 1 and must be inverted — and at that point the doc's parameter table
becomes correct as written rather than needing the fix proposed for F1.

## Unchanged by this round

F2–F11 remain as recorded in round 1, all at unit level. F2 (coordinated scaling
is not limited to first deployment) would be the next most valuable one to take
live, since its consequence — HPA-driven scale-out being throttled without an
obvious cause — is the kind of thing that only looks real to a maintainer when
observed on a cluster. It was not attempted this round.
