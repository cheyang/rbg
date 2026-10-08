# autoscaling-docs — documentation fact-check for PR #389

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/389 (doc: add doc best-practice/configuring-autoscaling).

The PR adds 2 807 lines of new documentation (4 files: en/zh × concept + operations guide)
describing how to autoscale RBG services via ScalingAdapter / HPA / KEDA / RBG Planner.
For a docs PR the "defect" surface is **factual accuracy**: field names, commands, labels,
phases, images, charts and links that a reader will copy. The harness checks each doc claim
against ground truth: this repo's CRDs and controllers, and snapshots of the external
projects the docs reference (rbg-planner, KEDA, sglang — see `ground_truth/SOURCES.md`).

Layers run against the **code under review** (`origin/pr/389` = 3f30e486da26523895a237e2bbb7e110bc76684b):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (static) | doc claims vs CRD schemas / controller / upstream sources | `python3 -I scripts/run_l1_checks.py` → `results/l1-static-checks.txt` |
| 1. Unit (live) | repo links + ghcr.io chart/image existence | `bash scripts/run_l1_network_checks.sh` → `results/l1-network-checks.txt` |
| 1. Unit (behavior) | repo's own RBGSA controller tests (Op 1 sync-back) | `go test ./internal/controller/workloads/ -run 'ScalingAdapter\|...'` → `results/unit-rbgsa-controller.txt` |
| 2. Integration | real API server (envtest) + real kubectl admits every manifest the docs tell the user to apply | `KUBEBUILDER_ASSETS=… go test ./docs/verification/autoscaling-docs/harness/ -v` → `results/l2-envtest.txt`, `results/l2-kubectl-transcript.txt` |
| 3. Live | real cluster with controller + KEDA + planner | **not run** (round instructed: unit+integration only). No KUBECONFIG was provided and the cluster probe found nothing reachable. |

> Polarity: **contract** checks (C*) assert the doc is *correct* — they must stay PASS.
> **Bug-canaries** (B*) assert the doc is *wrong* — they PASS while the defect exists and
> FLIP to FAIL when the doc is fixed (then invert/drop them). `scripts/re-verify.sh`
> applies this automatically.

## Problem premise (P0)

Docs-only PR → premise validation skipped per the review rubric carve-out (documentation
does not owe a problem statement). Recorded for completeness:

- PR has **no body** and **no linked issue**; the title states the goal: "doc: add doc
  best-practice/configuring-autoscaling". Intent is legible from the diff itself.
- What the docs claim to teach splits 60/40: Operations 1–3 (ScalingAdapter, HPA, KEDA) are
  about **this repo's** API and check out against this repo's code (C1–C8, L2 envtest);
  Operation 4 + Scenario 3 (RBG Planner) document an **external, unreleased** project
  (`rolebasedgroup/rbg-planner`) — every confirmed defect below lives in that half.

## Summary of results

