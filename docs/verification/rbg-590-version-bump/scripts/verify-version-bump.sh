#!/usr/bin/env bash
# verify-version-bump.sh — contract harness for the release-manifest version bump
# pattern used by sgl-project/rbg (PR #590 and its predecessors #555/#505/#468).
#
# It asserts the *contract* a release bump must uphold, deriving the expected tag
# from Chart.yaml at the commit under test (so it stays valid for future bumps):
#
#   C1  cross-file consistency  — helm template renders; controller AND crd-upgrade
#        images carry tag v+appVersion; version-configmap records chart/app version;
#        kustomize build image tag and deploy/kubectl/manifests.yaml image equal
#        v+appVersion as well.
#   C2  old defaults replaced  — the image tag the merge-base Chart.yaml pointed at
#        appears nowhere in the head tree (no stale references left behind).
#   C3  images published       — both rbgs-controller and rbgs-upgrade-crd exist in
#        the Docker Hub registry under the new tag (HTTP 200).
#   C4  generated file in sync — `kustomize build config/default` is byte-identical
#        to the committed deploy/kubectl/manifests.yaml.
#   C5  chart version bumped   — Chart.yaml version is valid semver and strictly
#        greater than the merge-base chart version.
#   C6  chart test suite       — `go test ./test/chart/` (make test-chart) passes.
#
# Output: one line per check on stdout:  "Cn<TAB>pass|fail"
#         details go to stderr and to $RESULTS_DIR/verify-version-bump.log
# Exit code 0 iff all checks pass.
#
# Requires: git, helm, go, curl, (kustomize — downloaded via `make kustomize` if
# bin/kustomize is missing). Network is needed for C3 and possibly kustomize.
set -uo pipefail

RESULTS_DIR="${RESULTS_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../results" && pwd)}"
mkdir -p "$RESULTS_DIR"
LOG="$RESULTS_DIR/verify-version-bump.log"
: >"$LOG"

CHART=deploy/helm/rbgs/Chart.yaml
KUSTDIR=config/manager/kustomization.yaml
MANIFESTS=deploy/kubectl/manifests.yaml

STATUS="$RESULTS_DIR/status.tsv"
: >"$STATUS"

emit() { # $1=check $2=result ; rest = detail
  local c="$1" r="$2"; shift 2
  printf '%s\t%s\n' "$c" "$r"
  printf '%s\t%s\t%s\n' "$c" "$r" "$*" >>"$STATUS"
  printf '[%s] %s %s: %s\n' "$(date -u +%H:%M:%S)" "$c" "$r" "$*" | tee -a "$LOG" >&2
}

