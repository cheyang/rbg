#!/usr/bin/env bash
# No live layer for this PR — the defect is a count-unit mismatch in a pure
# calculation (CalculateScalingForAllCoordination), fully decided at the unit layer.
set -euo pipefail
echo "no live setup needed; run scripts/re-verify.sh"
