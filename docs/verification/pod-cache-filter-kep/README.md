# Verification — KEP-284 Pod Cache Filtering (PR #530)

Reviewer: Codex (Reviewer B) · Round: 3 (incremental) · Reviewed head: `d1c331ae`
Base (merge-base): `09accf63` (moved from `4a0765f7` by the round-3 rebase) · Harness: additive only, production code untouched.

## Premise verdict (P0): Confirmed

The PR is a docs-only KEP proposing a four-source union Pod cache plus
asynchronous `managed=true` backfill, answering issue #284 ("[Feature] add pod
cache filter in informer", OPEN). The premise checks out against the base
branch:

- `cmd/rbgs/main.go` `cacheOptions()` filters Service/StatefulSet/Deployment by
  `group-name` EXISTS but has **no Pod entry** — the operator caches every Pod
  in the cluster today (static check P0).
- Issue #284's heap profiles attribute operator memory to irrelevant Pods, and
  record that a naive single-label Pod filter breaks sts/lws/deploy — exactly
  the compatibility trap the KEP's union design addresses. Symptom and
  component match the patch target.
- The KEP's load-bearing compatibility claim — *every supported Pod carries at
  least one admission label before backfill* — holds for all four creation
  paths in the reviewed tree: RBG workload pod templates
  (`GetCommonLabelsFromRole` → `group-name`), RoleInstance component pods
  (`InitComponentPodLabels` → `role-instance-name`), RoleInstanceSet component
  templates (merged `matchLabels` → `group-name`), Warmup pods
  (`workloads.x-k8s.io/warmup-name`). Pinned by unit tests C3/C4.

## Layers

| layer | what | command |
| --- | --- | --- |
| L1 unit | admission-rule selector semantics; label-coverage contracts | `go test ./test/kep284/ -run 'TestAdmissionRuleTable\|TestAdmissionRuleNotExpressibleAsOneSelector\|TestLegacyLabelKeysMatchKEPTable\|TestWorkloadPodTemplatesCarryGroupName\|TestRoleInstancePodsCarryRoleInstanceName' -count=1` |
| L2 integration | serial four-source filtered List union against a real apiserver | `go test ./test/kep284/ -run TestAdmissionRuleAgainstRealAPIServer -count=1 -v` |
| static | premise/pointer/arithmetic checks | `bash docs/verification/pod-cache-filter-kep/scripts/kep284-static-checks.sh` |
| L3 live | **skipped** — docs-only PR, no production behavior to run | — |

L2 uses the repo's envtest assets `bin/k8s/1.31.0-linux-amd64` — the same
kube-apiserver version (v1.31.0) the KEP's prototype was validated against. It
skips cleanly when the binaries are absent (set `KUBEBUILDER_ASSETS` to
override).

## Observed vs expected

| claim | expected | observed | verdict |
| --- | --- | --- | --- |
| P0 operator caches all Pods today | no Pod entry in cacheOptions | confirmed, `kep284-static-checks.sh` | Confirmed |
| C1 admission table (4 rows + managed=false edge cases) | union admits exactly the supported shapes | 10-case table passes; managed=false+legacy admitted, managed=false/1/True alone rejected | Confirmed |
| C1-inexpressibility | no single ANDed selector expresses the OR union → 4 sources needed | pairwise AND of any two KEP sources never admits both disjoint shapes | Confirmed |
| C1i serial four-source union vs real apiserver | union = {5 supported pods}, per-source sets exact | exact match on envtest 1.31.0 | Confirmed |
| C3 label keys match code | constants equal KEP table strings | GroupNameLabelKey / RoleInstanceNameLabelKey / LabelWarmupName all match | Confirmed |
| C4 creation paths carry ≥1 admission label | group-name / role-instance-name / warmup-name present | both exported label builders carry their key for every template type | Confirmed |
| C2 `getExistingPod` is the cache-miss→create path | exists, cached Get, called from createPods | `instance_scale.go:267,345` | Confirmed |
| C5 `managed` label is new | no existing constant | no occurrences outside the harness | Confirmed |
| D2 performance numbers | internally consistent | all 8 percentage/ratio claims recompute within tolerance; README sample values all present in performance.md | Confirmed |
| client-go pager claim | "pager accumulates all pages into one list" | `vendor/k8s.io/client-go/tools/pager/pager.go` `list()` appends every page into one `metainternalversion.List` before returning | Confirmed |

