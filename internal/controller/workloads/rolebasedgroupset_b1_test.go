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

// These tests are the verification harness for review finding B1 on PR #463
// (RBGS parent/child strategy non-convergence on the legacy-upgrade path).
// They live in package workloads so they can call the unexported compare/copy
// paths directly. They are ADDITIVE — no production code is changed.
//
// Polarity (see docs/verification/pr463-legacy-strategy-heal/):
//   - needsUpdate/non-convergence tests are CONTRACT tests: they assert the
//     intended fixed behavior (a healed child is up-to-date vs. a legacy
//     parent). They are RED on the buggy PR code = the reproduction; GREEN
//     after the fix (normalize at the RBGS->RBG boundary).
//   - updateExistingRBGs / newRBGForSet tests are BUG-CANARIES: they pin the
//     current wrong behavior (the un-normalized legacy value is written/copied
//     verbatim). They PASS on the buggy code and must be inverted (assert
//     RecreatePod instead of Recreate) once the fix lands.
package workloads

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// legacyRBGS is a pre-webhook RoleBasedGroupSet whose stored GroupTemplate
// carries the v1alpha1 spelling "Recreate". The webhook never heals it because
// the controller's only RBGS write is Status().Update, which doesn't trigger
// the spec defaulter.
func legacyRBGS() *workloadsv1alpha2.RoleBasedGroupSet {
	return &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-set", Namespace: "default", UID: "legacy-set-uid"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(1)),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{legacyRole()},
				},
			},
		},
	}
}

// legacyRole is the parent template role carrying the legacy spelling.
func legacyRole() workloadsv1alpha2.RoleSpec {
	return workloadsv1alpha2.RoleSpec{
		Name:     "worker",
		Replicas: ptr.To(int32(1)),
		RolloutStrategy: &workloadsv1alpha2.RolloutStrategy{
			Type: workloadsv1alpha2.RollingUpdateStrategyType,
			RollingUpdate: &workloadsv1alpha2.RollingUpdate{
				Type: workloadsv1alpha2.LegacyRecreateUpdateStrategyType,
			},
		},
	}
}

// healedRole is the same role after the RBG mutating webhook has normalized the
// type to RecreatePod — i.e. what the child's stored value looks like once the
// webhook is live.
func healedRole() workloadsv1alpha2.RoleSpec {
	r := legacyRole()
	r.RolloutStrategy.RollingUpdate.Type = workloadsv1alpha2.RecreatePodUpdateStrategyType
	return r
}

// healedChild is the child RBG the controller would find in the API after the
// webhook healed its create/update. Labels/annotations are synced from the
// (empty) template, so the only delta vs. the parent is the strategy spelling.
func healedChild() *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "legacy-set-0",
			Namespace: "default",
			Labels: map[string]string{
				constants.GroupSetNameLabelKey:  "legacy-set",
				constants.GroupSetIndexLabelKey: "0",
			},
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{healedRole()},
		},
	}
}

func rbgsReconciler(scheme *runtime.Scheme, objs ...client.Object) *RoleBasedGroupSetReconciler {
	return &RoleBasedGroupSetReconciler{
		client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
		scheme: scheme,
	}
}

// TestB1_NeedsUpdate_LegacyParentVsHealedChild_Diverges is a CONTRACT test:
// a child whose strategy was healed to RecreatePod should be considered
// up-to-date relative to a legacy Recreate parent (they are the same strategy).
// On the buggy code rolesEqual() uses reflect.DeepEqual, so Recreate !=
// RecreatePod and needsUpdate() returns true -> this assertion fails = RED,
// the reproduction.
func TestB1_NeedsUpdate_LegacyParentVsHealedChild_Diverges(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = workloadsv1alpha2.AddToScheme(scheme)
	r := rbgsReconciler(scheme)

	parent := legacyRBGS()
	child := healedChild()

	got := r.needsUpdate(parent, child)
	t.Logf("needsUpdate(legacyParent=Recreate, healedChild=RecreatePod) = %v (want false: semantically equal)", got)
	assert.Falsef(t, got,
		"a healed child (RecreatePod) must be up-to-date vs. a legacy parent (Recreate); "+
			"rolesEqual uses reflect.DeepEqual with no semantic normalization, so the controller sees a permanent divergence")
}

