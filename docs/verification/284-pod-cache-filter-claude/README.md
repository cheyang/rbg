# KEP-284 (PR #530) — verification harness — Reviewer A (Claude)

PR: https://github.com/sgl-project/rbg/pull/530 (docs-only: `keps/284-pod-cache-filter/`)
PR head reviewed: `139b91cdad4e917e2b27949cc3d4ca4a340b4280`
Base: `7894133d5b59d44f2a19dcf6b75c531996285bcf`

The PR adds a KEP; it changes no runtime code. The harness therefore verifies, at
the unit layer, the KEP's claims **that are decidable against the current codebase**:

1. the premise (the problem exists on the base branch),
2. the no-rolling-update guarantee for the proposed `rbg.workloads.x-k8s.io/managed`
   template marker (it does not hold against the current drift detection),
3. the label-selector semantics the four-watch design rests on (including the
   feasibility of the disjoint-partition alternative).

## Premise (P0): CONFIRMED

Claim (issue #284, quoted): "the reason is that irrelevant `Pods` consume most of
the memory" — the operator caches all Pods.

Evidence: `TestCacheOptionsConstrainsPodCache` (cmd/rbgs) fails on the base code —
`cacheOptions()` attaches the group-name label selector to Service, StatefulSet and
Deployment only; there is no `*corev1.Pod` entry, so the Pod informer runs unfiltered
and its retained memory scales with total cluster Pods. Issue #284 is OPEN, its
symptom (controller RSS dominated by unrelated Pods) and component (the operator's
informer cache) match what the KEP designs. Confirmed.

## Observed vs expected

| # | claim / finding | test | polarity | observed | expected | verdict |
| --- | --- | --- | --- | --- | --- | --- |
| P0 | operator's Pod cache is unfiltered on base | `TestCacheOptionsConstrainsPodCache` | contract | FAIL (no Pod ByObject entry) | would pass once a Pod filter exists | premise CONFIRMED (red is the reproduction) |
| F1 | adding `rbg.workloads.x-k8s.io/managed` to a workload pod template must not be label drift (KEP: "do not trigger a template rolling update") | `TestFilterSystemLabelsIgnoresManagedMarker` | contract | FAIL — `FilterSystemLabels` (pkg/utils/utils.go:194) keeps the key | marker ignored like `app.kubernetes.io/*` | F1 CONFIRMED |
| F1 | templates differing only by the managed marker must compare equal | `TestPodTemplateSpecEqualIgnoresManagedMarker` | contract | FAIL — `podTemplateSpecEqual` reports "label not equal" | equal (no workload patch, no Pod roll) | F1 CONFIRMED |
| mechanics | no single label selector covers the four admission categories (the KEP's reason for four watches) | `TestKEP284NoSingleSelectorCoversTheUnion` | contract | PASS (conjunction matches nothing; each source drops ≥1 category) | same | premise mechanics hold |
| mechanics | a Pod with legacy labels but `managed=false` stays admitted | `TestKEP284LegacyPodWithManagedFalseStaysAdmitted` | contract | PASS | same | admission table as documented |
| F4 | disjoint partition (`role-instance-name` exists AND `group-name` not exists) is expressible | `TestKEP284DisjointPartitionIsExpressible` | contract | PASS | same | disjoint alternative is feasible, not analyzed in the KEP |

Findings **not** covered by the harness (reasoning only — no runnable claim, the PR
changes no code): F2 (serial four-source sync vs `--cache-sync-timeout` 120s at scale;
cmd/rbgs/main.go:192 flag verified), F3 (missing `cache.ByObject{Transform}` stub
alternative; support verified in vendor/sigs.k8s.io/controller-runtime/pkg/cache/cache.go:290-295),
F5 (owned Pod stripped of admission labels is lost to the ownerRef-UID index List in
pkg/reconciler/roleinstance/instance_reconciler.go:250-257; index itself verified
label-agnostic), F6 (performance data not reproducible from the PR: baseline `91fcdbe`
is not an upstream commit, harness local-only).

## How to run

```bash
go test ./cmd/rbgs/ ./pkg/utils/ ./pkg/reconciler/ \
  -run 'TestKEP284|TestFilterSystemLabelsIgnoresManagedMarker|TestPodTemplateSpecEqualIgnoresManagedMarker|TestCacheOptionsConstrainsPodCache' \
  -count=1 -v
```

Expected on the PR head: 3 PASS (mechanics) + 3 FAIL (P0 premise, F1 x2). The three
FAILs are the reproduction — do not "fix" the tests.

## Harness bites (skill step 4)

Applied the proposed F1 fix temporarily (make `FilterSystemLabels` ignore
`rbg.workloads.x-k8s.io/managed`): both F1 contract tests flipped PASS. Reverted —
production diff empty, tests red again. See `results/harness-bites.txt`.

## Continuing after the implementation lands

- `bash scripts/re-verify.sh` (no argument) resolves the current PR head from the
  manifest `pr` URL, grafts this harness onto it, and re-runs the unit layer.
- **F1 fixed** ⇔ both `*ManagedMarker*` contract tests PASS — either the
  implementation extends `FilterSystemLabels`/`objectMetaEqual` to ignore the
  managed marker, or the KEP stops requiring the marker in workload pod templates.
- **P0 fixed** ⇔ `TestCacheOptionsConstrainsPodCache` PASSes (a Pod cache filter
  exists) — that is the PR's own goal, so at that point the premise test should be
  inverted or promoted to a regression test.
- The `TestKEP284*` semantics tests are guards: a flip means the vendored
  client-go selector semantics changed and the KEP's four-watch premise must be
  revisited.
- No integration/live layer this round (docs-only PR). When the implementation
  PR exists, the KEP's own test plan (four-source sync, unified view, ownership
  collision, backfill, serial init, e2e for the supported creators) should be
  grafted onto this branch as the L2/L3 layers.

Round 1 (initial): `.last-reviewed` = 139b91cdad4e917e2b27949cc3d4ca4a340b4280.
