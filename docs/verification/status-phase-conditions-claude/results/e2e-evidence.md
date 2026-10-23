# E2E CI evidence for F-lws-ready (not run locally — no cluster in the review sandbox)

## CI differential

Base `a0b11e6ab95ca6dfcfdf6fca713352c8482ebd41` check-runs (GitHub API, fetched 2026-10-24):
E2E lifecycle / workload / rollout / feature / v1alpha1 / update / webhook-rbac / scheduler /
Manifest installation, envtest, lint, build, unit-test — **all success**.

Head `42e4479ea0b67840f1dd8d011c6b194ecf15d762` (run 39920528759 / 39920528850):
- lint — FAILURE
- E2E lifecycle — FAILURE (2 specs)
- E2E workload — FAILURE (1 spec)
- E2E rollout — FAILURE (2 specs)
- E2E feature — FAILURE (1 spec)
- E2E v1alpha1 — FAILURE (8 specs)
- Manifest installation e2e — FAILURE (8 specs)
- envtest / unit-test / build / E2E update / webhook-rbac / scheduler / gang e2e — success

## Every failing spec has a LeaderWorkerSet role

- lifecycle: "scenario of overriding changes that fail the semantically equal check" (roles:
  deployment, sts, **lws**); "semantic comparison can reconcile modifications on the workload"
  (**lws** only)
- workload: "create leader-worker role with engine runtime" (**lws**)
- rollout: "update leader-worker role replicas & template" (**lws**); "lws with rollingUpdate" (**lws**)
- feature: "test scale role in rbg via ScalingAdapter" (role-1 RIS + **role-2 LWS**)
- v1alpha1: create/update LWS compat specs, revision specs (contain **lws**), multi workloads
  (contains **lws**)
- manifest installation: same suite, all 8 failures are the LWS-bearing specs above.

## Stuck-state evidence (from the jobs' ginkgo debug output)

RBG never reaches Ready while children are converged (rbg_v2_expect.go:86 Eventually):

    rbg v2 not ready, status: {Phase:Running ObservedGeneration:1 Conditions:[
      {Type:Accepted Status:True Reason:ValidSpec ...}
      {Type:Ready Status:False Reason:WaitingForInstances Message:WaitingForInstances}
      {Type:Progressing Status:True Reason:WaitingForInstances ...}]
      RoleStatuses:[{Name:role-1 ReadyReplicas:1 Replicas:1 UpdatedReplicas:1}]}

ScalingAdapter failure dump (manifest job, 08:29:43) — both children converged, RBG still not
ready; role-2 is the LWS:

    [RoleInstanceSet] Status: Replicas=1, ReadyReplicas=1, CurrentReplicas=1, UpdatedReplicas=1
    [RoleInstanceSet] CurrentRevision=e2e-test-role-1-5fdc8f8b6c, UpdateRevision=e2e-test-role-1-5fdc8f8b6c
      - Type=Ready, Status=True, Reason=AllInstancesReady
      - Type=Progressing, Status=False, Reason=Converged
    [LWS] Status: Replicas=1, ReadyReplicas=1, UpdatedReplicas=1
      - Type=Progressing, Status=False, Reason=GroupsProgressing
      - Type=Available, Status=True, Reason=AllGroupsReady
    [POD] all pods Running/Ready

Root cause (code + upstream facts):
- internal/controller/workloads/rolebasedgroup_controller.go:939
  `fresh = available != nil && available.ObservedGeneration >= child.Generation`
- sigs.k8s.io/lws@v0.7.0 has no status.observedGeneration field
  (api/leaderworkerset/v1/leaderworkerset_types.go:337-354) and its condition writer
  makeCondition (pkg/controllers/leaderworkerset_controller.go:779-803) never sets
  ObservedGeneration → `fresh` is permanently false → `ready` false → RBG Ready=False.

Log sources: job logs 120305554111 (lifecycle), 120305554103 (workload), 120305554227
(rollout), 120305554112 (feature), 120305554295 (v1alpha1), 120305554087 (manifest install),
downloaded from the GitHub Actions API on 2026-10-24.
