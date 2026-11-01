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

//go:build verification_envtest

package reconciler

// Integration layer for the PR #540 / issue #539 verification harness.
//
// Runs reconcileStatefulSet against a real local Kubernetes API server
// (controller-runtime envtest), mirroring the reproduction in issue #539: a
// client wrapper denies only Pod List requests during an in-progress rollout.
//
// Polarity: CONTRACT — asserts the intended correct behavior. On the pre-fix
// code this test FAILS with the live StatefulSet scaled to 0; on the fixed code
// it PASSES with the replica count untouched.
//
// Run with:
//   KUBEBUILDER_ASSETS=<envtest-bin-dir> go test -mod=vendor \
//     -tags verification_envtest ./pkg/reconciler \
//     -run '^TestEnvtest_ReconcileStatefulSet_NoScaleToZeroOnPodListError$' \
//     -count=1 -v -timeout=300s

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// podListDeniedClient forwards every request to the real API server except
// Pod List, which fails with Forbidden — an API/RBAC outage during rollout.
type podListDeniedClient struct {
	client.Client
}

func (c *podListDeniedClient) List(
	ctx context.Context, list client.ObjectList, opts ...client.ListOption,
) error {
	if _, ok := list.(*corev1.PodList); ok {
		return apierrors.NewForbidden(
			schema.GroupResource{Group: "", Resource: "pods"}, "", errors.New("pods is forbidden"),
		)
	}
	return c.Client.List(ctx, list, opts...)
}

func TestEnvtest_ReconcileStatefulSet_NoScaleToZeroOnPodListError(t *testing.T) {
	testEnv := &envtest.Environment{}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	defer func() {
		if stopErr := testEnv.Stop(); stopErr != nil {
			t.Logf("stop envtest: %v", stopErr)
		}
	}()

	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	realClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	rbg := wrappersv2.BuildBasicRoleBasedGroup("test-rbg", "default").Obj()
	rbg.Spec.Roles[0].Replicas = ptr.To(int32(4))
	rbg.Spec.Roles[0].RolloutStrategy = &workloadsv1alpha2.RolloutStrategy{
		Type: workloadsv1alpha2.RollingUpdateStrategyType,
		RollingUpdate: &workloadsv1alpha2.RollingUpdate{
			MaxUnavailable: ptr.To(intstr.FromInt32(1)),
			MaxSurge:       ptr.To(intstr.FromInt32(1)),
			Partition:      ptr.To(intstr.FromInt32(0)),
		},
	}
	role := &rbg.Spec.Roles[0]
	ctx := log.IntoContext(context.Background(), zap.New().WithValues("env", "test"))

	// 1) First reconcile with a healthy client creates the StatefulSet with the
	// desired four replicas.
	r := NewStatefulSetReconciler(scheme, realClient)
	if err := r.reconcileStatefulSet(ctx, rbg, role, nil, "rev-1"); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	stsKey := types.NamespacedName{Name: rbg.GetWorkloadName(role), Namespace: rbg.Namespace}
	sts := &appsv1.StatefulSet{}
	if err := realClient.Get(ctx, stsKey, sts); err != nil {
		t.Fatalf("get created sts: %v", err)
	}
	assert.Equal(t, int32(4), *sts.Spec.Replicas, "created sts should have the desired replicas")
	assert.NotEmpty(t, sts.UID, "the real API server must have assigned a UID")

	// 2) Move the live replica count to a surge value (5), keeping the template
	// identical, so the next reconcile must read Pod state instead of taking
	// the completed-rollout shortcut.
	sts.Spec.Replicas = ptr.To(int32(5))
	if err := realClient.Update(ctx, sts); err != nil {
		t.Fatalf("update sts replicas: %v", err)
	}

	// 3) Re-reconcile while Pod List is denied. The contract: the error is
	// returned and the live replica count is left at 5. The pre-fix code
	// returned nil and wrote spec.replicas: 0 to the real API server.
	deniedR := NewStatefulSetReconciler(scheme, &podListDeniedClient{Client: realClient})
	err = deniedR.reconcileStatefulSet(ctx, rbg, role, nil, "rev-1")
	assert.Error(t, err, "reconcileStatefulSet must return the Pod List failure")

	stsAfter := &appsv1.StatefulSet{}
	if err := realClient.Get(ctx, stsKey, stsAfter); err != nil {
		t.Fatalf("get sts after failed reconcile: %v", err)
	}
	assert.Equal(t, int32(5), *stsAfter.Spec.Replicas,
		"a failed read must not shrink the live StatefulSet; observed replicas=%d (scale-to-zero if 0)",
		*stsAfter.Spec.Replicas)

	// 4) After the outage recovers, reconciliation succeeds again, proving the
	// error path did not wedge the controller. envtest runs no StatefulSet
	// controller, so seed the ControllerRevision the read path expects.
	seedRevision := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rev-1",
			Namespace: rbg.Namespace,
			Labels:    rbg.GetCommonLabelsFromRole(role),
		},
		Revision: 1,
		Data:     runtime.RawExtension{Raw: []byte(`{}`)},
	}
	if err := realClient.Create(ctx, seedRevision); err != nil {
		t.Fatalf("seed controller revision: %v", err)
	}
	recovered := NewStatefulSetReconciler(scheme, realClient)
	recErr := recovered.reconcileStatefulSet(ctx, rbg, role, nil, "rev-1")
	assert.NoError(t, recErr, "reconciliation must succeed once the read path recovers")

	stsRecovered := &appsv1.StatefulSet{}
	if err := realClient.Get(ctx, stsKey, stsRecovered); err != nil {
		t.Fatalf("get sts after recovery: %v", err)
	}
	// With no pods running, every replica reads as unready, so the reconciler
	// keeps the surge count (roleReplicas + maxSurge = 5); what matters for the
	// contract is that the value is decided by the rollout logic, not by a
	// swallowed error writing 0.
	assert.NotEqual(t, int32(0), *stsRecovered.Spec.Replicas,
		"recovered reconcile must not scale the StatefulSet to zero; observed %d",
		*stsRecovered.Spec.Replicas)
}
