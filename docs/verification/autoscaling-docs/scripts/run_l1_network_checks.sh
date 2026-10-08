#!/usr/bin/env bash
# L1 network checks for PR #389 docs — live evidence that the artifact references
# the docs point to do not resolve publicly.
#
#   B1  github.com/sgl-project/rbg-planner      -> repo must 404 (doc links it)
#       github.com/rolebasedgroup/rbg-planner   -> repo must exist (real location)
#   B2  ghcr.io/sgl-project/charts/rbg-planner  -> anonymous token DENIED (no public OCI chart)
#       calibration: ghcr.io/stefanprodan/charts/podinfo -> token OK + tags listed
#   B3  ghcr.io/sgl-project/rbg-profiler        -> anonymous token DENIED
#
# Calibration matters: a DENIED token for the sgl-project paths is only evidence of
# non-existence if the same probe succeeds for a known-public package.
set -uo pipefail

OUT="$(dirname "$0")/../results/l1-network-checks.txt"
: > "$OUT"

log() { echo "$@" | tee -a "$OUT"; }

log "== B1: rbg-planner repository locations ($(date -u +%FT%TZ))"
for repo in sgl-project/rbg-planner rolebasedgroup/rbg-planner; do
  code=$(gh api "repos/$repo" --jq '.full_name' >/dev/null 2>&1; echo $?)
  if [ "$code" = "0" ]; then
    log "  $repo -> EXISTS"
  else
    log "  $repo -> NOT FOUND (HTTP 404 via gh api)"
  fi
done
log "  docs reference github.com/sgl-project/rbg-planner in: $(grep -c 'sgl-project/rbg-planner' doc/best-practice/en/08-configuring-autoscaling*.md doc/best-practice/zh/08-configuring-autoscaling*.md | tr '\n' ' ')"

log ""
log "== B2/B3: ghcr.io package existence (anonymous token probe + calibration)"
probe() {
  local repo="$1"
  local resp tok
  resp=$(curl -s "https://ghcr.io/token?service=ghcr.io&scope=repository:${repo}:pull")
  tok=$(printf '%s' "$resp" | python3 -c "import sys,json;print(json.load(sys.stdin).get('token') or '')" 2>/dev/null)
  if [ -z "$tok" ]; then
    log "  ghcr.io/$repo -> DENIED (not publicly pullable / does not exist)"
  else
    log "  ghcr.io/$repo -> public (token granted); tags: $(curl -s -H "Authorization: Bearer $tok" "https://ghcr.io/v2/$repo/tags/list" | head -c 120)"
  fi
}
probe "stefanprodan/charts/podinfo"   # calibration: known-public OCI chart
probe "sgl-project/charts/rbg-planner" # what the doc's helm install uses
probe "sgl-project/rbg-profiler"      # what the doc's profiling.image uses
probe "rolebasedgroup/rbg-profiler"   # upstream-documented default

log ""
log "== upstream planner repo state"
log "  releases: $(gh api repos/rolebasedgroup/rbg-planner/releases --jq 'length' 2>/dev/null) published releases"
log "  README quick start: $(gh api repos/rolebasedgroup/rbg-planner/readme --jq '.content' 2>/dev/null | base64 -d | grep -m1 'helm install' | tr -s ' ')"
echo "results written to $OUT"
