#!/usr/bin/env bash
# KEP-284 static checks: pin the claims the KEP makes about the current
# codebase. Run from the repository root. Exit 0 iff all checks pass.
set -uo pipefail

fail=0
ok()   { echo "PASS: $1"; }
bad()  { echo "FAIL: $1"; fail=1; }

# --- P0 premise: the operator's Pod cache is unfiltered today -----------------
# cmd/rbgs/main.go cacheOptions() filters Service/StatefulSet/Deployment by
# group-name EXISTS but must NOT list corev1.Pod — that is the overly broad
# cache scope the KEP exists to fix.
cache_block="$(sed -n '/func cacheOptions() cache.Options/,/^}/p' cmd/rbgs/main.go)"
if echo "$cache_block" | grep -q "corev1.Pod{}"; then
  bad "P0: cacheOptions already filters Pods — the KEP premise needs re-validation"
else
  ok "P0: cacheOptions has no Pod filter (operator caches all Pods today)"
fi
for t in 'corev1.Service{}' 'appsv1.StatefulSet{}' 'appsv1.Deployment{}'; do
  if echo "$cache_block" | grep -q "$t"; then
    ok "P0: cacheOptions filters $t by group-name (matches issue #284 description)"
  else
    bad "P0: cacheOptions no longer filters $t — KEP background section is stale"
  fi
done

# --- C2: getExistingPod is a cached Get on the RoleInstance create path -------
if grep -q "func (c \*realControl) getExistingPod" pkg/reconciler/roleinstance/sync/instance_scale.go \
   && grep -q "existingPod, err := c.getExistingPod" pkg/reconciler/roleinstance/sync/instance_scale.go; then
  ok "C2: getExistingPod exists and is called from the pod-create path (KEP item 1 target)"
else
  bad "C2: getExistingPod call chain changed — KEP item 1 / alpha criteria reference stale code"
fi

# --- C5: the managed label does not exist yet (KEP introduces it) -------------
if grep -rn "workloads.x-k8s.io/managed" --include="*.go" api/ pkg/ internal/ cmd/ | grep -v test/kep284 | grep -q .; then
  bad "C5: an rbg 'managed' label already exists in code — KEP novelty claim is stale"
else
  ok "C5: no existing 'managed' label constant — KEP introduces it as a new marker"
fi

# --- D1: kep.yaml sanity -------------------------------------------------------
ky=keps/284-pod-cache-filter/kep.yaml
grep -q "^kep-number: 284" "$ky"          && ok "D1: kep-number 284"            || bad "D1: kep-number mismatch"
grep -q "^status: provisional" "$ky"      && ok "D1: status provisional"        || bad "D1: status mismatch"
grep -q "issues/284" "$ky"                && ok "D1: see-also issue #284"       || bad "D1: see-also missing issue 284"
grep -q 'KEP-284' keps/284-pod-cache-filter/README.md && ok "D1: README titled KEP-284" || bad "D1: README title mismatch"

# --- D2: performance arithmetic consistency ------------------------------------
python3 "$(dirname "$0")/check-perf-numbers.py" && ok "D2: performance/README numbers internally consistent" || bad "D2: performance numbers inconsistent"

exit $fail