# ---- derive expected values from the commit under test -----------------------
APPVER="$(grep '^appVersion:' "$CHART" | sed -E 's/^appVersion:[[:space:]]*"?([^"]+)"?$/\1/')"
CHARTVER="$(grep '^version:' "$CHART" | sed -E 's/^version:[[:space:]]*//')"
EXPECT_TAG="v${APPVER}"

BASE_SHA="$(git merge-base origin/main HEAD 2>/dev/null || true)"
if [ -z "$BASE_SHA" ]; then
  echo "WARN: origin/main not resolvable; falling back to env PREV_APPVER/PREV_CHARTVER" | tee -a "$LOG" >&2
  BASE_SHA=HEAD
fi
PREV_APPVER="${PREV_APPVER:-$(git show "$BASE_SHA:$CHART" 2>/dev/null | grep '^appVersion:' | sed -E 's/^appVersion:[[:space:]]*"?([^"]+)"?$/\1/')}"
PREV_CHARTVER="${PREV_CHARTVER:-$(git show "$BASE_SHA:$CHART" 2>/dev/null | grep '^version:' | sed -E 's/^version:[[:space:]]*//')}"
PREV_TAG="v${PREV_APPVER}"

{
  echo "head:        $(git rev-parse HEAD)"
  echo "merge-base:  $BASE_SHA"
  echo "appVersion:  $APPVER  (expected tag $EXPECT_TAG)"
  echo "chart ver:   $CHARTVER (previous $PREV_CHARTVER, previous tag $PREV_TAG)"
} >>"$LOG"

# ---- C1 cross-file consistency ----------------------------------------------
RENDER="$RESULTS_DIR/helm-render.yaml"
if helm template deploy/helm/rbgs >"$RENDER" 2>>"$LOG"; then
  ctrl="$(grep -o 'rolebasedgroup/rbgs-controller:[^" ]*' "$RENDER" | head -1)"
  upg="$(grep -o 'rolebasedgroup/rbgs-upgrade-crd:[^" ]*' "$RENDER" | head -1)"
  cm_chart="$(grep -A8 'rbgs-controller-version-info' "$RENDER" | grep 'chartVersion' | tr -d ' " ' | cut -d: -f2)"
  cm_app="$(grep -A8 'rbgs-controller-version-info' "$RENDER" | grep 'appVersion' | tr -d ' " ' | cut -d: -f2)"
  kust_tag="$(grep 'newTag:' "$KUSTDIR" | awk '{print $2}')"
  mani_img="$(grep -m1 'image: rolebasedgroup/rbgs' "$MANIFESTS" | awk '{print $2}')"
  ok=1
  [ "$ctrl" = "rolebasedgroup/rbgs-controller:$EXPECT_TAG" ] || { ok=0; echo "controller image=$ctrl" >>"$LOG"; }
  [ "$upg"  = "rolebasedgroup/rbgs-upgrade-crd:$EXPECT_TAG" ]  || { ok=0; echo "crdUpgrade image=$upg" >>"$LOG"; }
  [ "$cm_chart" = "$CHARTVER" ] || { ok=0; echo "configmap chartVersion=$cm_chart" >>"$LOG"; }
  [ "$cm_app"   = "$APPVER" ]   || { ok=0; echo "configmap appVersion=$cm_app" >>"$LOG"; }
  [ "$kust_tag" = "$EXPECT_TAG" ] || { ok=0; echo "kustomize newTag=$kust_tag" >>"$LOG"; }
  [ "$mani_img" = "rolebasedgroup/rbgs-controller:$EXPECT_TAG" ] || { ok=0; echo "manifests image=$mani_img" >>"$LOG"; }
  [ -n "$ctrl" ] && [ -n "$upg" ] || { ok=0; echo "render missing image lines" >>"$LOG"; }
  if [ "$ok" = 1 ]; then emit C1 pass "all of helm controller/crd-upgrade images, version-configmap, kustomize newTag, manifests.yaml == $EXPECT_TAG / $CHARTVER / $APPVER"
  else emit C1 fail "inconsistent version strings — see $LOG"; fi
else
  emit C1 fail "helm template failed"
fi

# ---- C2 previous defaults fully replaced ------------------------------------
# check BOTH the committed tree and the working tree (after re-verify grafts the
# harness onto a fixed ref, the working tree is the code actually under test)
if git grep -q "$PREV_TAG" HEAD -- 2>>"$LOG" || git grep -q "$PREV_TAG" -- 2>>"$LOG"; then
  emit C2 fail "stale reference to $PREV_TAG in HEAD or working tree"
else
  emit C2 pass "no occurrence of $PREV_TAG anywhere in HEAD tree"
fi

# ---- C3 images published to the registry ------------------------------------
c3=pass
for img in rbgs-controller rbgs-upgrade-crd; do
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 "https://hub.docker.com/v2/repositories/rolebasedgroup/$img/tags/$EXPECT_TAG")"
  echo "registry: rolebasedgroup/$img:$EXPECT_TAG -> HTTP $code" | tee -a "$LOG" >&2
  [ "$code" = "200" ] || c3=fail
done
[ "$c3" = pass ] && emit C3 pass "both images exist in Docker Hub at $EXPECT_TAG" || emit C3 fail "missing image tag in registry (see $LOG)"

# ---- C4 generated manifests in sync with kustomize --------------------------
if [ ! -x bin/kustomize ]; then
  echo "bin/kustomize missing; downloading via make kustomize" | tee -a "$LOG" >&2
  make kustomize >>"$LOG" 2>&1 || true
fi
KUSTOM="bin/kustomize"
[ -x "$KUSTOM" ] || KUSTOM="$(command -v kustomize || true)"
if [ -n "$KUSTOM" ] && [ -x "$KUSTOM" ]; then
  "$KUSTOM" build config/default >"$RESULTS_DIR/kustomize-build.yaml" 2>>"$LOG"
  if diff -q "$RESULTS_DIR/kustomize-build.yaml" "$MANIFESTS" >>"$LOG" 2>&1; then
    emit C4 pass "kustomize build config/default == deploy/kubectl/manifests.yaml (byte-identical)"
  else
    emit C4 fail "deploy/kubectl/manifests.yaml out of sync with kustomize build (see $LOG)"
  fi
else
  emit C4 fail "kustomize binary unavailable"
fi

# ---- C5 chart version is a strict semver bump -------------------------------
v_ok=1
echo "$CHARTVER" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || v_ok=0
newer="$(printf '%s\n%s\n' "$PREV_CHARTVER" "$CHARTVER" | sort -V | tail -1)"
[ "$newer" = "$CHARTVER" ] && [ "$CHARTVER" != "$PREV_CHARTVER" ] || v_ok=0
if [ "$v_ok" = 1 ]; then emit C5 pass "chart version $PREV_CHARTVER -> $CHARTVER is a valid semver increase"
else emit C5 fail "chart version $CHARTVER is not a semver increase over $PREV_CHARTVER"; fi

# ---- C6 chart test suite (make test-chart equivalent) ------------------------
if go test ./test/chart/ -count=1 >"$RESULTS_DIR/test-chart.out" 2>&1; then
  emit C6 pass "go test ./test/chart/ (make test-chart) passed"
else
  emit C6 fail "chart test suite failed — see $RESULTS_DIR/test-chart.out"
fi

# ---- aggregate: exit 0 iff every emitted check passed ------------------------
FAILS="$(awk -F'\t' '$2=="fail"' "$STATUS" | wc -l)"
TOTAL="$(wc -l <"$STATUS")"
if [ "$FAILS" -gt 0 ] || [ "$TOTAL" -lt 6 ]; then
  echo "verify-version-bump: $FAILS/$TOTAL contract checks FAILED (details: $LOG, status: $STATUS)" >&2
  exit 1
fi
echo "verify-version-bump: all $TOTAL contract checks passed (status: $STATUS)" >&2
exit 0
