#!/usr/bin/env bash
# citation-check.sh: re-verify that the file/symbol citations in the rbg-conventions skill still
# resolve on the current tree. Docs rot; this makes the rot grep-able. Prints PASS/FAIL lines.
set -uo pipefail
repo=$(cd "$(dirname "$0")/../../../../" && pwd)
failed=0
chk() { # $1 id, then a grep-style test command
  local id=$1; shift
  if "$@" >/dev/null 2>&1; then echo "PASS cite-$id"; else echo "FAIL cite-$id"; failed=1; fi
}

cd "$repo"
chk revision-utils        grep -q "func getRBGPatch" pkg/utils/revision_utils.go
chk roles-hash            grep -q "func GetRolesRevisionHash" pkg/utils/revision_utils.go
chk ris-getpatch          grep -q "getPatch" pkg/reconciler/roleinstanceset/statelessmode/revision/revision.go
chk ris-getpatch-sts      grep -q "getPatch\|GetPatch" pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_helper.go
chk ri-buildpatch         grep -q "func buildPatch\|buildPatch" pkg/reconciler/roleinstance/revision/instance_revision.go
chk construct-ris         grep -q "func (r \*RoleInstanceSetReconciler) constructRoleInstanceSetApplyConfiguration" pkg/reconciler/roleinstanceset_reconciler.go
chk getters               grep -q "func (r \*RoleSpec) GetRestartPolicyConfig" api/workloads/v1alpha2/helper.go
chk raw-getters           grep -q "func (r \*RoleSpec) GetRawRestartPolicyConfig" api/workloads/v1alpha2/helper.go
chk activate-after        grep -q "func (c RestartPolicyConfig) GetActivateAfter" api/workloads/v1alpha2/rolebasedgroup_types.go
chk upgrade-test          grep -q "func TestRoleInstanceSetReconciler_RestartPolicyUpgrade" pkg/reconciler/roleinstanceset_reconciler_test.go
chk upgrade-fixtures      test -d pkg/reconciler/testdata/restart-policy-upgrade
chk rbgpredicate          grep -q "func RBGPredicate" internal/controller/workloads/rolebasedgroup_controller.go
chk cacheoptions          grep -q "func cacheOptions" cmd/rbgs/main.go
chk claimable             grep -q "func checkWorkloadClaimable" pkg/reconciler/common.go
chk patch-apply           grep -q "func PatchObjectApplyConfiguration" pkg/utils/utils.go
chk e2e-upgrade-profile   test -f test/e2e/upgrade/profile.go
chk e2e-upgrade-preflight test -f test/e2e/upgrade/preflight.go
chk fold-restart          grep -q "func foldRestartPolicyShape" test/e2e/upgrade/snapshot.go
chk v1a1-contract-setup   test -f test/envtest/testutil/v1alpha1_contract_setup.go
chk expectations          test -d pkg/utils/expectations
chk stress-skill          test -d .claude/skills/stress-test
chk envtest-version       grep -q "ENVTEST_K8S_VERSION ?= 1.31" Makefile
chk deprecated-flag       grep -q "enable-deprecated-workload-types" cmd/rbgs/main.go
chk webhooks-none         grep -q "enable-webhooks" cmd/rbgs/main.go
chk deprecation-webhook   grep -q "matchPolicy=Exact" api/workloads/v1alpha1/deprecation_webhook.go
chk no-update-crd-target  bash -c '! grep -q "^update-crd:" Makefile'
chk apicompat-target      grep -q "^test-e2e-deprecated-disabled:" Makefile
chk helm-rbac-gen         test -d hack/gen-helm-rbac
chk codegen-script        test -f hack/update-codegen.sh
chk kubectl-manifests     test -f deploy/kubectl/manifests.yaml
chk chart-webhook         test -f deploy/helm/rbgs/templates/webhook/manifests.yaml
chk chart-crd-upgrade     test -f deploy/helm/rbgs/templates/crd-upgrade/crd-upgrade.yaml
chk warmup-maxitems       grep -q "kubebuilder:validation:MaxItems=1024" api/workloads/v1alpha2/rolebasedgroupwarmup_types.go
chk markdownlint-config   test -f .github/.markdownlint.json
chk pr-template           test -f .github/pull_request_template.md
chk pod-construct         grep -q "func (r \*PodReconciler) ConstructPodTemplateSpecApplyConfiguration" pkg/reconciler/pod_reconciler.go
chk statefulmode-assert   grep -q "toolscache.SharedIndexInformer" pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_controller.go
chk v1a1-conv-annotation  grep -q "RoleWorkloadTypeAnnotationKey\]" api/workloads/v1alpha1/rolebasedgroup_conversion.go
chk v1a1-conv-materialize grep -q "RestartPolicyConfig: &v2.RestartPolicyConfig{" api/workloads/v1alpha1/rolebasedgroup_conversion.go

echo
[ "$failed" -eq 0 ] && echo "ALL CITATIONS RESOLVE" || echo "STALE CITATIONS PRESENT"
exit "$failed"
