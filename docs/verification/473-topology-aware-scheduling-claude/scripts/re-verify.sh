#!/usr/bin/env bash
# re-verify.sh — re-run the review harness for PR #473 (KEP-473) against an
# updated (fixed) PR head and report, per finding, Fixed / Still-present /
# Partial / Harness-update, applying TEST POLARITY.
#
# Adapted from the review-finding-verifier skill's stock re-verify.sh for this
# docs-only PR: the "unit" layer is a python claim-check over the KEP text (canary
# semantics: DEFECT-PRESENT = finding still live; DEFECT-ABSENT = flipped = fixed),
# the "integration" layer is `go test` strict-decoding the KEP's example YAML
# against the vendored upstream API types.
#
# Usage (from a checkout of the branch that CONTAINS this harness):
#   bash scripts/re-verify.sh [<fixed-ref>]
# With no ref, the current PR head is resolved from manifest.pr
# (git fetch <repo-url> pull/473/head) — machine-independent.
#
# Requires: git, python3, go, jq. Exit 0 iff every finding is fixed.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOPIC_DIR="$(dirname "$HERE")"
MANIFEST="$TOPIC_DIR/verify-manifest.json"

command -v jq >/dev/null || { echo "re-verify: jq required" >&2; exit 2; }
command -v python3 >/dev/null || { echo "re-verify: python3 required" >&2; exit 2; }
git rev-parse --git-dir >/dev/null 2>&1 || { echo "re-verify: not a git repo" >&2; exit 2; }
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "re-verify: working tree has uncommitted tracked changes; commit or stash first" >&2
  exit 2
fi

FIXED_REF="${1:-}"
if [ -z "$FIXED_REF" ]; then
  PR_URL="$(jq -r '.pr // empty' "$MANIFEST")"
  rest="${PR_URL#*://}"; host="${rest%%/*}"; path="${rest#*/}"
  owner="$(printf '%s' "$path" | cut -d/ -f1)"
  repo="$(printf '%s' "$path" | cut -d/ -f2)"
  num="$(printf '%s' "$path" | sed -E 's#.*/pull/([0-9]+).*#\1#')"
  cloneurl="https://$host/$owner/$repo.git"
  echo "re-verify: resolving PR head via 'git fetch $cloneurl pull/$num/head'"
  git fetch --quiet "$cloneurl" "pull/$num/head" || { echo "re-verify: fetch failed" >&2; exit 2; }
  FIXED_REF="$(git rev-parse FETCH_HEAD)"
fi
echo "re-verify: fixed-ref = $FIXED_REF"

LAST_REVIEWED_FILE="$TOPIC_DIR/.last-reviewed"
if [ -s "$LAST_REVIEWED_FILE" ]; then
  LAST_REVIEWED="$(tr -d '[:space:]' < "$LAST_REVIEWED_FILE")"
else
  LAST_REVIEWED="$(git merge-base origin/main "$FIXED_REF" 2>/dev/null || echo '')"
fi
echo "re-verify: last-reviewed = ${LAST_REVIEWED:-<none>}  (review delta = ${LAST_REVIEWED:-base}..$FIXED_REF)"

RESULTS_DIR="$TOPIC_DIR/results/reverify"
mkdir -p "$RESULTS_DIR"
# The manifest and results live under the harness dir, which `git checkout -f
# FIXED_REF` will wipe (the fixed ref carries no harness). Copy the manifest to a
# temp dir outside the repo so reads survive the checkout; the harnessPaths graft
# below restores the rest (including results/) inside the repo.
RUNTIME_DIR="$(mktemp -d)"
cp "$MANIFEST" "$RUNTIME_DIR/manifest.json"
MANIFEST="$RUNTIME_DIR/manifest.json"
ORIG_REF="$(git symbolic-ref --quiet --short HEAD || git rev-parse HEAD)"
HARNESS_SRC="$(git rev-parse HEAD)"
cleanup() { git checkout -f "$ORIG_REF" >/dev/null 2>&1 || true; rm -rf "$RUNTIME_DIR" 2>/dev/null || true; }
trap cleanup EXIT

git checkout -f "$FIXED_REF" >/dev/null 2>&1 || { echo "re-verify: cannot checkout $FIXED_REF" >&2; exit 2; }

# graft the harness (docs/verification/<topic> + nothing else — production tree
# of the PR stays pristine so the evidence is unambiguous)
HPATHS="$(jq -r '.harnessPaths | join(" ")' "$MANIFEST")"
# shellcheck disable=SC2086
git checkout "$HARNESS_SRC" -- $HPATHS || { echo "re-verify: failed to graft harness" >&2; exit 2; }

# ---- unit layer: python claim-check ---------------------------------------
echo "re-verify: running unit layer (check_claims.py)" >&2
python3 -I "$HERE/check_claims.py" --repo "$(git rev-parse --show-toplevel)" \
  --evidence "$TOPIC_DIR/results" > "$RESULTS_DIR/unit.out" 2>&1 || true

