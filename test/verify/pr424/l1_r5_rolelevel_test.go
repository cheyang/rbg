/*
Copyright 2026 The RBG Authors.

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

// Round 5. Checking a review claim: that a v0.7.0 role-level `restartPolicy: None`
// silently flips to the RecreateRoleInstanceOnPodRestart default on upgrade, because
// the new controller no longer reads the role-level field.
package pr424

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func v1a1LWSRole(policy workloadsv1alpha1.RestartPolicyType) *workloadsv1alpha1.RoleBasedGroup {
	return &workloadsv1alpha1.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec: workloadsv1alpha1.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha1.RoleSpec{{
				Name:          "inference",
				Replicas:      ptr.To(int32(1)),
				RestartPolicy: policy, // the role-level field, which only v1alpha1 has
				LeaderWorkerSet: &workloadsv1alpha1.LeaderWorkerTemplate{
					Size: ptr.To(int32(2)),
				},
				TemplateSource: workloadsv1alpha1.TemplateSource{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "main", Image: "nginx:latest"}},
						},
					},
				},
			}},
		},
	}
}

// R5-1 / L1 — the claim under test. A v1alpha1 role with an explicit role-level
// restartPolicy: None must NOT come out of conversion as the pattern default.
func TestL1_R5_RoleLevelNoneIsNotSilentlyFlippedToRecreate(t *testing.T) {
	src := v1a1LWSRole(workloadsv1alpha1.NoneRestartPolicy)

	dst := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, src.ConvertTo(dst))

	role := dst.Spec.Roles[0]
	require.NotNil(t, role.Pattern.LeaderWorkerPattern)
	t.Logf("role-level None -> restartPolicyConfig=%+v deprecated=%q effective=%q",
		role.Pattern.LeaderWorkerPattern.RestartPolicyConfig,
		role.Pattern.LeaderWorkerPattern.RestartPolicy, //nolint:staticcheck
		role.GetRestartPolicy())

	assert.Equal(t, workloadsv1alpha2.RestartPolicyNone, role.GetRestartPolicy(),
		"an explicit role-level None must survive conversion, not fall back to the pattern default")
}

// R5-2 / L1 — the other half of the claim: Recreate should pass through unchanged.
func TestL1_R5_RoleLevelRecreatePassesThrough(t *testing.T) {
	src := v1a1LWSRole(workloadsv1alpha1.RecreateRoleInstanceOnPodRestart)

	dst := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, src.ConvertTo(dst))

	role := dst.Spec.Roles[0]
	t.Logf("role-level Recreate -> restartPolicyConfig=%+v effective=%q",
		role.Pattern.LeaderWorkerPattern.RestartPolicyConfig, role.GetRestartPolicy())
	assert.Equal(t, workloadsv1alpha2.RecreateRoleInstanceOnPodRestart, role.GetRestartPolicy())
}

// R5-3 / L1 — does the v1alpha2 API even have a role-level restartPolicy for a
// v0.7.0 user to have set? If RoleSpec has no such field, the premise of the claim
// does not apply to v1alpha2 objects at all: there was nothing there to lose.
func TestL1_R5_V1alpha2RoleSpecHasNoRoleLevelRestartPolicy(t *testing.T) {
	role := workloadsv1alpha2.RoleSpec{Name: "r"}
	// Compile-time proof: uncommenting the next line must not compile.
	//   role.RestartPolicy = workloadsv1alpha2.RestartPolicyNone
	// Runtime proof via the resolver: with no pattern set at all, the effective
	// policy is None, and there is no role-level field feeding it.
	assert.Equal(t, workloadsv1alpha2.RestartPolicyNone, role.GetRestartPolicy(),
		"a v1alpha2 RoleSpec carries no role-level restartPolicy; the policy lives on the pattern")

	lwp := workloadsv1alpha2.RoleSpec{
		Name:    "r",
		Pattern: workloadsv1alpha2.Pattern{LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{}},
	}
	assert.Equal(t, workloadsv1alpha2.RecreateRoleInstanceOnPodRestart, lwp.GetRestartPolicy(),
		"the pattern default only applies when the pattern itself carries no policy")
}
