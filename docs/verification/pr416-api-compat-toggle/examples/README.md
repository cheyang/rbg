# Proposed preflight guard — complete, tested example

A working implementation of the check recommended in
[`../UPGRADE-GUARD-DESIGN.md`](../UPGRADE-GUARD-DESIGN.md) and in inline comment #1 on
[PR #416](https://github.com/sgl-project/rbg/pull/416#discussion_r3717866540).

**These files live here, not under `tools/` or `deploy/`, on purpose.** This is a
reviewer's branch and the production tree is deliberately untouched; copy them into
place if you want to adopt them.

| File | Goes to | What it is |
|---|---|---|
| `check-deprecated-workloads.sh` | `tools/preflight/` | the check itself |
| `preflight.yaml` | `deploy/helm/rbgs/templates/preflight/` | the hook Job + read-only RBAC |

Both are exercised by
[`../scripts/14-preflight-script-test.sh`](../scripts/14-preflight-script-test.sh)
(12 cases) and
[`../scripts/13-preflight-prototype.sh`](../scripts/13-preflight-prototype.sh)
(end-to-end against a real cluster). Captured output is in
[`../results/round3/design/`](../results/round3/design/).

## Adopting it — three edits beyond dropping in the files

**1. Add `jq` to the CRD-upgrader image.** One word on the existing `apk` line in
`tools/crd-upgrade/Dockerfile`, and copy the script in:

```dockerfile
RUN apk add --update --no-cache bash curl jq && \
    chmod +x /rbgs/ensure-crds-up-to-date.sh /rbgs/check-deprecated-workloads.sh
COPY ./tools/preflight/check-deprecated-workloads.sh /rbgs/check-deprecated-workloads.sh
```

The template reuses that image rather than introducing a third one — it already has
`kubectl`, and it is already pulled on the install path.

**2. Add the values block** to `deploy/helm/rbgs/values.yaml`:

```yaml
# Preflight check run before install/upgrade when the deprecated workload types are
# disabled. It refuses the operation if the cluster already contains objects that
# use one, which would otherwise be left unreconcilable.
preflight:
  enabled: true
  maxReported: 20
  image: {}
```

**3. Mention the failure mode in `NOTES.txt` / the chart README.** When the check
refuses an *upgrade*, Helm records a `failed` revision. Recovery needs no `--force` —
a normal `helm upgrade` after fixing the cause succeeds — but operators should know
the `failed` entry is expected rather than damage.

## The four settings that must differ from `crd-upgrade.yaml`

| Setting | Value | Why not the `crd-upgrade` value |
|---|---|---|
| `restartPolicy` | `Never` + `backoffLimit: 0` | `crd-upgrade` uses `OnFailure` because it is meant to succeed. A check that is *expected* to fail restarts forever under `OnFailure`, and the operator sees a Helm timeout instead of the reason. **This is the trap.** |
| `hook-weight` | RBAC `-7`, Job `-6` | must precede `crd-upgrade`'s `-5`/`-4`, or the CRDs are already rewritten when the check reads them |
| `hook-delete-policy` (Job) | `hook-succeeded,before-hook-creation` — **no `hook-failed`** | the failed pod must survive so `kubectl logs` can show which objects are the problem |
| render condition | only when `enabled=false` | a check that can never say no should not run on the default path |

## Exit codes, and why `2` exists

```
0  nothing uses a deprecated workload type (or the toggle is on -- no-op)
1  offenders found; they are listed
2  the check could not be completed (API error, missing RBAC)
```

`2` is the one that matters. A preflight that exits `0` when it could not see the
cluster is worse than no preflight, because it converts *unknown* into *approved*.
The script therefore separates two cases that both make `kubectl get` fail:

- **the CRD is not installed** — expected on a genuinely fresh cluster → treated as
  "no objects", continue;
- **anything else** (unreachable API, denied RBAC) → exit `2`.

Both directions are covered by the test suite, since getting this backwards is
silent.

## What it checks — an allowlist, not a list of deprecated types

The effective workload type is just the role annotation, defaulting to
`RoleInstanceSet` — see
[`rolebasedgroup_types.go:258`](https://github.com/sgl-project/rbg/blob/aac6056d5c615dae4c90ce8431d404043c5d2032/api/workloads/v1alpha2/rolebasedgroup_types.go#L258).
There is no defaulting chain to reproduce, which is why this is a short shell script
rather than a Go binary. Both kinds are covered by one `jq` filter reading
`.spec.roles` and `.spec.groupTemplate.spec.roles`, so a `RoleBasedGroupSet` template
cannot slip past.

The check refuses **anything that is not `RoleInstanceSet`**, rather than matching a
list of deprecated types. `constants/external.go:43-46` declares exactly four workload
types and `RoleInstanceSet` is the only one that is not deprecated, so the two are
equivalent today — verified, identical verdicts on all four plus an unannotated role.

The allowlist is preferred for two reasons:

- **There is no second list to keep in sync.** An earlier draft duplicated the three
  deprecated type strings; that duplication is exactly the drift class behind R2-F14
  and R3-F23.
- **It fails closed.** A fourth deprecated type added to `isDeprecatedWorkloadType` is
  caught here automatically. With a denylist, forgetting to update it means silently
  approving an install that will strand objects.

The cost, stated plainly: this is **stricter** than `isDeprecatedWorkloadType`. An
unknown type is refused too (pinned by a test). That is the better direction — such an
object cannot be reconciled anyway, since `NewWorkloadReconciler` returns
`unsupported workload type` for it — but if a fifth, genuinely supported workload type
is ever added, it must be appended to `SUPPORTED_TYPES`. A missed deprecated type
strands objects silently; a false refusal names the object and is fixable.

## Test coverage

`14-preflight-script-test.sh`, 12 cases, all passing:

| Group | Cases |
|---|---|
| toggle on | short-circuits even with offenders present |
| clean inputs | roles with no annotation (default `RoleInstanceSet`); no objects at all |
| each deprecated type | `StatefulSet` and `Deployment` on a RoleBasedGroup, `LeaderWorkerSet` via a RoleBasedGroupSet `groupTemplate` |
| output | 25 offenders with `MAX_REPORTED=5` — capped, and says how many were hidden |
| **fail-closed** | an unknown type (`acme.io/v1/FutureThing`) is refused, not waved through |
| **failure direction** | unreachable API → `2`, not `0`; both CRDs absent → `0` (fresh cluster); one absent, one present |
| live | the real cluster, toggle off → `0` |

`13-preflight-prototype.sh` additionally verified the Helm mechanics against a real
cluster: nothing rendered on the default path, the preflight completing before the
CRD-upgrade stage (`07:15:40.283` → `07:15:45`), a 7-second failure rather than the
180-second timeout, and the crd-upgrade Job never being created when the check
refuses.

The template was also rendered inside the real chart: 0 objects at `enabled=true`;
SA + ClusterRole + ClusterRoleBinding + Job at `enabled=false`, with weights
`-7/-7/-7` and `-6` against crd-upgrade's `-5`/`-4`; the preflight ClusterRole holds a
single read-only rule on `rolebasedgroups`/`rolebasedgroupsets`; `helm lint` clean. The chart was
restored afterwards — the production tree on this branch is unchanged.

## Why there is no `checkStoredVersions`

An earlier draft had an optional arm that also refused when a CRD still listed
`v1alpha1` in `status.storedVersions`, as a replacement for the blanket
`upgrade-guard.yaml`. It was dropped: it added an option, an extra RBAC rule and a
caveat, in exchange for an imprecise signal.

`status.storedVersions` is **sticky** — demonstrated in
[`../scripts/15-storedversions-stickiness.sh`](../scripts/15-storedversions-stickiness.sh):

| Step | `storedVersions` |
|---|---|
| v1 is the storage version, one object written | `["v1"]` |
| storage version migrated to v2 | `["v1","v2"]` |
| **every object rewritten as v2** | `["v1","v2"]` — unchanged |
| explicit operator `status` patch (control) | `["v2"]` |

So "contains `v1alpha1`" means "`v1alpha1` was a storage version at some point and
nobody cleaned up the bookkeeping", not "`v1alpha1` objects exist". Gating on it would
refuse clusters that are in fact fully migrated.

**Consequence worth being explicit about:** with that arm gone, this preflight says
nothing about F4. `upgrade-guard.yaml` still needs its own decision — either remove it
(and handle the "no upgrades from before `0.8.0-alpha.3`" intent through release
notes), or keep the blanket refusal and accept that `helm upgrade --install` works
only once per cluster.
