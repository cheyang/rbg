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

// Regression harness for PR #540 / issue #539: a read failure mid-rollout must
// surface as an error and must never yield (partition=0, replicas=0, err=nil),
// because reconcileStatefulSet would then apply spec.replicas: 0 to the live
// StatefulSet.
//
// Polarity: CONTRACT for every test in this file — each asserts the intended
// correct behavior, so it FAILS on the pre-fix code and PASSES on the fixed
// code, except TestRollingUpdateParameters_PropagatesPartitionConversionError,
// which guards the defer's partitionErr branch and passes on both (labeled in
// its own comment).

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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// forbiddenForList denies List calls for the given list kind, so reads fail
// exactly the way an RBAC or API outage would while every other verb still
// reaches the (fake) API server.
func forbiddenForList(kind schema.GroupVersionKind) interceptor.Funcs {
	return interceptor.Funcs{
		List: func(
			ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption,
		) error {
			if list.GetObjectKind().GroupVersionKind() == kind {
				return apierrors.NewForbidden(
					schema.GroupResource{Group: kind.Group, Resource: kind.Kind}, "", errors.New("denied"),
				)
			}
			return client.List(ctx, list, opts...)
		},
	}
}

// midRolloutFixtures returns a role and the live StatefulSet it manages,
// configured the way issue #539 describes: desired replicas 4, live replicas 5
// (surge state), rolling update in progress so reconciliation must read Pod and
// ControllerRevision state before deciding partition/replicas.
func midRolloutFixtures() (
	*workloadsv1alpha2.RoleBasedGroup, *workloadsv1alpha2.RoleSpec, *appsv1.StatefulSet,
) {
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

	commonLabels := map[string]string{
		constants.GroupNameLabelKey: "test-rbg",
		constants.RoleNameLabelKey:  "test-role",
		constants.GroupUIDLabelKey:  rbg.GenGroupUniqueKey(),
	}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbg.GetWorkloadName(role),
			Namespace: rbg.Namespace,
			UID:       "sts-uid",
			Labels:    commonLabels,
			Annotations: map[string]string{
				constants.RoleSizeAnnotationKey: "4",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(5)),
			Selector: &metav1.LabelSelector{MatchLabels: commonLabels},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{
					Partition: ptr.To(int32(0)),
				},
			},
		},
	}
	return rbg, role, sts
}

// podListKind / controllerRevisionListKind are the GVKs whose List calls the
// failing-read cases inject errors for.
var (
	podListKind                = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "PodList"}
	controllerRevisionListKind = schema.GroupVersionKind{
		Group: "apps", Version: "v1", Kind: "ControllerRevisionList",
	}
)

// buildScheme registers every GVK the reconciler touches in these tests.
func buildScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = workloadsv1alpha2.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	return scheme
}

// TestRollingUpdateParameters_PreservesPodListError is the core regression test
// for issue #539: a failing Pod List mid-rollout must be returned as an error.
// On the pre-fix code the deferred partition conversion overwrote err with nil
// and the function returned (0, 0, nil) — the scale-to-zero trigger.
func TestRollingUpdateParameters_PreservesPodListError(t *testing.T) {
	scheme := buildScheme()
	rbg, role, sts := midRolloutFixtures()

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(rbg, sts).
		WithInterceptorFuncs(forbiddenForList(podListKind)).
		Build()
	r := NewStatefulSetReconciler(scheme, fakeClient)

	ctx := log.IntoContext(context.Background(), zap.New().WithValues("env", "test"))
	retPartition, retReplicas, retErr := r.rollingUpdateParameters(ctx, role, sts, false, nil)

	assert.Error(t, retErr, "a Pod List failure must surface as an error, not be swallowed by the defer")
	if retErr == nil {
		assert.Fail(t, "scale-to-zero: rollingUpdateParameters returned nil error",
			"partition=%d replicas=%d — the caller would apply these to the live StatefulSet",
			retPartition, retReplicas)
	}
	assert.False(t, retErr == nil && retReplicas == 0, "must not return (any, 0, nil): the caller applies replicas=0")
}

