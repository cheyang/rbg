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

// zz_verify_pr487_ownership_test.go is an additive verification harness for
// https://github.com/sgl-project/rbg/pull/487 ("fix: do not claim a child workload
// the RBG does not control"). It is written to compile against BOTH the base branch
// and the PR head so the same file can prove the premise on base and the fix on head.
//
// Polarity: every test here is a CONTRACT test — it asserts the intended-correct
// behavior. On the base branch the P0/F-leftover tests are expected to FAIL (that red
// run IS the premise reproduction); on the PR head they must PASS.

package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	lwsv1 "sigs.k8s.io/lws/api/leaderworkerset/v1"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

const (
	zzVerifyPreviousUID = types.UID("zz-previous-rbg-uid")
	zzVerifyCurrentUID  = types.UID("zz-current-rbg-uid")
	zzVerifyRevision    = "zz-verify-revision-hash"
)

func zzVerifyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, lwsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

func zzVerifyControllerRef(rbg *workloadsv1alpha2.RoleBasedGroup, uid types.UID) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         workloadsv1alpha2.GroupVersion.String(),
		Kind:               "RoleBasedGroup",
		Name:               rbg.Name,
		UID:                uid,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
}

// zzVerifyKind describes one workload kind: how to build a fully-ready object under a
// given ObjectMeta, an empty object for re-reads, and its reconciler.
type zzVerifyKind struct {
	kind       string
	role       workloadsv1alpha2.RoleSpec
	object     func(meta metav1.ObjectMeta) client.Object
	empty      func() client.Object
	reconciler func(s *runtime.Scheme, c client.Client) WorkloadReconciler
}

func zzVerifyKinds() []zzVerifyKind {
	return []zzVerifyKind{
		{
			kind: "RoleInstanceSet",
			role: wrappersv2.BuildStandaloneRole("worker").Obj(),
			object: func(meta metav1.ObjectMeta) client.Object {
				return &workloadsv1alpha2.RoleInstanceSet{
					ObjectMeta: meta,
					Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: ptr.To(int32(1))},
					Status: workloadsv1alpha2.RoleInstanceSetStatus{
						ObservedGeneration: 1, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1,
					},
				}
			},
			empty:      func() client.Object { return &workloadsv1alpha2.RoleInstanceSet{} },
			reconciler: func(s *runtime.Scheme, c client.Client) WorkloadReconciler { return NewRoleInstanceSetReconciler(s, c) },
		},
		{
			kind: "Deployment",
			role: wrappersv2.BuildStandaloneRole("worker").WithWorkload("apps/v1", "Deployment").Obj(),
			object: func(meta metav1.ObjectMeta) client.Object {
				return &appsv1.Deployment{
					ObjectMeta: meta,
					Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))},
					Status: appsv1.DeploymentStatus{
						ObservedGeneration: 1, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1,
					},
				}
			},
			empty:      func() client.Object { return &appsv1.Deployment{} },
			reconciler: func(s *runtime.Scheme, c client.Client) WorkloadReconciler { return NewDeploymentReconciler(s, c) },
		},
		{
			kind: "StatefulSet",
			role: wrappersv2.BuildStandaloneRole("worker").WithWorkload("apps/v1", "StatefulSet").Obj(),
			object: func(meta metav1.ObjectMeta) client.Object {
				return &appsv1.StatefulSet{
					ObjectMeta: meta,
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To(int32(1)),
						UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
							Type:          appsv1.RollingUpdateStatefulSetStrategyType,
							RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(0))},
						},
					},
					Status: appsv1.StatefulSetStatus{
						ObservedGeneration: 1, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1,
					},
				}
			},
			empty:      func() client.Object { return &appsv1.StatefulSet{} },
			reconciler: func(s *runtime.Scheme, c client.Client) WorkloadReconciler { return NewStatefulSetReconciler(s, c) },
		},
		{
			kind: "LeaderWorkerSet",
			role: wrappersv2.BuildLeaderWorkerRole("worker").Obj(),
			object: func(meta metav1.ObjectMeta) client.Object {
				return &lwsv1.LeaderWorkerSet{
					ObjectMeta: meta,
					Spec:       lwsv1.LeaderWorkerSetSpec{Replicas: ptr.To(int32(1))},
					Status:     lwsv1.LeaderWorkerSetStatus{Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
				}
			},
			empty:      func() client.Object { return &lwsv1.LeaderWorkerSet{} },
			reconciler: func(s *runtime.Scheme, c client.Client) WorkloadReconciler { return NewLeaderWorkerSetReconciler(s, c) },
		},
	}
}

