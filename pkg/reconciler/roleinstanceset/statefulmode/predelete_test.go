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

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	instancelifecycle "sigs.k8s.io/rbgs/pkg/inplace/instance/lifecycle"
)

const preDeleteTestLabel = "example.com/drain-protection"

// Exercise the real lifecycle patches and delete calls, including the release
// path. A finalizer alone must not count as protection: it only delays garbage
// collection after deletion has already started.
func TestStatefulPreDeleteBlocksAndReleases(t *testing.T) {
	for _, handler := range []string{"label", "finalizer", "both"} {
		for _, markNotReady := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/markNotReady=%t", handler, markNotReady), func(t *testing.T) {
				set := buildSet("predelete", 1, nil, nil)
				hook := &workloadsv1alpha2.RoleInstanceSetLifecycleHook{MarkNotReady: markNotReady}
				instance := buildInst(set.Name, 0, testOldRev, true, true)
				if handler != "finalizer" {
					hook.LabelsHandler = map[string]string{preDeleteTestLabel: "hold"}
					instance.Labels[preDeleteTestLabel] = "hold"
				}
				if handler != "label" {
					hook.FinalizersHandler = []string{preDeleteTestLabel}
					instance.Finalizers = []string{preDeleteTestLabel}
				}
				if markNotReady {
					// Shared readiness-gate initialization/Pod propagation is a
					// separate task in #534. Verify the existing RI-level policy.
					instance.Spec.ReadinessGates = []workloadsv1alpha2.RoleInstanceReadinessGate{{ConditionType: workloadsv1alpha2.RoleInstanceCustomReady}}
					instance.Status.Conditions = append(instance.Status.Conditions, workloadsv1alpha2.RoleInstanceCondition{
						Type: workloadsv1alpha2.RoleInstanceCustomReady, Status: corev1.ConditionTrue,
					})
				}
				set.Spec.Lifecycle = &workloadsv1alpha2.RoleInstanceSetLifecycle{PreDelete: hook}
				control, c := newPreDeleteTestControl(t, instance)
				before := instance.DeepCopy()

				for range 2 {
					_, deleting, err := control.deleteInstance(set, instance)
					require.NoError(t, err)
					require.False(t, deleting, "waiting for a hook is not a successful delete")
					require.Empty(t, c.deletes)
					got := getPreDeleteTestInstance(t, c, instance)
					require.Nil(t, got.DeletionTimestamp, "the delete request must not be issued")
					require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete, instancelifecycle.GetRoleInstanceLifecycleState(got))
					for _, condition := range got.Status.Conditions {
						if condition.Type == workloadsv1alpha2.RoleInstanceReady {
							want := corev1.ConditionTrue
							if markNotReady {
								want = corev1.ConditionFalse
							}
							require.Equal(t, want, condition.Status)
						}
					}
					require.Equal(t, before, instance, "must not mutate the informer object")
					instance = got
					before = got.DeepCopy()
				}

				got := getPreDeleteTestInstance(t, c, instance)
				delete(got.Labels, preDeleteTestLabel)
				require.NoError(t, c.Update(context.Background(), got))
				if handler == "both" {
					_, deleting, err := control.deleteInstance(set, got)
					require.NoError(t, err)
					require.False(t, deleting, "the remaining finalizer still matches")
					require.Empty(t, c.deletes)
				}
				got = getPreDeleteTestInstance(t, c, instance)
				got.Finalizers = nil
				require.NoError(t, c.Update(context.Background(), got))
				_, deleting, err := control.deleteInstance(set, got)
				require.NoError(t, err)
				require.True(t, deleting)
				require.Equal(t, []string{instance.Name}, c.deletes)
				require.True(t, apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKeyFromObject(instance), &workloadsv1alpha2.RoleInstance{})))
			})
		}
	}
}

func TestStatefulPreDeleteDoesNotChangeUnhookedDeletion(t *testing.T) {
	for _, lifecycle := range []*workloadsv1alpha2.RoleInstanceSetLifecycle{
		nil,
		{},
		{PreDelete: &workloadsv1alpha2.RoleInstanceSetLifecycleHook{LabelsHandler: map[string]string{preDeleteTestLabel: "hold"}}},
	} {
		set := buildSet("unhooked", 1, nil, nil)
		set.Spec.Lifecycle = lifecycle
		instance := buildInst(set.Name, 0, testOldRev, true, true)
		control, c := newPreDeleteTestControl(t, instance)
		_, deleting, err := control.deleteInstance(set, instance)
		require.NoError(t, err)
		require.True(t, deleting)
		require.Equal(t, []string{instance.Name}, c.deletes)
	}
}

func TestStatefulPreDeletePatchFailureDoesNotDelete(t *testing.T) {
	set := buildSet("patch-failure", 1, nil, nil)
	set.Spec.Lifecycle = &workloadsv1alpha2.RoleInstanceSetLifecycle{PreDelete: &workloadsv1alpha2.RoleInstanceSetLifecycleHook{
		LabelsHandler: map[string]string{preDeleteTestLabel: "hold"},
	}}
	instance := buildInst(set.Name, 0, testOldRev, true, true)
	instance.Labels[preDeleteTestLabel] = "hold"
	control, c := newPreDeleteTestControl(t, instance)
	c.patchErr = errors.New("injected lifecycle patch failure")
	_, deleting, err := control.deleteInstance(set, instance)
	require.ErrorIs(t, err, c.patchErr)
	require.False(t, deleting)
	require.Empty(t, c.deletes)
	require.Nil(t, getPreDeleteTestInstance(t, c, instance).DeletionTimestamp)
}

