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
(11 cases) and
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
  # Also refuse when a CRD still lists v1alpha1 in status.storedVersions. Off by
  # default: storedVersions is sticky and only shrinks when an operator patches it,
  # so it can refuse a cluster that is in fact fully migrated.
  checkStoredVersions: false
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

## What it checks, and the one thing to keep in sync

The effective workload type is just the role annotation, defaulting to
`RoleInstanceSet` — see
[`rolebasedgroup_types.go:258`](https://github.com/sgl-project/rbg/blob/aac6056d5c615dae4c90ce8431d404043c5d2032/api/workloads/v1alpha2/rolebasedgroup_types.go#L258).
There is no defaulting chain to reproduce, which is why this is a short shell script
rather than a Go binary.

Both kinds are covered by one `jq` filter reading `.spec.roles` and
`.spec.groupTemplate.spec.roles`, so a `RoleBasedGroupSet` template cannot slip past.

The only duplicated knowledge is the three type strings in `DEPRECATED_TYPES`, which
must track `isDeprecatedWorkloadType`. Exporting a `DeprecatedWorkloadTypes` slice
from `api/workloads/v1alpha2` and generating this list from it would remove that, and
would also close R2-F14's remaining fail-open in `hack/gen-helm-rbac`'s
`mustGroupResources` — worth doing, but not a blocker.

## Test coverage

`14-preflight-script-test.sh`, 11 cases, all passing:

| Group | Cases |
|---|---|
| toggle on | short-circuits even with offenders present |
| clean inputs | roles with no annotation (default `RoleInstanceSet`); no objects at all |
| each deprecated type | `StatefulSet` and `Deployment` on a RoleBasedGroup, `LeaderWorkerSet` via a RoleBasedGroupSet `groupTemplate` |
| output | 25 offenders with `MAX_REPORTED=5` — capped, and says how many were hidden |
| **failure direction** | unreachable API → `2`, not `0`; both CRDs absent → `0` (fresh cluster); one absent, one present |
| live | the real cluster, toggle off → `0` |

`13-preflight-prototype.sh` additionally verified the Helm mechanics against a real
cluster: nothing rendered on the default path, the preflight completing before the
CRD-upgrade stage (`07:15:40.283` → `07:15:45`), a 7-second failure rather than the
180-second timeout, and the crd-upgrade Job never being created when the check
refuses.

The template was also rendered inside the real chart: 0 objects at `enabled=true`;
SA + ClusterRole + ClusterRoleBinding + Job at `enabled=false`, with weights
`-7/-7/-7` and `-6` against crd-upgrade's `-5`/`-4`; `helm lint` clean. The chart was
restored afterwards — the production tree on this branch is unchanged.