| ID | Claim (one sentence) | Layer | Verdict | Evidence |
|----|-----------------------|-------|---------|----------|
| B1 | doc links `github.com/sgl-project/rbg-planner` — repo does not exist (404); real repo is `rolebasedgroup/rbg-planner` (linked by this repo's own README) | 1 | **Confirmed** | `results/l1-network-checks.txt` |
| B2 | planner install `helm install rbg-planner oci://ghcr.io/sgl-project/charts/rbg-planner` — no such public OCI chart; upstream installs from a local chart path and has 0 releases | 1 | **Confirmed** | `results/l1-network-checks.txt` (calibrated ghcr probe) |
| B3 | `profiling.image: ghcr.io/sgl-project/rbg-profiler:latest` contradicts upstream default `ghcr.io/rolebasedgroup/rbg-profiler:latest` (and neither is publicly pullable today) | 1 | **Confirmed** | `results/l1-network-checks.txt` |
| B4 | `metricsEndpoint.port` documented as "Inference engine metrics port"; the planner CRD itself says it is the **planner's own** metrics-exposition port (default 9091) | 1 | **Confirmed** | `results/l1-static-checks.txt` B4 |
| B5 | Operation 4 never sets the planner's `prometheus.endpoint` (helm value) and the sglang pods carry no `prometheus.io/scrape` annotation → the planner has **no metrics source**, so the documented verification ("logs show observed TTFT, ITL") cannot happen | 1 | **Confirmed** | `results/l1-static-checks.txt` B5 |
| B6 | `kubectl get pods -l app=rbg-planner` / `kubectl logs -l app=rbg-planner` match nothing — planner pods only carry `app.kubernetes.io/name` + `app.kubernetes.io/instance` labels | 1 | **Confirmed** | `results/l1-static-checks.txt` B6 |
| B7 | guide says AutoScaler phase is "`Ready` or `Profiling`" — the phase enum is `Pending;Initializing;Ready;Failed`; `Profiling` does not exist | 1 | **Confirmed** | `results/l1-static-checks.txt` B7 |
| B8 | doc comment advertises `metricSource: sglang \| vllm \| dynamo` — CRD enum is `sglang\|vllm\|patio`; a user setting `dynamo` gets the AutoScaler **rejected** | 1+2 | **Confirmed** | `results/l1-static-checks.txt` B8, `results/l2-envtest.txt` `TestCanaryMetricSourceDynamoRejected` |
| N1 | KEDA demo queries `sglang_num_queue_requests`; real sglang v0.5.9 gauge is `sglang:num_queue_reqs` (self-consistent inside the simulated demo; misleading if copied to a real deployment) | 1 | Mismatch present | `results/l1-static-checks.txt` N1 |
| C1–C12 | positive: scalingAdapter/standalonePattern fields, RBGSA scale subresource + `rbgsa` shortname, Bound phase, labels, scale→RBG sync, adapter lifecycle cleanup, all AutoScaler manifest paths, sglang v0.5.9 flags, KEDA ScaledObject fields, scalingInterval default 180, RBGSA-or-RBG-patch scaling | 1+2 | **All PASS** | `results/l1-static-checks.txt`, `results/l2-envtest.txt` |

Integration result: **all 14 apply-able doc manifests are admitted by the real CRDs**
(RBG ×7, HPA ×3, ScaledObject ×2, AutoScaler ×2 — including the Prometheus stack), and
`kubectl scale rbgsa … --replicas=4` round-trips through the real `/scale` subresource
(see `results/l2-kubectl-transcript.txt`). Negative controls prove the schema validation
actually bites (unknown spec field on RBG rejected; unknown DynamoPlanner field rejected).

## Per-finding detail

- **B1/B2/B3 — artifact references.** `gh api repos/sgl-project/rbg-planner` → 404;
  `repos/rolebasedgroup/rbg-planner` → exists. The OCI-chart probe is calibrated: a
  known-public chart (`stefanprodan/charts/podinfo`) grants an anonymous pull token and
  lists tags, while `sgl-project/charts/rbg-planner`, `sgl-project/rbg-profiler` *and*
  `rolebasedgroup/rbg-profiler` all deny — the planner's images/chart are not published
  anywhere yet (repo has 0 releases; upstream README installs `./charts/rbg-planner`).
  Consequence: the Operation 4 prerequisite (install command) and the profiling Job image
  cannot work as written, on top of pointing at the wrong org for the repo links.
- **B4 — field semantics.** The planner CRD says: `port: {default: 9091, description:
  "Port is the port for planner's own Prometheus metrics exposition."}`. The operator maps
  it to env `PLANNER_PROMETHEUS_PORT` on the planner container, and `planner.py` calls
  `start_http_server(config.planner_prometheus_port)` — i.e. metrics the *planner exports*,
  not metrics it *reads*. The doc's parameter table and the fix commit 3f30e486's stated
  rationale ("set metricsEndpoint.port to 8000 so the Planner has a real metrics source")
  are both wrong; that change also moved the value away from the CRD default for no effect.
- **B5 — metrics path.** The planner reads engine metrics by querying Prometheus at the
  helm value `prometheus.endpoint` (default `…kube-prometheus…:9090`), via its Prometheus
  metrics adapter. The guide's Operation 4: helm install has no `--set prometheus.endpoint=`,
  the RBG pods have no `prometheus.io/scrape` annotation (the Operation 3 Prometheus only
  scrapes annotated pods), and the prerequisites list Prometheus only for Operations 2–3.
  Net: planner starts, queries an unreachable/wrong Prometheus with no scraped targets,
  and Step 3's expected output is unreachable. (Operation 3's Prometheus, `prometheus.monitoring:9090`,
  would at least need the endpoint wired and annotations added to the pods.)