// TestRollingUpdateParameters_PreservesControllerRevisionListError covers the
// ControllerRevision List failure path inside getReplicaStates, which also
// reached `return 0, 0, err` and was cleared by the same defer on the base.
func TestRollingUpdateParameters_PreservesControllerRevisionListError(t *testing.T) {
	scheme := buildScheme()
	rbg, role, sts := midRolloutFixtures()

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(rbg, sts).
		WithInterceptorFuncs(forbiddenForList(controllerRevisionListKind)).
		Build()
	r := NewStatefulSetReconciler(scheme, fakeClient)

	ctx := log.IntoContext(context.Background(), zap.New().WithValues("env", "test"))
	_, retReplicas, retErr := r.rollingUpdateParameters(ctx, role, sts, false, nil)

	assert.Error(t, retErr, "a ControllerRevision List failure must surface as an error")
	assert.False(t, retErr == nil && retReplicas == 0, "must not return (any, 0, nil): the caller applies replicas=0")
}

// TestRollingUpdateParameters_PreservesMissingRevision covers the
// "sts has no revision" early return in getReplicaStates: the pod list succeeds
// but no ControllerRevision matches, so GetHighestRevision returns nil.
func TestRollingUpdateParameters_PreservesMissingRevision(t *testing.T) {
	scheme := buildScheme()
	rbg, role, sts := midRolloutFixtures()

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(rbg, sts).
		Build()
	r := NewStatefulSetReconciler(scheme, fakeClient)

	ctx := log.IntoContext(context.Background(), zap.New().WithValues("env", "test"))
	_, retReplicas, retErr := r.rollingUpdateParameters(ctx, role, sts, false, nil)

	assert.Error(t, retErr, "a missing ControllerRevision must surface as an error")
	assert.False(t, retErr == nil && retReplicas == 0, "must not return (any, 0, nil): the caller applies replicas=0")
}

// TestRollingUpdateParameters_PreservesMalformedRoleSizeAnnotation covers the
// strconv.Atoi failure on the role-size annotation (missing or malformed),
// another `return 0, 0, err` path that the base defer cleared.
func TestRollingUpdateParameters_PreservesMalformedRoleSizeAnnotation(t *testing.T) {
	scheme := buildScheme()
	rbg, role, sts := midRolloutFixtures()
	sts.Annotations[constants.RoleSizeAnnotationKey] = "not-a-number"

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(rbg, sts).
		Build()
	r := NewStatefulSetReconciler(scheme, fakeClient)

	ctx := log.IntoContext(context.Background(), zap.New().WithValues("env", "test"))
	_, retReplicas, retErr := r.rollingUpdateParameters(ctx, role, sts, false, nil)

	assert.Error(t, retErr, "a malformed role-size annotation must surface as an error")
	assert.False(t, retErr == nil && retReplicas == 0, "must not return (any, 0, nil): the caller applies replicas=0")
}

// TestRollingUpdateParameters_PropagatesPartitionConversionError guards the
// defer's separate partitionErr branch: a partition value that cannot be
// converted must propagate even when the main calculation succeeded.
// This path behaved the same before and after PR #540; the test keeps it from
// regressing while the defer is edited.
func TestRollingUpdateParameters_PropagatesPartitionConversionError(t *testing.T) {
	scheme := buildScheme()
	rbg, role, sts := midRolloutFixtures()
	rbg.Spec.Roles[0].RolloutStrategy.RollingUpdate.Partition = ptr.To(intstr.FromString("bogus"))
	role = &rbg.Spec.Roles[0]

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(rbg, sts).
		Build()
	r := NewStatefulSetReconciler(scheme, fakeClient)

	ctx := log.IntoContext(context.Background(), zap.New().WithValues("env", "test"))
	// stsUpdated=true takes Case 3, which returns successfully before any Pod
	// read, so only the deferred conversion can fail here.
	_, _, retErr := r.rollingUpdateParameters(ctx, role, sts, true, nil)

	assert.Error(t, retErr, "an unconvertible Partition value must surface as an error")
}

