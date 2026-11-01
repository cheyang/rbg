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
// (issue #539: StatefulSet reconciler swallows errors in defer and scales
// replicas to zero). Additive test file only; production code untouched.
//
// Polarity: CONTRACT tests. They assert the intended correct behavior:
// read failures during an in-progress rollout must propagate as a non-nil
// error out of rollingUpdateParameters. On the buggy base (4a0765f7) the
// deferred partition conversion overwrites the named return err with nil and
// these tests FAIL; on the fixed head (cf4bff4c) they PASS.

package reconciler

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// midRolloutSTS returns a live StatefulSet whose replica count (5, a surge
// state) differs from the desired role replica count (4), so
// rollingUpdateParameters cannot take the "rollout completed" short-circuit
// and must read Pods / ControllerRevisions.
func midRolloutSTS() *appsv1.StatefulSet {
	commonLabels := map[string]string{
		constants.GroupNameLabelKey: "test-rbg",
		constants.RoleNameLabelKey:  "test-role",
	}
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rbg-test-role",
			Namespace: "default",
			UID:       "sts-uid",
			Labels:    commonLabels,
			Annotations: map[string]string{
				constants.RoleSizeAnnotationKey: "4",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: commonLabels},
			Replicas: ptr.To(int32(5)),
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{
					Partition: ptr.To(int32(0)),
				},
			},
		},
	}
}

func rolloutRole() *workloadsv1alpha2.RoleSpec {
	rbg := wrappersv2.BuildBasicRoleBasedGroup("test-rbg", "default").Obj()
	role := &rbg.Spec.Roles[0]
	role.Replicas = ptr.To(int32(4))
	role.RolloutStrategy = &workloadsv1alpha2.RolloutStrategy{
		Type: workloadsv1alpha2.RollingUpdateStrategyType,
		RollingUpdate: &workloadsv1alpha2.RollingUpdate{
			MaxUnavailable: ptr.To(intstr.FromInt32(1)),
			MaxSurge:       ptr.To(intstr.FromInt32(1)),
			Partition:      ptr.To(intstr.FromInt32(0)),
		},
	}
	return role
}

// TestVerifyRollingUpdateParametersPreservesPodListError injects a Forbidden
// error for Pod List requests (an API/RBAC failure during a rolling update)
// and requires the error to reach the caller of rollingUpdateParameters.
//
// On base: the defer overwrites err with nil, the function returns
// (0, 0, nil), and reconcileStatefulSet would go on to apply replicas=0.
// This test FAILS there, reproducing issue #539 at the function level.
func TestVerifyRollingUpdateParametersPreservesPodListError(t *testing.T) {
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

	sts := midRolloutSTS()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(sts).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*corev1.PodList); ok {
					return apierrors.NewForbidden(
						schema.GroupResource{Group: "", Resource: "pods"}, "",
						errors.New("injected: pods is forbidden"),
					)
				}
				return c.List(ctx, list, opts...)
			},
		}).Build()
	r := NewStatefulSetReconciler(scheme, fakeClient)

	ctx := log.IntoContext(context.Background(), zap.New().WithValues("env", "verify"))
	partition, replicas, err := r.rollingUpdateParameters(ctx, rolloutRole(), sts, false, nil)
	if err == nil {
		t.Fatalf(
			"BUG #539 reproduced: Pod List error swallowed by defer; rollingUpdateParameters returned "+
				"(partition=%d, replicas=%d, err=nil) — caller will apply replicas to the live StatefulSet",
			partition, replicas,
		)
	}
	t.Logf("error correctly propagated: %v", err)
}

// TestVerifyRollingUpdateParametersPreservesRevisionListError covers the
// second read-failure path named in issue #539: ControllerRevision List
// errors reach the same swallowing defer via getReplicaStates ->
// getHighestRevision.
func TestVerifyRollingUpdateParametersPreservesRevisionListError(t *testing.T) {
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

	sts := midRolloutSTS()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(sts).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*appsv1.ControllerRevisionList); ok {
					return apierrors.NewForbidden(
						schema.GroupResource{Group: "apps", Resource: "controllerrevisions"}, "",
						errors.New("injected: controllerrevisions is forbidden"),
					)
				}
				return c.List(ctx, list, opts...)
			},
		}).Build()
	r := NewStatefulSetReconciler(scheme, fakeClient)

	ctx := log.IntoContext(context.Background(), zap.New().WithValues("env", "verify"))
	partition, replicas, err := r.rollingUpdateParameters(ctx, rolloutRole(), sts, false, nil)
	if err == nil {
		t.Fatalf(
			"BUG #539 reproduced: ControllerRevision List error swallowed by defer; "+
				"rollingUpdateParameters returned (partition=%d, replicas=%d, err=nil)",
			partition, replicas,
		)
	}
	t.Logf("error correctly propagated: %v", err)
}
