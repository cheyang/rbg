# LeaderWorkerSet → LeaderWorkerPattern migration parity

Prepared because the roadmap removes `Deployment` / `StatefulSet` / `LeaderWorkerSet` support
entirely, leaving `RoleInstanceSet` + the v1alpha2 pattern family. Whether
`LeaderWorkerPattern` actually covers what the LWS path did is the load-bearing claim of any
deprecation announcement, so it is checked here rather than assumed.

Sources: `sigs.k8s.io/lws@v0.7.0` (`api/leaderworkerset/v1/leaderworkerset_types.go`),
`pkg/reconciler/lws_reconciler.go`, `pkg/reconciler/roleinstanceset_reconciler.go`,
`pkg/discovery/{env_builder,injector}.go`, `api/workloads/v1alpha1/rolebasedgroup_types.go`.

---

## Headline

**The API surface is safe; the pod environment is not.**

RBG never exposed most of LWS. Its entire LWS knob set (`v1alpha1.LeaderWorkerTemplate`) was
three fields — `Size`, `PatchLeaderTemplate`, `PatchWorkerTemplate` — all three of which have
direct homes in `LeaderWorkerPattern`. So nothing meaningful is lost at the API level.

The break is one level lower and invisible to any API diff: the **environment variables user
containers read**. LWS injected `LWS_*`; RoleInstanceSet injects `RBG_LWP_*`. Same semantics,
different names, **no aliases**. A workload that reads the old names starts fine and fails at
runtime.

---

## Field-by-field

### What RBG actually exposed, and where it lands

| RBG LWS surface (v1alpha1) | `LeaderWorkerPattern` | Verdict |
|---|---|---|
| `size` | `size` | ✅ direct |
| `patchLeaderTemplate` | `leaderTemplatePatch` | ✅ direct |
| `patchWorkerTemplate` | `workerTemplatePatch` + `TemplateSource` | ✅ direct |
| role `replicas` | `RoleSpec.Replicas` → `RoleInstanceSetSpec.Replicas` | ✅ |
| role `rolloutStrategy.rollingUpdate` (`maxSurge`/`maxUnavailable`/`partition`) | same struct, role-level | ✅ carried by `lws_reconciler.go:301-330` on the old path, native on the new |
| role restart policy | `RestartPolicyConfig` | ✅ **superset** — adds `BaseDelaySeconds` / `MaxDelaySeconds` |

### LWS features RBG never used → no regression possible

| LWS field | RBG usage | Verdict |
|---|---|---|
| `leaderWorkerTemplate.subGroupPolicy` | never set — literal `// TODO support SubGroupPolicy` at `lws_reconciler.go:278` | ✅ never available, nothing to lose |
| `spec.networkConfig.subdomainPolicy` | never set (LWS default `Shared`) | ✅ and `LeaderWorkerPattern.SharedServiceSelection` is a **superset**: `LeaderOnly` routing is RoleInstanceSet-only, enforced by a CEL rule at `rolebasedgroup_types.go:199` |
| `rollingUpdateConfiguration.partition` | set from RBG's own strategy | ✅ |

### Things RBG implements itself, so they survive

| Concern | Mechanism | Verdict |
|---|---|---|
| Exclusive topology | RBG's own: `rbg.GetExclusiveKey()` → `GroupExclusiveTopologyKey` + `setExclusiveAffinities(...)` in `pod_reconciler.go:114-125`. Not an LWS annotation passthrough. | ✅ survives |
| Group identity labels | RBG's own `ComponentIndexLabelKey` / `ComponentSizeLabelKey` | ✅ |

---

## P1 — the real gap: the pod env contract is renamed with no alias

Proven deterministically by `TestVerifyPR416_P1_MigrationDropsTheLWSEnvContract`
(`pkg/discovery/pr416_lws_parity_test.go`), including a harness-bite guard that fails the test
outright if the replacements are absent (so it cannot pass or fail vacuously).

| Old path (LWS-backed) | New path (RoleInstanceSet + LeaderWorkerPattern) |
|---|---|
| `LWS_LEADER_ADDRESS` | `RBG_LWP_LEADER_ADDRESS` |
| `LWS_GROUP_SIZE` | `RBG_LWP_GROUP_SIZE` |
| `LWS_WORKER_INDEX` | `RBG_LWP_WORKER_INDEX` |

Who injects what, and why the split exists:

* `lws_reconciler.go:270` requests injectors `["config", "common_env"]` — deliberately **not**
  `lwp_env`, because the LWS controller itself adds the `LWS_*` trio ("Environment variable
  added to all containers in the LeaderWorkerSet", per the LWS API docs).
* `roleinstanceset_reconciler.go:379` and `:398` request
  `["config", "sidecar", "common_env", "lwp_env"]`, and `lwp_env` →
  `InjectLeaderWorkerSetEnv` → `BuildLwsEnv` → the `RBG_LWP_*` trio.

Observed env on the new path (full set):

```
RBG_GROUP_NAME  RBG_ROLE_NAME  RBG_ROLE_INDEX  RBG_ROLE_INSTANCE_NAME
RBG_COMPONENT_NAME  RBG_COMPONENT_INDEX
RBG_LWP_LEADER_ADDRESS  RBG_LWP_GROUP_SIZE  RBG_LWP_WORKER_INDEX
```

**Why this is the dangerous kind of break:** it is not an API rejection. The RBG is admitted,
the RoleInstanceSet is created, the pods run. The failure is inside the user's process, at
startup, when `$LWS_LEADER_ADDRESS` is empty. Nothing appears in the RBG status, no event, no
controller error. For RBG's primary workload — multi-node LLM inference, where the launcher
resolves the head node from exactly this variable — that is a silent outage.

**The shim already has its constants.** `api/workloads/v1alpha1/constant.go:280-286` declares
`DeprecatedEnvLwsLeaderAddress` / `DeprecatedEnvLwsWorkerIndex` / `DeprecatedEnvLwsGroupSize`.
They are referenced **nowhere** in the tree. Injecting them as aliases in `BuildLwsEnv`
alongside the `RBG_LWP_*` names is a ~3-line change and makes the migration transparent for
unmodified workloads.

## P2 — the one in-repo pointer to the new names is wrong

The deprecation comments a migrating user would consult name the wrong variables:

| `api/workloads/v1alpha1/constant.go` says | `api/workloads/constants/env.go` actually defines |
|---|---|
| `EnvRBGLeaderAddress ("RBG_LEADER_ADDRESS")` | `RBG_LWP_LEADER_ADDRESS` (:60) |
| `EnvRBGIndex ("RBG_INDEX")` | `RBG_LWP_WORKER_INDEX` (:63) |
| `EnvRBGSize ("RBG_SIZE")` | `RBG_LWP_GROUP_SIZE` (:67) |

So the migration is silent (P1) *and* the only documentation of the replacement names is
incorrect (P2). Cheap to fix, but it must be fixed before any announcement points users here.

*(Minor, related: the Go constant identifiers `EnvRBGLeaderAddress` / `EnvRBGIndex` /
`EnvRBGSize` omit the `LWP` that their values carry, which is probably how the stale comments
arose.)*

---

## P3 — startup ordering: NOT established, needs a runtime observation

The one gap I could not close by reading.

* LWS has `spec.startupPolicy`, defaulting to `LeaderCreated` (`+kubebuilder:default=LeaderCreated`),
  with `LeaderReady` as the alternative. RBG never sets it, so every LWS-backed role ran with
  `LeaderCreated` — workers are created as soon as the leader pod is *created*.
* The RoleInstanceSet side has `PodManagementPolicy` (`OrderedReady` / `Parallel`), which is
  StatefulSet-shaped, not leader/worker-shaped. I found no leader-readiness gating in
  `roleinstanceset_reconciler.go` or the roleinstance sync package.

Whether `LeaderCreated` ≡ `Parallel` in observable pod-creation order is a behavioural question
that code reading cannot settle, and I am not going to assert it either way. It needs one live
run comparing pod creation timestamps for an LWS-backed vs a LeaderWorkerPattern role. Flagging
it as open rather than guessing.

---

## Verdict

| # | Item | Status |
|---|---|---|
| — | API surface (`size`, template patches, replicas, rollout, restart policy) | ✅ full parity, restart policy is a superset |
| — | `subGroupPolicy`, `subdomainPolicy` | ✅ never used; `SharedServiceSelection` is a superset |
| — | Exclusive topology | ✅ RBG-native, survives |
| **P1** | Pod env renamed `LWS_*` → `RBG_LWP_*`, no aliases, silent runtime break | ❌ **must fix before announcing** |
| **P2** | Deprecation comments name the wrong replacement variables | ❌ fix |
| **P3** | Startup ordering (`LeaderCreated` vs `PodManagementPolicy`) | ⚠️ open, needs one live run |

**Bottom line for the announcement:** the migration story is sound and can be stated
confidently — *capabilities are preserved, several are improved* — but it must include an
explicit "update these three environment variables in your containers" step, and ideally ship
the alias shim so that step is optional rather than load-bearing. Do not publish the LWS
removal timeline until P1 and P2 are fixed and P3 is answered.
