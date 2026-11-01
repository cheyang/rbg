# KEP-284 (PR #530) — verification harness — Reviewer A (Claude)

PR: https://github.com/sgl-project/rbg/pull/530
Round 2 head reviewed: `f8d67fa701956638658594bccfe34b7947222e36` (base `4a0765f78b0930114a4b73a64433ee8f27c34372`, docs-only delta in the base — no Go contracts affected)
Round 1 head reviewed: `139b91cdad4e917e2b27949cc3d4ca4a340b4280` (base `7894133d5b59d44f2a19dcf6b75c531996285bcf`)

Round 2 re-verified the round-1 harness on the new head, reviewed the
`previousHead..head` delta (the KEP rewritten as the transform-stub design plus a
2,400-line tagged prototype in `test/podcacheunion/`), and extended the harness
with round-2 findings and an integration layer that runs the PR's own prototype
suite. Round 1's record is kept below unchanged.

## Premise (P0): CONFIRMED (re-confirmed on the round-2 head)

Claim (issue #284, quoted): "the reason is that irrelevant `Pods` consume most of
the memory" — the operator caches all Pods. The revision-2 PR still changes no
production code (`cacheOptions()` has no `*corev1.Pod` entry), so the problem the
KEP designs against still exists on the head: `TestCacheOptionsConstrainsPodCache`
stays red (`results/round2/unit-head.txt`).

## Round-2 observed vs expected

| # | claim / finding | test | polarity | observed on f8d67fa7 | verdict |
| --- | --- | --- | --- | --- | --- |
| P0 | Pod informer cache is unfiltered (premise) | `TestCacheOptionsConstrainsPodCache` | contract | FAIL (no Pod ByObject entry; production code unchanged) | premise re-CONFIRMED; implementation deferred by design |
| F1 | marker-only template diff must not be drift | `TestFilterSystemLabelsIgnoresManagedMarker`, `TestPodTemplateSpecEqualIgnoresManagedMarker` | contract | FAIL | round-1 major **fixed at design level** (KEP now: marker never in templates + key-scoped `FilterSystemLabels` guard, and it names these tests as the implementation PR's regression suite); the code contract stays red until that PR — pinned here |
| F1-guard | group-name/role-name drift must stay detectable after the exemption lands (the KEP's own named guard) | `TestGroupNameDriftStillDetected` | contract | PASS | guard pinned green; a flip means the exemption was implemented too broadly |
| P-readpaths | the transform design's read-path claims (incl. round-1 A-F5: a label-stripped owned Pod stays visible to `getOwnedPods`; round-1 B-F1/B-F2: no boundary reads, named existence checks stay cached) | the PR's own suite, re-run by this harness in both gate configs with `-race` + mutation detector | contract | 9 PASS (+ median canary), 4 perf tests SKIP by design (`results/round2/integration-race-watchlist-*.txt`) | A-F5 fixed and **proven** (production `instanceutil.GetActiveAndInactivePods` driven through the transform cache); B-F1/B-F2 dissolved by the redesign (no boundary reads exist) |
| A-F6 | performance evidence must be re-runnable from the PR | smoke campaign (`SHAPES=48:1000 ROUNDS=0`, both batches) re-run on this machine | campaign | exit 0, summary + per-mode JSON + provenance captured (`results/round2/campaign-smoke/`) | **fixed** — harness is in the PR, re-runnable, numbers match the KEP's shape (full 47.5 vs filtered 6.8 MiB at 48/1000, amd64) |
| R2-F1 | campaign must not be able to escape to the kubeconfig cluster | `TestKEP284CampaignForcesIsolatedEnvtestCluster` | contract | FAIL | confirmed — envtest `UseExistingCluster` is nil and nothing pins the env var (bite: green with a one-line export) |
| R2-F2 | the tagged prototype must be compiled by CI | `TestKEP284PodcacheunionCompiledInCI` | contract | FAIL | confirmed — no workflow/Makefile target references `podcacheunion` |
| R2-F3 | KEP read-path audit claim must match the code | `TestKEP284NodeNameReadPathAuditClaim` | contract | FAIL | confirmed — `RecordNodeBindings` reads `pod.Spec.NodeName`; "No code reads Pods by `spec.nodeName`" is false as written |
| R2-F4 | even-group median convention | `TestMedianRangeUpperMiddleForEvenGroups` | canary | PASS (upper-middle) | nit confirmed; published medians use 3 rounds so they are true medians |

Round-1 findings resolved by the revision itself (no runnable claim needed — the
design text changed): A-F3 (the KEP now *is* the transform design; union,
disjoint partition, bounded paging, and leader backfill are all weighed in
Alternatives with measurements), A-F2 core (the serial four-source startup is
gone; one full List, same as today, with the budget statement and beta
target-scale measurement), A-F4 (disjoint partition analyzed and rejected),
A-Q1 (backfill moved to `kubectl rbg migrate pod-labels` + Helm pre-upgrade
hook), A-Q2 (`--pod-cache-scope=all|filtered`, `all` default in alpha),
B-F3 (kep.yaml cross-references the mechanism), C-4431747890 (pagination
machinery separated in Alternatives). N1 residue: the PR body still carries
`fixes #XXXX` (verified via `gh pr view`).

## How to run (round 2)

```bash
# unit layer (round-1 + round-2 findings)
go test ./cmd/rbgs/ ./pkg/utils/ ./pkg/reconciler/ \
  -run 'TestKEP284|TestFilterSystemLabelsIgnoresManagedMarker|TestPodTemplateSpecEqualIgnoresManagedMarker|TestGroupNameDriftStillDetected|TestCacheOptionsConstrainsPodCache' \
  -count=1 -v
# integration layer (the PR's own prototype suite, both gate configs)
bash docs/verification/284-pod-cache-filter-claude/scripts/run-integration.sh
# full re-verify against the current PR head
bash docs/verification/284-pod-cache-filter-claude/scripts/re-verify.sh
```

Expected on the round-2 head: unit 4 PASS (mechanics ×3, F1-guard) + 6 FAIL
(P0, F1 ×2, R2-F1/F2/F3) — the FAILs are the reproductions; integration 9 PASS
+ canary PASS + 4 SKIP. See `results/round2/`.

## Harness bites (round 2, skill step 4)

`results/round2/harness-bites.txt`: R2-F1/F2/F3 red on the head, all green after
applying the three one-line fixes, reverted with an empty production diff, red
again. Round 1's bite record is in `results/harness-bites.txt`.

## Continuing after the fix

Graft this harness onto the author's next head (re-verify.sh does it
automatically — it resolves the PR head from `pr` in the manifest):

- R2-F1/F2/F3 go green when the author pins the isolated control plane, adds a
  CI/Makefile compile step for the tagged package, and qualifies the nodeName
  sentence.
- F1's two contract tests stay red until the **implementation** PR lands the
  key-scoped exemption; `TestGroupNameDriftStillDetected` must stay green then.
- `TestCacheOptionsConstrainsPodCache` goes green when the implementation adds
  the Pod transform to `cacheOptions()`.
- The median canary flips red if `medianRange` starts averaging even groups;
  invert it then.

---

# Round 1 record (head 139b91cd, unchanged)

The PR adds a KEP; it changes no runtime code. The harness therefore verifies, at
the unit layer, the KEP's claims **that are decidable against the current codebase**:

1. the premise (the problem exists on the base branch),
2. the no-rolling-update guarantee for the proposed `rbg.workloads.x-k8s.io/managed`
   template marker (it does not hold against the current drift detection),
3. the label-selector semantics the four-watch design rests on (including the
   feasibility of the disjoint-partition alternative).

## Round-1 observed vs expected

| # | claim / finding | test | polarity | observed | expected | verdict |
| --- | --- | --- | --- | --- | --- | --- |
| P0 | operator's Pod cache is unfiltered on base | `TestCacheOptionsConstrainsPodCache` | contract | FAIL (no Pod ByObject entry) | would pass once a Pod filter exists | premise CONFIRMED (red is the reproduction) |
| F1 | adding `rbg.workloads.x-k8s.io/managed` to a workload pod template must not be label drift (KEP: "do not trigger a template rolling update") | `TestFilterSystemLabelsIgnoresManagedMarker` | contract | FAIL — `FilterSystemLabels` (pkg/utils/utils.go:194) keeps the key | marker ignored like `app.kubernetes.io/*` | F1 CONFIRMED |
| F1 | templates differing only by the managed marker must compare equal | `TestPodTemplateSpecEqualIgnoresManagedMarker` | contract | FAIL — `podTemplateSpecEqual` reports "label not equal" | equal (no workload patch, no Pod roll) | F1 CONFIRMED |
| mechanics | no single label selector covers the four admission categories (the KEP's reason for four watches) | `TestKEP284NoSingleSelectorCoversTheUnion` | contract | PASS (conjunction matches nothing; each source drops ≥1 category) | same | premise mechanics hold |
| mechanics | a Pod with legacy labels but `managed=false` stays admitted | `TestKEP284LegacyPodWithManagedFalseStaysAdmitted` | contract | PASS | same | admission table as documented |
| F4 | disjoint partition (`role-instance-name` exists AND `group-name` not exists) is expressible | `TestKEP284DisjointPartitionIsExpressible` | contract | PASS | same | disjoint alternative is feasible, not analyzed in the KEP |

Findings **not** covered by the harness in round 1 (reasoning only — the PR
changes no code): F2 (serial four-source sync vs `--cache-sync-timeout` 120s at scale;
cmd/rbgs/main.go:192 flag verified), F3 (missing `cache.ByObject{Transform}` stub
alternative; support verified in vendor/sigs.k8s.io/controller-runtime/pkg/cache/cache.go:290-295),
F5 (owned Pod stripped of admission labels is lost to the ownerRef-UID index List in
pkg/reconciler/roleinstance/instance_reconciler.go:250-257; index itself verified
label-agnostic), F6 (performance data not reproducible from the PR: baseline `91fcdbe`
is not an upstream commit, harness local-only).
