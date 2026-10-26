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

package v1alpha2

// Reviewer-B verification harness for PR #524 (additive; production code
// untouched). Carries forward contracts F3/F4 from the PR #509 review and pins
// the RoleBasedGroupSet replacement-child exemption semantics introduced here.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
)

func pr524Role(name, workloadType string) RoleSpec {
	r := RoleSpec{Name: name, Replicas: ptr.To(int32(1))}
	if workloadType != "" {
		r.Annotations = map[string]string{constants.RoleWorkloadTypeAnnotationKey: workloadType}
	}
	return r
}

func pr524RBG(name string, roles ...RoleSpec) *RoleBasedGroup {
	return &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       RoleBasedGroupSpec{Roles: roles},
	}
}

func pr524Set(name string, uid types.UID, roles ...RoleSpec) *RoleBasedGroupSet {
	return &RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: uid},
		Spec: RoleBasedGroupSetSpec{
			GroupTemplate: RoleBasedGroupTemplateSpec{Spec: RoleBasedGroupSpec{Roles: roles}},
		},
	}
}

// F4 carried: the compatibility-mode rejection must not advise re-enabling the
// flag that is already enabled (the flag=true remedy would be a no-op). The
// flag=false path keeps the re-enable advice as the control.
func TestPR524Contract_CompatRejectionHasNoReenableAdvice(t *testing.T) {
	err := validateNoNewDeprecatedWorkloadTypes("spec.roles",
		[]RoleSpec{pr524Role("worker", constants.StatefulSetWorkloadType)}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "--enable-deprecated-workload-types=true",
		"compat-mode rejection must not advise re-enabling the flag already in effect")
	assert.Contains(t, err.Error(), "RoleInstanceSet", "rejection should point at the migration target")

	strictErr := validateNoDeprecatedWorkloadTypes("spec.roles",
		[]RoleSpec{pr524Role("worker", constants.StatefulSetWorkloadType)})
	require.Error(t, strictErr)
	assert.Contains(t, strictErr.Error(), "enable-deprecated-workload-types",
		"control: the final-cutoff message keeps the flag advice")
}

// F3 carried (unit level): once a role migrated off the deprecated type,
// re-introducing it is a new use and is rejected; the admission request's
// oldObj is the only source of the exemption.
func TestPR524Contract_ReintroductionRejected(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, AddToScheme(scheme))
	v := &RoleBasedGroupValidator{
		Client:                        fake.NewClientBuilder().WithScheme(scheme).Build(),
		EnableDeprecatedWorkloadTypes: true,
	}
	oldRBG := pr524RBG("rbg", pr524Role("worker", "")) // migrated: no annotation -> RoleInstanceSet
	newRBG := pr524RBG("rbg", pr524Role("worker", constants.StatefulSetWorkloadType))
	_, err := v.ValidateUpdate(context.Background(), oldRBG, newRBG)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new uses are not accepted")

	// Control: an unchanged deprecated role stays admitted.
	oldDep := pr524RBG("rbg", pr524Role("worker", constants.StatefulSetWorkloadType))
	newDep := oldDep.DeepCopy()
	newDep.Spec.Roles[0].Replicas = ptr.To(int32(5))
	_, err = v.ValidateUpdate(context.Background(), oldDep, newDep)
	assert.NoError(t, err)
}

// F1 carried (unit level): the replacement-child exemption applies exactly when
// the parent set is live, the UID matches, and the role name+type appears in
// the parent template — and in no other case.
func TestPR524Contract_LegacySetExemptionGuards(t *testing.T) {
	parent := pr524Set("legacy-set", "uid-real",
		pr524Role("worker", constants.StatefulSetWorkloadType))

	mkValidator := func() *RoleBasedGroupValidator {
		scheme := runtime.NewScheme()
		require.NoError(t, AddToScheme(scheme))
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(parent).Build()
		return &RoleBasedGroupValidator{Client: c, APIReader: c, EnableDeprecatedWorkloadTypes: true}
	}
	mkChild := func(uid types.UID, roles ...RoleSpec) *RoleBasedGroup {
		child := pr524RBG("legacy-set-0", roles...)
		child.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: GroupVersion.String(),
			Kind:       "RoleBasedGroupSet",
			Name:       "legacy-set",
			UID:        uid,
			Controller: ptr.To(true),
		}}
		return child
	}

	t.Run("controller child with matching UID and template role admitted", func(t *testing.T) {
		_, err := mkValidator().ValidateCreate(context.Background(),
			mkChild("uid-real", pr524Role("worker", constants.StatefulSetWorkloadType)))
		assert.NoError(t, err)
	})

	t.Run("wrong UID rejected", func(t *testing.T) {
		_, err := mkValidator().ValidateCreate(context.Background(),
			mkChild("uid-other", pr524Role("worker", constants.StatefulSetWorkloadType)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "new uses are not accepted")
	})

	t.Run("no owner reference rejected", func(t *testing.T) {
		child := pr524RBG("bare", pr524Role("worker", constants.StatefulSetWorkloadType))
		_, err := mkValidator().ValidateCreate(context.Background(), child)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "new uses are not accepted")
	})

	t.Run("role name absent from parent template rejected", func(t *testing.T) {
		_, err := mkValidator().ValidateCreate(context.Background(),
			mkChild("uid-real", pr524Role("other-role", constants.StatefulSetWorkloadType)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "new uses are not accepted")
	})

	t.Run("workload type mismatch with parent template rejected", func(t *testing.T) {
		_, err := mkValidator().ValidateCreate(context.Background(),
			mkChild("uid-real", pr524Role("worker", constants.DeploymentWorkloadType)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "new uses are not accepted")
	})

	t.Run("migrated parent stops exempting old-shape children", func(t *testing.T) {
		migrated := pr524Set("legacy-set", "uid-real", pr524Role("worker", ""))
		scheme := runtime.NewScheme()
		require.NoError(t, AddToScheme(scheme))
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(migrated).Build()
		v := &RoleBasedGroupValidator{Client: c, APIReader: c, EnableDeprecatedWorkloadTypes: true}
		_, err := v.ValidateCreate(context.Background(),
			mkChild("uid-real", pr524Role("worker", constants.StatefulSetWorkloadType)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "new uses are not accepted")
	})

	t.Run("parent not found means no exemption, no error", func(t *testing.T) {
		child := mkChild("uid-real", pr524Role("worker", constants.StatefulSetWorkloadType))
		child.OwnerReferences[0].Name = "gone-set"
		_, err := mkValidator().ValidateCreate(context.Background(), child)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "new uses are not accepted")
		assert.False(t, strings.Contains(err.Error(), "unable to verify"),
			"a deleted parent must not surface as a webhook error: %v", err)
	})
}
