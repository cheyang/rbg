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

package reconciler

// Reviewer harness for PR #474 — round 6, the F16 upgrade-path contract.
//
// AddGroupAnnotationsToRoleHashes (51bf17bb) folds group-exclusive-topology
// and gang-scheduling=true into the role revision hashes. On controller
// upgrade every already-running annotated RBG computes a different expected
// role hash. This test pins what actually happens to the workload then: the
// role-hash label is refreshed and the Deployment spec is untouched, so no
// pod rollout follows a label-only hash migration (the PR body's round-6
// claim; the round-5 ledger had feared a one-time re-roll).
//
// Polarity: contract — FAILs if a hash-only drift starts rewriting the spec.

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func verifyF16Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

// TestVerifyPR474_GroupAnnotationFold_LabelOnlyRefresh: a pre-upgrade
// Deployment carries the un-folded role hash; after the upgrade the expected
// hash differs. The reconciler must refresh the label and leave the spec
// byte-identical (no pod replacement).
func TestVerifyPR474_GroupAnnotationFold_LabelOnlyRefresh(t *testing.T) {
	scheme := verifyF16Scheme(t)
	role := &workloadsv1alpha2.RoleSpec{
		Name:     "worker",
		Replicas: ptr.To(int32(2)),
		Pattern: workloadsv1alpha2.Pattern{
			StandalonePattern: &workloadsv1alpha2.StandalonePattern{
				TemplateSource: workloadsv1alpha2.TemplateSource{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{Containers: []corev1.Container{
							{Name: "c", Image: "app:v1"},
						}},
					},
				},
			},
		},
	}
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "upgrade-rbg", Namespace: "default", UID: types.UID("upgrade-rbg-uid"),
			Labels: map[string]string{constants.GroupNameLabelKey: "upgrade-rbg"},
			// The annotation whose fold changes the expected role hash on upgrade.
			Annotations: map[string]string{constants.GangSchedulingAnnotationKey: "true"},
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{*role}},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &DeploymentReconciler{scheme: scheme, client: c}
	ctx := context.Background()
	hashKey := fmt.Sprintf(constants.RoleRevisionLabelKeyFmt, role.Name)

	// Pre-upgrade state: the workload exists with the old (un-folded) hash.
	require.NoError(t, r.Reconciler(ctx, rbg, role, nil, "prefold-hash"))
	before := &appsv1.Deployment{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: rbg.GetWorkloadName(role), Namespace: rbg.Namespace}, before))
	require.Equal(t, "prefold-hash", before.Labels[hashKey], "fixture: the pre-upgrade hash is seeded")
	specBefore := before.Spec.DeepCopy()

	// Post-upgrade state: the expected hash now includes the folded annotation.
	require.NoError(t, r.Reconciler(ctx, rbg, role, nil, "folded-hash"))
	after := &appsv1.Deployment{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: rbg.GetWorkloadName(role), Namespace: rbg.Namespace}, after))

	assert.Equal(t, "folded-hash", after.Labels[hashKey],
		"the role-hash label is refreshed to the folded hash")
	assert.True(t, equalPodTemplate(specBefore.Template, after.Spec.Template),
		"the pod template must be unchanged: a hash-only migration must not roll pods")
	assert.Equal(t, *specBefore.Replicas, *after.Spec.Replicas)
	assert.True(t, equalLabelSelector(specBefore.Selector, after.Spec.Selector),
		"the selector must be unchanged")
}

func equalPodTemplate(a, b corev1.PodTemplateSpec) bool {
	return reflect.DeepEqual(a, b)
}

func equalLabelSelector(a, b *metav1.LabelSelector) bool {
	return reflect.DeepEqual(a, b)
}
