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

package workloads

// Verification harness for PR #510 (status: add lifecycle phase and aggregated
// condition reporting), reviewer branch verify/status-phase-conditions-claude.
//
// F-lws-ready (contract test): a fully converged LeaderWorkerSet child must
// make the owning role report ready=true. LWS v0.7.0 never sets
// ObservedGeneration on its status conditions and has no
// status.observedGeneration field, so roleWorkloadStatus' `fresh` gate
// (`available.ObservedGeneration >= child.Generation`) can never pass for a
// real LWS child. On the PR head this test FAILS (reproduction); with the
// fresh-gate fixed it PASSES.

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lwsv1 "sigs.k8s.io/lws/api/leaderworkerset/v1"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// newLWSRoleFixture builds an RBG with one LeaderWorkerSet role plus a fully
// converged LWS child, mirroring what the LWS v0.7.0 controller actually
// persists: Available=True (AllGroupsReady) with NO ObservedGeneration, and
// replica counts matching the desired spec.
func newLWSRoleFixture() (*workloadsv1alpha2.RoleBasedGroup, *lwsv1.LeaderWorkerSet, workloadsv1alpha2.RoleSpec) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "status", Namespace: "default", UID: "rbg", Generation: 1},
	}
	role := workloadsv1alpha2.RoleSpec{
		Name:     "role-lws",
		Replicas: ptr.To[int32](1),
		Annotations: map[string]string{
			constants.RoleWorkloadTypeAnnotationKey: constants.LeaderWorkerSetWorkloadType,
		},
	}
	rbg.Spec.Roles = []workloadsv1alpha2.RoleSpec{role}

	lws := &lwsv1.LeaderWorkerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       rbg.GetWorkloadName(&role),
			Namespace:  rbg.Namespace,
			UID:        "lws",
			Generation: 1,
			Labels: map[string]string{
				// The RBG workload reconcilers stamp this label; roleWorkloadStatus
				// requires it to match the expected role revision hash.
				fmt.Sprintf(constants.RoleRevisionLabelKeyFmt, "role-lws"): "role-revision",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: workloadsv1alpha2.GroupVersion.String(),
				Kind:       "RoleBasedGroup",
				Name:       rbg.Name,
				UID:        rbg.UID,
				Controller: ptr.To(true),
			}},
		},
		Spec: lwsv1.LeaderWorkerSetSpec{Replicas: ptr.To[int32](1)},
		// Exactly the steady-state shape LWS v0.7.0 writes (leaderworkerset_controller.go:
		// makeCondition() sets only Type/Status/Reason/Message/LastTransitionTime —
		// never ObservedGeneration).
		Status: lwsv1.LeaderWorkerSetStatus{
			Replicas:        1,
			ReadyReplicas:   1,
			UpdatedReplicas: 1,
			Conditions: []metav1.Condition{{
				Type:               string(lwsv1.LeaderWorkerSetAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             "AllGroupsReady",
				Message:            "All replicas are ready",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
	return rbg, lws, role
}

// TestClaudeRoleWorkloadStatusLWSReady is the F-lws-ready contract test:
// a converged LWS child (Available=True, 1/1/1, revision label matching)
// must yield ready=true for the role.
func TestClaudeRoleWorkloadStatusLWSReady(t *testing.T) {
	rbg, lws, role := newLWSRoleFixture()
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, lwsv1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rbg, lws).Build()

	r := &RoleBasedGroupReconciler{client: c, scheme: scheme}
	phase, reason, ready, err := r.roleWorkloadStatus(context.Background(), rbg, &role, "role-revision")
	require.NoError(t, err)
	require.Equal(t, workloadsv1alpha2.RoleBasedGroupPhaseRunning, phase, "converged LWS child should project RBG phase Running, got reason %q", reason)
	require.True(t, ready, "F-lws-ready: LWS child is converged (Available=True, Replicas=1/1, UpdatedReplicas=1, revision label matches) but roleWorkloadStatus reports ready=false — the `fresh` gate compares available.ObservedGeneration (never set by LWS v0.7.0) against child.Generation")
}

// TestClaudeRoleWorkloadStatusLWSObservedGenerationSet is the same shape with
// ObservedGeneration populated, documenting that the rest of the LWS branch
// is correct and only the ObservedGeneration gate blocks readiness.
func TestClaudeRoleWorkloadStatusLWSObservedGenerationSet(t *testing.T) {
	rbg, lws, role := newLWSRoleFixture()
	lws.Status.Conditions[0].ObservedGeneration = 1
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, lwsv1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rbg, lws).Build()

	r := &RoleBasedGroupReconciler{client: c, scheme: scheme}
	_, _, ready, err := r.roleWorkloadStatus(context.Background(), rbg, &role, "role-revision")
	require.NoError(t, err)
	require.True(t, ready, "with ObservedGeneration populated the LWS branch must report ready=true; failure here means the test setup, not the PR, is wrong")
}

// TestClaudeRoleWorkloadStatusDeploymentReady is a control: the Deployment
// branch (ObservedGeneration comes from status, which Deployment does
// populate) reports ready=true for a converged child. It guards the harness
// against setup mistakes that would make the LWS test red for the wrong
// reason.
func TestClaudeRoleWorkloadStatusDeploymentReady(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "status", Namespace: "default", UID: "rbg", Generation: 1},
	}
	role := workloadsv1alpha2.RoleSpec{
		Name:     "role-deploy",
		Replicas: ptr.To[int32](1),
		Annotations: map[string]string{
			constants.RoleWorkloadTypeAnnotationKey: constants.DeploymentWorkloadType,
		},
	}
	rbg.Spec.Roles = []workloadsv1alpha2.RoleSpec{role}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       rbg.GetWorkloadName(&role),
			Namespace:  rbg.Namespace,
			UID:        "deploy",
			Generation: 1,
			Labels: map[string]string{
				fmt.Sprintf(constants.RoleRevisionLabelKeyFmt, "role-deploy"): "role-revision",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: workloadsv1alpha2.GroupVersion.String(),
				Kind:       "RoleBasedGroup",
				Name:       rbg.Name,
				UID:        rbg.UID,
				Controller: ptr.To(true),
			}},
		},
		Spec:   appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
	}
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rbg, deploy).Build()

	r := &RoleBasedGroupReconciler{client: c, scheme: scheme}
	_, _, ready, err := r.roleWorkloadStatus(context.Background(), rbg, &role, "role-revision")
	require.NoError(t, err)
	require.True(t, ready)

}
