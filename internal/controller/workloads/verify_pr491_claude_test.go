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

// Verification harness for PR #491 (KEP-455: RBGSet rolling update).
// Reviewer A (Claude), first round, 2026-10-11.
//
// The PR is docs-only (keps/455-rbgs-rolling-update/), so these tests do not
// assert behavior of the PR; they pin down the codebase facts the KEP's design
// statements rest on. They are contract checks: they must keep passing as long
// as those facts hold.
package workloads

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestVerify491P0PropagationGap: KEP premise — the RBGSet controller does not
// propagate groupTemplate.spec.roleTemplates to child RBGs today. Children are
// built from spec.roles only, so a set template defining roleTemplates produces
// children that cannot resolve a templateRef.
func TestVerify491P0PropagationGap(t *testing.T) {
	set := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "v491", Namespace: "default", UID: "uid-v491"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{
						{Name: "server"},
					},
					RoleTemplates: []workloadsv1alpha2.RoleTemplate{
						{Name: "tpl"},
					},
				},
			},
		},
	}

	child := newRBGForSet(set, 0)
	assert.Empty(t, child.Spec.RoleTemplates,
		"newRBGForSet must drop groupTemplate.spec.roleTemplates for the KEP premise to hold")

	// The same gap on the update path: updateExistingRBGs copies roles only.
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	existing := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      child.Name,
			Namespace: "default",
			Labels: map[string]string{
				"rbg.workloads.x-k8s.io/groupset-name":  "v491",
				"rbg.workloads.x-k8s.io/groupset-index": "0",
			},
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{Name: "server"}},
		},
	}
	r := &RoleBasedGroupSetReconciler{
		client: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build(),
		scheme: scheme,
	}
	require.NoError(t, r.updateExistingRBGs(t.Context(), set, []*workloadsv1alpha2.RoleBasedGroup{existing}))

	updated := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, r.client.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: existing.Name}, updated))
	assert.Empty(t, updated.Spec.RoleTemplates,
		"updateExistingRBGs must leave spec.roleTemplates untouched; the KEP's propagation gap claim covers the update path too")
}

// TestVerify491E1SpecBlastRadiusBounded: KEP claim — "RoleBasedGroupSpec holds
// only roles and roleTemplates, so overwriting the whole spec cannot discard
// another field today."
func TestVerify491E1SpecBlastRadiusBounded(t *testing.T) {
	typ := reflect.TypeOf(workloadsv1alpha2.RoleBasedGroupSpec{})
	var fields []string
	for i := range typ.NumField() {
		fields = append(fields, typ.Field(i).Name)
	}
	assert.ElementsMatch(t, []string{"Roles", "RoleTemplates"}, fields,
		"RoleBasedGroupSpec grew new fields; the KEP's blast-radius bound is stale")
}

// TestVerify491E4ControllerOwnedAnnotationWipe: F3 mechanism — the RBG
// controller writes rbg.workloads.x-k8s.io/discovery-config-mode onto every
// child (ensureDiscoveryConfigMode), the set's needsUpdate treats that
// child-only annotation as template drift, and syncRBGMetadata then replaces
// child annotations wholesale (nil when the template has none). The child
// controller re-initializes the annotation, so the pair never converges.
//
// Bug-canary: it pins today's behavior. When propagation learns to preserve
// controller-owned keys (KEP-455), the wipe assertions below flip to red and
// the drift assertion flips to false — invert them then.
func TestVerify491E4ControllerOwnedAnnotationWipe(t *testing.T) {
	const modeKey = "rbg.workloads.x-k8s.io/discovery-config-mode"

	set := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "v491-e4", Namespace: "default", UID: "uid-v491-e4"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{Name: "server"}},
				},
			},
		},
	}
	child := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "v491-e4-0",
			Namespace: "default",
			Labels: map[string]string{
				"rbg.workloads.x-k8s.io/groupset-name":  "v491-e4",
				"rbg.workloads.x-k8s.io/groupset-index": "0",
			},
			Annotations: map[string]string{modeKey: "refine"},
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{Name: "server"}},
		},
	}

	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	r := &RoleBasedGroupSetReconciler{
		client: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(child).Build(),
		scheme: scheme,
	}

	// Roles already match the template; the only delta is the controller-owned
	// annotation — yet the child counts as drifted.
	assert.True(t, r.needsUpdate(set, child),
		"a controller-owned annotation alone must not mark a child as drifted; today it does")

	require.NoError(t, r.updateExistingRBGs(t.Context(), set, []*workloadsv1alpha2.RoleBasedGroup{child}))

	updated := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, r.client.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: child.Name}, updated))
	assert.Empty(t, updated.Annotations[modeKey],
		"syncRBGMetadata must not wipe a controller-owned annotation written by the child controller; today it does")

	// With the annotation gone the child no longer looks drifted — the pair
	// only "converges" by deleting what the child controller re-initializes
	// (ensureDiscoveryConfigMode re-adds it on the next RBG reconcile).
	assert.False(t, r.needsUpdate(set, updated),
		"after the wipe the child looks converged; the RBG controller then re-adds the annotation and the pair oscillates")
}
