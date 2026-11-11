#!/usr/bin/env bash
# Entry point for re-verify.sh (kind=gotest): runs both harness layers and
# emits one go-test-compatible JSON line per assertion on stdout.
# IMPORTANT: stdout must stay pure JSON (re-verify.sh merges stderr into the
# parsed output); human-readable notes go to results/run-all.log.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOPIC_DIR="$(cd "$HERE/.." && pwd)"
LOG="$TOPIC_DIR/results/run-all.log"
mkdir -p "$TOPIC_DIR/results"
: >"$LOG"
bash "$HERE/audit-image-coverage.sh" docker.io 2>>"$LOG"
bash "$HERE/audit-image-coverage.sh" aliyun 2>>"$LOG"
bash "$HERE/script-behavior-tests.sh" 2>>"$LOG"
