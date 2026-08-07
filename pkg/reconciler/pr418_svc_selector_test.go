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

// pr418_svc_selector_test.go is a reviewer-private verification harness for
// https://github.com/sgl-project/rbg/pull/418. It is NOT part of the PR.
//
// It probes the shared headless Service selector that PR#418 derives from
// LeaderWorkerPattern.GetSharedServiceSelection(), which now falls back to LeaderOnly when
// the field is unset.
//
// Polarity legend:
//
//	[CONTRACT] asserts the intended correct behavior -> FAILS on the code under review.
//	[CANARY]   asserts the current (suspected wrong) behavior -> PASSES on the code under
//	           review and must be inverted once the behavior is changed.
package reconciler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/utils"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// pr418Scheme builds the scheme shared by the harness cases.
func pr418Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(s))
	require.NoError(t, appsv1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	return s
}

// pr418ServiceSelectorFor reconciles the headless service for a single-role RBG and returns
// the resulting selector, so each case only has to describe the role it cares about.
func pr418ServiceSelectorFor(
	t *testing.T, role workloadsv1alpha2.RoleSpec, workloadKind string,
) map[string]string {
	t.Helper()
	s := pr418Scheme(t)

	rbg := wrappersv2.BuildBasicRoleBasedGroup("pr418-rbg", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()

	objects := []runtime.Object{rbg}
	switch workloadKind {
	case "StatefulSet":
		objects = append(objects, &appsv1.StatefulSet{
			TypeMeta: metav1.TypeMeta{Kind: "StatefulSet", APIVersion: "apps/v1"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      rbg.GetWorkloadName(&rbg.Spec.Roles[0]),
				Namespace: rbg.Namespace,
				UID:       "pr418-sts",
			},
		})
	default:
		objects = append(objects, &workloadsv1alpha2.RoleInstanceSet{
			TypeMeta: metav1.TypeMeta{
				Kind: "RoleInstanceSet", APIVersion: "workloads.x-k8s.io/v1alpha2",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      rbg.GetWorkloadName(&rbg.Spec.Roles[0]),
				Namespace: rbg.Namespace,
				UID:       "pr418-ris",
			},
		})
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objects...).Build()
	require.NoError(t, NewServiceReconciler(cl).
		reconcileHeadlessService(context.TODO(), rbg, &rbg.Spec.Roles[0]))

	svcName, err := utils.GetCompatibleHeadlessServiceName(context.TODO(), cl, rbg, &rbg.Spec.Roles[0])
	require.NoError(t, err)
	svc := &corev1.Service{}
	require.NoError(t, cl.Get(context.TODO(),
		types.NamespacedName{Name: svcName, Namespace: rbg.Namespace}, svc))
	return svc.Spec.Selector
}

// TestVerifyPR418_F1_StatefulSetLeaderWorkerSelector is the [CONTRACT] test for F1.
//
// A role that carries a leaderWorkerPattern but runs on a StatefulSet still gets an
// RBG-managed headless service (sts_reconciler.go calls reconcileHeadlessService). StatefulSet
// pods are never labelled with ComponentNameLabelKey -- that label is only written on the
// RoleInstanceSet path (pkg/reconciler/roleinstance/utils/instance_utils.go). So narrowing the
// selector to component-name=leader can only ever produce a service with zero endpoints.
//
// The role deliberately leaves sharedServiceSelection UNSET, which is the only shape the
// restored CEL rule still admits for this workload type. Expected: the selector keeps
// selecting the role's pods. Suspected: GetSharedServiceSelection() returns LeaderOnly and
// the selector is narrowed.
func TestVerifyPR418_F1_StatefulSetLeaderWorkerSelector(t *testing.T) {
	role := wrappersv2.BuildLeaderWorkerRole("pr418-sts-lwp").
		WithWorkload("apps/v1", "StatefulSet").Obj()
	role.LeaderWorkerPattern.SharedServiceSelection = nil

	selector := pr418ServiceSelectorFor(t, role, "StatefulSet")

	t.Logf("F1 observed selector for StatefulSet+leaderWorkerPattern (policy unset): %v", selector)

	assert.NotContains(t, selector, constants.ComponentNameLabelKey,
		"F1: the shared service of a StatefulSet role must not be narrowed to "+
			"component-name=leader -- StatefulSet pods never carry that label, so the "+
			"service ends up with zero endpoints")
}

// TestVerifyPR418_F1b_ExplicitLeaderOnlyIsRejectedButDefaultIsNot documents the guard gap
// that commit f8f2a59f introduced. [CANARY]
//
// Polarity note: this is a canary, not a contract test. Scoping the *selector* to
// RoleInstanceSet roles (the fix that turns F1 green) does not change what the helper
// returns, so this stays red under that fix by design. It records the modelling
// inconsistency itself and flips only if the helper stops claiming LeaderOnly for roles whose
// workload type the API forbids it on.
//
// The restored CEL rule refuses an EXPLICIT LeaderOnly on a non-RoleInstanceSet role. The
// controller-side default is not subject to CEL at all, because CEL only sees the stored
// object where the field is absent. So the very value the API declares unsupported for this
// workload type is exactly the value the controller applies to it.
func TestVerifyPR418_F1b_ExplicitLeaderOnlyIsRejectedButDefaultIsNot(t *testing.T) {
	role := wrappersv2.BuildLeaderWorkerRole("pr418-sts-lwp").
		WithWorkload("apps/v1", "StatefulSet").Obj()
	role.LeaderWorkerPattern.SharedServiceSelection = nil

	effective := role.LeaderWorkerPattern.GetSharedServiceSelection()
	t.Logf("F1b effective policy the controller applies to a StatefulSet role: %q "+
		"(CEL rejects this value when written explicitly)", effective)

	assert.Equal(t, workloadsv1alpha2.SharedServiceSelectionLeaderOnly, effective,
		"F1b canary: the helper claims LeaderOnly for a workload type whose CEL rule rejects "+
			"that value when written explicitly -- the API and the controller disagree")
}

// TestVerifyPR418_F2_UnsetPolicyNarrowsRoleInstanceSetSelector is the [CANARY] for F2.
//
// Before PR#418, svc_reconciler required `SharedServiceSelection != nil && == LeaderOnly`, so
// an unset field left the selector at {group, role} and worker pods were part of the shared
// service endpoints. After PR#418 the unset field resolves to LeaderOnly, so worker pods drop
// out of the endpoints with no user action -- purely from upgrading the controller.
//
// Whether LeaderOnly is the right default is a product decision, so this records the NEW
// behavior instead of asserting the old one. It PASSES on the code under review. If the
// authors later keep the pre-PR default, this flips to red and must be inverted.
func TestVerifyPR418_F2_UnsetPolicyNarrowsRoleInstanceSetSelector(t *testing.T) {
	role := wrappersv2.BuildLeaderWorkerRole("pr418-ris-lwp").Obj()
	role.LeaderWorkerPattern.SharedServiceSelection = nil

	selector := pr418ServiceSelectorFor(t, role, "RoleInstanceSet")

	t.Logf("F2 observed selector for RoleInstanceSet+leaderWorkerPattern (policy unset): %v",
		selector)

	assert.Equal(t, string(constants.LeaderComponentType),
		selector[constants.ComponentNameLabelKey],
		"F2 canary: an unset policy now narrows the selector to the leader component, "+
			"removing worker pods from the shared service endpoints on upgrade")
}
