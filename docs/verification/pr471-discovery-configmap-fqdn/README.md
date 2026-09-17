# Verification — RBG PR #471: correct the FQDN in ConfigMap when using LWP or CCP

- **PR:** https://github.com/sgl-project/rbg/pull/471 (`fix: correct the FQDN in configmap when using lwp or ccp`, fixes #448)
- **PR head:** `861d02c5` (branch `cm-ccp-fix`)
- **Base:** `0aff5f6` (upstream/main)
- **Reviewed on:** verify branch `verify/pr471-discovery-configmap-fqdn` (based on PR head; production code untouched — diff is only the harness)
- **Scope:** RoleInstanceSet only (the project does **not** support LWS / LeaderWorkerSet; that path is out of scope and not reviewed).
- **Cluster:** provided ACK Kubernetes cluster; installed controller `rolebasedgroup/rbgs-controller:v0.8.0-0c00546d` (= git `0c00546d`, ancestor of base, **identical** `config_builder.go` to base → pre-fix). Volcano installed.

## Premise (P0) — CONFIRMED live

Claim: on the base branch the discovery ConfigMap's `instances[].address` for LWP/CCP roles does not match any real Pod, and `size` = `role.replicas` (not the actual Pod count).

Evidence: deployed the PR's own 3-pattern RBG (`discovery-cm-test`: router Standalone×2, prefill LWP-RIS size2, decode CCP leader+worker) against the running base controller and read the live ConfigMap:

| role | base address | real pods | size (base → real pod count) |
|---|---|---|---|
| prefill (LWP-RIS) | `…-prefill-0.s-…` (no such pod) | `…-prefill-0-0`, `…-prefill-0-1` | 1 → 2 |
| decode (CCP) | `…-decode-0.s-…` (no such pod) | `…-decode-0-leader-0`, `…-decode-0-worker-0` | 1 → 2 |
| router (Standalone) | `…-router-{0,1}.s-…` ✓ | `…-router-{0,1}` | 2 → 2 ✓ |

Full capture: `results/01-base-configmap-premise.txt`.

## Observed-vs-expected table

| id | finding | layer | polarity | verdict | evidence |
|---|---|---|---|---|---|
| P0 | Premise: base ConfigMap addresses wrong for multi-pod patterns; size=replicas not pod count | live | contract (base wrong) | **Confirmed** | `results/01-base-configmap-premise.txt` |
| F2 | LWP-RIS addresses match real pods (size1 & size2) | live | contract | **Confirmed** | `results/02-pr-head-configmap-vs-live-pods.txt` |
| F3 | CCP addresses match real pods; leader+worker both resolve | live | contract | **Confirmed** | `results/02`, `results/03` |
| F4 | `size` = actual pod count per role (was `role.replicas`) | unit + live | contract | **Confirmed** | `results/02` |
| F5 | LWP-RIS worker FQDN is canonical but NXDOMAIN under default LeaderOnly (documented caveat) | live | — (design note) | **Confirmed** | `results/03` |

**Verdict: COMMENT** — no blocker/major. The fix is proven correct for every supported (RoleInstanceSet) pattern. F5 is a documented design caveat (the author already notes LWP workers don't resolve under default LeaderOnly).

## How to run the harness

```bash
# L1 unit — PR's own tests (green on PR head)
go test ./pkg/discovery/ -run TestConfigBuilder -v -count=1

# L3 live — PR-head ConfigBuilder vs real pods (needs the RBG deployed + KUBECONFIG)
export KUBECONFIG=/path/to/kubeconfig RBG_LIVE_PROBE=1
# deploy the 3-pattern RBG (see scripts/deploy_scenario.sh) then:
go test ./pkg/discovery/ -run TestLiveProbe_ConfigMapVsPods -v -count=1
```

## Continuing after the fix / cross-machine

1. `bash docs/verification/pr471-discovery-configmap-fqdn/scripts/re-verify.sh` — fetches the current PR head from the manifest `pr` URL (no sha needed), runs the L1 unit layer, prints per-finding status.
2. L3 live: set `KUBECONFIG`, run `scripts/deploy_scenario.sh` to deploy the 3-pattern RBG, then `RBG_LIVE_PROBE=1 go test ./pkg/discovery/ -run TestLiveProbe_ConfigMapVsPods -v -count=1`.

## Environment notes / limitations

- The installed controller (`0c00546d`) is base-equivalent for `config_builder`, so the premise is provable live without rebuilding. The full PR-head controller could not rewrite the live ConfigMap because the PR-head binary's RoleInstanceSet informer fails to decode the cluster's **legacy `restartPolicy` object-form data** on existing RoleInstances (`cannot unmarshal object into … restartPolicy of type RestartPolicyType`) — a pre-existing cluster-data incompatibility (documented across prior rbg reviews), **not** a PR defect. The live fix is therefore proven by running the PR-head `ConfigBuilder` directly against live cluster state (the exact code path the RBG reconciler calls), cross-checked against real pods — see `results/02`.
- LWS / LeaderWorkerSet is **not a supported workload type** for this project (out of scope); the `buildInstances` LWS branch was not reviewed.
