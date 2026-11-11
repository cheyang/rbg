#!/usr/bin/env bash
# Entry point for re-verify.sh (kind=gotest): runs both harness layers and
# emits one go-test-compatible JSON line per assertion on stdout.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
bash "$HERE/audit-image-coverage.sh" docker.io
bash "$HERE/audit-image-coverage.sh" aliyun
bash "$HERE/script-behavior-tests.sh"