func zzVerifyRBG(role workloadsv1alpha2.RoleSpec) *workloadsv1alpha2.RoleBasedGroup {
	rbg := wrappersv2.BuildBasicRoleBasedGroup("zz-verify-rbg", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	rbg.UID = zzVerifyCurrentUID
	return rbg
}

// TestZZVerifyP0LeftoverReadinessNotInherited is the premise (P0) contract: a workload
// left behind by a same-named RBG deleted in the background is still controlled by the
// OLD UID. A re-created RBG must not read readiness from it.
//
//	red on base  (ConstructRoleStatus returns the leftover's 1/1, CheckWorkloadReady=true)
//	green on head (0/0, false)
func TestZZVerifyP0LeftoverReadinessNotInherited(t *testing.T) {
	scheme := zzVerifyScheme(t)
	for _, k := range zzVerifyKinds() {
		t.Run(k.kind, func(t *testing.T) {
			ctx := context.Background()
			role := k.role
			rbg := zzVerifyRBG(role)

			leftover := k.object(metav1.ObjectMeta{
				Name:            rbg.GetWorkloadName(&role),
				Namespace:       rbg.Namespace,
				Generation:      1,
				Labels:          map[string]string{constants.GroupNameLabelKey: rbg.Name},
				OwnerReferences: []metav1.OwnerReference{zzVerifyControllerRef(rbg, zzVerifyPreviousUID)},
			})
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(leftover).Build()
			rec := k.reconciler(scheme, c)

			status, err := rec.ConstructRoleStatus(ctx, rbg, &role)
			require.NoError(t, err)
			assert.Zero(t, status.Replicas,
				"P0: replicas must not be inherited from a workload controlled by another UID")
			assert.Zero(t, status.ReadyReplicas,
				"P0: readyReplicas must not be inherited from a workload controlled by another UID")

			ready, err := rec.CheckWorkloadReady(ctx, rbg, &role)
			require.NoError(t, err)
			assert.False(t, ready,
				"P0: dependents must not trust readiness of a workload controlled by another UID")
		})
	}
}

// TestZZVerifyP0LeftoverReconcileDoesNotWrite is the second half of P0: reconciling a
// role whose name resolves to a leftover must not write to the leftover. The revision
// hash differs so the base branch cannot take its "equal, skip" path.
//
//	red on base  (apply silently re-points/extends ownership; returns nil error)
//	green on head (returns an error; owner references untouched)
func TestZZVerifyP0LeftoverReconcileDoesNotWrite(t *testing.T) {
	scheme := zzVerifyScheme(t)
	for _, k := range zzVerifyKinds() {
		t.Run(k.kind, func(t *testing.T) {
			ctx := context.Background()
			role := k.role
			rbg := zzVerifyRBG(role)
			staleRefs := []metav1.OwnerReference{zzVerifyControllerRef(rbg, zzVerifyPreviousUID)}

			leftover := k.object(metav1.ObjectMeta{
				Name:            rbg.GetWorkloadName(&role),
				Namespace:       rbg.Namespace,
				Generation:      1,
				Labels:          map[string]string{constants.GroupNameLabelKey: rbg.Name},
				OwnerReferences: staleRefs,
			})
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(leftover).Build()
			rec := k.reconciler(scheme, c)

			err := rec.Reconciler(ctx, rbg, &role, nil, zzVerifyRevision)
			assert.Error(t, err,
				"P0: reconciling over a foreign-controlled workload must fail instead of claiming it")

			got := k.empty()
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(leftover), got))
			assert.Equal(t, staleRefs, got.GetOwnerReferences(),
				"P0: the leftover's owner references must not be modified")
		})
	}
}

