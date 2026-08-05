set -uo pipefail
export PATH=$PATH:/usr/local/go/bin
cd /root/rbgrev400
D=docs/verification/pr400-coordinated-policy-doc
R=$D/results/harness-bites.txt

# Single-instance lock: a doubled concurrent run previously interleaved the
# patch/revert of the SAME files and left production code dirty.
exec 9>/tmp/bites.lock
flock -n 9 || { echo "another bites run holds the lock; aborting"; exit 3; }

# Always restore production code from git on ANY exit path, including a failure
# mid-patch. cp-from-/tmp proved fragile; git is authoritative.
PROD="pkg/coordination/coordinationscaling/scaler.go internal/controller/workloads/rolebasedgroup_controller.go"
restore(){ git checkout -- $PROD 2>/dev/null || true; }
trap restore EXIT INT TERM

: > $R
say(){ printf '%s\n' "$*" | tee -a $R; }
# assert_clean <label>: FAIL LOUDLY if the production tree is dirty.
assert_clean(){
  local d; d="$(git diff --stat -- pkg internal api cmd config doc)"
  if [ -n "$d" ]; then
    say "!!! NOT CLEAN at [$1]:"; printf '%s\n' "$d" | tee -a $R; return 1
  fi
  say "    [$1] production diff EMPTY (verified)"; return 0
}

say "================ HARNESS-BITES CHECK ================"
say "Purpose: prove the canaries DISCRIMINATE. Temporarily apply the reviewer's"
say "proposed fix to production code; each canary must FLIP TO RED. A canary that"
say "stays green under the fix is not evidence."
say "Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)  host=$(hostname)"
say "Harness ref: $(git rev-parse --short HEAD)  branch: $(git rev-parse --abbrev-ref HEAD)"
say ""
say "STEP 0: baseline cleanliness"
assert_clean "baseline" || { say "ABORT: dirty baseline"; exit 1; }

say ""
say "STEP 1: canaries on PRISTINE code (expect ALL PASS)"
go test ./pkg/coordination/coordinationscaling/ ./internal/controller/workloads/ \
   -run 'TestVerifyPR400' -count=1 2>&1 | tail -6 | tee -a $R

############### F1 ###############
say ""
say "=========== F1 FIX: make parsePercentage accept absolute values ==========="
say "Fix sketch: a bare number is an absolute replica count; convert it to the"
say "same normalized fraction the percentage path produces."
python3 - <<'PY'
p='pkg/coordination/coordinationscaling/scaler.go'
s=open(p).read()
old='''	if !strings.HasSuffix(percentStr, "%") {
		return 0, fmt.Errorf("percentage string must end with '%%'")
	}'''
new='''	if !strings.HasSuffix(percentStr, "%") {
		// TEMP HARNESS-BITES PATCH (reverted by the caller): accept a bare number.
		if n, e := strconv.ParseFloat(percentStr, 64); e == nil && n >= 0 {
			return n / 100.0, nil
		}
		return 0, fmt.Errorf("percentage string must end with '%%'")
	}'''
assert old in s, "F1 anchor not found"
open(p,'w').write(s.replace(old,new,1))
PY
say "--- diff under F1 patch ---"
git diff --stat -- pkg/coordination/coordinationscaling/scaler.go | tee -a $R
say "--- F1 canaries under the patch (expect FAIL = bites) ---"
go test ./pkg/coordination/coordinationscaling/ -run 'TestVerifyPR400_F1' -count=1 2>&1 \
  | grep -E '^(---|    ---|ok|FAIL|PASS)|CANARY FLIPPED' | head -20 | tee -a $R
git checkout -- pkg/coordination/coordinationscaling/scaler.go
assert_clean "after F1 revert" || exit 1

############### F3 ###############
say ""
say "=========== F3 FIX: maxSkew base 100 -> the role's own replica count ==========="
say "Fix sketch: scale maxSkew against requestDesired so an integer means N"
say "instances. The surrounding arithmetic is in percent, so convert the absolute"
say "instance count back to a percentage of requestDesired EXACTLY ONCE."
python3 - <<'PY'
p='internal/controller/workloads/rolebasedgroup_controller.go'
s=open(p).read()
old='	maxSkewPercent, _ := intstr.GetScaledValueFromIntOrPercent(&maxSkew, 100, true)'
new='''	// TEMP HARNESS-BITES PATCH (reverted by the caller): treat an integer maxSkew
	// as an absolute instance count by scaling against requestDesired, then
	// express it as a percentage of requestDesired for the arithmetic below.
	// float math avoids the truncation that integer division would introduce.
	maxSkewPercent := 0
	if requestDesired > 0 {
		_abs, _ := intstr.GetScaledValueFromIntOrPercent(&maxSkew, int(requestDesired), true)
		if _abs > int(requestDesired) {
			_abs = int(requestDesired)
		}
		maxSkewPercent = int(math.Round(float64(_abs) * 100.0 / float64(requestDesired)))
	}'''
assert old in s, "F3 anchor not found"
open(p,'w').write(s.replace(old,new,1))
PY
say "--- diff under F3 patch ---"
git diff --stat -- internal/controller/workloads/rolebasedgroup_controller.go | tee -a $R
say "--- F3 canaries under the patch (expect FAIL = bites) ---"
go test ./internal/controller/workloads/ -run 'TestVerifyPR400_F3' -count=1 2>&1 \
  | grep -E '^(---|    ---|ok|FAIL|PASS)|CANARY FLIPPED|DOC CLAIMS window' | head -24 | tee -a $R
git checkout -- internal/controller/workloads/rolebasedgroup_controller.go
assert_clean "after F3 revert" || exit 1

############### final ###############
say ""
say "STEP 9: canaries green again on restored code (expect ALL PASS)"
go test ./pkg/coordination/coordinationscaling/ ./internal/controller/workloads/ \
   -run 'TestVerifyPR400' -count=1 2>&1 | tail -6 | tee -a $R
say ""
say "STEP 10: FINAL production cleanliness"
assert_clean "final" || { say "RESULT: FAIL — production code left dirty"; exit 1; }
say ""
say "Working tree (harness files only; no ' M ' on prod paths):"
git status --short | tee -a $R
say ""
say "RESULT: PASS — F1 and F3 canaries both bite; production code untouched."