// TestB1_NonConvergence_AcrossSimulatedReconciles is a CONTRACT test: it
// simulates N reconcile iterations on a cluster where the webhook is live
// (so every child Update the controller issues is healed back to RecreatePod).
// The intended behavior is that reconcile converges (needsUpdate -> false after
// the first heal). On the buggy code, needsUpdate stays true on every iteration
// -> the looped assertion fails = RED, proving non-convergence.
func TestB1_NonConvergence_AcrossSimulatedReconciles(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = workloadsv1alpha2.AddToScheme(scheme)
	r := rbgsReconciler(scheme)

	parent := legacyRBGS()
	child := healedChild()

	const iterations = 5
	for i := 0; i < iterations; i++ {
		// What the controller sees at the top of each reconcile.
		got := r.needsUpdate(parent, child)
		t.Logf("iter %d: needsUpdate(parent=Recreate, child=%s) = %v",
			i, child.Spec.Roles[0].RolloutStrategy.RollingUpdate.Type, got)
		// Intended: once the child is healed, reconcile must see no-op.
		if got {
			t.Errorf("iter %d: needsUpdate returned true after the child was healed to RecreatePod; "+
				"the RBGS layer never converges (legacy parent is never healed, child is re-healed every Update)", i)
		}
		// The controller then issues updateExistingRBGs (writes parent=Recreate).
		// The live webhook heals it back to RecreatePod before persist.
		_ = r.updateExistingRBGs(context.Background(), parent, []*workloadsv1alpha2.RoleBasedGroup{child})
		child.Spec.Roles[0].RolloutStrategy.RollingUpdate.Type = workloadsv1alpha2.RecreatePodUpdateStrategyType
	}
}

// TestB1_UpdateExistingRBGs_WritesLegacyValueBack is a BUG-CANARY: it pins the
// current behavior that updateExistingRBGs writes the parent's un-normalized
// Recreate into the child. (The fake client has no admission webhook, so the
// value persists as-written — exposing the controller's intent.) On the buggy
// code this PASSES. After the fix (normalize the copy), the child receives
// RecreatePod and this must be inverted to assert RecreatePod.
func TestB1_UpdateExistingRBGs_WritesLegacyValueBack(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = workloadsv1alpha2.AddToScheme(scheme)

	parent := legacyRBGS()
	seed := healedChild()
	r := rbgsReconciler(scheme, parent, seed)

	err := r.updateExistingRBGs(context.Background(), parent, []*workloadsv1alpha2.RoleBasedGroup{seed})
	assert.NoError(t, err)

	got := &workloadsv1alpha2.RoleBasedGroup{}
	assert.NoError(t, r.client.Get(context.Background(),
		client.ObjectKey{Name: seed.Name, Namespace: seed.Namespace}, got))
	gotType := got.Spec.Roles[0].RolloutStrategy.RollingUpdate.Type
	t.Logf("updateExistingRBGs wrote child strategy type = %q (canary: buggy code writes the legacy value back)", gotType)
	assert.Equalf(t, workloadsv1alpha2.LegacyRecreateUpdateStrategyType, gotType,
		"canary (invert after fix): buggy code writes the un-normalized Recreate into the child; "+
			"after the fix the child should receive RecreatePod")
}

// TestB1_newRBGForSet_CopiesLegacyValueVerbatim is a BUG-CANARY: newRBGForSet
// copies the parent GroupTemplate verbatim, so a new child carries Recreate.
// On the buggy code this PASSES. After the fix it must be inverted to assert
// RecreatePod.
func TestB1_newRBGForSet_CopiesLegacyValueVerbatim(t *testing.T) {
	parent := legacyRBGS()
	child := newRBGForSet(parent, 0)
	gotType := child.Spec.Roles[0].RolloutStrategy.RollingUpdate.Type
	t.Logf("newRBGForSet child strategy type = %q (canary: buggy code copies the legacy value verbatim)", gotType)
	assert.Equalf(t, workloadsv1alpha2.LegacyRecreateUpdateStrategyType, gotType,
		"canary (invert after fix): buggy code copies the legacy Recreate verbatim into a new child; "+
			"after the fix the new child should carry RecreatePod")
}
