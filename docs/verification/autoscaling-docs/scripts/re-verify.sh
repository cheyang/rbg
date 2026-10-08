#!/usr/bin/env bash
# re-verify.sh — re-run the PR #389 (autoscaling docs) verification harness against
# an updated (fixed) doc set and report, per finding, Fixed / Still-broken / Partial.
#
# Findings are bug-canaries: a canary is "fixed" ONLY when it flips from PASS
# (defect present) to FAIL (defect gone) — then invert or drop it. Contract
# checks (C*) must STAY PASS; a contract flip means a doc fix broke something
# that used to be correct.
#
# Usage (from a checkout of the branch that contains this harness):
#   bash docs/verification/autoscaling-docs/scripts/re-verify.sh [<fixed-ref>]
#
#   <fixed-ref>  ref/sha of the updated PR head. Omitted -> auto-discovered by
#                fetching pull/389/head from the repo URL in verify-manifest.json.
#
# Requires: git, python3 (+PyYAML), go, kubectl, KUBEBUILDER_ASSETS envtest
# binaries (default $HOME/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64),
# network (gh, curl) for the live link/registry probes.
# Exit 0 iff every finding is Fixed.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VDIR="$(cd "$HERE/.." && pwd)"
MANIFEST="$VDIR/verify-manifest.json"
[ -f "$MANIFEST" ] || { echo "re-verify: manifest not found: $MANIFEST" >&2; exit 2; }
command -v jq >/dev/null || { echo "re-verify: jq required" >&2; exit 2; }
git rev-parse --git-dir >/dev/null 2>&1 || { echo "re-verify: not a git repo" >&2; exit 2; }

if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "re-verify: uncommitted tracked changes; commit or stash first (git checkout -f below)" >&2
  exit 2
fi

FIXED_REF="${1:-}"
if [ -z "$FIXED_REF" ]; then
  PR_URL="$(jq -r '.pr // empty' "$MANIFEST")"
  rest="${PR_URL#*://}"; host="${rest%%/*}"; path="${rest#*/}"
  owner="$(printf '%s' "$path" | cut -d/ -f1)"
  repo="$(printf '%s' "$path" | cut -d/ -f2)"
  num="$(printf '%s' "$path" | sed -E 's#.*/pull/([0-9]+).*#\1#')"
  git fetch --quiet "https://$host/$owner/$repo.git" "pull/$num/head" || { echo "re-verify: PR head fetch failed" >&2; exit 2; }
  FIXED_REF="$(git rev-parse FETCH_HEAD)"
fi
echo "re-verify: fixed-ref = $FIXED_REF"

LAST_REVIEWED_FILE="$VDIR/.last-reviewed"
if [ -s "$LAST_REVIEWED_FILE" ]; then
  LR="$(tr -d '[:space:]' < "$LAST_REVIEWED_FILE")"
else
  LR="$(git merge-base origin/main "$FIXED_REF" 2>/dev/null || echo '')"
fi
echo "re-verify: last-reviewed = ${LR:-<none>}  (review delta = ${LR:-base}..$FIXED_REF)"
echo "           advance it after this round:  git rev-parse $FIXED_REF > $LAST_REVIEWED_FILE"

RUNTIME_DIR="$(mktemp -d)"
RESULTS_DIR="$RUNTIME_DIR/results"; mkdir -p "$RESULTS_DIR"
ORIG_REF="$(git symbolic-ref --quiet --short HEAD || git rev-parse HEAD)"
HARNESS_SRC="$(git rev-parse HEAD)"
cleanup() { git checkout -f "$ORIG_REF" >/dev/null 2>&1 || true; rm -rf "$RUNTIME_DIR" 2>/dev/null || true; }
trap cleanup EXIT

git checkout -f "$FIXED_REF" >/dev/null 2>&1 || { echo "re-verify: cannot checkout $FIXED_REF" >&2; exit 2; }
HPATHS=()
while IFS= read -r p; do [ -n "$p" ] && HPATHS+=("$p"); done < <(jq -r '.harnessPaths[]' "$MANIFEST")
git checkout "$HARNESS_SRC" -- "${HPATHS[@]}" 2>/dev/null || { echo "re-verify: graft failed" >&2; exit 2; }

# ---- run the layers -------------------------------------------------------
REPO_ROOT="$(git rev-parse --show-toplevel)"

echo "re-verify: L1 static checks"
( cd "$REPO_ROOT" && python3 -I docs/verification/autoscaling-docs/scripts/run_l1_checks.py ) \
  > "$RESULTS_DIR/l1.txt" 2>&1 || true

echo "re-verify: L1 live registry/link checks"
( cd "$REPO_ROOT" && bash docs/verification/autoscaling-docs/scripts/run_l1_network_checks.sh ) \
  > "$RESULTS_DIR/l1net.txt" 2>&1 || true

if [ -z "${KUBEBUILDER_ASSETS:-}" ]; then
  d="$HOME/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64"
  [ -d "$d" ] && export KUBEBUILDER_ASSETS="$d"
