# Verification — PR #389 "doc: add doc best-practice/configuring-autoscaling"

Reviewed head: `3f30e486da26523895a237e2bbb7e110bc76684b` (`.last-reviewed`).
Harness: `scripts/check_docs.py` (contract polarity — checks FAIL on the reviewed head and PASS once fixed).
Results: `results/pr-head-run.txt` (11 contract failures = reproduction), `results/fixed-scratch-run.txt` (all green after proposed fixes = harness bites), `results/markdownlint.txt`, `results/autocorrect-zh.txt`.

## P0 — premise

Docs-only PR, so the premise carve-out applies (no bug claim to reproduce). The gap is real:
`doc/best-practice/` on main has 01–07 + 10 and no autoscaling entry; `07-configuring-coordinated-policy.md`
forward-references "Configuring Autoscaling Policies for RBG Services" as not-yet-written.
All **RBG-side** claims in the new docs were verified accurate against the code:

| doc claim | code evidence |
| --- | --- |
| `scalingAdapter.enable/labels` fields | `api/workloads/v1alpha2/rolebasedgroup_types.go:412-424` |
| RBGSA naming `<rbg>-<role>` | `pkg/scale/scaling_adapter.go:25` (`GenerateScalingAdapterName`) |
| RBGSA `/scale` subresource + `status.selector` for HPA | `config/crd/bases/workloads.x-k8s.io_rolebasedgroupscalingadapters.yaml` (scale: specReplicasPath/statusReplicasPath/labelSelectorPath) |
| `rbgsa` shortname, PHASE/REPLICAS/READY_REPLICAS columns | same CRD, v1alpha2 printer columns |
| `Bound`/`NotBound` phases | `api/workloads/constants/constants.go:111-113` |
| scale write syncs into RBG role replicas | `internal/controller/workloads/rolebasedgroupscalingadapter_controller.go` (`updateRoleReplicas`) + `rolebasedgroup_controller.go` `applyRBGSAReplicasOverride` |
| RBGSA cleanup on role removal / RBG delete | `rolebasedgroup_controller.go` `ReconcileScalingAdapter` (delete path) + OwnerReference |
| pod label `rbg.workloads.x-k8s.io/group-name` | `api/workloads/constants/label.go:33`, applied in `pkg/reconciler/*` |

markdownlint (CI config `.github/.markdownlint.json`): 4 new files clean (`results/markdownlint.txt`).
All 49 fenced YAML blocks parse (`results/pr-head-run.txt` F0). en/zh code blocks are in parity.

## Observed vs expected (per finding)

| finding | severity | layer | observed on PR head | expected | status |
| --- | --- | --- | --- | --- | --- |
| F1 wrong org for rbg-planner (links/chart/profiler image) | blocker | unit+integration | 13 references to `sgl-project` org; `api.github.com/repos/sgl-project/rbg-planner` → **404**; `rolebasedgroup/rbg-planner` → 200 (rbg README.md:282 links this org) | links/install point at the real project | **Confirmed** |
| F2 `metricSource` documented as `sglang \| vllm \| dynamo` | major | unit+integration | upstream CRD enum = `sglang,vllm,patio` (pinned `0d35ed76`); `dynamo` rejected by validation | doc lists the real enum | **Confirmed** |
| F3 `metricsEndpoint.port: 8000` described as engine metrics port | major | unit+integration | upstream wires it to env `PLANNER_PROMETHEUS_PORT` + planner container port `metrics` (default 9091); engine metrics actually come via operator-level `PROMETHEUS_ENDPOINT` | describe as planner's own metrics port / drop the 8000 override | **Confirmed** |
| F4 `kubectl get/logs -l app=rbg-planner` | minor | unit+integration | planner pods carry `app.kubernetes.io/name=rbg-planner`,`app.kubernetes.io/instance=<name>` — no `app` key; selector matches 0 pods | use the real label | **Confirmed** |
| F5 stale "Related Documents" TODO | minor | unit | en+zh concept docs claim 01–04 "not created yet"; they exist on main; siblings 05/07 link them | link `./01-...md` etc. | **Confirmed** |
| F6 3 AutoCorrect errors in zh concept doc | nit | unit | lines 273,274 (halfwidth `,` after CJK), 432 (missing spaces around `-`) | autocorrect-clean like rest of tree | **Confirmed** |
| F7 trailing whitespace en:169 | nit | unit | `name: hpa-demo-backend ` | stripped | **Confirmed** |

Note on F1 residual uncertainty: ghcr.io's token endpoint is blocked from the review host, so the
*non-existence* of `ghcr.io/sgl-project/charts/rbg-planner` could not be probed directly. Proven instead:
the `sgl-project` org does not host the repo (404 + absent from public org repo list), upstream
`rolebasedgroup/rbg-planner` has no chart-publish workflow (only lint/test/verify/docker-to-DockerHub),
its README documents only `helm install ./charts/rbg-planner` from a clone, and its code defaults to
`ghcr.io/rolebasedgroup/rbg-profiler`.

## Continuing after the fix

```bash
git fetch https://github.com/cheyang/rbg.git verify/autoscaling-docs-codex
git checkout verify/autoscaling-docs-codex
bash docs/verification/pr389-autoscaling-docs/scripts/re-verify.sh   # fetches current PR 389 head, grafts, runs
```

All checks are contract polarity: a finding is **Fixed** when all its checks pass. The integration
layer needs only outbound HTTPS (GitHub API + raw.githubusercontent for the pinned planner repo);
`--offline` runs the unit layer only. `scripts/apply_proposed_fixes.py <src> <dst>` reproduces the
scratch "fixed" tree used for the harness-bites check (it is a review aid, not the suggested patch —
the author may prefer dropping `metricsEndpoint` entirely rather than re-describing it).

Production code untouched: this branch adds only `docs/verification/**`.
