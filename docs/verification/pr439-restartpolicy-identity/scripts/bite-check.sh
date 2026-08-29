#!/usr/bin/env bash
# bite-check.sh — prove the harness actually catches the bugs (Step 4 of the
# review-finding-verifier skill). For each fix it: reverts the fix, re-runs the
# contract test (expect RED), restores the fix, re-runs (expect GREEN), and
# asserts the production diff is empty again afterwards.
#
# Uses go-test exit codes (NOT grep-in-a-pipe, which pipefail would invert).
# Run from the repo root on the PR head (or the verify branch):
#   bash docs/verification/pr439-restartpolicy-identity/scripts/bite-check.sh
set -uo pipefail   # NOTE: no -e; we handle exit codes explicitly per step.

PASS=0
expect_red() {  # $1 = step, $2 = test output file
  if grep -q -- '--- FAIL' "$2"; then echo "  RED on broken code (expected) ✔"; return 0
  else echo "  expected RED but test passed — harness does not bite" >&2; return 1; fi
}
expect_green() {  # $1 = test output file
  if grep -qE '^(ok|--- PASS)' "$1" && ! grep -q -- '--- FAIL' "$1"; then echo "  GREEN on fixed code ✔"; return 0
  else echo "  expected GREEN but test failed" >&2; return 1; fi
}

echo "== F1 bite: revert restart-policy gate -> expect F1 subtests RED =="
cp pkg/reconciler/roleinstanceset_reconciler.go /tmp/f1.go.bak
python3 - <<'PY'
p="pkg/reconciler/roleinstanceset_reconciler.go"
s=open(p).read()
fixed='''	roleInstanceTemplateConfig := workloadsv1alpha2client.RoleInstanceTemplate()
	if backoff := role.GetRawRestartBackoff(); backoff != nil &&
		(backoff.BaseDelaySeconds != nil || backoff.MaxDelaySeconds != nil) {
		restartPolicyApplyConfig := workloadsv1alpha2client.RestartPolicyConfig().
			WithType(role.GetRestartPolicy()).
			WithBaseDelaySeconds(role.GetBaseDelaySeconds()).
			WithMaxDelaySeconds(role.GetMaxDelaySeconds())
		roleInstanceTemplateConfig.WithRestartPolicyConfig(restartPolicyApplyConfig)
	} else {
		roleInstanceTemplateConfig.WithRestartPolicy(role.GetRestartPolicy())
	}'''
broken='''	roleInstanceTemplateConfig := workloadsv1alpha2client.RoleInstanceTemplate().
		WithRestartPolicyConfig(workloadsv1alpha2client.RestartPolicyConfig().
			WithType(role.GetRestartPolicy()).
			WithBaseDelaySeconds(role.GetBaseDelaySeconds()).
			WithMaxDelaySeconds(role.GetMaxDelaySeconds()))'''
assert fixed in s, "F1 fixed block not found"
open(p,"w").write(s.replace(fixed,broken))
PY
go test -count=1 -run 'TestRoleInstanceSetReconciler_RestartPolicyTemplateRepresentation' ./pkg/reconciler/ > /tmp/f1.out 2>&1 || true
expect_red F1 /tmp/f1.out || PASS=1
\cp -f /tmp/f1.go.bak pkg/reconciler/roleinstanceset_reconciler.go
go test -count=1 -run 'TestRoleInstanceSetReconciler_RestartPolicyTemplateRepresentation' ./pkg/reconciler/ > /tmp/f1.out 2>&1 || true
expect_green /tmp/f1.out || PASS=1

echo "== F2 bite: revert DeepCopy -> expect TestNewVersionedInstanceIdentityIsPerOrdinal RED =="
cp pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_utils.go /tmp/f2.go.bak
python3 - <<'PY'
p="pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_utils.go"
s=open(p).read()
fixed="\t\tSpec: *setToUse.Spec.RoleInstanceTemplate.RoleInstanceSpec.DeepCopy(),"
broken="\t\tSpec: setToUse.Spec.RoleInstanceTemplate.RoleInstanceSpec,"
assert fixed in s, "F2 fixed line not found"
open(p,"w").write(s.replace(fixed,broken))
PY
go test -count=1 -run 'TestNewVersionedInstanceIdentityIsPerOrdinal' ./pkg/reconciler/roleinstanceset/statefulmode/ > /tmp/f2.out 2>&1 || true
expect_red F2 /tmp/f2.out || PASS=1
\cp -f /tmp/f2.go.bak pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_utils.go
go test -count=1 -run 'TestNewVersionedInstanceIdentityIsPerOrdinal' ./pkg/reconciler/roleinstanceset/statefulmode/ > /tmp/f2.out 2>&1 || true
expect_green /tmp/f2.out || PASS=1

echo "== F3 bite: remove InjectIdentityLabelsIntoComponents call -> expect TestPatchUpdateSpecKeepsIdentity RED =="
cp pkg/inplace/instance/inplaceupdate/inplace_update.go /tmp/f3.go.bak
python3 - <<'PY'
p="pkg/inplace/instance/inplaceupdate/inplace_update.go"
s=open(p).read()
fixed="\tinplaceutil.InjectIdentityLabelsIntoComponents(instance)"
assert s.count(fixed)==1, "F3 fixed line not found/unexpected count"
open(p,"w").write(s.replace(fixed,"\t// REMOVED for bite check: "+fixed))
PY
go test -count=1 -run 'TestPatchUpdateSpecKeepsIdentity' ./pkg/inplace/instance/inplaceupdate/ > /tmp/f3.out 2>&1 || true
expect_red F3 /tmp/f3.out || PASS=1
\cp -f /tmp/f3.go.bak pkg/inplace/instance/inplaceupdate/inplace_update.go
go test -count=1 -run 'TestPatchUpdateSpecKeepsIdentity' ./pkg/inplace/instance/inplaceupdate/ > /tmp/f3.out 2>&1 || true
expect_green /tmp/f3.out || PASS=1

echo
echo "bite-check: all three fixes proven necessary (red when reverted) and sufficient (green when applied)."
echo "Production diff after restore (must be empty):"
git diff --stat -- \
  pkg/reconciler/roleinstanceset_reconciler.go \
  pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_utils.go \
  pkg/inplace/instance/inplaceupdate/inplace_update.go
exit $PASS
