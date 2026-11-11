#!/usr/bin/env bash
# Layer L2 (integration, stubbed container runtime) for the preload-e2e-images
# harness. Runs the real hack/preload-e2e-images.sh against stub docker/kind and
# checks its observable behavior: registry-mode dispatch, re-tagging, retries,
# exit codes, and (documenting current behavior) the aliyun-mode aliasing and
# the post-final-attempt backoff sleep.
#
# Output: go-test-compatible JSON lines for scripts/re-verify.sh.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOPIC_DIR="$(cd "$HERE/.." && pwd)"
REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

emit() { printf '{"Test":"%s","Action":"%s"}\n' "$1" "$2"; }
note() { printf '%s\n' "$*" >&2; }

STUB_LOG="$(mktemp)"
RUNNER_TMP="$(mktemp -d)"
mkdir -p "$TOPIC_DIR/results"
trap 'rm -f "$STUB_LOG"; rm -rf "$RUNNER_TMP"' EXIT

run_script() { # env passthrough via exported vars set by the caller
  PATH="$HERE/stub-bin:$PATH" \
  RUNNER_TEMP="$RUNNER_TMP" \
  bash hack/preload-e2e-images.sh
}

# --- F5a: invalid registry mode is rejected ---------------------------------
STUB_LOG="$STUB_LOG" E2E_IMAGE_REGISTRY=bogus \
E2E_IMAGE_PULL_BACKOFF=0 run_script >/dev/null 2>&1
if [ $? -eq 1 ] && ! grep -q '^PULL' "$STUB_LOG"; then
  emit F5a_invalid_registry_rejected pass
else
  emit F5a_invalid_registry_rejected fail
fi
: >"$STUB_LOG"

# --- F5b: retry exhaustion fails the step (with retries observed) ------------
STUB_LOG="$STUB_LOG" \
STUB_FAIL_PATTERN='.*' \
E2E_IMAGE_REGISTRY=docker.io \
E2E_IMAGE_PULL_ATTEMPTS=3 \
E2E_IMAGE_PULL_BACKOFF=0 run_script >/dev/null 2>&1
rc=$?
pull_count=$(grep -c '^PULL' "$STUB_LOG" || true)
if [ "$rc" -eq 1 ] && [ "$pull_count" -eq 3 ]; then
  emit F5b_retry_exhaustion_exits_1 pass
else
  emit F5b_retry_exhaustion_exits_1 fail
  note "F5b: rc=$rc pull_count=$pull_count (expected rc=1, pulls=3)"
fi
: >"$STUB_LOG"

# --- F3 (canary, documents current behavior): aliyun mode re-tags both
# nginx:latest and nginx:1.25 from the SAME mirror image --------------------
STUB_LOG="$STUB_LOG" E2E_IMAGE_REGISTRY=aliyun E2E_IMAGE_PULL_BACKOFF=0 run_script >/dev/null 2>&1
tag_125="$(grep '^TAG' "$STUB_LOG" | grep 'nginx:1.25' || true)"
tag_latest="$(grep '^TAG' "$STUB_LOG" | grep 'nginx:latest' | head -1 || true)"
src_125="$(printf '%s\n' "$tag_125" | awk '{print $2}')"
src_latest="$(printf '%s\n' "$tag_latest" | awk '{print $2}')"
if [ -n "$tag_125" ] && [ -n "$src_125" ] && [ "$src_125" = "$src_latest" ]; then
  # current (suspect) behavior: both tags alias one mirror blob
  emit F3_aliyun_mode_retag_aliases_versions pass
else
  emit F3_aliyun_mode_retag_aliases_versions fail
fi
note "F3: nginx:1.25 tagged from '$src_125', nginx:latest from '$src_latest'"
aliyun_mode_results="$STUB_LOG"

# --- F4 (canary, documents current behavior): backoff sleep runs even after
# the FINAL failed attempt ----------------------------------------------------
: >"$STUB_LOG"
start=$(date +%s%N)
STUB_LOG="$STUB_LOG" \
STUB_FAIL_PATTERN='registry.k8s.io/pause.*' \
E2E_IMAGE_REGISTRY=docker.io \
E2E_IMAGE_PULL_ATTEMPTS=2 \
E2E_IMAGE_PULL_BACKOFF=2 run_script >/dev/null 2>&1
end=$(date +%s%N)
elapsed_ms=$(( (end - start) / 1000000 ))
pause_pulls=$(grep -c '^PULL registry.k8s.io/pause' "$STUB_LOG" || true)
# 2 attempts -> 2 sleeps (one after each failure, incl. the last) = ~4000ms.
# Correct behavior would sleep only between attempts (~2000ms).
if [ "$pause_pulls" -eq 2 ] && [ "$elapsed_ms" -ge 3800 ]; then
  emit F4_backoff_sleeps_after_final_attempt pass
elif [ "$pause_pulls" -eq 2 ] && [ "$elapsed_ms" -lt 2200 ]; then
  emit F4_backoff_sleeps_after_final_attempt fail
else
  # ambiguous timing (loaded machine): report what we saw, don't guess
  emit F4_backoff_sleeps_after_final_attempt fail
  note "F4: elapsed=${elapsed_ms}ms pause_pulls=$pause_pulls (expected >=3800ms for 2 sleeps)"
fi
: >"$STUB_LOG"

# --- coverage evidence for aliyun mode re-use by the audit layer -------------
# (F1/F2 in aliyun mode: the loaded set uses the SHORT target names only)
STUB_LOG="$STUB_LOG" E2E_IMAGE_REGISTRY=aliyun E2E_IMAGE_PULL_BACKOFF=0 run_script >/dev/null 2>&1
loaded_aliyun="$(grep '^SAVE ' "$STUB_LOG" | awk '{print $2}' | sort -u | tr '\n' ' ')"
note "aliyun-mode loaded set: $loaded_aliyun"
{
  echo "aliyun-mode loaded set (docker image save targets):"
  printf '%s\n' "$loaded_aliyun" | tr ' ' '\n' | sed '/^$/d' | sed 's/^/  /'
} >"$TOPIC_DIR/results/preload-loaded-set-aliyun.txt"

cp "$aliyun_mode_results" "$TOPIC_DIR/results/stub-log-aliyun.txt" 2>/dev/null || true
