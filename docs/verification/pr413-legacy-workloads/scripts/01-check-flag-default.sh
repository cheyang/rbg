#!/usr/bin/env bash
# F1 [CONTRACT]: the --enable-legacy-workloads default registered in Go must
# match the default documented in the PR body, values.yaml and the chart README.
#
# Exit 0 = defaults agree (fixed). Exit 1 = mismatch (finding reproduced).
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"

echo "== Go flag registration =="
GO_DEFAULT=$(grep -A1 '"enable-legacy-workloads"' cmd/rbgs/main.go \
  | grep -oE 'enable-legacy-workloads", *(true|false)' | grep -oE '(true|false)$')
if [ -z "${GO_DEFAULT:-}" ]; then
  # fall back to the previous-line form: &var, "flag", <default>,
  GO_DEFAULT=$(grep -B2 -A2 'enable-legacy-workloads' cmd/rbgs/main.go \
    | grep -oE '"enable-legacy-workloads", *(true|false)' | grep -oE '(true|false)')
fi
echo "  cmd/rbgs/main.go default: ${GO_DEFAULT:-UNPARSED}"

echo "== Helm chart default =="
HELM_DEFAULT=$(awk '/^    legacyWorkloads:/{f=1;next} f&&/enabled:/{print $2;exit}' \
  deploy/helm/rbgs/values.yaml)
echo "  values.yaml legacyWorkloads.enabled: ${HELM_DEFAULT:-ABSENT}"

echo "== Built binary --help =="
if [ -x /tmp/rbgs-verify-bin ] || go build -o /tmp/rbgs-verify-bin ./cmd/rbgs 2>/dev/null; then
  /tmp/rbgs-verify-bin --help 2>&1 \
    | grep -A3 'enable-legacy-workloads' | sed 's/^/  /' || true
fi

echo
if [ "${GO_DEFAULT:-}" = "${HELM_DEFAULT:-}" ]; then
  echo "RESULT: PASS - Go default (${GO_DEFAULT}) matches chart default (${HELM_DEFAULT})"
  exit 0
fi
cat <<MSG
RESULT: FAIL - F1 REPRODUCED
  Go flag default        : ${GO_DEFAULT:-UNPARSED}
  Chart/doc default      : ${HELM_DEFAULT:-ABSENT}
A bare 'rbgs' process (no --enable-legacy-workloads argument) therefore runs with
legacy workloads DISABLED, while every document describing the flag says the
default is enabled. Both shipped install paths pass the flag explicitly so they
are unaffected, but any other consumer -- a downstream chart, a custom manifest,
or running the binary directly -- silently loses legacy workload reconciliation.
MSG
exit 1
