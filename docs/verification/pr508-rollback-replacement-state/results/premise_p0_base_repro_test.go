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

// Premise P0 repro for sgl-project/rbg#482 on the BASE branch (main @ ecd fd6e6):
// a stateful early rollback (A -> B -> A) that lost its stale base instance,
// seen by a controller process that restarted after the deletion began. The
// in-memory earlyRollbackReplacementSignals map is empty in this fresh process,
// so there is no process-local replacement signal. The expected symptom
// (issue #482 step 9-10): the controller treats the rollout as complete and
// condemns the existing surge while the replacement base is still unavailable,
// violating maxUnavailable=0.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apps "k8s.io/api/apps/v1"
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

type p0FixedControl struct {
	core                  *defaultStatefulInstanceSetControl
	current, intermediate *apps.ControllerRevision
	listCalls, syncCalls  int
}

func (c *p0FixedControl) ListRevisions(_ *workloadsv1alpha2.RoleInstanceSet) ([]*apps.ControllerRevision, error) {
	c.listCalls++
	return []*apps.ControllerRevision{c.current, c.intermediate}, nil
}

func (c *p0FixedControl) AdoptOrphanRevisions(_ *workloadsv1alpha2.RoleInstanceSet, _ []*apps.ControllerRevision) error {
	return nil
}

func (c *p0FixedControl) UpdateStatefulInstanceSet(ctx context.Context, set *workloadsv1alpha2.RoleInstanceSet, instances []*workloadsv1alpha2.RoleInstance) error {
	c.syncCalls++
	// Early rollback on a fresh process: CurrentRevision and UpdateRevision
	// both resolve to A, and the stale B base is already deleted. Only the
	// healthy A surge remains at ordinal 1.
	status, err := c.core.updateStatefulInstanceSet(ctx, set, c.current, c.current, 0,
		instances, []*apps.ControllerRevision{c.current, c.intermediate})
	if err != nil {
		return err
	}
	return c.core.statusUpdater.UpdateInstanceSetStatus(ctx, set, status)
}

type p0Harness struct {
	set           *workloadsv1alpha2.RoleInstanceSet
	instanceCache cache.Indexer
	api           client.Client
	control       *p0FixedControl
	objects       *fakeInstanceObjectManager
	reconciler    *ReconcileStatefulInstanceSet
	request       reconcile.Request
}

func newP0Harness(t *testing.T) *p0Harness {
	t.Helper()
	set := newRevisionTestSet("nginx:1.0")
	set.Name, set.UID = "p0-restart", types.UID(t.Name())
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

	h := &p0Harness{
		set:           set,
		instanceCache: cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
		objects:       &fakeInstanceObjectManager{},
		request:       reconcile.Request{NamespacedName: types.NamespacedName{Namespace: set.Namespace, Name: set.Name}},
	}
	setCache := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	require.NoError(t, setCache.Add(set.DeepCopy()))
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	h.api = fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(set).WithObjects(set).Build()
	setLister := instancelisters.NewRoleInstanceSetLister(setCache)
	recorder := record.NewFakeRecorder(64)
	h.control = &p0FixedControl{
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
		updateExpectations.DeleteExpectations(getInstanceSetKey(set))
		durationStore.Pop(getInstanceSetKey(set))
		instanceUnhealthySince.Delete(h.request.NamespacedName)
	})
	return h
}

func (h *p0Harness) instance(ordinal int, revision string, ready bool) *workloadsv1alpha2.RoleInstance {
	instance := buildInst(h.set.Name, ordinal, revision, ready, true)
	instance.Labels["app"] = h.set.Name
	instance.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(h.set, controllerKind)}
	return instance
}

func (h *p0Harness) reconcile(t *testing.T) reconcile.Result {
	t.Helper()
	h.objects.created, h.objects.deleted = nil, nil
	result, err := h.reconciler.Reconcile(context.Background(), h.request)
	require.NoError(t, err, "instance operations before return: created=%v, deleted=%v", h.objects.created, h.objects.deleted)
	return result
}

func TestPremiseP0RestartLosesSurgeProtection(t *testing.T) {
	h := newP0Harness(t)
	// Post-restart state: the stale B base at ordinal 0 is already deleted
	// (replacement in flight), the healthy A surge at ordinal 1 is the only
	// remaining instance. In-memory replacement signals are empty (fresh process).
	surge := h.instance(1, h.control.current.Name, true)
	require.NoError(t, h.instanceCache.Add(surge))

	h.reconcile(t)

	// Issue #482 symptom: with no stale base visible and CurrentRevision ==
	// UpdateRevision, the rollout looks complete, so the surge is condemned
	// while the replacement base is still unavailable (maxUnavailable=0 violated).
	t.Logf("created=%v deleted=%v", h.objects.created, h.objects.deleted)
	require.Contains(t, h.objects.deleted, surge.Name,
		"BASE BRANCH: surge condemned after restart while replacement base is unavailable (issue #482 symptom reproduced)")
}