// TestRollingUpdateParameters_PanicOnShrunkLiveSts_CANARY documents a
// PRE-EXISTING defect (present on base and on PR #540's head alike, untouched
// by this PR): when the live StatefulSet replica count sits more than
// maxUnavailable + continuously-ready replicas below the desired role replicas
// (externally shrunk workload, or a mid-scale-down state), rollingUpdatePartition
// indexes states[] beyond its length and panics.
// Polarity: BUG-CANARY — asserts the current (panicking) behavior. If this ever
// flips to "no panic", the latent defect was fixed and this canary must be
// inverted into a contract test. It does NOT gate PR #540.
func TestRollingUpdateParameters_PanicOnShrunkLiveSts_CANARY(t *testing.T) {
	scheme := buildScheme()
	rbg, role, sts := midRolloutFixtures()
	// Shrink the live replica count to 1 while the role still wants 4.
	sts.Spec.Replicas = ptr.To(int32(1))

	commonLabels := sts.Labels
	rev := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "rev-1", Namespace: rbg.Namespace, Labels: commonLabels},
		Revision:   1,
		Data:       runtime.RawExtension{Raw: []byte(`{}`)},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(rbg, sts, rev).
		Build()
	r := NewStatefulSetReconciler(scheme, fakeClient)

	ctx := log.IntoContext(context.Background(), zap.New().WithValues("env", "test"))
	panicked := true
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Logf("canary: reconcile panics as currently coded: %v", rec)
			} else {
				panicked = false
			}
		}()
		r.rollingUpdateParameters(ctx, role, sts, false, nil)
	}()
	assert.True(t, panicked,
		"canary flipped: rollingUpdateParameters no longer panics on a shrunk live STS — "+
			"invert this canary into a contract test and link the fix")
}
// user-visible contract at the reconcileStatefulSet level: after a first
// successful reconcile creates the workload, a failing Pod List during an
// in-progress rollout must (a) return an error and (b) leave the live
// StatefulSet replica count untouched. On the pre-fix code the second reconcile
// returned nil and the fake API server stored spec.replicas: 0.
func TestReconcileStatefulSet_NoScaleToZeroOnPodListError(t *testing.T) {
	scheme := buildScheme()
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

	// 1) Create the workload with a healthy client.
	healthyClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(rbg).Build()
	r := NewStatefulSetReconciler(scheme, healthyClient)
	if err := r.reconcileStatefulSet(ctx, rbg, role, nil, "rev-1"); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	stsKey := types.NamespacedName{Name: rbg.GetWorkloadName(role), Namespace: rbg.Namespace}
	sts := &appsv1.StatefulSet{}
	if err := healthyClient.Get(ctx, stsKey, sts); err != nil {
		t.Fatalf("get created sts: %v", err)
	}
	createdReplicas := *sts.Spec.Replicas
	assert.Equal(t, int32(4), createdReplicas, "first reconcile should create the desired replicas")

	// 2) Move the live replica count to a surge value (5) with an identical
	// template, so the next reconcile must read Pod state instead of taking the
	// completed-rollout shortcut. The fake client does not assign a UID on the
	// server-side-apply create, and case 1 in rollingUpdateParameters treats an
	// empty UID as "not created yet", so give it one — a real API server
	// assigns this automatically.
	sts.Spec.Replicas = ptr.To(int32(5))
	sts.UID = "sts-uid"
	if err := healthyClient.Update(ctx, sts); err != nil {
		t.Fatalf("update sts replicas: %v", err)
	}

	// 3) Re-reconcile through a client whose Pod List fails.
	failingClient := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(rbg, sts).
		WithInterceptorFuncs(forbiddenForList(podListKind)).
		Build()
	failingR := NewStatefulSetReconciler(scheme, failingClient)
	err := failingR.reconcileStatefulSet(ctx, rbg, role, nil, "rev-1")

	assert.Error(t, err, "reconcileStatefulSet must return the Pod List failure")

	stsAfter := &appsv1.StatefulSet{}
	if getErr := failingClient.Get(ctx, stsKey, stsAfter); getErr != nil {
		t.Fatalf("get sts after failed reconcile: %v", getErr)
	}
	assert.Equal(t, int32(5), *stsAfter.Spec.Replicas,
		"a failed read must not shrink the live StatefulSet; observed replicas=%d (scale-to-zero if 0)",
		*stsAfter.Spec.Replicas)
}
