//go:build envtest_verify

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

// Verification harness for PR https://github.com/sgl-project/rbg/pull/540
// (issue #539). Integration layer: drives the full reconcileStatefulSet path
// against a real API server (envtest) with Pod List requests denied.
//
// Polarity: CONTRACT test. Asserts the intended behavior: a Pod List failure
// during an in-progress rollout must surface as a non-nil reconcile error and
// must leave the live StatefulSet spec.replicas untouched.
// On base (4a0765f7): reconcile returns nil and the real API server stores
// spec.replicas=0 -> test FAILS (bug reproduced end-to-end).
// On head (cf4bff4c): the error propagates and the write never happens ->
// test PASSES.
//
// Requires KUBEBUILDER_ASSETS (e.g. via bin/setup-envtest use 1.31.0).

package reconciler

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// podListDeniedClient makes Pod List fail while every other request goes to
// the real API server. It models an API/RBAC failure during a rolling update.
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

func TestVerifyStatefulSetNotScaledToZeroOnPodListError(t *testing.T) {
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
	r := NewStatefulSetReconciler(scheme, &podListDeniedClient{Client: realClient})

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
	ctx := context.Background()

	if err := r.reconcileStatefulSet(ctx, rbg, role, nil, "rev-1"); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	sts := &appsv1.StatefulSet{}
	key := types.NamespacedName{Name: rbg.GetWorkloadName(role), Namespace: rbg.Namespace}
	if err := realClient.Get(ctx, key, sts); err != nil {
		t.Fatalf("get created sts: %v", err)
	}
	t.Logf("created sts: replicas=%d partition=%d", *sts.Spec.Replicas,
		*sts.Spec.UpdateStrategy.RollingUpdate.Partition)

	// Move the live replicas away from the desired count so the rolling-update
	// path reads Pods instead of taking the "completed" short-circuit.
	sts.Spec.Replicas = ptr.To(int32(5))
	if err := realClient.Update(ctx, sts); err != nil {
		t.Fatalf("update sts replicas: %v", err)
	}

	reconcileErr := r.reconcileStatefulSet(ctx, rbg, role, nil, "rev-1")
	t.Logf("second reconcile error: %v", reconcileErr)

	if err := realClient.Get(ctx, key, sts); err != nil {
		t.Fatalf("get sts after second reconcile: %v", err)
	}
	t.Logf("replicas after second reconcile: %d", *sts.Spec.Replicas)

	if reconcileErr == nil {
		t.Errorf("BUG #539: Pod List error swallowed; reconcileStatefulSet returned nil")
	}
	if *sts.Spec.Replicas == 0 {
		t.Errorf("BUG #539 reproduced: the StatefulSet was scaled to 0 by the failed reconcile")
	} else if *sts.Spec.Replicas != 5 {
		t.Errorf("unexpected replica drift: got %d, want 5 (unchanged)", *sts.Spec.Replicas)
	}
}
