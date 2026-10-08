# Ground-truth snapshots

Authoritative sources used by the L1 checks. Snapshotted so the harness is re-runnable
offline; `../scripts/run_l1_network_checks.sh` and `../scripts/fetch_ground_truth.sh`
re-verify them live. All fetched 2026-10-09.

| file | source | ref |
| --- | --- | --- |
| `roleautoscaler_types.go` | https://github.com/rolebasedgroup/rbg-planner/blob/main/api/v1alpha1/roleautoscaler_types.go | main |
| `roleautoscaler_controller.go` | https://github.com/rolebasedgroup/rbg-planner/blob/main/internal/controller/roleautoscaler_controller.go | main |
| `rbg_connector.py` | https://github.com/rolebasedgroup/rbg-planner/blob/main/python/planner/rbg_planner/rbg_connector.py | main |
| `config.py` | https://github.com/rolebasedgroup/rbg-planner/blob/main/python/planner/rbg_planner/config.py | main |
| `planner.py` | https://github.com/rolebasedgroup/rbg-planner/blob/main/python/planner/rbg_planner/planner.py | main |
| `values.yaml` | https://github.com/rolebasedgroup/rbg-planner/blob/main/charts/rbg-planner/values.yaml | main |
| `planner_readme.md` | https://github.com/rolebasedgroup/rbg-planner/blob/main/README.md | main |
| `../harness/crds/inference-extension.rolebasedgroup.io_autoscalers.yaml` | https://github.com/rolebasedgroup/rbg-planner/blob/main/config/crd/inference-extension.rolebasedgroup.io_autoscalers.yaml | main |
| `../harness/crds/keda_scaledobjects.yaml` | https://github.com/kedacore/keda/blob/v2.17.2/config/crd/bases/keda.sh_scaledobjects.yaml | v2.17.2 |
| `sglang_server_args.py` | https://github.com/sgl-project/sglang/blob/v0.5.9/python/sglang/srt/server_args.py | v0.5.9 |
| `sglang_metrics_collector.py` | https://github.com/sgl-project/sglang/blob/v0.5.9/python/sglang/srt/metrics/collector.py | v0.5.9 |

In-repo ground truth (checked out with the branch, not snapshotted):

- `config/crd/bases/*.yaml` — RBG / RBGSA CRDs (v1alpha2)
- `api/workloads/v1alpha2/rolebasedgroup_types.go`, `api/workloads/constants/constants.go`
- `internal/controller/workloads/rolebasedgroup_controller.go`,
  `internal/controller/workloads/rolebasedgroupscalingadapter_controller.go`
