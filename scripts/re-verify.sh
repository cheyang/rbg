#!/usr/bin/env bash
# Re-verify PR #479. Resolves the current PR head from the manifest and the
# delta start from .last-reviewed; runs the unit harness.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

PR_URL="https://github.com/sgl-project/rbg/pull/479"
TOPIC="pr479-isolate-unhealthy-timers"
MANIFEST="docs/verification/${TOPIC}/verify-manifest.json"
BASE_COMMIT=$(python3 -c "import json;print(json.load(open('${MANIFEST}'))['baseCommit'])")
HEAD_COMMIT=$(python3 -c "import json;print(json.load(open('${MANIFEST}'))['headCommit'])")

echo "==> PR ${PR_URL}"
echo "==> base (premise): ${BASE_COMMIT}  head (fix): ${HEAD_COMMIT}"
echo "==> last-reviewed: $(cat .last-reviewed 2>/dev/null || echo '<none>')"

echo
echo "=== HEAD unit harness (premise-fix + PR tests) ==="
go test -mod=vendor -race \
  -run 'TestPremiseCrossSetTimerIsolated_HEAD|TestObserveInstanceHealthAndIsStablyUnhealthy|TestUpdateStatefulInstanceSetRetriesUnhealthyRollout|TestInstanceHealthRecreation|TestPruneInstanceHealth' \
  ./pkg/reconciler/roleinstanceset/statefulmode -count=1

echo
echo "=== go vet ==="
go vet -mod=vendor ./pkg/reconciler/roleinstanceset/statefulmode

echo
echo "=== Premise (P0) against BASE — requires a base ${BASE_COMMIT} checkout ==="
echo "Run from a worktree at ${BASE_COMMIT}:"
echo "  git worktree add /tmp/rbg-base ${BASE_COMMIT}"
echo "  cp docs/verification/${TOPIC}/premise_crossset_base_test.go \\"
echo "     /tmp/rbg-base/pkg/reconciler/roleinstanceset/statefulmode/premise_crossset_test.go"
echo "  (cd /tmp/rbg-base && go test -mod=vendor -run TestPremiseCrossSetTimerWipe ./pkg/reconciler/roleinstanceset/statefulmode -count=1 -v)"

echo
echo "=== Harness-bites (remove the set-scope guard on head, expect failures) ==="
echo "Edit pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_control.go:"
echo "  delete the 'if healthKey.instanceSet.Namespace != set.Namespace || ... { return true }' guard"
echo "  in observeInstanceHealth's Range, then re-run the HEAD unit harness — the cross-set"
echo "  tests must FAIL. Revert after."

echo
echo "==> Done. Live A/B reproduction: see docs/verification/${TOPIC}/README.md"
