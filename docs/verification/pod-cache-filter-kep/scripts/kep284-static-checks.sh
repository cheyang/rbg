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
if grep -rn "workloads.x-k8s.io/managed" --include="*.go" api/ pkg/ internal/ cmd/ | grep -v '_test.go' | grep -q .; then
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


# --- Round-3 revision checks (head d1c331ae) ----------------------------------
# R2-F1: the measurement campaign must be pinned to an isolated envtest control
# plane regardless of the caller's environment (script export AND TestMain pin).
if grep -q '^export USE_EXISTING_CLUSTER=false' test/podcacheunion/scripts/transform-campaign.sh \
   && grep -q 'UseExistingCluster: *ptr.To(false)' test/podcacheunion/envtest_test.go; then
  ok "R2-F1: campaign pins USE_EXISTING_CLUSTER=false and TestMain pins UseExistingCluster:false"
else
  bad "R2-F1: campaign can escape to the kubeconfig cluster via USE_EXISTING_CLUSTER"
fi

# R2-F2: something in CI or the Makefile must compile the podcacheunion-tagged
# package, so the KEP's evidence cannot rot silently on a dependency bump.
if git grep -q "podcacheunion" -- Makefile .github/ 2>/dev/null; then
  ok "R2-F2: CI/Makefile references the podcacheunion build tag"
else
  bad "R2-F2: nothing in CI/Makefile compiles test/podcacheunion (tag referenced nowhere)"
fi

# R2-F3: the read-path audit must not claim "No code reads Pods by spec.nodeName"
# (RecordNodeBindings and five other sites do); the qualified sentence must name
# the scoped-source reason.
if grep -q 'No code reads Pods by `spec.nodeName`' keps/284-pod-cache-filter/README.md; then
  bad "R2-F3: bare 'No code reads Pods by spec.nodeName' claim is present but false"
elif grep -q 'RecordNodeBindings' keps/284-pod-cache-filter/README.md \
  && grep -q 'nodeName' keps/284-pod-cache-filter/README.md; then
  ok "R2-F3: nodeName audit sentence qualified (RecordNodeBindings named, scoped sources)"
else
  bad "R2-F3: nodeName audit sentence missing or rewritten without the RecordNodeBindings qualification"
fi

# R2-F4: medianRange must average the two middle values for even-sized groups
# (was: upper-middle only). Contract check on the averaging branch.
if sed -n '/^func medianRange/,/^}/p' test/podcacheunion/transform_measure_test.go | grep -q 'len(sorted)%2 == 0'; then
  ok "R2-F4: medianRange averages even-sized groups"
else
  bad "R2-F4: medianRange still reports the upper-middle value for even-sized groups"
fi

# N2: the sync-budget claim must be derivable from performance.md (no unbacked
# '1.14 seconds' quote; the 344-807 ms envelope is tabulated in the startup detail).
if grep -q '1.14 seconds' keps/284-pod-cache-filter/README.md; then
  bad "N2: sync-budget still quotes the underivable 1.14s sample"
elif grep -q '344.*807\|807.*344' keps/284-pod-cache-filter/README.md \
  && grep -q 'startup-detail-200-related--4800-unrelated' keps/284-pod-cache-filter/README.md; then
  ok "N2: sync budget quotes the tabulated 344-807 ms envelope with a performance.md anchor"
else
  bad "N2: sync-budget sentence changed in an unexpected way"
fi

# N3: GOMEMLIMIT operator guidance for memory-bound filtered deployments.
if grep -q 'GOMEMLIMIT' keps/284-pod-cache-filter/README.md; then
  ok "N3: README carries GOMEMLIMIT guidance for filtered deployments"
else
  bad "N3: no GOMEMLIMIT guidance in the KEP README"
fi

# N1-repo-half: PR-body 'fixes #XXXX' placeholder cannot be checked from the tree
# (checked via gh, recorded in results/round3). The repo half: kep.yaml must not
# resurrect the title mismatch fixed in round 2.
grep -q 'Pod Cache Filtering with a Transform Stub' keps/284-pod-cache-filter/kep.yaml \
  && grep -q 'Pod Cache Filtering with a Transform Stub' keps/284-pod-cache-filter/README.md \
  && ok "N1-repo: kep.yaml/README titles aligned" \
  || bad "N1-repo: kep.yaml/README title mismatch regressed"

# R3-scope: the KEP's scoping claim matches the prototype (wrapper wraps only
# *corev1.Pod*; vendored controller-runtime's default NewInformer is plain
# NewSharedIndexInformer, so other types keep the default constructor).
if grep -q 'if _, isPod := obj.(\*corev1.Pod); isPod' test/podcacheunion/transform_stream.go \
   && grep -q 'newInformer := cache.NewSharedIndexInformer' vendor/sigs.k8s.io/controller-runtime/pkg/cache/internal/informers.go; then
  ok "R3-scope: NewInformer wrapper is Pod-scoped and the default constructor is untouched for other types"
else
  bad "R3-scope: wrapper scoping or the vendored default constructor changed"
fi

exit $fail
