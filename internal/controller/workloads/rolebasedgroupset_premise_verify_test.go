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

package workloads

// Verification harness (reviewer additive test; production code untouched).
//
// P0 premise check for PR #474 ("support RoleBasedGroupSet rolling update").
// Claim under test (quoted from the PR body): "when the template changes, every
// outdated child is updated within a single reconcile, with no ordering and no
// availability gating."
//
// This file deliberately uses only pre-PR symbols so it compiles and runs on the
// BASE branch (0821cb5b) as well as the PR head. On the base branch it must PASS,
// which confirms the premise: one Reconcile updates every child. On the PR head it
// must also PASS with spec.rolloutStrategy unset, which confirms the PR's backward
// compatibility claim for the static path.

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func premisePodTemplate(image string) *corev1.PodTemplateSpec {
	return &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "worker", Image: image}},
		},
	}
}

func premiseSet(image string) *workloadsv1alpha2.RoleBasedGroupSet {
	return &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "premise", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(3)),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{
						Name:     "worker",
						Replicas: ptr.To(int32(1)),
						Pattern: workloadsv1alpha2.Pattern{
							StandalonePattern: &workloadsv1alpha2.StandalonePattern{
								TemplateSource: workloadsv1alpha2.TemplateSource{Template: premisePodTemplate(image)},
							},
						},
					}},
				},
			},
		},
	}
}

func premiseChild(set *workloadsv1alpha2.RoleBasedGroupSet, index int) *workloadsv1alpha2.RoleBasedGroup {
	child := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      set.Name + "-" + string(rune('0'+index)),
			Namespace: set.Namespace,
			Labels: map[string]string{
				constants.GroupSetNameLabelKey:  set.Name,
				constants.GroupSetIndexLabelKey: string(rune('0' + index)),
			},
		},
		Spec: *set.Spec.GroupTemplate.Spec.DeepCopy(),
	}
	child.Status.Conditions = []metav1.Condition{{
		Type:               string(workloadsv1alpha2.RoleBasedGroupReady),
		Status:             metav1.ConditionTrue,
		ObservedGeneration: 1,
	}}
	child.Status.ObservedGeneration = 1
	return child
}

func TestVerify_P0_StaticPathUpdatesAllChildrenInOnePass(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	set := premiseSet("image:v1")
	objects := []client.Object{set}
	for i := 0; i < 3; i++ {
		objects = append(objects, premiseChild(set, i))
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupSet{}, &workloadsv1alpha2.RoleBasedGroup{}).
		Build()
	r := &RoleBasedGroupSetReconciler{client: c, apiReader: c, scheme: scheme}

	// One reconcile to settle status.
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: set.Name, Namespace: set.Namespace}})
	require.NoError(t, err)

	// Change the template image: the premise claims every child moves in one reconcile.
	latest := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(set), latest))
	latest.Spec.GroupTemplate.Spec.Roles[0].StandalonePattern.Template = premisePodTemplate("image:v2")
	require.NoError(t, c.Update(ctx, latest))

	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: set.Name, Namespace: set.Namespace}})
	require.NoError(t, err)

	children := &workloadsv1alpha2.RoleBasedGroupList{}
	require.NoError(t, c.List(ctx, children, client.InNamespace(set.Namespace)))
	require.Len(t, children.Items, 3)
	for i := range children.Items {
		got := children.Items[i].Spec.Roles[0].StandalonePattern.Template.Spec.Containers[0].Image
		require.Equal(t, "image:v2", got,
			"premise: child %s must be updated within a single reconcile (no pacing)", children.Items[i].Name)
	}
	t.Logf("P0 confirmed: all %d children moved to the new template in ONE reconcile", len(children.Items))
}
