#!/usr/bin/env bash
# Re-verify the PR#414 findings after the author pushes a fix, on this machine
# or another one. Takes NO sha: it resolves the current PR head from
# manifest.pr and the delta start from .last-reviewed.
#
#   bash docs/verification/pr414-v1alpha1-compat-flag/scripts/re-verify.sh
#   LIVE=1 bash .../re-verify.sh        # also run the live layer (needs a cluster)
#
# Reads polarity from verify-manifest.json and prints, per finding:
#   FIXED / STILL-BROKEN / CANARY-FLIPPED(=fixed, invert the test) / HARNESS-UPDATE
set -uo pipefail
ROOT="$(git rev-parse --show-toplevel)"
TOPIC=pr414-v1alpha1-compat-flag
DIR="$ROOT/docs/verification/$TOPIC"
MANIFEST="$DIR/verify-manifest.json"
RESULTS="$DIR/results"
LIVE=${LIVE:-0}
mkdir -p "$RESULTS"

PR=$(python3 -c "import json;print(json.load(open('$MANIFEST'))['pr'])")
LAST=$(tr -d '[:space:]' < "$DIR/.last-reviewed" 2>/dev/null)
NUM=${PR##*/}

echo "== re-verify $TOPIC =="
echo "   pr            : $PR"
echo "   last reviewed : ${LAST:-<none>}"

# --- resolve the current PR head -------------------------------------------
if ! git fetch upstream "pull/$NUM/head:pr${NUM}head" --force >/dev/null 2>&1; then
  echo "   WARNING: could not fetch upstream pull/$NUM/head; falling back to the local ref" >&2
fi
HEAD_SHA=$(git rev-parse "pr${NUM}head" 2>/dev/null || git rev-parse HEAD)
echo "   current head  : $(git rev-parse --short "$HEAD_SHA")"

if [ -n "$LAST" ] && [ "$LAST" = "$HEAD_SHA" ]; then
  echo "   head is unchanged since the last round -- re-running anyway to catch"
  echo "   environment-dependent regressions."
else
  echo
  echo "== delta to review this round =="
  if [ -n "$LAST" ] && git cat-file -e "$LAST^{commit}" 2>/dev/null; then
    git log --oneline "$LAST..$HEAD_SHA" 2>/dev/null | sed 's/^/   /' \
      || echo "   (histories unrelated -- the branch was force-pushed; review the full diff)"
    echo "   files:"
    git diff --name-only "$LAST" "$HEAD_SHA" 2>/dev/null | grep -v '^vendor/' | sed 's/^/     /'
  else
    echo "   (.last-reviewed not resolvable; review base...head in full)"
  fi
fi

# The harness must run against the code under review. The harness commit sits ON
# TOP of the PR head, so the correct test is ancestry -- not equality. (An earlier
# version compared for equality and printed the rebase hint even right after a
# successful rebase, which trains the reader to ignore it.)
CUR=$(git rev-parse HEAD)
if git merge-base --is-ancestor "$HEAD_SHA" "$CUR" 2>/dev/null; then
  AHEAD=$(git rev-list --count "$HEAD_SHA..$CUR")
  echo
  echo "   worktree: $(git rev-parse --short "$CUR") = PR head + $AHEAD harness commit(s) -- OK"
  # Belt and braces: the harness must not be sitting on modified production code.
  PROD=$(git diff --name-only "$HEAD_SHA" "$CUR" -- api cmd internal pkg deploy config Makefile test)
  if [ -n "$PROD" ]; then
    echo
    echo "   WARNING: the harness commit(s) modify code under review -- results are NOT"
    echo "            about the PR as submitted:"
    echo "$PROD" | sed 's/^/              /'
  fi
else
  echo
  echo "   NOTE: worktree is at $(git rev-parse --short "$CUR"), which does NOT contain the"
  echo "         PR head $(git rev-parse --short "$HEAD_SHA"). Rebase before trusting the"
  echo "         results below:"
  echo "           git rebase --onto pr${NUM}head $LAST $(git rev-parse --abbrev-ref HEAD)"
fi

run() {  # run <label> <outfile> <cmd...>
  local label=$1 out=$2; shift 2
  printf "\n----- %s -----\n" "$label"
  "$@" > "$RESULTS/$out" 2>&1
  local rc=$?
  grep -E "^RESULT:|REPRODUCED|CANARY FLIPPED|DOES NOT BITE|^(ok|FAIL|PASS)" "$RESULTS/$out" \
    | head -12 | sed 's/^/   /'
  echo "   exit=$rc  (full output: results/$out)"
  return $rc
}

echo
echo "############ SCRIPT LAYER ############"
run "F1  helm render (contract)"        f1-helm-render.txt        bash "$DIR/scripts/01-helm-render.sh"
run "F2  rbac drift (contract)"         f2-rbac-drift.txt         bash "$DIR/scripts/02-rbac-drift.sh"
run "     rbac gating (control)"        f3-rbac-gating.txt        bash "$DIR/scripts/03-rbac-gating.sh"
run "F1b whole-chart render (contract)" f5-chart-render-all.txt   bash "$DIR/scripts/05-chart-render-all.sh"
# Slowest, and it mutates the worktree -- keep it last in this layer.
run "     make manifests (contract)"    f4-manifests-freshness.txt bash "$DIR/scripts/04-manifests-freshness.sh"

echo
echo "############ UNIT LAYER ############"
run "F3-F10 go test"                    l1-unit.txt \
  go test ./internal/controller/workloads/ ./cmd/rbgs/ ./api/workloads/v1alpha2/ \
  -run 'TestVerifyPR414' -v -count=1

if [ "$LIVE" = "1" ]; then
  echo
  echo "############ LIVE LAYER ############"
  : "${KUBECONFIG:=$HOME/.kube/config}"; export KUBECONFIG
  run "F1  helm install dry-run (live)"  l3-f1-helm-live.txt  bash "$DIR/scripts/20-live-helm-install.sh"
  run "F4  mixed RBG (live)"             l3-f4-mixed.txt      bash "$DIR/scripts/30-live-mixed-rbg.sh"
else
  echo
  echo "############ LIVE LAYER: skipped (set LIVE=1) ############"
fi

cat <<'EOF'

############ HOW TO READ THIS ############
CONTRACT tests (F1, F2, F3, F6, F7, F8, F8b, F7b, make-manifests):
    PASS = fixed        FAIL = still broken
CANARY tests (F4, F5, F9, F10) assert the CURRENT suspected-wrong behavior:
    PASS = still broken        FAIL = FIXED -> invert the assertion and
                                      record the flip in verify-manifest.json
A "HARNESS DOES NOT BITE" or "FIXTURE INVALID"/"CONTROLLER VOID" message means
the result is VOID, not a pass or a fail -- fix the harness and re-run.

Then: update the README table, advance .last-reviewed to the head above,
commit and push this branch.
EOF
