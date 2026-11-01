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

// Reviewer A (Claude) differential half of F2 for PR sgl-project/rbg#537.
//
// The stateful half (TestClaudeF2PartialRestoreKeepsStatefulHandshakeOpen in
// pkg/reconciler/roleinstanceset/statefulmode) shows the PR's stateful
// completion rule keeps an instance in Updated while only part of a
// multi-handler hook is restored. This test runs the EXACT same fixture shape
// through the stateless reference implementation
// (sync/update.go refreshInstanceState) and shows it completes to Normal
// instead, because stateless uses !IsRoleInstanceHooked where the PR's
// stateful code uses !IsRoleInstanceAllHooked.
//
// CANARY test: documents current stateless behavior, which this PR does not
// change. It exists to make the stateful/stateless divergence observable.

package sync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	instancelifecycle "sigs.k8s.io/rbgs/pkg/inplace/instance/lifecycle"
)

func TestClaudeF2StatelessPartialRestoreCompletesImmediately(t *testing.T) {
	const label = "claude.example.com/inplace-protection"
	const finalizer = "claude.example.com/inplace-protection"

	gs := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-stateless", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleInstanceSetSpec{
			Lifecycle: &workloadsv1alpha2.RoleInstanceSetLifecycle{
				InPlaceUpdate: &workloadsv1alpha2.RoleInstanceSetLifecycleHook{
					LabelsHandler:     map[string]string{label: "hold"},
					FinalizersHandler: []string{finalizer},
				},
			},
		},
	}

	// Partial restore after the update: label handler back, finalizer missing.
	// Same fixture shape as the stateful F2 test.
	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "claude-stateless-0",
			Namespace:       "default",
			UID:             types.UID("claude-stateless-0-uid"),
			ResourceVersion: "1",
			Labels: map[string]string{
				apps.ControllerRevisionHashLabelKey: "rev-2",
				label:                               "hold",
				constants.RoleInstanceSetLifecycleStateKey: string(constants.RoleInstanceLifecycleStateUpdating),
			},
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			Components: []workloadsv1alpha2.RoleInstanceComponent{{Name: "worker"}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1,
			ComponentStatuses:  []workloadsv1alpha2.RoleInstanceComponentStatus{{Name: "worker", UpdatedReplicas: 1}},
		},
	}
	instance.Generation = 1

	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).WithStatusSubresource(instance).Build()

	rc := &realControl{
		Client:           fakeClient,
		inplaceControl:   &fakeStatelessInplaceControl{},
		lifecycleControl: instancelifecycle.New(fakeClient),
		recorder:         record.NewFakeRecorder(10),
	}

	modified, _, err := rc.refreshInstanceState(gs, &fakeStatelessCoreControl{}, instance)
	require.NoError(t, err)
	require.True(t, modified)

	got := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, got))
	require.Equal(t, constants.RoleInstanceLifecycleStateNormal, instancelifecycle.GetRoleInstanceLifecycleState(got),
		"F2 divergence: stateless completes the handshake on partial restore while stateful keeps waiting")
}
