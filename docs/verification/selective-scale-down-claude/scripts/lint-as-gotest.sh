#!/usr/bin/env bash
# F1 canary (reviewer A harness, PR #538): the required `lint` CI gate.
#
# Emits go-test JSON with a single synthetic test, TestClaudeLintGateGreen,
# so the standard gotest parser in re-verify.sh can consume it:
#   PASS  iff golangci-lint exits 0 (gate green),
#   FAIL  iff golangci-lint reports issues (gate red, PR 07ae1194 state),
#   skip  iff no linter can be obtained on this machine.
#
# Uses the Makefile-pinned version (GOLANGCI_LINT_VERSION ?= v2.13.1); older
# versions cannot parse Go 1.27 export data and produce typecheck noise.
set -u

TEST_NAME="TestClaudeLintGateGreen"
PINNED_VERSION="v2.13.1"

resolve_bin() {
  if [ -n "${GOLANGCI_LINT_BIN:-}" ] && command -v "${GOLANGCI_LINT_BIN}" >/dev/null 2>&1; then
    printf '%s\n' "${GOLANGCI_LINT_BIN}"
    return 0
  fi
  if command -v golangci-lint >/dev/null 2>&1; then
    printf 'golangci-lint\n'
    return 0
  fi
  if [ -x "bin/golangci-lint" ]; then
    printf 'bin/golangci-lint\n'
    return 0
  fi
  echo "lint-canary: installing golangci-lint ${PINNED_VERSION}" >&2
  GOBIN="$(pwd)/bin" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${PINNED_VERSION}" >&2 || return 1
  [ -x "bin/golangci-lint" ] && printf 'bin/golangci-lint\n'
}

BIN="$(resolve_bin || true)"
if [ -z "${BIN:-}" ]; then
  echo "{\"Action\":\"skip\",\"Test\":\"${TEST_NAME}\",\"Package\":\"lint\"}"
  exit 0
fi

DETAIL="$(mktemp)"
# Scope: the packages the PR touched. The full ./... run matches CI but takes
# ~5 minutes; this scope reproduces every PR-attributed finding in seconds.
# Output must stay pure go-test JSON: the re-verify parser runs jq over the
# whole stream, so lint detail is re-emitted as "output" events.
emit() { # $1 = Action, rest = prefix for detail lines
  printf '{"Action":"%s","Test":"%s","Package":"lint"}\n' "$1" "$TEST_NAME"
  if [ "$1" = "fail" ]; then
    # escape backslash, quote, and tab (control chars break strict JSON)
    sed $'s/\\\\/\\\\\\\\/g; s/"/\\\\"/g; s/\t/\\\\t/g' "$DETAIL" | while IFS= read -r line; do
      [ -n "$line" ] || continue
      printf '{"Action":"output","Test":"%s","Package":"lint","Output":"%s\\n"}\n' "$TEST_NAME" "$line"
    done
  fi
}
if "$BIN" run ./internal/... ./pkg/... ./test/envtest/... >"$DETAIL" 2>&1; then
  emit pass
else
  emit fail
fi
rm -f "$DETAIL"
