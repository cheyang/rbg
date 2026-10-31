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

// Review harness for sgl-project/rbg#535 (additive; no production code changes).
// Polarity legend:
//   - TestVerifyP0*     : bug-canary, runs against BASE. PASS there proves the
//                         premise (hook bypassed). It must FAIL on the fixed head.
//   - TestVerifyContract*: contract tests for the fixed behavior. FAIL on base,
//                         PASS on the fixed head.
//   - TestVerifyF1*     : bug-canary for the regret gap (no PreparingDelete ->
//                         Normal rescue in stateful mode). PASSES while the gap
//                         exists; must FLIP to fail once a rescue path lands.
package statefulmode

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	instancelifecycle "sigs.k8s.io/rbgs/pkg/inplace/instance/lifecycle"
)

const verifyHookLabel = "verify.example.com/drain-protection"

type verifyRecordingClient struct {
	client.Client
	deletes []string
}

func (c *verifyRecordingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.deletes = append(c.deletes, obj.GetName())
	return c.Client.Delete(ctx, obj, opts...)
}

func newVerifyControl(t *testing.T, instances ...*workloadsv1alpha2.RoleInstance) (*defaultStatefulInstanceSetControl, *verifyRecordingClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	objects := make([]client.Object, 0, len(instances))
	for _, instance := range instances {
		objects = append(objects, instance)
	}
	c := &verifyRecordingClient{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(objects...).Build()}
	recorder := record.NewFakeRecorder(64)
	return &defaultStatefulInstanceSetControl{
		instanceControl:  NewStatefulInstanceControl(c, nil, recorder),
		lifecycleControl: instancelifecycle.New(c),
	}, c
}

func verifyGetInstance(t *testing.T, c client.Client, instance *workloadsv1alpha2.RoleInstance) *workloadsv1alpha2.RoleInstance {
	t.Helper()
	got := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(instance), got))
	return got
}

func verifyHookedSet(name string, replicas int32) *workloadsv1alpha2.RoleInstanceSet {
	set := buildSet(name, replicas, nil, nil)
	set.Spec.Lifecycle = &workloadsv1alpha2.RoleInstanceSetLifecycle{
		PreDelete: &workloadsv1alpha2.RoleInstanceSetLifecycleHook{
			LabelsHandler: map[string]string{verifyHookLabel: "hold"},
		},
	}
	return set
}

// P0 (premise, bug-canary, runsAgainst=base): issue #534 claims
// "DeleteStatefulInstance() calls DeleteInstance() without checking the
// configured PreDelete hook". On the base code a hooked condemned instance is
// deleted outright; this canary PASSES there and must FAIL once the hook is
// honored.
func TestVerifyP0HookedCondemnedInstanceDeletedOutright(t *testing.T) {
	set := verifyHookedSet("p0", 1)
	instance := buildInst(set.Name, 0, testOldRev, true, true)
	instance.Labels[verifyHookLabel] = "hold"
	control, c := newVerifyControl(t, instance)

	shouldExit, err := control.processCondemned(context.Background(), set, nil, false,
		[]*workloadsv1alpha2.RoleInstance{instance}, 0)
	require.NoError(t, err)
	_ = shouldExit
	require.NotEmpty(t, c.deletes,
		"P0 canary: base deletes a hooked instance outright; on the fixed code no delete is issued")
}

// Contract (fix behavior, head must PASS, base must FAIL): a hooked instance is
// retained and enters PreparingDelete; deleting resumes only after the hook is
// released. Independent re-derivation of the PR's own
// TestStatefulPreDeleteBlocksAndReleases for the scale-down entry point.
func TestVerifyContractScaleDownBlockedUntilRelease(t *testing.T) {
	set := verifyHookedSet("contract", 1)
	instance := buildInst(set.Name, 0, testOldRev, true, true)
	instance.Labels[verifyHookLabel] = "hold"
	control, c := newVerifyControl(t, instance)

	// Repeated reconciles must not bypass the hook.
	for i := 0; i < 3; i++ {
		_, err := control.processCondemned(context.Background(), set, nil, false,
			[]*workloadsv1alpha2.RoleInstance{instance}, 0)
		require.NoError(t, err)
		require.Empty(t, c.deletes, "reconcile %d: hooked instance must not be deleted", i)
		got := verifyGetInstance(t, c, instance)
		require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete,
			instancelifecycle.GetRoleInstanceLifecycleState(got))
		require.Nil(t, got.DeletionTimestamp)
		instance = got
	}

	// Release the hook: metadata-only change must allow the next reconcile to delete.
	got := verifyGetInstance(t, c, instance)
	delete(got.Labels, verifyHookLabel)
	require.NoError(t, c.Update(context.Background(), got))
	_, err := control.processCondemned(context.Background(), set, nil, false,
		[]*workloadsv1alpha2.RoleInstance{got}, 0)
	require.NoError(t, err)
	require.Equal(t, []string{instance.Name}, c.deletes)
}

// F1 (bug-canary, runsAgainst=head): once a Stateful instance has entered
// PreparingDelete through a blocked scale-down, canceling the scale-down
// (instance back in range) never returns it to Normal — no code path in the
// stateful reconciler clears the lifecycle state (stateless mode has
// managePreparingDelete for exactly this). This canary PASSES while the gap
// exists; a fix must FLIP it to red (then invert the assertion).
func TestVerifyF1RegretLeavesInstanceInPreparingDelete(t *testing.T) {
	set := verifyHookedSet("regret", 1)
	instance := buildInst(set.Name, 0, testOldRev, true, true)
	instance.Labels[verifyHookLabel] = "hold"
	control, c := newVerifyControl(t, instance)

	// Phase 1: condemned + hooked -> blocked, enters PreparingDelete.
	_, err := control.processCondemned(context.Background(), set, nil, false,
		[]*workloadsv1alpha2.RoleInstance{instance}, 0)
	require.NoError(t, err)
	require.Empty(t, c.deletes)
	instance = verifyGetInstance(t, c, instance)
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete,
		instancelifecycle.GetRoleInstanceLifecycleState(instance))

	// Phase 2: regret — replicas scaled back up, instance is in range again and
	// flows through the replica (not condemned) path. processReplica is the only
	// per-replica handler in the stateful controller.
	status := &workloadsv1alpha2.RoleInstanceSetStatus{}
	for _, monotonic := range []bool{false, true} {
		_, _, err := control.processReplica(context.Background(), set, set, monotonic,
			[]*workloadsv1alpha2.RoleInstance{instance}, 0, status, 1)
		require.NoError(t, err)
	}

	got := verifyGetInstance(t, c, instance)
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete,
		instancelifecycle.GetRoleInstanceLifecycleState(got),
		"F1 canary: regretted instance keeps the stale PreparingDelete state; flip me when a rescue path lands")
	require.Empty(t, c.deletes)
}
