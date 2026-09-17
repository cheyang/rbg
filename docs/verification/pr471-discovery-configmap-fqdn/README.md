# Verification — RBG PR #471: correct the FQDN in ConfigMap when using LWP or CCP

- **PR:** https://github.com/sgl-project/rbg/pull/471 (`fix: correct the FQDN in configmap when using lwp or ccp`, fixes #448)
- **PR head:** `861d02c5` (branch `cm-ccp-fix`)
- **Base:** `0aff5f6` (upstream/main)
- **Reviewed on:** verify branch `verify/pr471-discovery-configmap-fqdn` (based on PR head; production code untouched — diff is only the harness)
- **Cluster:** provided ACK Kubernetes cluster; installed controller `rolebasedgroup/rbgs-controller:v0.8.0-0c00546d` (= git `0c00546d`, ancestor of base, **identical** `config_builder.go` to base → pre-fix). Volcano installed; **LWS (leaderworkerset) CRD/controller NOT installed**.

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
| F1 | **LWS branch emits contiguous ordinals `{w}-{i*size}` / `{w}-{i*size+j+1}` instead of the LWS contract `{w}-{leaderIndex}` / `{w}-{leaderIndex}-{workerIndex}` — wrong for size>1; `…-1` misroutes to group-1 leader; `…-2`/`…-3` don't exist. Regression of leader naming vs base.** | unit + doc | contract | **Confirmed (blocker)** | `results/04-lws-naming-contract-canary.txt`; vendored LWS API docs |
| F2 | LWP-RIS addresses match real pods (size1 & size2) | live | contract | **Confirmed** | `results/02-pr-head-configmap-vs-live-pods.txt` |
| F3 | CCP addresses match real pods; leader+worker both resolve | live | contract | **Confirmed** | `results/02`, `results/03` |
| F4 | `size` = actual pod count per role (was `role.replicas`) | unit + live | contract | **Confirmed** | `results/02` |
| F5 | LWP-RIS worker FQDN is canonical but NXDOMAIN under default LeaderOnly (documented caveat) | live | — (design note) | **Confirmed** | `results/03` |

## How to run the harness

```bash
# L1 unit — PR's own tests + the F1 contract canary (RED on PR head = reproduction)
go test ./pkg/discovery/ -run TestConfigBuilder -v -count=1
go test ./pkg/discovery/ -run TestLWS_NamingContract -v -count=1   # expect FAIL on PR head

# L3 live — PR-head ConfigBuilder vs real pods (needs the RBG deployed + KUBECONFIG)
export KUBECONFIG=/path/to/kubeconfig RBG_LIVE_PROBE=1
# deploy the 3-pattern RBG (see scripts/deploy_scenario.sh) then:
go test ./pkg/discovery/ -run TestLiveProbe_ConfigMapVsPods -v -count=1
```

## Proposed fixes (for the author)

### F1 — LWS branch (`pkg/discovery/config_builder.go`, buildInstances, `case lwp != nil`, `LeaderWorkerSetWorkloadType` branch)

Replace the contiguous-ordinal scheme with the LWS contract (leader = `{name}-{i}`, worker = `{name}-{i}-{j+1}`):

```go
if role.GetWorkloadType() == constants.LeaderWorkerSetWorkloadType {
    addresses = append(addresses, fmt.Sprintf("%s-%d.%s", workloadName, i, serviceName))
    for j := int32(0); j < size-1; j++ {
        addresses = append(addresses, fmt.Sprintf("%s-%d-%d.%s", workloadName, i, j+1, serviceName))
    }
}
```

And **invert** the PR's existing `leader_worker_pattern_with_size=2_on_LeaderWorkerSet` unit test: it currently asserts the wrong `{w}-0,{w}-1,{w}-2,{w}-3` output — flip its `expected` to the contract `{w}-0,{w}-0-1,{w}-1,{w}-1-1` (or delete it and rely on `TestLWS_NamingContract`). Verify the LWS pod **subdomain** matches `serviceName` on a cluster with LWS installed (this cluster has no LWS, so it is unverified — see `results/04`).

## Continuing after the fix (cross-machine)

1. `git fetch https://github.com/sgl-project/rbg.git pull/471/head` and check out the new head.
2. `go test ./pkg/discovery/ -run TestConfigBuilder -count=1` → should be green with the LWS test inverted.
3. `go test ./pkg/discovery/ -run TestLWS_NamingContract -count=1` → should flip to **PASS** (canary fixed; invert/promote it per the polarity rules).
4. Re-run the live probe against a cluster that has the 3-pattern RBG deployed (and, for the LWS path, an LWS install) to confirm the live ConfigMap.

**Polarity note:** `TestLWS_NamingContract` is a *contract* test (asserts correct behavior) — on the buggy PR head it is RED; when fixed it goes GREEN and stays as a regression guard. The PR's own `…on_LeaderWorkerSet` subtest is a *bug-canary* (asserts the wrong behavior) — when fixed it flips to RED and **must be inverted**; do not leave it asserting the old wrong output.

## Environment notes / limitations

- The installed controller (`0c00546d`) is base-equivalent for `config_builder`, so the premise is provable live without rebuilding. The full PR-head controller could not rewrite the live ConfigMap because the PR-head binary's RoleInstanceSet informer fails to decode the cluster's **legacy `restartPolicy` object-form data** on existing RoleInstances (`cannot unmarshal object into … restartPolicy of type RestartPolicyType`) — a pre-existing cluster-data incompatibility (documented across prior rbg reviews), **not** a PR defect. The live fix is therefore proven by running the PR-head `ConfigBuilder` directly against live cluster state (the exact code path the RBG reconciler calls), cross-checked against real pods — see `results/02`.
- LWS end-to-end is not exercisable here (no leaderworkerset CRD/controller). F1 is proven via the authoritative in-repo vendored LWS API documentation + the unit canary, not by a live LWS run.
