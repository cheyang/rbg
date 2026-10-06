#!/usr/bin/env bash
# Instrumented re-run of the BASE (pre-PR) backoff spec: polls pod phases/UIDs,
# RoleInstance restart tracking and events every 2s while the old test runs in
# a merge-base worktree. Output: results/observer-B2.log + results/runB2-old-test.log
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
RESULTS="$REPO_ROOT/docs/verification/pr489-restart-backoff-e2e/results"
BASE="${BASE_REF:-7ed1860c2841dd911ac8c01e1458a6b9ce5ee49a}"
WT="$(mktemp -d)/rbg-base"
mkdir -p "$RESULTS"
git -C "$REPO_ROOT" worktree add "$WT" "$BASE"
trap 'git -C "$REPO_ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || true; kill ${OBS_PID:-0} 2>/dev/null || true' EXIT

# observer loop
(
  while true; do
    echo "=== $(date -u +%H:%M:%S.%3N)"
    kubectl get pods -A -l workloads.x-k8s.io/group-name=e2e-backoff-test \
      -o json 2>/dev/null | jq -r '.items[] | "\(.metadata.name) uid=\(.metadata.uid[:8]) phase=\(.status.phase) rc=\(.status.containerStatuses[0].restartCount // "-") del=\(.metadata.deletionTimestamp != null)"' || true
    kubectl get roleinstance -A 2>/dev/null | grep e2e-backoff-test || true
    kubectl get roleinstance -A -l workloads.x-k8s.io/group-name=e2e-backoff-test \
      -o json 2>/dev/null | jq -r '.items[] | "RI \(.metadata.name) restartCount=\(.status.restartCount // "-") lastRestart=\(.status.lastRestartTime // "-") conds=\([.status.conditions[]? | "\(.type)=\(.status)"] | join(","))"' || true
    sleep 2
  done
) > "$RESULTS/observer-B2.log" 2>&1 &
OBS_PID=$!

cd "$WT"
set +e
go test ./test/e2e/ -run TestE2E -v -ginkgo.v \
  --ginkgo.focus='RecreateRoleInstanceOnPodRestart with backoff delays second recreation' \
  -timeout 15m 2>&1 | tee "$RESULTS/runB2-old-test.log"
rc=${PIPESTATUS[0]}
set -e
kill $OBS_PID 2>/dev/null || true
echo "[F2] instrumented old-test exit=$rc"
