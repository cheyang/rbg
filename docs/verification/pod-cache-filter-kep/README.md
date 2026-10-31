# Verification — KEP-284 Pod Cache Filtering (PR #530)

Reviewer: Codex (Reviewer B) · Round: initial · Reviewed head: `139b91cd`
Base (merge-base): `d162208e` · Harness: additive only, production code untouched.

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
