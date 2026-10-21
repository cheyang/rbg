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

package statefulmode

// Reviewer verification harness for sgl-project/rbg#508 (issue #482).
// Canary V1: after a controller restart, the persisted
// status.rolloutReplacementState must retain the existing surge while the
// replacement base is still unavailable, then release it (and clear the
// state) once the replacement is Ready. This is the exact scenario that the
// base branch fails (surge condemned while replacement base is unavailable).
// Contract V2: the protection ends when the recorded scope converges.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	instancelisters "sigs.k8s.io/rbgs/client-go/listers/workloads/v1alpha2"
)

type verify508FixedControl struct {
	core                  *defaultStatefulInstanceSetControl
	current, intermediate *apps.ControllerRevision
	// update defaults to current (early rollback). V2 flips it to intermediate.
	update               *apps.ControllerRevision
	listCalls, syncCalls int
}

func (c *verify508FixedControl) ListRevisions(_ *workloadsv1alpha2.RoleInstanceSet) ([]*apps.ControllerRevision, error) {
	c.listCalls++
	return []*apps.ControllerRevision{c.current, c.intermediate}, nil
}

func (c *verify508FixedControl) AdoptOrphanRevisions(_ *workloadsv1alpha2.RoleInstanceSet, _ []*apps.ControllerRevision) error {
	return nil
}

func (c *verify508FixedControl) UpdateStatefulInstanceSet(ctx context.Context, set *workloadsv1alpha2.RoleInstanceSet, instances []*workloadsv1alpha2.RoleInstance) error {
	c.syncCalls++
	update := c.update
	if update == nil {
		update = c.current
	}
	status, err := c.core.updateStatefulInstanceSet(ctx, set, c.current, update, 0,
		instances, []*apps.ControllerRevision{c.current, c.intermediate})
	if err != nil {
		return err
	}
	return c.core.statusUpdater.UpdateInstanceSetStatus(ctx, set, status)
}

type verify508Harness struct {
	set           *workloadsv1alpha2.RoleInstanceSet
	setCache      cache.Indexer
	instanceCache cache.Indexer
	api           client.Client
	control       *verify508FixedControl
	objects       *fakeInstanceObjectManager
	reconciler    *ReconcileStatefulInstanceSet
	request       reconcile.Request
}

func newVerify508Harness(t *testing.T) *verify508Harness {
	t.Helper()
	set := newRevisionTestSet("nginx:1.0")
	set.Name, set.UID = "verify508-restart", types.UID(t.Name())
	set.ResourceVersion = "1"
	set.Spec.Replicas = ptr.To[int32](1)
	set.Spec.PodManagementPolicy = constants.ParallelPodManagement
	set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": set.Name}}
	set.Spec.UpdateStrategy.Type = workloadsv1alpha2.RecreatePodUpdateStrategyType
	set.Spec.UpdateStrategy.MaxSurge = ptr.To(intstr.FromInt32(1))
	set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstr.FromInt32(0))
	current, err := newRevision(set, 1, ptr.To[int32](0))
	require.NoError(t, err)
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:2.0"
	intermediate, err := newRevision(set, 2, ptr.To[int32](0))
	require.NoError(t, err)
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:1.0"
	// Both revisions resolve to A: the rollback target equals the current revision.
	set.Status.CurrentRevision, set.Status.UpdateRevision = current.Name, current.Name

	h := &verify508Harness{
		set:           set,
		setCache:      cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
		instanceCache: cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
		objects:       &fakeInstanceObjectManager{},
		request:       reconcile.Request{NamespacedName: types.NamespacedName{Namespace: set.Namespace, Name: set.Name}},
	}
	require.NoError(t, h.setCache.Add(set.DeepCopy()))
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	h.api = fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(set).WithObjects(set).Build()
	setLister := instancelisters.NewRoleInstanceSetLister(h.setCache)
	recorder := record.NewFakeRecorder(64)
	h.control = &verify508FixedControl{
		current: current, intermediate: intermediate,
		core: &defaultStatefulInstanceSetControl{
			instanceControl: NewStatefulInstanceControlFromManager(h.objects, recorder),
			inplaceControl:  &fakeInplaceControl{},
			statusUpdater:   NewRealStatefulInstanceSetStatusUpdater(h.api, setLister),
			recorder:        recorder,
		},
	}
	h.reconciler = &ReconcileStatefulInstanceSet{
		control:               h.control,
		instanceControl:       &realInstanceControl{client: h.api, recorder: recorder},
		roleInstanceSetLister: setLister,
		roleInstanceLister:    instancelisters.NewRoleInstanceLister(h.instanceCache),
	}
	t.Cleanup(func() {
		rolloutStatusExpectations.Delete(set)
		updateExpectations.DeleteExpectations(getInstanceSetKey(set))
		durationStore.Pop(getInstanceSetKey(set))
		instanceUnhealthySince.Delete(h.request.NamespacedName)
	})
	return h
}