# unit-layer parse: "<id> <polarity> <verdict>" -> id<TAB>state
#   canary: DEFECT-PRESENT -> pass (still present), DEFECT-ABSENT -> fail (flipped)
#   contract: PASS -> pass, FAIL -> fail
awk '/^F[0-9]+ +canary/ {print $1 "\t" ($3=="DEFECT-PRESENT" ? "pass" : "fail")}
     /^P[0-9]+ +contract/ {print $1 "\t" ($3=="PASS" ? "pass" : "fail")}
     /^C[0-9]+ +contract/ {print $1 "\t" ($3=="PASS" ? "pass" : "fail")}' \
  "$RESULTS_DIR/unit.out" > "$RESULTS_DIR/unit.parsed" || true

# ---- integration layer: go test strict-decode ------------------------------
echo "re-verify: running integration layer (go test)" >&2
HARNESS_PKG="$(jq -r '.layers.integration.pkg // "docs/verification/473-topology-aware-scheduling-claude/harness"' "$MANIFEST")"
( cd "$(git rev-parse --show-toplevel)" && \
  go test "./$HARNESS_PKG/" -json > "$RESULTS_DIR/integration.out" 2>&1 ) || true
if grep -q '"Action":"build-fail"' "$RESULTS_DIR/integration.out" || grep -q 'build failed' "$RESULTS_DIR/integration.out" || \
   ! grep -q '"Action":"\(pass\|fail\)"' "$RESULTS_DIR/integration.out"; then
  : > "$RESULTS_DIR/integration.parsed"; touch "$RESULTS_DIR/integration.buildfail"
else
  jq -rs '[.[]|select(.Test!=null and (.Action=="pass" or .Action=="fail"))]
          | .[] | "\(.Test)\t\(.Action)"' \
    "$RESULTS_DIR/integration.out" > "$RESULTS_DIR/integration.parsed" 2>/dev/null || true
fi

lookup_unit() { # $1 = finding id
  awk -F'\t' -v id="$1" '$1==id{print $2}' "$RESULTS_DIR/unit.parsed" | tail -1
}
lookup_go() { # $1 = test-name substring
  [ -f "$RESULTS_DIR/integration.buildfail" ] && { echo build; return; }
  awk -F'\t' -v m="$1" 'index($1,m){print $2}' "$RESULTS_DIR/integration.parsed" | tail -1
}

printf '\n================  RE-VERIFY  ================\n'
printf '%-6s %-9s %-12s %-16s %s\n' ID POLARITY LAYER VERDICT DETAIL
ALL_FIXED=1
N="$(jq '.findings|length' "$MANIFEST")"
for i in $(seq 0 $((N-1))); do
  id="$(jq -r ".findings[$i].id" "$MANIFEST")"
  pol="$(jq -r ".findings[$i].polarity" "$MANIFEST")"
  layer="$(jq -r ".findings[$i].layer" "$MANIFEST")"
  detail=""
  if [ "$layer" = "unit" ]; then
    state="$(lookup_unit "$id")"
    [ -n "$state" ] || state="missing"
  else
    matches="$(jq -r ".findings[$i].match | join(\" \")" "$MANIFEST")"
    p=0; f=0; miss=0
    for m in $matches; do
      r="$(lookup_go "$m")"
      case "$r" in pass) p=$((p+1));; fail) f=$((f+1));; *) miss=$((miss+1));; esac
    done
    detail="pass=$p fail=$f miss=$miss"
    if [ "$miss" -gt 0 ]; then state="missing";
    elif [ "$f" -gt 0 ]; then state="fail"; else state="pass"; fi
  fi
  case "$state" in
    pass) if [ "$pol" = "canary" ]; then verdict="STILL-PRESENT"; ALL_FIXED=0;
          else verdict="OK(contract)"; fi;;
    fail) if [ "$pol" = "canary" ]; then verdict="FIXED(flipped)";
          else verdict="BROKEN(contract)"; ALL_FIXED=0; fi;;
    *)    verdict="HARNESS-UPDATE"; ALL_FIXED=0; detail="check not found in layer output";;
  esac
  printf '%-6s %-9s %-12s %-16s %s\n' "$id" "$pol" "$layer" "$verdict" "$detail"
done

printf '\nLive layer (L3): skipped by design for this docs-only PR (see README).\n'
printf 'Raw output: %s\n' "$RESULTS_DIR"
printf '\nReview delta this round: %s..%s\n' "${LAST_REVIEWED:-base}" "$FIXED_REF"
printf 'After reviewing that delta, advance the marker:\n'
printf '  echo %s > %s && git add %s && git commit -m "review: advance last-reviewed"\n' \
       "$FIXED_REF" "$LAST_REVIEWED_FILE" "$LAST_REVIEWED_FILE"
if [ "$ALL_FIXED" -eq 1 ]; then echo "RESULT: all findings fixed."; exit 0
else echo "RESULT: not all findings fixed."; exit 1; fi