func TestStatefulPreDeleteScaleDownOrdering(t *testing.T) {
	for _, monotonic := range []bool{false, true} {
		t.Run(fmt.Sprintf("ordered=%t", monotonic), func(t *testing.T) {
			set := buildSet("scale-down", 1, nil, nil)
			set.Spec.Lifecycle = &workloadsv1alpha2.RoleInstanceSetLifecycle{PreDelete: &workloadsv1alpha2.RoleInstanceSetLifecycleHook{
				LabelsHandler: map[string]string{preDeleteTestLabel: "hold"},
			}}
			condemned := []*workloadsv1alpha2.RoleInstance{
				buildInst(set.Name, 2, testOldRev, true, true),
				buildInst(set.Name, 1, testOldRev, true, true),
			}
			for _, instance := range condemned {
				instance.Labels[preDeleteTestLabel] = "hold"
			}
			control, c := newPreDeleteTestControl(t, condemned...)
			_, err := runForAll(condemned, func(i int) (bool, error) {
				return control.processCondemned(context.Background(), set, nil, monotonic, condemned, i)
			}, monotonic)
			require.NoError(t, err)
			require.Empty(t, c.deletes)
			for i, instance := range condemned {
				got := getPreDeleteTestInstance(t, c, instance)
				want := constants.RoleInstanceLifecycleStatePreparingDelete
				if monotonic && i > 0 {
					want = ""
				}
				require.Equal(t, want, instancelifecycle.GetRoleInstanceLifecycleState(got))
				require.Nil(t, got.DeletionTimestamp)
			}
		})
	}
}

func TestStatefulPreDeleteRolloutKeepsCurrentReplicas(t *testing.T) {
	set := buildSet("rollout-hook", 2, ptr.To(intstr.FromInt32(0)), ptr.To(intstr.FromInt32(1)))
	set.Spec.UpdateStrategy.Type = workloadsv1alpha2.RecreatePodUpdateStrategyType
	set.Spec.Lifecycle = &workloadsv1alpha2.RoleInstanceSetLifecycle{PreDelete: &workloadsv1alpha2.RoleInstanceSetLifecycleHook{
		LabelsHandler: map[string]string{preDeleteTestLabel: "hold"},
	}}
	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(set.Name, 0, testOldRev, true, true),
		buildInst(set.Name, 1, testOldRev, true, true),
	}
	for _, instance := range instances {
		instance.Labels[preDeleteTestLabel] = "hold"
	}
	control, c := newPreDeleteTestControl(t, instances...)
	control.inplaceControl = &fakeInplaceControl{}
	current, update := newStatefulRevision(testOldRev, 1), newStatefulRevision(testUpdateRev, 2)
	topo, err := computeTopology(set, instances, current.Name, update.Name, false)
	require.NoError(t, err)
	status := &workloadsv1alpha2.RoleInstanceSetStatus{CurrentReplicas: 2}
	status, err = control.progressUpdate(context.Background(), set, status, current, update, nil, instances, instances, 0, topo)
	require.NoError(t, err)
	require.Empty(t, c.deletes)
	require.EqualValues(t, 2, status.CurrentReplicas, "a blocked rollout has not removed an old replica")
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete, instancelifecycle.GetRoleInstanceLifecycleState(getPreDeleteTestInstance(t, c, instances[1])))
	require.Empty(t, instancelifecycle.GetRoleInstanceLifecycleState(getPreDeleteTestInstance(t, c, instances[0])), "the unavailable budget still limits this pass")

	// Releasing the highest ordinal permits exactly one actual deletion.
	instances[1] = getPreDeleteTestInstance(t, c, instances[1])
	delete(instances[1].Labels, preDeleteTestLabel)
	require.NoError(t, c.Update(context.Background(), instances[1]))
	status, err = control.progressUpdate(context.Background(), set, status, current, update, nil, instances, instances, 0, topo)
	require.NoError(t, err)
	require.Equal(t, []string{instances[1].Name}, c.deletes)
	require.EqualValues(t, 1, status.CurrentReplicas)
}

type preDeleteTestClient struct {
	client.Client
	deletes  []string
	patchErr error
}

func (c *preDeleteTestClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.deletes = append(c.deletes, obj.GetName())
	return c.Client.Delete(ctx, obj, opts...)
}

func (c *preDeleteTestClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.patchErr != nil {
		return c.patchErr
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func newPreDeleteTestControl(t *testing.T, instances ...*workloadsv1alpha2.RoleInstance) (*defaultStatefulInstanceSetControl, *preDeleteTestClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	objects := make([]client.Object, 0, len(instances))
	for _, instance := range instances {
		objects = append(objects, instance)
	}
	c := &preDeleteTestClient{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(objects...).Build()}
	recorder := record.NewFakeRecorder(32)
	return &defaultStatefulInstanceSetControl{
		instanceControl:  NewStatefulInstanceControl(c, nil, recorder),
		lifecycleControl: instancelifecycle.New(c),
	}, c
}

func getPreDeleteTestInstance(t *testing.T, c client.Client, instance *workloadsv1alpha2.RoleInstance) *workloadsv1alpha2.RoleInstance {
	t.Helper()
	got := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(instance), got))
	return got
}