// TestZZVerifyOwnWorkloadManagedNormally is the guard against over-refusal: a workload
// controlled by THIS rbg keeps being read and applied exactly as before. Green on both
// base and head.
func TestZZVerifyOwnWorkloadManagedNormally(t *testing.T) {
	scheme := zzVerifyScheme(t)
	for _, k := range zzVerifyKinds() {
		t.Run(k.kind, func(t *testing.T) {
			ctx := context.Background()
			role := k.role
			rbg := zzVerifyRBG(role)

			owned := k.object(metav1.ObjectMeta{
				Name:            rbg.GetWorkloadName(&role),
				Namespace:       rbg.Namespace,
				Generation:      1,
				OwnerReferences: []metav1.OwnerReference{zzVerifyControllerRef(rbg, zzVerifyCurrentUID)},
			})
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(owned).Build()
			rec := k.reconciler(scheme, c)

			status, err := rec.ConstructRoleStatus(ctx, rbg, &role)
			require.NoError(t, err)
			assert.Equal(t, int32(1), status.ReadyReplicas,
				"guard: own workload readiness must keep being reported")

			ready, err := rec.CheckWorkloadReady(ctx, rbg, &role)
			require.NoError(t, err)
			assert.True(t, ready, "guard: own workload must stay ready")
		})
	}
}

// TestZZVerifyTerminatingWorkloadNotClaimed pins the "terminating" row: a workload that
// is going away must report 0/0 and refuse reconcile even when this rbg controls it,
// because the name is about to be free.
//
//	red on base  (1/1, ready, nil error — base has no terminating check)
//	green on head (0/0, not ready, error)
func TestZZVerifyTerminatingWorkloadNotClaimed(t *testing.T) {
	scheme := zzVerifyScheme(t)
	for _, k := range zzVerifyKinds() {
		t.Run(k.kind, func(t *testing.T) {
			ctx := context.Background()
			role := k.role
			rbg := zzVerifyRBG(role)

			terminating := k.object(metav1.ObjectMeta{
				Name:              rbg.GetWorkloadName(&role),
				Namespace:         rbg.Namespace,
				Generation:        1,
				OwnerReferences:   []metav1.OwnerReference{zzVerifyControllerRef(rbg, zzVerifyCurrentUID)},
				DeletionTimestamp: &metav1.Time{Time: time.Now()},
				Finalizers:        []string{"zz.verify/finalizer"},
			})
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(terminating).Build()
			rec := k.reconciler(scheme, c)

			status, err := rec.ConstructRoleStatus(ctx, rbg, &role)
			require.NoError(t, err)
			assert.Zero(t, status.ReadyReplicas, "a terminating workload must report 0 ready")

			ready, err := rec.CheckWorkloadReady(ctx, rbg, &role)
			require.NoError(t, err)
			assert.False(t, ready, "a terminating workload must not gate dependents as ready")

			assert.Error(t, rec.Reconciler(ctx, rbg, &role, nil, zzVerifyRevision),
				"reconcile must wait for a terminating workload to disappear")
		})
	}
}

// TestZZVerifyOrphanWithoutGroupLabelRejected: an object under the role's name that no
// controller owns and that does not carry this group's label is foreign and must be
// refused, not silently adopted.
//
//	red on base  (base applies over it and attaches its controller reference)
//	green on head (error, owner references stay empty)
func TestZZVerifyOrphanWithoutGroupLabelRejected(t *testing.T) {
	scheme := zzVerifyScheme(t)
	for _, k := range zzVerifyKinds() {
		t.Run(k.kind, func(t *testing.T) {
			ctx := context.Background()
			role := k.role
			rbg := zzVerifyRBG(role)

			orphan := k.object(metav1.ObjectMeta{
				Name:       rbg.GetWorkloadName(&role),
				Namespace:  rbg.Namespace,
				Generation: 1,
			})
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(orphan).Build()
			rec := k.reconciler(scheme, c)

			assert.Error(t, rec.Reconciler(ctx, rbg, &role, nil, zzVerifyRevision),
				"a foreign workload with no controller ref and no group label must not be claimed")

			got := k.empty()
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(orphan), got))
			assert.Empty(t, got.GetOwnerReferences(), "the foreign object must stay untouched")
		})
	}
}

