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

// Round 4. Checking the conversion default that Copilot flagged on
// rolebasedgroup_conversion_test.go:1048, and the revision claim behind the
// "From v0.7.0: no action required" line in the upgrade note.
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

// R4-1 / L1 — CONTRACT. v1alpha1's RestartPolicy field documents a pattern-aware
// empty-value meaning:
//
//	// The default value is RecreateRoleInstanceOnPodRestart for LWS and None for
//	// STS & Deploy. Therefore, no default value is set.
//
// So an unset policy on a LeaderWorkerSet role means Recreate. Conversion maps every
// empty value to None unconditionally, which inverts that for LWS roles.
//
// FAILS on 6fc49a96.
func TestL1_R4_V1alpha1LWSEmptyPolicyMustStayRecreate(t *testing.T) {
	src := &workloadsv1alpha1.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec: workloadsv1alpha1.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha1.RoleSpec{{
				Name:     "inference",
				Replicas: ptr.To(int32(1)),
				// RestartPolicy deliberately unset: per the field doc this means
				// RecreateRoleInstanceOnPodRestart for a LeaderWorkerSet role.
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

	dst := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, src.ConvertTo(dst))

	role := dst.Spec.Roles[0]
	require.NotNil(t, role.Pattern.LeaderWorkerPattern)
	t.Logf("converted restartPolicyConfig = %+v", role.Pattern.LeaderWorkerPattern.RestartPolicyConfig)

	assert.Equal(t, workloadsv1alpha2.RecreateRoleInstanceOnPodRestart, role.GetRestartPolicy(),
		"an unset v1alpha1 policy on a LeaderWorkerSet role means Recreate, per the v1alpha1 field doc")
}

// R4-2 / L1 — CONTROL. The same conversion for a StatefulSet/Deployment-shaped role,
// where the v1alpha1 doc does say the empty default is None. This must keep passing,
// so a fix for R4-1 has to be pattern-aware rather than a blanket change.
func TestL1_R4_Control_V1alpha1StandaloneEmptyPolicyIsNone(t *testing.T) {
	src := &workloadsv1alpha1.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec: workloadsv1alpha1.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha1.RoleSpec{{
				Name:     "worker",
				Replicas: ptr.To(int32(1)),
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

	dst := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, src.ConvertTo(dst))
	assert.Equal(t, workloadsv1alpha2.RestartPolicyNone, dst.Spec.Roles[0].GetRestartPolicy(),
		"a standalone role keeps None, which the v1alpha1 doc also specifies")
}

// R4-3 / L1 — CANARY for the upgrade note. "From v0.7.0: no action required. The
// restartPolicy wire format is unchanged." holds for what a user writes on a
// RoleBasedGroup, but not for the roleInstanceTemplate the CONTROLLER writes.
// v0.7.0's reconciler did:
//
//	RoleInstanceTemplate().WithRestartPolicy(role.GetRestartPolicy())   // a string
//
// and this release does:
//
//	RoleInstanceTemplate().WithRestartPolicyConfig(...)                 // an object
//
// That template is exactly what the ControllerRevision hashes, so upgrading from
// v0.7.0 does change the wire format where it matters for revisions.
//
// PASSES on 6fc49a96, documenting the gap between the note and the behaviour.
func TestL1_R4_Canary_V0_7_0TemplateShapeStillChangesTheRevision(t *testing.T) {
	v070Shape := risWith(func(s *workloadsv1alpha2.RoleInstanceSet) {
		// What a v0.7.0 controller stored: the string field.
		s.Spec.RoleInstanceTemplate.RestartPolicy = //nolint:staticcheck
			workloadsv1alpha2.RestartPolicyNone
	})
	thisRelease := risWith(func(s *workloadsv1alpha2.RoleInstanceSet) {
		// What this release stores for the same effective policy.
		s.Spec.RoleInstanceTemplate.RestartPolicyConfig = &workloadsv1alpha2.RestartPolicyConfig{
			Type:             workloadsv1alpha2.RestartPolicyNone,
			BaseDelaySeconds: ptr.To(int32(30)),
			MaxDelaySeconds:  ptr.To(int32(600)),
		}
	})

	oldName, oldData := revisionHashOf(t, v070Shape)
	newName, newData := revisionHashOf(t, thisRelease)
	t.Logf("v0.7.0 template shape -> %s", oldName)
	t.Logf("   %s", oldData)
	t.Logf("this release          -> %s", newName)
	t.Logf("   %s", newData)

	assert.NotEqual(t, oldName, newName,
		"CANARY: upgrading from v0.7.0 changes the revision even though the effective policy is identical")
}
