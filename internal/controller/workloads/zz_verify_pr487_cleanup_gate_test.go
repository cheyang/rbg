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

// zz_verify_pr487_cleanup_gate_test.go is part of the additive verification harness for
// https://github.com/sgl-project/rbg/pull/487. It pins the RESIDUAL gap left after commit
// 9b6c83eb: the commit's own invariant is "cleanup must keep running", and it fixes the
// terminating-owned-workload trigger, but reconcileRoles can still fail for other persistent
// reasons — an unclaimable workload (foreign uid until the GC collects it; an unlabeled orphan
// until a human intervenes), an unmet dependency, a validation error — and every one of those
// returns from step 8 before the step-9 cleanup, so a role removed from the spec keeps its
// leftover workload (and its pods/accelerators) for the whole wedge. The gang-incompatible
// branch shows the alternative pattern: it runs cleanup on the error path precisely because
// cleanup only deletes workloads the user already removed from the spec.
//
// Polarity: CANARY — it asserts the CURRENT behavior (leftover leaks while a sibling role is
// wedged). Whether that is wrong is a design question (see review finding F5); if the
// controller starts running cleanup despite a role-reconcile error, this test FLIPS to red and
// must be inverted/promoted to a contract test.

package workloads

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/reconciler"
	"sigs.k8s.io/rbgs/test/wrappers"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

func TestZZVerifyCleanupSkippedWhileSiblingRoleWedged(t *testing.T) {
	testScheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(testScheme)
	_ = workloadsv1alpha2.AddToScheme(testScheme)

	const rbgUID = types.UID("zz-rbg-uid")
	ownerRef := metav1.OwnerReference{
		APIVersion: workloadsv1alpha2.GroupVersion.String(),
		Kind:       "RoleBasedGroup",
		Name:       "test-rbg",
		Controller: ptr.To(true),
		UID:        rbgUID,
	}

	// Role "keep" is in the spec but its workload is an UNLABELED ORPHAN (no controller
	// reference, no group-name label) — permanently unclaimable until a human intervenes.
	rbg := wrappersv2.BuildBasicRoleBasedGroup("test-rbg", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{
			wrappersv2.BuildStandaloneRole("keep").WithWorkload("apps/v1", "StatefulSet").Obj(),
		}).Obj()
	rbg.UID = rbgUID

	wedgingOrphan := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rbg-keep",
			Namespace: "default",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(1)),
			Template: wrappers.BuildBasicPodTemplateSpec().Obj(),
		},
	}
	// Role "removed" is gone from the spec; its leftover workload is owned by THIS rbg and
	// is exactly what the cleanup step exists to delete.
	removedLeftover := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rbg-removed",
			Namespace: "default",
			Labels: map[string]string{
				constants.GroupNameLabelKey: "test-rbg",
				constants.RoleNameLabelKey:  "removed",
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(1)),
			Template: wrappers.BuildBasicPodTemplateSpec().Obj(),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(rbg, wedgingOrphan, removedLeftover).
		Build()

	r := &RoleBasedGroupReconciler{
		client:                        fakeClient,
		apiReader:                     fakeClient,
		scheme:                        testScheme,
		recorder:                      record.NewFakeRecorder(20),
		workloadReconciler:            make(map[string]reconciler.WorkloadReconciler),
		enableDeprecatedWorkloadTypes: true,
	}

	logger := zap.New().WithValues("env", "unit-test")
	ctx := ctrl.LoggerInto(context.TODO(), logger)

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-rbg", Namespace: "default"}})
	require.Error(t, err, "the unclaimable orphan wedges the role reconcile (that refusal is intended)")

	got := &appsv1.StatefulSet{}
	getErr := fakeClient.Get(ctx, types.NamespacedName{Name: "test-rbg-removed", Namespace: "default"}, got)
	assert.True(t, getErr == nil || !apierrors.IsNotFound(getErr),
		"CANARY: the removed role's leftover still exists — cleanup was skipped because the "+
			"sibling role's reconcile error returns before step 9; if the controller starts running "+
			"cleanup on this path, flip this canary to assert IsNotFound")
}
