#!/usr/bin/env bash
# Premise/substance check (integration layer): the round-1 harness envtest
# specs (B2 stable-period reset clobbered; B4b apiserver accepts negative
# baseDelaySeconds on RoleInstance) reproduce against the PR #5 base.
#
# Expected (bug present, matching results/layer2-envtest.txt committed in the PR):
#   [It] B2: RestartCount>1 must reset to 1 ...  FAILED  (count stuck at 5)
#   [It] B4: apiserver must reject negative ...  FAILED  (RoleInstance err = <nil>)
#
# Needs KUBEBUILDER_ASSETS (setup-envtest 1.31.0); downloads them on first use.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
RESULTS="$(cd "$(dirname "${BASH_SOURCE[0]}")/../results" && pwd)"
BASE_SHA="0c0fcc1183b9fdd5ae705e06500515eedf6ced48"
ROUND1="f592f533"
OUT="$RESULTS/round1-envtest-repro.txt"
: >"$OUT"

say() { echo "$*" | tee -a "$OUT"; }

# envtest binaries: resolved from the main checkout (bin/ is not tracked).
if [ ! -x "$REPO_ROOT/bin/setup-envtest" ]; then
  (cd "$REPO_ROOT" && GOBIN="$REPO_ROOT/bin" go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest) >>"$OUT" 2>&1
fi
ASSETS="$(cd "$REPO_ROOT" && ./bin/setup-envtest use 1.31.0 --bin-dir bin -p path 2>/dev/null)"
case "$ASSETS" in /*) ;; *) ASSETS="$REPO_ROOT/$ASSETS";; esac   # absolute: the test runs in a worktree
say "KUBEBUILDER_ASSETS=$ASSETS"

say "=== Round-1 integration-layer reproduction at base $BASE_SHA with round-1 harness $ROUND1 ==="

WT="$(mktemp -d /tmp/pr5-e.XXXXXX)"
git -C "$REPO_ROOT" worktree add --detach "$WT" "$BASE_SHA" >/dev/null 2>&1
git -C "$WT" checkout "$ROUND1" -- \
  pkg/reconciler/roleinstance/sync/restart_backoff_verify_test.go \
  test/envtest/testcase/restart_policy/backoff_bug_verify_test.go

(cd "$WT" && KUBEBUILDER_ASSETS="$ASSETS" \
  go test ./test/envtest/testcase/restart_policy/... -run TestRestartPolicy \
  -ginkgo.focus 'PR394 Restart Backoff Bug Verification' -timeout 10m) >>"$OUT" 2>&1
RUN_RC=$?
git -C "$REPO_ROOT" worktree remove --force "$WT" >/dev/null 2>&1
say "go test rc=$RUN_RC (non-zero expected: the specs assert the intended contract)"

b2_obs="$(grep -oE 'B2 observed RestartCount=[0-9]+' "$OUT" | sort -u | tr '\n' ' ')"
b4_ri="$(grep -E 'B4 create RoleInstance' "$OUT" | tail -1)"
b4_rbg="$(grep -E 'B4 create RBG' "$OUT" | tail -1 | cut -c1-140)"
say "B2 observed counts: ${b2_obs:-<none>}"
say "B4 RBG:    ${b4_rbg:-<none>}"
say "B4 RoleInst: ${b4_ri:-<none>}"

ok=1
grep -qE 'FAIL.*B2: RestartCount' "$OUT" || { say "  B2 spec did not FAIL (unexpected)"; ok=0; }
grep -qE 'FAIL.*B4: apiserver' "$OUT" || { say "  B4 spec did not FAIL (unexpected)"; ok=0; }
echo "$b4_ri" | grep -q 'err = <nil>' || { say "  B4 RoleInstance create unexpectedly errored"; ok=0; }

if [ "$ok" -eq 1 ]; then
  say "VERDICT: REPRODUCED — B2 reset clobbered (stuck at 5) and B4b validation gap (RoleInstance accepts -30)."
  echo "INTEG-REPRO REPRODUCED"
  exit 0
fi
say "VERDICT: NOT REPRODUCED."
echo "INTEG-REPRO NOT-REPRODUCED"
exit 1
