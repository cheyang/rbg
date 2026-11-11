#!/usr/bin/env bash
# Layer L1 (unit / static-differential) for the preload-e2e-images harness.
#
# Contract under test (per PR #567's own claim): in E2E_IMAGE_REGISTRY=docker.io
# mode the pre-load "removes the overseas-runner dependency on the Hangzhou
# registry entirely", i.e. every image the e2e pods reference that is hosted on
# an *.aliyuncs.com registry must be present in the Kind node without a runtime
# pull, because overseas runners reach those registries only intermittently.
#
# Method:
#   1. Extract the image set referenced by e2e pod specs from Go sources
#      (test/e2e, test/wrappers, test/utils), resolving the DefaultImage const.
#   2. Run hack/preload-e2e-images.sh in a given mode against stub docker/kind
#      and record which images end up saved/loaded into the Kind node.
#   3. Assert coverage: every referenced aliyun-hosted image is loaded.
#
# Output: go-test-compatible JSON lines ({"Test":..,"Action":..}) so
# scripts/re-verify.sh (kind=gotest) can parse verdicts, plus a human-readable
# coverage table on stderr and results/preload-coverage-<mode>.txt.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOPIC_DIR="$(cd "$HERE/.." && pwd)"
REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

emit() { printf '{"Test":"%s","Action":"%s"}\n' "$1" "$2"; }
note() { printf '%s\n' "$*" >&2; }

MODE="${1:-docker.io}"
OUT_FILE="$TOPIC_DIR/results/preload-coverage-${MODE}.txt"
STUB_LOG="$(mktemp)"
trap 'rm -f "$STUB_LOG"' EXIT

is_image() {
  # looks like an image reference (has a tag or a registry/repo path), not a
  # container name like "pull-check" or a path like "/does-not-exist"
  case "$1" in
    /*) return 1 ;;
  esac
  printf '%s' "$1" | grep -Eq '^[a-z0-9][a-z0-9./_-]*(:[a-zA-Z0-9._-]+)?$' || return 1
  case "$1" in
    *:*) return 0 ;;                # has a tag
    *.*/*|*/*) return 0 ;;          # has a registry/repo path
    *) return 1 ;;
  esac
}
is_fake() {
  # intentionally-unpullable fixtures used by negative tests
  case "$1" in
    nonexistent-registry.example.com/*|registry.invalid/*) return 0 ;;
    *) return 1 ;;
  esac
}

# --- 1. images referenced by e2e pod specs ---------------------------------
ref_tmp="$(mktemp)"
{
  # Image: "..." / Images: []string{...} (literals)
  git grep -h -E 'Image(s)?[[:space:]]*[:=][[:space:]]*("|\[\]string)' HEAD \
    -- test/e2e test/wrappers test/utils 2>/dev/null \
    | grep -oE '"[^"]+"' | tr -d '"'
  # DefaultImage constant (referenced symbolically dozens of times)
  git show HEAD:test/utils/utils.go \
    | grep -oE 'DefaultImage[[:space:]]*=[[:space:]]*"[^"]+"' \
    | grep -oE '"[^"]+"' | tr -d '"'
} | while IFS= read -r line; do is_image "$line" && printf '%s\n' "$line"; done \
  | sort -u >"$ref_tmp"

referenced_aliyun="$(grep -E 'aliyuncs\.com' "$ref_tmp" || true)"
# fakes excluded from the coverage requirement
note "referenced images (pod specs, resolved consts):"
cat "$ref_tmp" | sed 's/^/  /' >&2

# --- 2. run the production script against stubs -----------------------------
mkdir -p "$TOPIC_DIR/results"
STUB_LOG="$STUB_LOG" \
PATH="$HERE/stub-bin:$PATH" \
E2E_IMAGE_REGISTRY="$MODE" \
E2E_IMAGE_PULL_BACKOFF=0 \
KIND_CLUSTER_NAME=rbgs-stub \
RUNNER_TEMP="$(mktemp -d)" \
bash hack/preload-e2e-images.sh >/dev/null 2>&1
# script exit code is irrelevant here (stubs succeed); the log is the evidence

pulled="$(grep '^PULL ' "$STUB_LOG" | awk '{print $2}' | sort -u)"
loaded="$(grep '^SAVE ' "$STUB_LOG" | awk '{print $2}' | sort -u)"
tagged="$(grep '^TAG ' "$STUB_LOG" | awk '{print $4}' | sort -u)"
tagged_from="$(grep '^TAG ' "$STUB_LOG" | awk '{print $2}' | sort -u)"

{
  echo "mode: $MODE"
  echo "== pulled =="
  printf '%s\n' "$pulled"
  echo "== tagged (src -> dst) =="
  grep '^TAG ' "$STUB_LOG" || true
  echo "== loaded into kind (docker image save targets) =="
  printf '%s\n' "$loaded"
  echo "== referenced by e2e pod specs =="
  cat "$ref_tmp"
} >"$OUT_FILE"

# --- 3. assertions (go-test JSON on stdout) ---------------------------------
# F1: testutils.DefaultImage (acs-sample/nginx:latest) must be loaded
default_image="$(git show HEAD:test/utils/utils.go \
  | grep -oE 'DefaultImage[[:space:]]*=[[:space:]]*"[^"]+"' \
  | grep -oE '"[^"]+"' | tr -d '"')"
if printf '%s\n' "$loaded" | grep -Fxq "$default_image"; then
  emit "F1_DefaultImage_loaded_in_${MODE}_mode" pass
else
  emit "F1_DefaultImage_loaded_in_${MODE}_mode" fail
fi
note "F1: DefaultImage=$default_image loaded=$(printf '%s' "$loaded" | tr '\n' ' ')"

# F2: patio-runtime image must be loaded (it is referenced by every suite's
# ClusterEngineRuntimeProfile via testutils.CreatePatioRuntime)
patio="$(git show HEAD:test/utils/utils.go \
  | grep -oE 'registry-cn-hangzhou[^"]+' | head -1)"
if printf '%s\n' "$loaded" | grep -Fxq "$patio"; then
  emit "F2_patio_runtime_loaded_in_${MODE}_mode" pass
else
  emit "F2_patio_runtime_loaded_in_${MODE}_mode" fail
fi
note "F2: patio=$patio loaded=$(printf '%s' "$loaded" | tr '\n' ' ')"

# Generalized contract: every referenced aliyun-hosted image is loaded.
missing=0
while IFS= read -r img; do
  [ -z "$img" ] && continue
  if ! printf '%s\n' "$loaded" | grep -Fxq "$img"; then
    note "COVERAGE GAP: referenced but not loaded: $img"
    missing=$((missing+1))
  fi
done <<<"$referenced_aliyun"
if [ "$missing" -eq 0 ]; then
  emit "G1_all_referenced_aliyun_images_loaded_${MODE}_mode" pass
else
  emit "G1_all_referenced_aliyun_images_loaded_${MODE}_mode" fail
fi

# P0-fix check: in docker.io mode the pre-load step itself must never contact
# an aliyuncs.com registry (this is the part of the PR's claim that IS true).
aliyun_pulls="$(printf '%s\n' "$pulled" | grep -c 'aliyuncs\.com' || true)"
if [ "${MODE}" = "docker.io" ]; then
  if [ "$aliyun_pulls" -eq 0 ]; then
    emit "P0fix_no_aliyun_pull_in_preload_step" pass
  else
    emit "P0fix_no_aliyun_pull_in_preload_step" fail
  fi
fi

rm -f "$ref_tmp"