- **B6 — selector.** The planner Deployment template labels are exactly
  `app.kubernetes.io/name: rbg-planner` + `app.kubernetes.io/instance: <name>`. Upstream's
  own README uses `kubectl logs -l app.kubernetes.io/name=rbg-planner`.
- **B7 — phase.** `+kubebuilder:validation:Enum=Pending;Initializing;Ready;Failed` in the
  planner API types. First-run profiling happens during `Initializing`.
- **B8 — enum comment.** The envtest canary applies exactly what the doc's inline comment
  suggests (`metricSource: dynamo`) and the real CRD rejects it. `patio` is a valid value
  the doc never mentions.

## Live run notes

Not run this round (no KUBECONFIG, no reachable cluster). If run later: Operation 1 is the
cheapest end-to-end check (RBG + scalingAdapter + `kubectl scale rbgsa` → pods scale, RBG
spec.replicas syncs); Operations 2–3 need metrics-server / KEDA + Prometheus.

## Harness-bites check

`results/harness-bites.txt`: with a sed-applied temporary fix of all eight defects,
8/8 canaries + the nit FLIPPED to FAIL and 12/12 contracts stayed PASS; fix reverted
afterwards (production diff vs `origin/pr/389` = 0 lines).

## Proposed fixes (NOT applied here)

1. Point all planner references at `github.com/rolebasedgroup/rbg-planner` (or wherever
   the project ends up publishing), and replace the OCI install with the upstream-documented
   local-chart install until a chart is actually published; drop the `profiling.image`
   override or use the upstream default registry.
2. Correct the `metricsEndpoint.port` table row to "planner's own Prometheus metrics
   exposition port" (and consider just omitting it to keep the CRD default).
3. Add a Prometheus step to Operation 4: install/point `prometheus.endpoint` at the
   cluster's Prometheus and add `prometheus.io/scrape: "true"` + `prometheus.io/port: "8000"`
   annotations to the engine pods (mirroring Operation 3), or state the prerequisite.
4. `kubectl get/logs -l app.kubernetes.io/name=rbg-planner`.
5. Expected phase `Initializing` (not `Profiling`).
6. metricSource comment → `sglang | vllm | patio`.
7. (nit) either note that `sglang_num_queue_requests` is a simulation-only name or use the
   real gauge `sglang:num_queue_reqs` and add labels the real exporter provides.

## Continuing after the fix

```
git fetch https://github.com/sgl-project/rbg.git pull/389/head
git checkout verify/autoscaling-docs-claude          # holds this harness
bash docs/verification/autoscaling-docs/scripts/re-verify.sh   # no sha needed
```

Canaries are *fixed only when they flip to FAIL*; re-verify.sh reports that per finding
and exits 0 iff all are fixed. Advance the marker afterwards:
`git rev-parse <new-head> > docs/verification/autoscaling-docs/.last-reviewed`.

Kickoff prompt for a fresh agent:

> You are resuming a PR review verification. Checkout branch `verify/autoscaling-docs-claude`
> of cheyang/rbg (fork of sgl-project/rbg), read `docs/verification/autoscaling-docs/README.md`,
> then run `bash docs/verification/autoscaling-docs/scripts/re-verify.sh` (auto-resolves PR
> #389's current head). Report the per-finding table it prints, treating a canary PASS as
> "still broken" and a flip to FAIL as "fixed". Do not modify anything under doc/ or
> production code; commit only harness updates and the advanced `.last-reviewed`.