func (h *verify508Harness) instance(ordinal int, revision string, ready bool) *workloadsv1alpha2.RoleInstance {
	instance := buildInst(h.set.Name, ordinal, revision, ready, true)
	instance.Labels["app"] = h.set.Name
	instance.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(h.set, controllerKind)}
	return instance
}

func (h *verify508Harness) reconcile(t *testing.T) reconcile.Result {
	t.Helper()
	h.objects.created, h.objects.deleted = nil, nil
	result, err := h.reconciler.Reconcile(context.Background(), h.request)
	require.NoError(t, err, "instance operations before return: created=%v, deleted=%v", h.objects.created, h.objects.deleted)
	return result
}

// syncSetCache mirrors the set informer delivering the persisted status write.
func (h *verify508Harness) syncSetCache(t *testing.T) *workloadsv1alpha2.RoleInstanceSet {
	t.Helper()
	stored := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, h.api.Get(context.Background(), client.ObjectKeyFromObject(h.set), stored))
	require.NoError(t, h.setCache.Update(stored))
	return stored
}

// seedPersistedReplacementState writes replacement intent through the real
// status updater, as the pre-restart controller would have done before
// deleting the stale base.
func (h *verify508Harness) seedPersistedReplacementState(t *testing.T) {
	t.Helper()
	writer := h.control.core.statusUpdater.(*realStatefulInstanceSetStatusUpdater)
	require.NoError(t, writer.PersistRolloutReplacementState(context.Background(), h.set,
		&workloadsv1alpha2.RolloutReplacementState{
			UpdateRevision: h.control.current.Name,
			Replicas:       1,
			Partition:      0,
		}))
	h.syncSetCache(t)
}

// TestVerifyPR508_V1_RestartRetainsSurgeUntilReplacementReady is the canary
// for issue #482: the base branch deletes the surge in this scenario.
func TestVerifyPR508_V1_RestartRetainsSurgeUntilReplacementReady(t *testing.T) {
	h := newVerify508Harness(t)
	h.seedPersistedReplacementState(t)

	// Post-restart state: stale B base already deleted, healthy A surge at
	// ordinal 1 is the only remaining instance.
	surge := h.instance(1, h.control.current.Name, true)
	require.NoError(t, h.instanceCache.Add(surge))

	h.reconcile(t)

	// The replacement base is created, and the surge must survive: deleting it
	// while the replacement is unavailable would violate maxUnavailable=0.
	require.Contains(t, h.objects.created, h.set.Name+"-0", "replacement base must be recreated")
	require.NotContains(t, h.objects.deleted, surge.Name,
		"CANARY: surge must be retained after restart while the replacement base is unavailable (issue #482)")

	// The replacement base becomes Ready; only then is the surge released.
	replacement := h.instance(0, h.control.current.Name, false)
	replacement.Generation = 1
	require.NoError(t, h.instanceCache.Add(replacement))
	h.syncSetCache(t)
	h.reconcile(t)
	require.NotContains(t, h.objects.deleted, surge.Name, "surge must survive while the replacement is not Ready")

	ready := replacement.DeepCopy()
	ready.Status.Conditions[0].Status = v1.ConditionTrue
	ready.Status.ObservedGeneration = ready.Generation
	require.NoError(t, h.instanceCache.Update(ready))
	h.syncSetCache(t)
	h.reconcile(t)
	require.Contains(t, h.objects.deleted, surge.Name,
		"CONTRACT: surge must be released once the replacement base is Ready")

	stored := h.syncSetCache(t)
	require.Nil(t, stored.Status.RolloutReplacementState,
		"CONTRACT: completed replacement intent must be cleared from status")
}

// TestVerifyPR508_V2_RevisionChangeEndsProtection pins the invalidation rule:
// once the target revision changes, the recorded scope must not retain surge.
func TestVerifyPR508_V2_RevisionChangeEndsProtection(t *testing.T) {
	h := newVerify508Harness(t)
	h.seedPersistedReplacementState(t)

	surge := h.instance(1, h.control.current.Name, true)
	require.NoError(t, h.instanceCache.Add(surge))
	// A base at B exists so the rollout to B has real work in range.
	base := h.instance(0, h.control.intermediate.Name, true)
	require.NoError(t, h.instanceCache.Add(base))

	// The spec now rolls toward B: updateRevision no longer matches the
	// recorded state for A, so the old protection must end.
	h.control.update = h.control.intermediate
	stored := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, h.api.Get(context.Background(), client.ObjectKeyFromObject(h.set), stored))
	stored.Status.UpdateRevision = h.control.intermediate.Name
	require.NoError(t, h.api.Status().Update(context.Background(), stored))
	h.syncSetCache(t)

	h.reconcile(t)
	// With the rollout to B active and every base in range at B healthy, the
	// A surge falls out of the update range and is condemned per normal
	// rollout semantics; the recorded scope for A must not pin it.
	require.Contains(t, h.objects.deleted, surge.Name,
		"CONTRACT: a changed target revision must end the recorded scope's surge protection")
}