## Design gaps handed to the review (not runtime bugs — KEP refinement asks)

- **F1** — Tombstone→re-admission named GET (KEP cache-implementation item 5):
  every same-name Pod replacement (the normal STS/LWS/RoleInstance rolling path)
  pays a DELETE-confirm GET **and** a re-admit GET, both synchronous on the event
  path; the Scalability section budgets boundary reads only for selector
  *exits*; and the re-admit direction has no specified error semantics (a
  transient GET failure with no retry/requeue leaves a genuinely re-created Pod
  invisible until the next event or relist — note the manager sets no
  SyncPeriod today). Also: given the KEP's own barrier rule (tombstone RV ≥
  every applied RV) plus UID incarnation tracking and monotonic numeric RVs, an
  event newer than the barrier is definitionally the post-deletion incarnation,
  so the GET appears redundant for correctness — justify the case it covers,
  price it, or drop it.
- **F2** — Per-create confirmation reads missing from the cost accounting:
  `getExistingPod` runs a cached Get before every Pod create; under the union
  cache each create of a not-yet-existing Pod is a cache miss that item 1
  requires confirming with a rate-limited named APIReader GET, so boundary
  reads scale with Pod *creation* rate, not just selector exits. The
  rate-limiter sizing should include the create path, or the KEP's own alpha
  alternative ("handle `AlreadyExists` explicitly") should be preferred.


## Round-3 observed vs expected (head `d1c331ae`, delta `f8d67fa7..d1c331ae`)

The round-3 revision scopes the `NewInformer` wrapper to `*corev1.Pod` (plus a
new envtest test `TestAPITransformInformerScope`), pins the isolated envtest
control plane, qualifies the `spec.nodeName` audit sentence, averages
even-sized median groups, makes the sync budget derivable, and adds GOMEMLIMIT
guidance. Re-verify of every carried contract plus new round-3 checks:

