#!/usr/bin/env bash
# Tracker for the F1 class (PR #552): every webhook path the chart renders
# must be registered by the binary built from the chart's default image pin
# (values.yaml controller.image.tag -> source commit). A rendered Fail-policy
# path the pinned image does not serve fail-closes every matching write with
# an admission 404.
#
# Round 3 state: the chart renders only the six shipped v1alpha2 entries
# (the v1alpha1 deprecation handlers were removed by #556 and this PR no
# longer adds chart entries), and the pin moved to v0.9.0-73e05db4 (#555),
# which registers them all. Kept as the regression guard: any future chart
# entry whose path is missing at the pinned commit fails this check.
#
# Usage: check-default-image-paths.sh [chart-dir]   (run inside the repo)
set -euo pipefail

CHART_DIR="${1:-deploy/helm/rbgs}"
VALUES="$CHART_DIR/values.yaml"

PIN="$(sed -n 's/^    tag: "\(.*\)"$/\1/p' "$VALUES" | head -1)"
[ -n "$PIN" ] || { echo "cannot find controller.image.tag in $VALUES" >&2; exit 2; }

COMMIT=""
if git rev-parse --verify --quiet "${PIN}^{commit}" >/dev/null; then
  COMMIT="$(git rev-parse "${PIN}^{commit}")"
else
  SUFFIX="${PIN##*-}"
  if [ "$SUFFIX" != "$PIN" ] && git rev-parse --verify --quiet "${SUFFIX}^{commit}" >/dev/null; then
    COMMIT="$(git rev-parse "${SUFFIX}^{commit}")"
  fi
fi
[ -n "$COMMIT" ] || { echo "cannot resolve image tag $PIN to a source commit" >&2; exit 2; }

KC="$(mktemp)"; printf 'apiVersion: v1\nkind: Config\n' > "$KC"; chmod 600 "$KC"
RENDERED="$(KUBECONFIG="$KC" helm template rbgs "$CHART_DIR" --namespace default 2>/dev/null)"

ENTRIES="$(RENDERED="$RENDERED" python3 - <<'PY'
import os, yaml
docs = yaml.safe_load_all(os.environ["RENDERED"])
for d in docs:
    if not isinstance(d, dict) or d.get("kind") not in ("ValidatingWebhookConfiguration", "MutatingWebhookConfiguration"):
        continue
    for w in d.get("webhooks", []):
        path = ((w.get("clientConfig") or {}).get("service") or {}).get("path", "")
        print(f"{w.get('name','')}\t{path}\t{w.get('failurePolicy','')}")
PY
)"

[ -n "$ENTRIES" ] || { echo "chart renders no webhooks at all: vacuously OK"; exit 0; }

echo "default image pin: $PIN (commit $COMMIT)"
printf '%-42s %-58s %-7s %s\n' NAME PATH POLICY SERVED-BY-PIN
fail=0
while IFS=$'\t' read -r name path policy; do
  if git grep -q --fixed-strings "$path" "$COMMIT" -- '*.go' 2>/dev/null; then
    served="yes"
  else
    served="NO"
  fi
  printf '%-42s %-58s %-7s %s\n' "$name" "$path" "$policy" "$served"
  if [ "$served" = "NO" ] && [ "$policy" = "Fail" ]; then fail=1; fi
  if [ "$served" = "NO" ] && [ "$policy" = "Ignore" ]; then
    echo "  note: $name unserved by the pin -> fail-open, behavior silently missing"
  fi
done <<< "$ENTRIES"

if [ "$fail" -ne 0 ]; then
  echo "RESULT: RED - at least one Fail-policy path is not served by the pinned image"
  exit 1
fi
echo "RESULT: GREEN - every Fail-policy path the chart renders is served by the pinned image"
