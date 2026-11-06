#!/usr/bin/env bash
# L1 tracker for F1 / AC-3 (PR #552): every v1alpha1 deprecation webhook path
# the chart renders must be registered by the binary built from the chart's
# default image pin (values.yaml controller.image.tag).
#
#   - Fail-policy path unserved by the pinned image  -> admission 404 ->
#     fail-closed: every matching v1alpha1 write is REJECTED (F1 mechanism).
#   - Ignore-policy path unserved by the pinned image -> fail-open: the write
#     succeeds but the deprecation warning is silently missing.
#
# Exit 0 iff every rendered Fail-policy path is served by the pinned commit.
# Usage: check-default-image-paths.sh [chart-dir]   (run from the repo checkout
# whose chart/values are under test; git history must be available)
set -euo pipefail

CHART_DIR="${1:-deploy/helm/rbgs}"
VALUES="$CHART_DIR/values.yaml"

PIN="$(sed -n 's/^    tag: "\(.*\)"$/\1/p' "$VALUES" | head -1)"
[ -n "$PIN" ] || { echo "cannot find controller.image.tag in $VALUES" >&2; exit 2; }

# Map the image tag to a source commit: tags are v<semver>-<shortsha> (chore
# pins) or plain release tags.
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

# Render the chart with a sanitized kubeconfig so helm's stderr never leaks.
KC="$(mktemp)"; printf 'apiVersion: v1\nkind: Config\n' > "$KC"; chmod 600 "$KC"
trap 'rm -f "$KC"' EXIT
RENDERED="$(KUBECONFIG="$KC" helm template rbgs "$CHART_DIR" --namespace default 2>/dev/null)"

# Extract the v1alpha1 deprecation entries: name, path, failurePolicy.
ENTRIES="$(RENDERED="$RENDERED" python3 - <<'PY'
import os, yaml
docs = yaml.safe_load_all(os.environ["RENDERED"])
for d in docs:
    if not isinstance(d, dict) or d.get("kind") != "ValidatingWebhookConfiguration":
        continue
    for w in d.get("webhooks", []):
        name = w.get("name", "")
        if "v1alpha1" not in name or "deprecation" not in name:
            continue
        path = ((w.get("clientConfig") or {}).get("service") or {}).get("path", "")
        print(f"{name}\t{path}\t{w.get('failurePolicy','')}")
PY
)"

[ -n "$ENTRIES" ] || { echo "chart renders no v1alpha1 deprecation webhooks (pre-gate chart): vacuously OK"; exit 0; }

echo "default image pin: $PIN (commit $COMMIT)"
printf '%-52s %-52s %-7s %s\n' NAME PATH POLICY SERVED-BY-PIN
fail=0
while IFS=$'\t' read -r name path policy; do
  if git grep -q --fixed-strings "$path" "$COMMIT" -- '*.go' 2>/dev/null; then
    served="yes"
  else
    served="NO"
  fi
  printf '%-52s %-52s %-7s %s\n' "$name" "$path" "$policy" "$served"
  if [ "$served" = "NO" ] && [ "$policy" = "Fail" ]; then fail=1; fi
  if [ "$served" = "NO" ] && [ "$policy" = "Ignore" ]; then
    echo "  note: $name unserved by the pin -> fail-open, warnings silently missing"
  fi
done <<< "$ENTRIES"

if [ "$fail" -ne 0 ]; then
  echo "RESULT: RED - at least one Fail-policy path is not served by the pinned image"
  exit 1
fi
echo "RESULT: GREEN - every Fail-policy path is served by the pinned image"