// TestZZVerifyEqualOrphanAdopted pins the skip-path fix of the second commit: an
// orphaned workload (kubectl delete --cascade=orphan) whose spec already matches the
// desired one must STILL be applied so the controller reference is re-attached.
// Only kinds with a "semantically equal -> skip" fast path discriminate here.
//
//	red on base  ("equal, skip reconcile" leaves the orphan without a controller)
//	green on head (apply re-attaches the controller reference)
func TestZZVerifyEqualOrphanAdopted(t *testing.T) {
	scheme := zzVerifyScheme(t)
	ctx := context.Background()

	build := func(t *testing.T, kind string) (client.Object, WorkloadReconciler, *workloadsv1alpha2.RoleBasedGroup, workloadsv1alpha2.RoleSpec) {
		var role workloadsv1alpha2.RoleSpec
		var applyConfig any
		var obj client.Object
		var rec WorkloadReconciler
		seed := fake.NewClientBuilder().WithScheme(scheme).Build()
		switch kind {
		case "Deployment":
			role = wrappersv2.BuildStandaloneRole("worker").WithWorkload("apps/v1", "Deployment").Obj()
			r := NewDeploymentReconciler(scheme, seed)
			cfg, err := r.constructDeployApplyConfiguration(ctx, zzVerifyRBG(role), &role, &appsv1.Deployment{}, nil, zzVerifyRevision)
			require.NoError(t, err)
			applyConfig, obj, rec = cfg, &appsv1.Deployment{}, r
		case "StatefulSet":
			role = wrappersv2.BuildStandaloneRole("worker").WithWorkload("apps/v1", "StatefulSet").Obj()
			r := NewStatefulSetReconciler(scheme, seed)
			cfg, err := r.constructStatefulSetApplyConfiguration(ctx, zzVerifyRBG(role), &role, &appsv1.StatefulSet{}, zzVerifyRevision)
			require.NoError(t, err)
			applyConfig, rec = cfg, r
			sts := &appsv1.StatefulSet{}
			obj = sts
			defer func() {
				// the apiserver defaults updateStrategy; the reconcile path dereferences it
				sts.Spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{
					Type:          appsv1.RollingUpdateStatefulSetStrategyType,
					RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(0))},
				}
			}()
		case "LeaderWorkerSet":
			role = wrappersv2.BuildLeaderWorkerRole("worker").Obj()
			r := NewLeaderWorkerSetReconciler(scheme, seed)
			cfg, err := r.constructLWSApplyConfiguration(ctx, zzVerifyRBG(role), &role, nil, zzVerifyRevision)
			require.NoError(t, err)
			applyConfig, obj, rec = cfg, &lwsv1.LeaderWorkerSet{}, r
		default:
			t.Fatalf("unknown kind %s", kind)
		}
		raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(applyConfig)
		require.NoError(t, err)
		require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(raw, obj))
		obj.SetUID("zz-orphan-uid")
		obj.SetGeneration(1)
		obj.SetOwnerReferences(nil)
		return obj, rec, zzVerifyRBG(role), role
	}

	for _, kind := range []string{"Deployment", "StatefulSet", "LeaderWorkerSet"} {
		t.Run(kind, func(t *testing.T) {
			orphan, rec, rbg, role := build(t, kind)
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(orphan).Build()
			switch rec.(type) {
			case *DeploymentReconciler:
				rec = NewDeploymentReconciler(scheme, c)
			case *StatefulSetReconciler:
				rec = NewStatefulSetReconciler(scheme, c)
			case *LeaderWorkerSetReconciler:
				rec = NewLeaderWorkerSetReconciler(scheme, c)
			}

			require.NoError(t, rec.Reconciler(ctx, rbg, &role, nil, zzVerifyRevision))

			got := func() client.Object {
				switch kind {
				case "Deployment":
					return &appsv1.Deployment{}
				case "StatefulSet":
					return &appsv1.StatefulSet{}
				default:
					return &lwsv1.LeaderWorkerSet{}
				}
			}()
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(orphan), got))
			controller := metav1.GetControllerOf(got)
			require.NotNil(t, controller,
				"an equal-spec orphan must still be applied so the controller reference returns")
			assert.Equal(t, zzVerifyCurrentUID, controller.UID)
		})
	}
}