| claim | expected | observed | verdict |
| --- | --- | --- | --- |
| P0 premise at the rebased tree | cacheOptions still has no Pod entry | PASS (cmd/rbgs/main.go identical across the base move) | Confirmed |
| C1/C1i/C3/C4 admission + label contracts | green | green at d1c331ae (unit + envtest integration) | Confirmed |
| D3 marker-exemption drift contracts | red by design (implementation PR's gate) | red at d1c331ae, same failure text | Red-by-design |
| D4 group-name-drift guard / D5 TransformingStore pin | green | green at d1c331ae | Confirmed |
| D2 perf-number consistency | green | green against the revised README | Confirmed |
| R2-F1 cluster escape | fixed | FIXED: `export USE_EXISTING_CLUSTER=false` in transform-campaign.sh AND `UseExistingCluster: ptr.To(false)` in TestMain; empirical: suite green with `USE_EXISTING_CLUSTER=true` + bogus KUBECONFIG (results/round3-r2f1-empirical.txt) | Fixed |
| R2-F2 / R2-F2-ci no CI compile of tagged package | still open | STILL-OPEN: `git grep podcacheunion -- Makefile .github/` empty at d1c331ae | Open |
| R2-F3 nodeName audit claim | fixed | FIXED: qualified sentence names RecordNodeBindings; the six sites still all scoped (line 1431→1429 shift from the base move), no field selector anywhere | Fixed |
| R2-F4 medianRange upper-middle | fixed | FIXED: averages even groups; author's TestMedianRange green; canary polarity inverted to a contract check on my branch | Fixed |
| N1 PR-body placeholder/stale paragraph | still open | STILL-OPEN: `gh pr view 530` shows literal `fixes #XXXX` and the round-1 union-design opening paragraph | Open |
| N2 underivable 1.14s sync sample | fixed | FIXED: README quotes the tabulated 344–807 ms envelope with a performance.md anchor (recomputed: min 344 macOS off stream+wl, max 807 macOS on stream+wl); heading slug verified | Fixed |
| N3 GOMEMLIMIT guidance | fixed | FIXED: soft GOMEMLIMIT below container limit, validate startup/relist peaks | Fixed |
| R3-scope (new) | wrapper wraps only Pods; others get the default constructor | Confirmed statically (vendored informers.go:65 default IS NewSharedIndexInformer; SetTransform/SetWatchErrorHandler applied after either way) and end-to-end: author's TestAPITransformInformerScope green in watch+relist modes | Confirmed |
| Author's full tagged suite | green | `go test -mod=vendor -tags=podcacheunion ./test/podcacheunion -count=1 -race` exit 0 in both WatchListClient configurations (results/round3-author-suite-gate{off,on}.txt) | Confirmed |

Harness-bites for the new static checks: all round-3 checks (R2-F1, R2-F3,
R2-F4, N2, N3, R3-scope) are RED on the previous head `f8d67fa7` and R2-F2 is
red on both heads (results/round3-static-bites-prevhead.txt). re-verify.sh now
supports `kind: script` layers (PASS:/FAIL: lines), so the static layer reports
per-finding verdicts; full sweep: results/reverify/.

## Continuing after revisions

```bash
git fetch https://github.com/cheyang/rbg.git verify/pod-cache-filter-kep-codex
git checkout verify/pod-cache-filter-kep-codex
bash docs/verification/pod-cache-filter-kep/scripts/re-verify.sh   # resolves current PR head from manifest.pr
```

All tests are CONTRACT polarity: green = claim holds. If a future PR renames a
label key or changes a pod-label builder, the C3/C4 contracts fail and the KEP
admission rule must be updated in the same change. Re-verification runs the
unit + integration layers; the static layer runs directly.

Kickoff prompt for a fresh agent: "Check out
https://github.com/cheyang/rbg.git branch verify/pod-cache-filter-kep-codex,
read docs/verification/pod-cache-filter-kep/README.md, run the three layer
commands, then re-verify the KEP-284 claims against the current head of
sgl-project/rbg PR #530."

---

# Round 2 — incremental, head `f8d67fa` (base moved `d162208e` → `4a0765f7`, PR rebased)

The author replaced the design wholesale: the four-source union + merge layer +
in-leader backfill is now the **rejected** alternative; the selected design is a
single informer with a `cache.ByObject` **Transform** stub plus an early
watch-stream wrapper, behind `--pod-cache-scope=all|filtered` (alpha default
`all`), with marker backfill moved to an admin CLI (`kubectl rbg migrate
pod-labels`) run by a Helm pre-upgrade hook. The PR also ships the previously
local-only prototype as `test/podcacheunion` (build tag `podcacheunion`,
excluded from ordinary builds and CI).

## Round-2 observed vs expected

| claim / finding | observed | verdict |
| --- | --- | --- |
| A-F1 (no-roll guarantee unimplementable) | KEP now specifies: marker never in templates + key-scoped `FilterSystemLabels` exemption + named contract tests + upgrade e2e. Drift contract tests RED by design until the implementation PR (bites-verified: one-line exemption flips them green, reverted) | **fixed** at KEP level; D3 guards the implementation |
| A-F3 (transform-stub alternative unweighed) | transform design adopted; union + disjoint partition analyzed in Alternatives with rationale | **fixed** |
| A-F4 (overlap amplification / disjoint partition) | union rejected; disjoint partition rejected with it; maximum-overlap caveat recorded | **fixed** |
| A-F5 (label-stripped owned Pod lost to owner-index List) | 5th admission condition: any ownerReference into `workloads.x-k8s.io` keeps the Pod whole; KEP names `getOwnedPods`; integration test planned; `TestAPITransformProductionReadPaths` exercises the production owner-index read | **fixed** |
| A-F6 (perf evidence not reproducible) | harness published, base pinned to public `4a0765f7`; reviewer reproduced: author's `-race` suite passes on Linux amd64 in both gate configs; one-shape smoke campaign reproduces −85.6% retained heap (claimed −86%) and the streaming wrapper peak halving | **fixed** |
| A-F2 (serial 4-source startup vs 120s timeout) | union gone: one informer; List startup is parity with today, streaming opt-in; sync budget section names the cache sync timeout; target-scale cgroup validation is a beta criterion | **fixed** by redesign |
| B-F1 (tombstone re-admit GET semantics) | merge layer and boundary GETs are gone | **fixed** (dissolved) |
| B-F2 (per-create confirmation reads) | no boundary reads; stubs keep names so `getExistingPod` stays cached | **fixed** (dissolved) |
| A-Q1 (backfill location) | decided: admin CLI + Helm pre-upgrade hook, `check` mode as completion criterion | **fixed** |
| A-Q2 (no kill switch) | `--pod-cache-scope` flag, alpha default `all` | **fixed** |
| B-F3 (kep.yaml disable-supported) | cross-referencing comment added; `metrics:` block matches the template | **fixed** |
| N1 (title / `fixes #XXXX`) | kep.yaml title aligned with README H1; PR body still carries the literal `fixes #XXXX` placeholder (verified via gh at f8d67fa) | **partially open** (nit) |
| A-T1/A-T2 (test-plan gaps) | drift contract tests and the label-stripped owned-Pod integration case are named in the KEP test plan | **fixed** |

## Round-2 mechanism verification (the load-bearing new claim)

KEP item 6: with streaming lists, the informer transform runs too late to
shrink the reflector's temporary store, hence the watch-stream wrapper.
Verified at three levels (results/round2-probes.txt,
results/round2-author-suite-and-diag.txt):

1. **Source**: `TransformingStore` (vendor client-go v0.34.2 reflector.go:82-85)
   embeds the **full** `Store` interface; `*DeltaFIFO`/`*RealFIFO` implement
   only `ReflectorStore` (reflector.go:59-78), so
   `r.store.(TransformingStore)` fails for informer FIFOs.
2. **Assertion**: both FIFO types fail the runtime type assertion
   (`TestFIFOsDoNotImplementTransformingStore`, GREEN — it flips red when a
   future client-go makes the wrapper obsolete).
3. **End-to-end**: the author's `TestAPITransformSyncHeapTimeline` reproduced
   on Linux amd64 — store-only streaming transforms all 1,048 Pods at Replace
   (peak HeapAlloc 71.3 MiB); the wrapper transforms from 2ms (peak 18.3 MiB).
   Reviewer probes at the pure client-go and direct-reflector levels confirm
   the same late-transform behavior with streaming proven active.

## Round-2 harness layout

- `test/kep284/drift_contract_test.go` — D3/D4 (marker exemption + scope guard)
- `pkg/reconciler/zz_kep284_drift_contract_test.go` — D3's in-package half
  (`podTemplateSpecEqual` is unexported)
- `test/kep284/transformingstore_contract_test.go` — D5 mechanism pin
- `scripts/check-perf-numbers.py` — rewritten for the transform revision; all
  quoted percentages/ratios recompute from the published tables (round2-static.txt)
- Round-1 admission/premise contracts (C1/C1i/C3/C4/P0s) still pass unchanged.

Default sweep: `bash docs/verification/pod-cache-filter-kep/scripts/re-verify.sh`
(unit+integration). Drift suite (red by design until the implementation PR):
`... re-verify.sh --layers unitdrift`.
