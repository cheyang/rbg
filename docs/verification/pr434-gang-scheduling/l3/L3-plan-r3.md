# L3 live plan — round 3 (PR head bd9ee4dd, reworked)

## Why L3 again

Round 2 proved ten defects live on this cluster. The PR was force-pushed and
reworked; L1/L2 now assert the fixed behavior. L3 round 3 re-runs the same
scenarios live to confirm the fixes hold end to end on
ACK v1.36.1 + Volcano 1.14.4 + `--scheduler-name=volcano`, plus the two
behaviors unit tests cannot exercise: policy-edit propagation through the new
`Watches(CoordinatedPolicy)` (F9) and rejection surfacing as the
`GangConfigured` condition with no retry storm (F2/F4).

## What changed vs round 2 setup

- The PR now sits on current `main`, so the round-2 `restartPolicy` decode
  skew between the PR binary and the cluster's installed CRD generation is
  gone for freshly created objects. Stale objects in other namespaces (if any
  remain from round 2's live workload) would still fail a cluster-wide LIST,
  so the same test-only, env-gated field selector is kept.
- The cluster currently has ZERO live RoleBasedGroups, so the blast radius is
  limited to the test namespace; the field selector is belt and braces.

## Controller under test

`rolebasedgroup/rbgs-controller:v434-r3` =
verify/pr434-gang-scheduling @ ce931129. Production code is byte-identical to
PR head bd9ee4dd; the only local modification is the test-only patch to
`cacheOptions()` in `cmd/rbgs/main.go` (env-gated
`RBG_TEST_WATCH_NAMESPACE=v434-r3` field selector on the RBG / RoleInstanceSet
/ RoleInstance informers; no-op when unset). Built on the sandbox with
`make docker-build-controller TAG=v434-r3`, pushed to Docker Hub.

## Swap procedure (mirrors round 2)

1. Backup: `kubectl -n rbg-system get deploy rbgs-controller-manager -o yaml`
   to `/root/v434-backup/deploy-r3-pre-swap.yaml`; record current image
   (expected `v0.8.0-0c00546d`) and container name.
2. `kubectl -n rbg-system set env deploy/rbgs-controller-manager RBG_TEST_WATCH_NAMESPACE=v434-r3`
3. `kubectl -n rbg-system set image deploy/rbgs-controller-manager <ctr>=rolebasedgroup/rbgs-controller:v434-r3`
4. `kubectl -n rbg-system rollout status --timeout=180s`; confirm exactly one
   ready pod and `--scheduler-name=volcano` still in args.
5. Apply `l3/cases-r3.yaml`, then run the watch step and the admission
   negatives below.
6. Restore: `set env ... RBG_TEST_WATCH_NAMESPACE-`, `set image` back to the
   backup image, rollout, `kubectl delete ns v434-r3`, confirm no stray
   RBG/RIS/RoleInstance/PodGroup anywhere.

## Cases and expected observations

| case | intent (round-2 finding) | expected observation |
|------|--------------------------|----------------------|
| r3-min | baseline per-role minimums + F10 derivation | PodGroup minMember=3; subGroupPolicy[2] (prefill 2/1, decode 1/1, matchLabelKeys=role-instance-name); GangConfigured=True; 4/4 Running; both RIS annotated `role-instance-gang-scheduling=true` |
| r3-merge | F7 fixed: two rules merge | minMember=2; 2 subGroupPolicy entries; 2/2 Running |
| r3-scope | F3/F6 fixed: scope honored | minMember=1; 1 entry; prefill RIS template has `scheduling.k8s.io/group-name`, sidecar's does not; both pods schedulerName=volcano; 2/2 Running |
| r3-typo | F2 fixed: unknown role rejected | admission OK; GangConfigured=False / IncompatibleGangConfig naming "prefil"; no PodGroup; no RIS; no backoff storm |
| r3-exceeds | F4 fixed: minimum > replicas rejected | GangConfigured=False "can never be satisfied"; no PodGroup; no RIS |
| r3-watch | F9 fixed: policy watch | after patching prefill 1→2, PodGroup minMember 1→2 within 60s with the RBG untouched |
| r3-adm-zero | F5 fixed at admission | create denied: "must be at least 1, got 0" |
| r3-adm-oos | F6 fixed at admission | create denied: "role is not listed in spec.policies[0].roles" |

## Observation commands

```bash
kubectl -n v434-r3 get podgroups.scheduling.volcano.sh -o custom-columns='NAME:.metadata.name,MIN:.spec.minMember,SGP:.spec.subGroupPolicy[*].name,PHASE:.status.phase'
kubectl -n v434-r3 get rbg -o custom-columns='NAME:.metadata.name,GANG:.status.conditions[?(@.type=="GangConfigured")].status,REASON:.status.conditions[?(@.type=="GangConfigured")].reason,MSG:.status.conditions[?(@.type=="GangConfigured")].message'
kubectl -n v434-r3 get roleinstancesets -o custom-columns='NAME:.metadata.name,GANGANN:.metadata.annotations.rbg\.workloads\.x-k8s\.io/role-instance-gang-scheduling'
kubectl -n v434-r3 get pods -o custom-columns='NAME:.metadata.name,SCHED:.spec.schedulerName,GRP:.metadata.annotations.scheduling\.k8s\.io/group-name,PHASE:.status.phase'
kubectl -n rbg-system logs deploy/rbgs-controller-manager --since=5m | grep -c "r3-typo\|r3-exceeds"   # retry-storm check
```

## Stop rules

- Any panic, CrashLoopBackOff, or unmarshal error in the controller: revert
  immediately (step 6) and treat as a finding only if it reproduces on a
  clean restart; otherwise classify as setup skew.
- Live objects outside `v434-r3` are never touched; the field selector keeps
  them out of the LIST entirely.