fi
echo "re-verify: L2 envtest (KUBEBUILDER_ASSETS=${KUBEBUILDER_ASSETS:-<unset>})"
( cd "$REPO_ROOT" && go test ./docs/verification/autoscaling-docs/harness/ -v -timeout 10m ) \
  > "$RESULTS_DIR/l2.txt" 2>&1 || true

echo "re-verify: repo RBGSA controller unit tests"
( cd "$REPO_ROOT" && go test ./internal/controller/workloads/ -run 'ScalingAdapter|ReadyReplicasSyncWithScale|RBGScalingAdapterPredicate' -count=1 ) \
  > "$RESULTS_DIR/unit.txt" 2>&1 || true

# ---- verdicts --------------------------------------------------------------
# canary B* in L1: PASS (defect present) / FAIL (flipped -> fixed)
l1_result() { grep -m1 "^\[\(PASS\|FAIL\)\] $1 " "$RESULTS_DIR/l1.txt" | sed 's/^\[\(PASS\|FAIL\)\].*/\1/'; }
# contract C* must stay PASS
c_result() { grep -m1 "^\[\(PASS\|FAIL\)\] $1 " "$RESULTS_DIR/l1.txt" | sed 's/^\[\(PASS\|FAIL\)\].*/\1/'; }
l1net_has() { grep -q "$1" "$RESULTS_DIR/l1net.txt" && echo yes || echo no; }
l2_test() { grep -m1 "^--- \(PASS\|FAIL\): $1 " "$RESULTS_DIR/l2.txt" | sed 's/^--- \(PASS\|FAIL\):.*/\1/'; }
unit_ok() { grep -q "^ok.*internal/controller/workloads" "$RESULTS_DIR/unit.txt" && echo yes || echo no; }

printf '\n================  RE-VERIFY (PR #389 autoscaling docs)  ================\n'
printf '%-5s %-8s %-12s %s\n' ID POLARITY VERDICT EVIDENCE
ALL_FIXED=1
declare -A VERDICT

canary() { # id, evidence
  r="$(l1_result "$1")"
  case "$r" in
    PASS) VERDICT[$1]="Still-broken"; ALL_FIXED=0; printf '%-5s %-8s %-12s %s\n' "$1" canary "Still-broken" "L1 canary still PASS (defect present)";;
    FAIL) VERDICT[$1]="Fixed";         printf '%-5s %-8s %-12s %s\n' "$1" canary "Fixed" "L1 canary FLIPPED to FAIL — invert/remove the canary now";;
    *)    VERDICT[$1]="Harness-update"; ALL_FIXED=0; printf '%-5s %-8s %-12s %s\n' "$1" canary "Harness-update" "check $1 no longer reported; doc structure changed?";;
  esac
}

canary B1; canary B2; canary B3; canary B4; canary B5; canary B6; canary B7; canary N1

# B8 has two canaries: static (L1) + envtest (metricSource=dynamo rejected)
r="$(l1_result B8)"; t="$(l2_test TestCanaryMetricSourceDynamoRejected)"
if [ "$r" = "FAIL" ] || [ "$t" = "FAIL" ]; then
  VERDICT[B8]="Fixed"; printf '%-5s %-8s %-12s %s\n' B8 canary "Fixed" "flipped: L1=$r envtest=$t (CRD enum now accepts dynamo?)"
elif [ "$r" = "PASS" ] && [ "$t" = "PASS" ]; then
  VERDICT[B8]="Still-broken"; ALL_FIXED=0; printf '%-5s %-8s %-12s %s\n' B8 canary "Still-broken" "L1=$r envtest=$t (metricSource dynamo still rejected / doc comment still wrong)"
else
  VERDICT[B8]="Partial"; ALL_FIXED=0; printf '%-5s %-8s %-12s %s\n' B8 canary "Partial" "L1=$r envtest=$t"
fi

# live corroboration for B1/B2/B3 (informational; verdict from L1)
echo ""
echo "live probes: planner repo 404 fixed? $(l1net_has 'sgl-project/rbg-planner -> EXISTS' && echo yes || echo 'not yet') | chart published? $(l1net_has 'ghcr.io/sgl-project/charts/rbg-planner -> public' && echo yes || echo 'not yet')"
echo "live results in $RESULTS_DIR/l1net.txt (also copied below)"
cp "$RESULTS_DIR"/l1*.txt "$RESULTS_DIR"/l2.txt "$RESULTS_DIR"/unit.txt "$VDIR/results/" 2>/dev/null || true

# contracts must not regress
echo ""
REGRESS=0
for c in C1 C2 C3 C4 C5 C6 C7 C8 C9 C10 C11 C12; do
  if [ "$(c_result "$c")" != "PASS" ]; then
    echo "REGRESSION: contract $c no longer PASS (doc fix broke a previously-correct claim)"
    REGRESS=1; ALL_FIXED=0
  fi
done
[ "$REGRESS" = "0" ] && echo "all 12 contract checks still PASS (no regression)"
[ "$(unit_ok)" = "yes" ] || { echo "REGRESSION: repo RBGSA controller unit tests failed"; ALL_FIXED=0; }

echo ""
if [ "$ALL_FIXED" = "1" ]; then
  echo "VERDICT: all findings Fixed"
  exit 0
else
  echo "VERDICT: not all findings fixed (see above)"
  exit 1
fi
