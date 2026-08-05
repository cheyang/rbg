# Design note — how should the install/upgrade guard actually work?

Reviewer-private analysis attached to the verification of
[PR #416](https://github.com/sgl-project/rbg/pull/416). This is **not** a finding;
it is the design exploration behind two of them:

- **F4** — `templates/upgrade-guard.yaml` (new in this PR) hard-fails *every*
  `helm upgrade`, breaking the command the repo documents in 10 places.
- **R3-F22** — the "fresh installation only" premise that justifies the whole
  `enabled=false` design is enforced nowhere.

Everything below was measured on a real cluster (ACK cn-hongkong, k8s
v1.36.1-aliyun.1, helm v3.16.3), not reasoned from documentation. Reproduce with
`scripts/11-guard-design-probe.sh` (read-only) and `scripts/12-guard-design-e2e.sh`
(throwaway namespaces, self-cleaning). Raw output in
[`results/round3/design/`](results/round3/design/).

## The stated intent

> 目的是暂时禁止 v0.8.0-alpha.3 之前的版本升级
> *(the goal is to temporarily forbid upgrades from versions before v0.8.0-alpha.3)*

The shipped implementation is:

```gotemplate
{{- if .Release.IsUpgrade }}
{{- fail "Upgrade is not supported for this chart version. Please uninstall and reinstall." }}
{{- end }}
```

That expresses "**refuse all upgrades**", which is strictly broader than the intent
and is what produces F4. So: what *can* a chart know at render time, and what should
it check?

## Measured constraint 1 — the previous chart version is not readable

| Candidate source | Reality |
|---|---|
| Deployment labels | only `control-plane: rbgs-controller` + Helm's `managed-by`. **No version label.** |
| Helm release Secret `sh.helm.release.v1.<rel>.v<n>` | labels are `name/owner/status/version`, where `version` is the **revision number** (`"1"`), not the chart version. The chart version is gzipped inside `data`, which a template cannot decompress. |

So a chart that wants to know "what version am I upgrading from" must **write that
fact down itself**.

## Measured constraint 2 — `lookup` is blind on every client-side path

| Invocation | `lookup` result |
|---|---|
| `helm template` | **EMPTY** |
| `helm template --is-upgrade` | **EMPTY** |
| `helm install --dry-run` (client) | **EMPTY** |
| `helm template --dry-run=server` | populated |
| real `helm install` / `helm upgrade` | populated |

Consequence: a template-level guard **cannot distinguish "old install" from "cannot
tell"**. A fail-closed template check therefore false-fails `helm diff upgrade` and
similar client-side tooling. (`helm template` itself is safe — `IsUpgrade` is false
there, so the guard never fires; ArgoCD uses this path.)

## Measured constraint 3 — `semverCompare` is fine, for once

Masterminds/semver is fussy about prerelease-vs-release constraints, so this was
worth checking rather than assuming. `semverCompare "< 0.8.0-alpha.3"`:

| Installed | `< 0.8.0-alpha.3` |
|---|---|
| `0.5.0-alpha.3` | true |
| `0.7.0` | true |
| `0.8.0-alpha.1` | true |
| `0.8.0-alpha.2` | true |
| `0.8.0-alpha.3` | false |
| `0.8.0-alpha.4` | false |
| `0.8.0` | false |
| `0.9.0` | false |

All eight correct. Version comparison is not the risky part of this design.

## Option A — chart-version marker + `semverCompare` (template `fail`)

The chart ships a `ConfigMap` recording `{{ .Chart.Version }}`; the guard looks it
up on upgrade and refuses when it is below the threshold, with an escape hatch for
"cannot tell".

Verified end-to-end (`12-guard-design-e2e.sh`, part 1):

| Scenario | Result |
|---|---|
| first `helm upgrade --install` (IsUpgrade=false) | succeeds, marker written |
| **re-run `helm upgrade --install`** | **succeeds — this is the F4 fix** |
| marker patched to `0.8.0-alpha.1` | refused, message names the old version |
| marker deleted | refused, message points at the escape hatch |
| `--set upgrade.allowUnsupported=true` | passes |
| release state after two refusals | still `deployed`, **no failed revision** |

**But Option A cannot deliver its own stated goal.** The marker ships with *this PR*,
while **`0.8.0-alpha.3` is already released without it** (`e9ab0a84`). An upgrade
from alpha.3 therefore lands in the "no marker" branch — exactly the version the
intent wants to *allow*. There is no way to retrofit a marker into a released chart,
so "allow upgrades from >= alpha.3" is unreachable by marker alone; operators would
have to pass the escape hatch once regardless.

A related trap: the PR branch's `Chart.yaml` is still `version: 0.8.0-alpha.2`,
*below* the threshold. Shipping as-is would write a marker of `alpha.2`, and the next
upgrade would be refused **by the chart's own guard**. Needs a rebase onto alpha.3+.

## Option B — check real cluster state (recommended)

Instead of a version proxy, ask the question that actually matters: *is this cluster
in a state this chart version can serve?* Two signals:

1. **Deprecated-workload objects** (for R3-F22) — scan `RoleBasedGroup` /
   `RoleBasedGroupSet` and refuse when `enabled=false` and any role's **effective**
   workload type is one of the deprecated three.
2. **Migration era** (replacing the version guard) — whether v1alpha1-era objects
   still exist. The CRD's `status.storedVersions` is readable via `lookup`
   (this cluster: `["v1alpha2"]`; `spec.versions` shows
   `v1alpha1(storage=false) v1alpha2(storage=true)`) and is maintained by Kubernetes
   itself — no bootstrap hole.

   **Caveat:** `storedVersions` is *sticky*. It only shrinks when an operator
   explicitly patches it, so a fully-migrated cluster can still list `v1alpha1`.
   Use it as a corroborating signal; make the actual object scan the gate.

### Which carrier — template `fail` or a hook Job?

Measured (`12-guard-design-e2e.sh`, part 2):

| | template `fail` + `lookup` | pre-install/pre-upgrade hook Job |
|---|---|---|
| release state on refusal | untouched, stays `deployed` | becomes `failed`, revision recorded |
| recovery | nothing to recover | plain `helm upgrade` works, **no `--force`** |
| `helm diff upgrade` | `lookup` EMPTY → false-fails if fail-closed | **hooks are not executed → cannot false-fail** |
| where the logic lives | Go templates | real Go — **can reuse `isDeprecatedWorkloadType`** |
| sees real state | only on real install/upgrade | always |

**A hook Job is the better carrier**, for two reasons that outweigh the failed
revision it leaves behind:

- **`helm diff upgrade` skips hooks entirely**, so the false-failure problem that
  sinks a fail-closed template check simply does not arise.
- **No logic duplication.** Deciding "does this role use a deprecated workload type"
  means handling the `rbg.workloads.x-k8s.io/role-workload-type` annotation and the
  v1alpha1 defaulting chain. Reimplementing that in Go templates would be a third
  hand-maintained copy — and this PR has already produced two findings of exactly
  that class (R2-F14's gate list, R3-F23's rule counting). A Job can run the
  controller image with a `preflight` subcommand and share the constants.

## Recommendation

1. **Replace the `IsUpgrade` blanket refusal with a state check** carried by a
   `pre-install,pre-upgrade` hook Job. This closes F4 (the documented
   `helm upgrade --install` becomes idempotent again on a healthy cluster, so none of
   the 10 call sites need changing) and R3-F22 with one mechanism.
2. **Gate on objects, not version numbers** — refuse `enabled=false` when
   deprecated-workload objects exist; refuse upgrades when v1alpha1-era objects
   remain. Keep `storedVersions` as a corroborating signal only.
3. **`hook-weight` must be below `-4`** — `crd-upgrade.yaml` is a `-4`
   `pre-install,pre-upgrade` hook. A check that runs after it inspects CRDs the
   upgrade job has already rewritten. Use `-10`, and do **not** set a `hook-failed`
   delete policy, so a failed check's logs survive for diagnosis.
4. **Needs its own ServiceAccount + RBAC** (cluster-wide read on CRDs and
   `rolebasedgroups`/`rolebasedgroupsets`). `rbgs-crds-upgrade` is the in-chart
   precedent.
5. **Keep a controller startup self-check as defence in depth, but not in this PR.**
   A Helm hook only covers Helm. The gap is narrower than it first appears:
   `deploy/kubectl/manifests.yaml` carries **no** `--enable-deprecated-workload-types`
   flag and grants the deprecated RBAC unconditionally, so the kubectl path is always
   `true` and therefore safe. The only uncovered route is hand-editing the Deployment
   args, which is out-of-band and low priority.

## Scope and safety of these experiments

Read-only against the shared cluster except for two throwaway namespaces
(`pr416-guard-test`, `pr416-hook-test`) holding a scratch chart whose only real
resource is a ConfigMap. No cluster-scoped object was created, so nothing could
collide with the unrelated `rbgs` release live in `rbg-system` (whose ClusterRole,
ClusterRoleBinding and ValidatingWebhookConfiguration have fixed names). Both
namespaces are deleted by an `EXIT` trap.

---

# Addendum — is `status.storedVersions` alone enough? And a working prototype

Two follow-up questions, both answered by measurement:
`scripts/13-preflight-prototype.sh`, raw output in
[`results/round3/design/preflight-prototype.txt`](results/round3/design/preflight-prototype.txt).

## 1. `storedVersions` alone is not enough — it is the wrong axis

It is a reasonable instinct (simpler, Kubernetes-maintained, no bootstrap hole), and for
the **upgrade-era gate** it does work: `v1alpha1` in `status.storedVersions` means
v1alpha1-era objects were persisted. The only caveat is that it is *sticky* — it shrinks
only when an operator explicitly patches it — so a fully-migrated cluster can be refused.
An escape hatch covers that.

**For R3-F22 it cannot work at all.** The effective workload type is a *role annotation*,
entirely independent of which API version the object is stored in:

```go
// api/workloads/v1alpha2/rolebasedgroup_types.go:258
func (r *RoleSpec) GetWorkloadType() string {
	if r.Annotations != nil {
		if wt := r.Annotations[constants.RoleWorkloadTypeAnnotationKey]; wt != "" {
			return wt
		}
	}
	return constants.RoleInstanceSetWorkloadType // default
}
```

So a **v1alpha2** object can carry `rbg.workloads.x-k8s.io/role-workload-type:
apps/v1/StatefulSet`, and that is precisely the object that becomes unreconcilable under
`enabled=false`. A cluster full of them still reports `storedVersions: ["v1alpha2"]` —
i.e. "clean".

Demonstrated both ways:

| | Input | `storedVersions` would say | The check says |
|---|---|---|---|
| A | the real cluster, 8 RoleBasedGroups | `["v1alpha2"]` — clean | `offenders=0` ✓ agrees |
| B | one **v1alpha2** object, role annotated `apps/v1/StatefulSet` | `["v1alpha2"]` — **clean** | `ns1/legacy-rbg role=worker` ✗ **caught** |

Row B is the whole argument: the two signals disagree exactly where it matters.

## 2. The check is much simpler than first proposed — no Go binary needed

Because `GetWorkloadType()` is just "read the role annotation, default `RoleInstanceSet`"
— no defaulting chain to reimplement — the earlier recommendation to build a Go
`preflight` binary and extend the controller image was **over-engineered, and is
withdrawn**. The whole check is a few lines of `jq` and fits the existing
`tools/crd-upgrade` shape (alpine + kubectl + a script):

```bash
kubectl get rolebasedgroups.workloads.x-k8s.io,rolebasedgroupsets.workloads.x-k8s.io -A -o json \
| jq -r --arg k rbg.workloads.x-k8s.io/role-workload-type '
    [ .items[]
      | .metadata as $m
      | ( (.spec.roles // []), (.spec.groupTemplate.spec.roles // []) )[]
      | select( (.annotations[$k] // "workloads.x-k8s.io/v1alpha2/RoleInstanceSet")
                | . == "apps/v1/Deployment"
                  or . == "apps/v1/StatefulSet"
                  or . == "leaderworkerset.x-k8s.io/v1/LeaderWorkerSet" )
      | "  \($m.namespace)/\($m.name)  role=\(.name)  type=\(.annotations[$k])" ] | .[]'
```

Non-empty → print and `exit 1`. The three literals are the only duplication left; exporting
a `DeprecatedWorkloadTypes` slice from `api/workloads/v1alpha2` and generating this list
from it would also close R2-F14's residual, but that is optional polish, not a blocker.

## 3. Prototype — all four mechanics verified

A working hook Job was built and run against the real cluster:

| Check | Result |
|---|---|
| `enabled=true` (default) renders no preflight objects | **0 objects** — no cost on the default path |
| preflight completes before the crd-upgrade stage | preflight end `07:15:40.283` → crd-upgrade start `07:15:45` ✓ |
| fails fast, with the check's own message | helm exited in **7s** (not the 180s timeout); pod log carries the object list |
| refuses before the CRD-upgrade stage | crd-upgrade Job never created ✓ |

The refusal the operator actually sees:

```
REFUSING INSTALL: controller.deprecatedWorkloadTypes.enabled=false, but these objects
already use a deprecated workload type:
  ns1/legacy-rbg  role=worker  type=apps/v1/StatefulSet
They would become unreconcilable: no RBAC is granted for those types and the webhook
would refuse every write.
Keep enabled=true on this cluster until a migration path to RoleInstanceSet ships.
```

### The settings that matter, and why

| Setting | Value | Why not the `crd-upgrade` value |
|---|---|---|
| `restartPolicy` | **`Never`** + `backoffLimit: 0` | `crd-upgrade` uses `OnFailure` because it is meant to succeed. A check that is *expected* to fail would restart forever under `OnFailure`, and the operator would see a Helm timeout instead of the reason. |
| `hook-weight` | RBAC `-7`, Job `-6` | must precede `crd-upgrade`'s `-5`/`-4`, or the CRDs are already rewritten when the check runs |
| `hook-delete-policy` (Job) | `hook-succeeded,before-hook-creation` — **no `hook-failed`** | a failed check's pod must survive so its log can be read |
| RBAC | read-only `get,list` on the two CRs | narrower than `crd-upgrade`'s `create/update/patch` on CRDs |
| render condition | only when `enabled=false` | the default path should not pay for a Job that can never say no |

## Two harness bugs found and fixed while building this

Recorded because they were briefly mistaken for design problems:

1. `hook-delete-policy: hook-succeeded` removed the Job before its log could be read, so
   the ordering check looked empty. The prototype drops `hook-succeeded` and notes that
   production should keep it.
2. Scenario 3 initially reported "ordering is broken". It was stale state: hook resources
   are **not** removed by `helm uninstall` when the delete policy omits `hook-succeeded`,
   so the previous scenario's crd-upgrade Job was still present. The script now deletes
   both Jobs before the offender run. Also, `helm --set-string` cannot parse a JSON blob
   whose keys contain dots and brackets — the fixture goes through a values file instead.

## Safety

Throwaway namespace `pr416-preflight-test`, throwaway release, read-only ServiceAccount,
and an `EXIT` trap that removes the namespace plus the two cluster-scoped RBAC objects.
The offender case is driven from a **fixture**, deliberately not by creating a real
RoleBasedGroup: the live rbgs controller on this cluster watches cluster-wide and would
have reconciled one into real StatefulSets.
