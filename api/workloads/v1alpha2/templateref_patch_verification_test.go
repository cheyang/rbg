/*
Copyright 2025 The RBG Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Verification harness for PR #410 (.claude/skills/rbg-inference-deploy).
// See docs/verification/pr410-deploy-skill/README.md. Production code untouched.
//
// PR #410 asserts, as the load-bearing rationale for "never use roleTemplates on
// tainted nodes":
//
//	yaml-rules.md:6   "tolerations 字段**无法**通过 templateRef.patch 传递到 Pod
//	                   (RBG controller 已知行为限制,已通过源码验证)"
//	yaml-rules.md:570 "tolerations 列表 | 不会被 patch 传递,controller 转换时丢失"
//	SKILL.md:230      "tolerations are silently dropped by RBG controller when
//	                   going through templateRef (verified in source code)"
//
// Polarity of every test below: CONTRACT (refutes the doc claim).
// They assert the behavior the code actually implements — a strategic merge patch
// over the whole PodTemplateSpec, with no field allow-list. They PASS on the code
// under review. If PR #410's claim were true they would FAIL, so a green run is
// the reproduction that the documented limitation does not exist.
package v1alpha2

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func baseRoleTemplate() RoleTemplate {
	return RoleTemplate{
		Name: "engine-base",
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "engine"}},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "engine", Image: "sglang:base"}},
			},
		},
	}
}

// rbgWithPatch builds an RBG whose single role uses standalonePattern.templateRef
// with the given raw patch JSON.
func rbgWithPatch(patchJSON string) *RoleBasedGroup {
	var patch *runtime.RawExtension
	if patchJSON != "" {
		patch = &runtime.RawExtension{Raw: []byte(patchJSON)}
	}
	return &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pd-inference", Namespace: "default"},
		Spec: RoleBasedGroupSpec{
			RoleTemplates: []RoleTemplate{baseRoleTemplate()},
			Roles: []RoleSpec{{
				Name: "prefill",
				Pattern: Pattern{
					StandalonePattern: &StandalonePattern{
						TemplateSource: TemplateSource{
							TemplateRef: &TemplateRef{Name: "engine-base", Patch: patch},
						},
					},
				},
			}},
		},
	}
}

// F1 / L1: tolerations supplied through templateRef.patch reach the resolved
// PodTemplateSpec. CONTRACT (refutes PR #410 yaml-rules.md:6 / SKILL.md:230).
func TestF1_TolerationsSurviveTemplateRefPatch(t *testing.T) {
	rbg := rbgWithPatch(`{"spec":{"tolerations":[{"key":"nvidia.com/gpu","operator":"Exists","effect":"NoSchedule"}]}}`)

	got, err := rbg.Spec.Roles[0].GetResolvedTemplate(rbg)
	if err != nil {
		t.Fatalf("GetResolvedTemplate: %v", err)
	}
	if len(got.Spec.Tolerations) != 1 {
		t.Fatalf("PR #410 claims tolerations are dropped by templateRef.patch; "+
			"want 1 toleration on the resolved template, got %d (%+v)",
			len(got.Spec.Tolerations), got.Spec.Tolerations)
	}
	tol := got.Spec.Tolerations[0]
	if tol.Key != "nvidia.com/gpu" || tol.Operator != corev1.TolerationOpExists ||
		tol.Effect != corev1.TaintEffectNoSchedule {
		t.Fatalf("toleration content mangled: %+v", tol)
	}
	// The base template must survive alongside the patched field.
	if len(got.Spec.Containers) != 1 || got.Spec.Containers[0].Image != "sglang:base" {
		t.Fatalf("base template lost: %+v", got.Spec.Containers)
	}
}

// F1 / L1: nodeSelector + tolerations together, the exact combination the skill
// tells the agent to avoid via templateRef. CONTRACT (refutes doc claim).
func TestF1_NodeSelectorAndTolerationsSurviveTogether(t *testing.T) {
	rbg := rbgWithPatch(`{"spec":{` +
		`"nodeSelector":{"node.kubernetes.io/instance-type":"ecs.gn7i"},` +
		`"tolerations":[{"key":"dedicated","operator":"Equal","value":"gpu","effect":"NoExecute"}]}}`)

	got, err := rbg.Spec.Roles[0].GetResolvedTemplate(rbg)
	if err != nil {
		t.Fatalf("GetResolvedTemplate: %v", err)
	}
	if got.Spec.NodeSelector["node.kubernetes.io/instance-type"] != "ecs.gn7i" {
		t.Fatalf("nodeSelector dropped: %+v", got.Spec.NodeSelector)
	}
	if len(got.Spec.Tolerations) != 1 || got.Spec.Tolerations[0].Value != "gpu" {
		t.Fatalf("tolerations dropped or mangled: %+v", got.Spec.Tolerations)
	}
}

// F1 / L1: documents the ACTUAL merge semantics for tolerations when the base
// template already has some. corev1.PodSpec.Tolerations carries no
// patchMergeKey, so strategic merge patch replaces the list wholesale. That is a
// real caveat worth documenting — but it is "replace", not "drop".
// CONTRACT (documents true semantics).
func TestF1_TolerationsListIsReplacedNotDropped(t *testing.T) {
	rbg := rbgWithPatch(`{"spec":{"tolerations":[{"key":"from-patch","operator":"Exists"}]}}`)
	rbg.Spec.RoleTemplates[0].Template.Spec.Tolerations = []corev1.Toleration{
		{Key: "from-base", Operator: corev1.TolerationOpExists},
	}

	got, err := rbg.Spec.Roles[0].GetResolvedTemplate(rbg)
	if err != nil {
		t.Fatalf("GetResolvedTemplate: %v", err)
	}
	if len(got.Spec.Tolerations) != 1 {
		t.Fatalf("want 1 toleration after replace, got %d (%+v)",
			len(got.Spec.Tolerations), got.Spec.Tolerations)
	}
	if got.Spec.Tolerations[0].Key != "from-patch" {
		t.Fatalf("patch toleration did not win: %+v", got.Spec.Tolerations)
	}
}

// F1 / L1: an empty patch keeps the base template's tolerations, i.e. tolerations
// declared once in spec.roleTemplates are inherited by every referencing role.
// CONTRACT (refutes doc claim).
func TestF1_EmptyPatchInheritsBaseTolerations(t *testing.T) {
	rbg := rbgWithPatch(`{}`)
	rbg.Spec.RoleTemplates[0].Template.Spec.Tolerations = []corev1.Toleration{
		{Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
	}

	got, err := rbg.Spec.Roles[0].GetResolvedTemplate(rbg)
	if err != nil {
		t.Fatalf("GetResolvedTemplate: %v", err)
	}
	if len(got.Spec.Tolerations) != 1 || got.Spec.Tolerations[0].Key != "nvidia.com/gpu" {
		t.Fatalf("base tolerations lost through empty patch: %+v", got.Spec.Tolerations)
	}
}

// F8 / L1: counter-check of Copilot's review comment on yaml-rules.md:7, which
// says templateRef.patch is optional. The controller-layer validation requires
// it, so PR #410's rule 3 ("patch 必须设置,即使无覆盖也需 patch: {}") is correct.
// CONTRACT (confirms the PR, refutes the review comment).
func TestF8_TemplateRefWithoutPatchIsRejected(t *testing.T) {
	rbg := rbgWithPatch("") // templateRef set, patch absent

	err := ValidateRoleTemplateReferences(rbg)
	if err == nil {
		t.Fatalf("expected validation to reject templateRef without patch, got nil")
	}
	t.Logf("validation error (expected): %v", err)

	// And with patch: {} it must be accepted.
	if err := ValidateRoleTemplateReferences(rbgWithPatch(`{}`)); err != nil {
		t.Fatalf("templateRef with empty patch should be valid, got: %v", err)
	}
}
