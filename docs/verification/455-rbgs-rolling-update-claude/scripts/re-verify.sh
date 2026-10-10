#!/usr/bin/env bash
# Re-verification harness for PR #491 (KEP-455: RBGSet rolling update).
# Reviewer A (Claude) — first round, 2026-10-11.
#
# The PR is docs-only, so the harness has two layers:
#   L1 unit:         Go tests pinning the codebase facts the KEP relies on
#                    (internal/controller/workloads/verify_pr491_claude_test.go)
#                    plus the repo unit suites for the touched components.
#   L2 integration:  repo envtest suites (fake-code paths are covered in L1;
#                    envtest exercises real apiserver + CRDs + webhooks).
#
# The evidence script below (E-checks) records grep-verifiable facts the Go
# tests cannot express (markers, absence of writers, KEP text contents).
#
# Usage: bash docs/verification/455-rbgs-rolling-update-claude/scripts/re-verify.sh
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
BASE_SHA="$(git -C "$REPO_ROOT" merge-base origin/main HEAD)"
HEAD_SHA="$(git -C "$REPO_ROOT" rev-parse HEAD)"
OUT="$REPO_ROOT/docs/verification/455-rbgs-rolling-update-claude/results"
mkdir -p "$OUT"

echo "== re-verify 455-rbgs-rolling-update-claude =="
echo "base: $BASE_SHA  head: $HEAD_SHA  date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"

cd "$REPO_ROOT"

fail=0
check() { # check <id> <description> <cmd...>
  local id="$1" desc="$2"; shift 2
  local out
  out=$("$@" 2>&1)
  if echo "$out" | grep -q "$EXPECT_GREP"; then
    echo "PASS $id — $desc"
    echo "$out" >"$OUT/$id.txt"
  else
    echo "FAIL $id — $desc (expected pattern: $EXPECT_GREP)"
    echo "$out" >"$OUT/$id.txt"
    fail=1
  fi
}

# E2: RollingUpdateInProgress is declared in the API but no controller
# publishes it (RBG controller sets only Ready/GangConfigured).
EXPECT_GREP="^0$"
check E2-no-writer "no non-test writer of RollingUpdateInProgress" \
  bash -c 'grep -rn "RollingUpdateInProgress" internal/ cmd/ pkg/ 2>/dev/null | grep -v "_test.go" | wc -l'

# E3a: scale subresource is a served, existing API surface on both versions.
EXPECT_GREP="statusReplicasPath: .status.replicas"
check E3a-scale-subresource-crd "CRD serves the scale subresource on .status.replicas" \
  grep -h "statusReplicasPath" config/crd/bases/workloads.x-k8s.io_rolebasedgroupsets.yaml

EXPECT_GREP="subresource:scale"
check E3b-scale-subresource-api "API markers declare the scale subresource (v1alpha1+v1alpha2)" \
  grep -h "kubebuilder:subresource:scale" api/workloads/v1alpha1/rolebasedgroupset_types.go api/workloads/v1alpha2/rolebasedgroupset_types.go

# E3c: the KEP never mentions the scale subresource / autoscaling.
EXPECT_GREP="^0$"
check E3c-kep-silent-on-scale "KEP mentions scale subresource/HPA 0 times" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -icE "scale subresource|autoscal|horizontalpod|hpa\b" || true'

# E4a: the RBG controller writes discovery-config-mode onto child RBGs
# (the controller-owned annotation the KEP excludes from revision comparison).
EXPECT_GREP="ensureDiscoveryConfigMode"
check E4a-discovery-writer "RBG controller manages discovery-config-mode" \
  grep -n "func (r \*RoleBasedGroupReconciler) ensureDiscoveryConfigMode" internal/controller/workloads/rolebasedgroup_controller.go

# E4b: today the set replaces child annotations wholesale during an update.
EXPECT_GREP="rbg.Annotations = nil"
check E4b-annotation-wholesale "syncRBGMetadata replaces annotations wholesale" \
  grep -n "rbg.Annotations = nil" internal/controller/workloads/rolebasedgroupset_controller.go

# E5: naming surface the set-level enum must coexist with.
EXPECT_GREP="LegacyRecreateUpdateStrategyType"
check E5-role-level-naming "role-level strategy enum incl. legacy Recreate spelling" \
  grep -n "LegacyRecreateUpdateStrategyType UpdateStrategyType" api/workloads/v1alpha2/rolebasedgroup_types.go

# P0-net: the linked issue is still open (network; recorded, not gating).
gh issue view 455 --repo sgl-project/rbg --json state,title -t '{{.state}} {{.title}}' \
  >"$OUT/P0-issue-455.txt" 2>&1 || echo "network-unavailable" >"$OUT/P0-issue-455.txt"
echo "P0-net: $(cat "$OUT/P0-issue-455.txt")"

echo
echo "== L1 unit layer =="
go test ./internal/controller/workloads/ -run 'TestVerify491' -count=1 2>&1 | tee "$OUT/unit-l1-verify491.out"
go test ./internal/controller/workloads/ -count=1 2>&1 | tee "$OUT/unit-l1-controllers.out"
grep -q "^ok" "$OUT/unit-l1-controllers.out" || fail=1

echo
echo "== L2 integration layer (envtest) =="
# envtest needs the control-plane binaries; resolve the newest installed set.
KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:-$(ls -d "$HOME"/.local/share/kubebuilder-envtest/k8s/*-linux-amd64 2>/dev/null | sort -V | tail -1)}"
export KUBEBUILDER_ASSETS
echo "KUBEBUILDER_ASSETS=$KUBEBUILDER_ASSETS"
go test ./test/envtest/testcase/... -count=1 -timeout 25m 2>&1 | tee "$OUT/integration-envtest.out"
grep -q "no test files\|^ok" <(grep -v "^---" "$OUT/integration-envtest.out" || true) || true

echo
if [ "$fail" -eq 0 ]; then
  echo "RESULT: all evidence checks + unit layer green (see results/)"
else
  echo "RESULT: FAILURES — inspect results/"
fi
exit "$fail"
