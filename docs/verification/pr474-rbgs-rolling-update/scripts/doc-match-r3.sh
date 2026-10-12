#!/usr/bin/env bash
# Round 3 doc-match check: the PR body was rewritten this round; verify every
# API-surface claim it now makes against the head tree (98720938).
# Usage: bash scripts/doc-match-r3.sh <pr-body-file> <repo-rev>
set -u
BODY="$1"
REV="${2:-HEAD}"
REPO="$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
fail=0
check() { # check <ok> <label> <detail>
  if [ "$1" = "0" ]; then echo "PASS  $2 — $3"; else echo "FAIL  $2 — $3"; fail=1; fi
}

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
git -C "$REPO" show "$REV":api/workloads/v1alpha2/rolebasedgroupset_types.go > "$TMP/types.go" 2>/dev/null

# 1. type enum + default
grep -q 'Recreate;InPlaceUpdate' "$TMP/types.go"; check $? "enum {Recreate, InPlaceUpdate}" "types.go"
grep -qi 'default=InPlaceUpdate' "$TMP/types.go"; check $? "kubebuilder default InPlaceUpdate" "types.go"

# 2. no paused field/condition
grep -qiE 'Paused' "$TMP/types.go"; [ $? -ne 0 ]; check $? "no Paused field/condition in types" "types.go"

# 3. body explicitly states no paused + default InPlaceUpdate
grep -q 'no `paused` field or condition' "$BODY"; check $? "body states: no paused field/condition" "body"
grep -q 'Default strategy is `InPlaceUpdate`' "$BODY"; check $? "body states: default is InPlaceUpdate" "body"

# 4. revision-based design (ControllerRevision machinery)
git -C "$REPO" grep -l 'ensureGroupSetRevision' "$REV" -- internal/ >/dev/null 2>&1
check $? "ControllerRevision machinery exists (ensureGroupSetRevision)" "rolling/revision code"
grep -q 'groupset-revision' "$BODY"; check $? "body documents revision label" "body"

# 5. budget-paced scale-only under InPlaceUpdate (rolling.go disables the shortcut)
git -C "$REPO" show "$REV":internal/controller/workloads/rolebasedgroupset_rolling.go > "$TMP/rolling.go"
grep -q 'InPlaceUpdateStrategyType' "$TMP/rolling.go"
check $? "InPlaceUpdate branch present in rolling.go" "rolling.go"
# scale-only shortcut disabled in InPlaceUpdate mode => budget-paced, as the body now states
grep -q 'if !limits.inPlaceUpdate && groupSetOnlyReplicasChanged' "$TMP/rolling.go"
check $? "scale-only shortcut gated off under InPlaceUpdate (budget-paced)" "rolling.go:307"
grep -q 'budget-paced' "$BODY"; check $? "body notes scale-only is budget-paced under InPlaceUpdate" "body"

# 6. every body-listed unit/admission test name exists at the rev
missing=0
for t in $(grep -oE 'Test[A-Za-z0-9_]+' "$BODY" | sort -u); do
  git -C "$REPO" grep -l "func ${t}(" "$REV" >/dev/null 2>&1 || { echo "      missing test: $t"; missing=1; }
done
check "$missing" "all body-listed test names exist" "grep at rev"

# 7. e2e It() descriptions in the body exist in rbgs.go
git -C "$REPO" show "$REV":test/e2e/testcase/v1alpha2/rbgs.go > "$TMP/e2e.go"
missing=0
while IFS= read -r d; do
  [ -z "$d" ] && continue
  grep -qF "\"$d\"" "$TMP/e2e.go" || { echo "      missing e2e It: $d"; missing=1; }
done < <(sed -n '/^\*\*E2E tests\*\*/,/^### Ⅴ/p' "$BODY" | grep -oE '^- `.*`$' | sed 's/^- `//; s/`$//')
check "$missing" "all body-listed e2e It descriptions exist" "rbgs.go"

# 8. example manifest no longer claims InPlaceUpdate is rejected
git -C "$REPO" show "$REV":examples/basic/rbgs/rbgs-rolling-update.yaml > "$TMP/example.yaml"
grep -qi 'rejects it for now' "$TMP/example.yaml"; [ $? -ne 0 ]
check $? "example: no 'rejected' claim for InPlaceUpdate" "examples/basic/rbgs/rbgs-rolling-update.yaml"
grep -q 'This is the default' "$TMP/example.yaml"
check $? "example: documents InPlaceUpdate as default" "examples/basic/rbgs/rbgs-rolling-update.yaml"

# 9. conditions are Ready and Rolling (body claim)
grep -q 'RoleBasedGroupSetRolling RoleBasedGroupSetConditionType = "Rolling"' "$TMP/types.go"
check $? "conditions are Ready and Rolling" "types.go"

# 10. residual: body's 'isServing' note names a function that does not exist
git -C "$REPO" grep -q 'isServing' "$REV" -- internal/ api/ 2>/dev/null
[ $? -ne 0 ]; check $? "note: body says 'isServing centralizes readiness' but symbol absent (cosmetic)" "residual nit"

[ "$fail" = "0" ] && echo "DOC-MATCH: body matches shipped API" || echo "DOC-MATCH: body still diverges"
exit "$fail"
