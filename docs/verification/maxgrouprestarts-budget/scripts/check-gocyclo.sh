#!/usr/bin/env bash
# F2 contract: calculateDiffsWithExpectation must satisfy the repo's gocyclo
# threshold (min-complexity 30 in .golangci.yml). On the reviewed head it is 33,
# which is why the lint CI job fails.
set -euo pipefail
f=pkg/reconciler/roleinstance/sync/instance_scale.go
v=$(/tmp/gobin/gocyclo -over 30 "$f" | awk '{print $1}' | head -1)
if [ -z "$v" ]; then echo "gocyclo OK: all functions <= 30"; exit 0; fi
echo "gocyclo FAIL: complexity $v > 30 in $f"; exit 1
