# Manual replay of the old spec's flow with full observation (2026-10-06, UTC)

Cluster: aliyun k8s v1.36.2, existing in-cluster controller v0.9.0-eadb6c20 (restart/backoff
code identical to PR merge-base; PR is test-only). RBG `rbg-verify-manual/e2e-backoff-manual`,
role-1 replicas=1, leaderWorkerPattern size=2, restartPolicyConfig
{type=RecreateRoleInstanceOnPodRestart, baseDelaySeconds=90, maxDelaySeconds=300}
— same shape as the e2e spec.

## Crash #1 — patch pod -0-0 status.phase=Failed (what SetPodFailed does)

- T0 07:16:21 patch accepted. By t+2s the old pod was gone; by t+4s both pods recreated with
  new UIDs. No backoff on the first crash (instance restartCount was 0).
- Event: `07:16:16Z ReCreateInstance ... (restartCount=1)` (node/controller clock ~5s behind
  the client host; all controller-side timestamps here carry that skew).
- After recovery: RoleInstance restartCount=1, lastRestartTime=07:16:16Z, Ready=True,
  Restarting cleared. Backoff expiry = lastRestartTime + 90s = **07:17:46Z**.

## Crash #2 — same patch at T2=07:17:02 (44s of backoff remaining)

Polled every 3s; pod -0-0 stayed `Failed` (rc=0, same UID 993d71aa), sibling Running,
instance restartCount stayed 1 — **no recreation during the backoff window**:

```
07:17:08..07:17:45  role-1-0-0/Failed/rc0/993d71aa   role-1-0-1/Running/rc0/db6c803d   RI rc=1
```

At backoff expiry (07:17:46) the controller recreated the instance — new UIDs, rc 1→2,
stable afterwards (observed to 07:19:08, no restart loop):

```
07:17:48  role-1-0-0/Failed/rc0/993d71aa   role-1-0-1/Running/rc0/db6c803d   RI rc=1
07:17:51  role-1-0-0/Pending/rc0/c5e6bae3  role-1-0-1/Succeeded/rc0/db6c803d RI rc=2
07:17:54  role-1-0-0/Running/rc0/c5e6bae3  role-1-0-1/Running/rc0/7b745a55   RI rc=2
```

Events: `07:17:46Z ReCreateInstance (restartCount=2)` + SuccessfulDelete/SuccessfulCreate x2.

## Contrast: bare pod on the same cluster (results/p0-demo.log)

```
t+3s phase=Failed   restartCount=0
t+6s phase=Failed   restartCount=0
t+9s phase=Running  restartCount=0   <- kubelet rewrote the status; container never killed
```

So on this kubelet the patched `Failed` phase is not durable for unmanaged pods (reverts to
Running in 6-9s), while for these RBG-managed pods it happened to survive the whole window —
the old spec's pass/fail hinges on kubelet behavior the test does not control. On the CI
kind-1.31 kubelet the same patch ended the pod as `Succeeded` (container killed, exit 0),
which is what broke the old spec there (runs 37258247385, 35751708068, 35559402297).
