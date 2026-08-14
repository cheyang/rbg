#!/bin/bash
# Step 4 of review-finding-verifier: prove the harness bites.
# Temporarily apply the candidate fix for F2, re-run, then REVERT and confirm the
# production diff is empty again.
set -uo pipefail
cd /root/rbg424
export KUBEBUILDER_ASSETS="$(pwd)/$(./bin/setup-envtest use 1.33.0 --bin-dir ./bin -p path)"

FILE=api/workloads/v1alpha2/rolebasedgroup_defaulter.go
cp "$FILE" /tmp/defaulter.orig

# Candidate fix: only materialize restartPolicyConfig.type when the deprecated
# field is empty. That keeps "config.type wins" for objects that really set both,
# while leaving the deprecated field authoritative (and therefore editable) for
# v0.7.0-shaped objects.
python3 - <<'PY'
p='api/workloads/v1alpha2/rolebasedgroup_defaulter.go'
s=open(p).read()
old = '''	for i := range rbg.Spec.Roles {
		role := &rbg.Spec.Roles[i]
		resolved := role.GetRestartPolicyConfig()
		switch {
		case role.LeaderWorkerPattern != nil:
			role.LeaderWorkerPattern.RestartPolicyConfig = &resolved
		case role.CustomComponentsPattern != nil:
			role.CustomComponentsPattern.RestartPolicyConfig = &resolved
		}
	}'''
new = '''	for i := range rbg.Spec.Roles {
		role := &rbg.Spec.Roles[i]
		resolved := role.GetRestartPolicyConfig()
		switch {
		case role.LeaderWorkerPattern != nil:
			if role.LeaderWorkerPattern.RestartPolicy == "" {
				role.LeaderWorkerPattern.RestartPolicyConfig = &resolved
			}
		case role.CustomComponentsPattern != nil:
			if role.CustomComponentsPattern.RestartPolicy == "" {
				role.CustomComponentsPattern.RestartPolicyConfig = &resolved
			}
		}
	}'''
assert old in s, "anchor not found"
open(p,'w').write(s.replace(old,new))
print("fix applied")
PY

echo "===== WITH CANDIDATE FIX APPLIED ====="
go test ./test/verify/pr424/... -v -timeout 30m 2>&1 \
  | grep -vE "^(I[0-9]|W[0-9]|E[0-9])" | grep -vE "^\t*>" \
  | grep -E "^(--- (PASS|FAIL)|ok|FAIL|PASS)" 

echo "===== REVERTING ====="
cp /tmp/defaulter.orig "$FILE"
git diff --stat -- . ':!test/verify' | tail -3
echo "production diff lines: $(git diff -- . ':!test/verify' | wc -l)"
echo BITES_DONE
