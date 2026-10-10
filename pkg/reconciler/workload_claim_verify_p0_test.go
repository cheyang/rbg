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

// Verification harness for PR #487 — claim P0 (premise), unit layer.
//
// POLARITY: contract. This test asserts the INTENDED behavior: a role's status must not
// be read from a workload the RBG does not control (foreign controller UID).
//   - on the BASE branch (0821cb5b, before the PR) it FAILS — that failure is the
//     reproduction of the premise: readiness inherited from a predecessor's leftover.
//   - on the PR head it PASSES.
// It deliberately uses only API surface that exists on both base and head, so the same
// file can be grafted onto the base commit for the premise check.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

const verifyClaimForeignUID = types.UID("verify-claim-foreign-rbg-uid")

// verifyClaimForeignLeftoverDeploy builds the workload a background-deleted RBG leaves
// behind: sitting at the name a role maps to, controlled by a UID that no longer exists,
// and fully "ready" until the garbage collector reclaims it.
func verifyClaimForeignLeftoverDeploy(rbg *workloadsv1alpha2.RoleBasedGroup, role *workloadsv1alpha2.RoleSpec) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       rbg.GetWorkloadName(role),
			Namespace:  rbg.Namespace,
			Generation: 1,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: workloadsv1alpha2.GroupVersion.String(),
				Kind:       "RoleBasedGroup",
				Name:       rbg.Name,
				UID:        verifyClaimForeignUID,
				Controller: ptr.To(true),
			}},
		},
		Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1,
		},
	}
}

// TestVerifyClaim_P0_Unit_NoReadyReplicasFromForeignWorkload is the unit-layer premise
// check: ConstructRoleStatus must not report ready replicas from a workload whose
// controller reference points at a foreign UID.
func TestVerifyClaim_P0_Unit_NoReadyReplicasFromForeignWorkload(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	ctx := context.Background()

	role := wrappersv2.BuildStandaloneRole("worker").WithWorkload("apps/v1", "Deployment").Obj()
	rbg := wrappersv2.BuildBasicRoleBasedGroup("verify-rbg", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	rbg.UID = "verify-claim-current-rbg-uid"

	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(verifyClaimForeignLeftoverDeploy(rbg, &role)).Build()
	rec := NewDeploymentReconciler(scheme, c)

	status, err := rec.ConstructRoleStatus(ctx, rbg, &role)
	require.NoError(t, err)
	assert.Zero(t, status.ReadyReplicas,
		"readiness must not be inherited from a workload controlled by a foreign UID")
	assert.Zero(t, status.Replicas)
}

// TestVerifyClaim_P0_Unit_NoReadyReplicasFromOrphanWithoutGroupLabel pins the fail-closed
// row: a workload with no controller reference and no group-name label must not feed the
// role status either.
func TestVerifyClaim_P0_Unit_NoReadyReplicasFromOrphanWithoutGroupLabel(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	ctx := context.Background()

	role := wrappersv2.BuildStandaloneRole("worker").WithWorkload("apps/v1", "Deployment").Obj()
	rbg := wrappersv2.BuildBasicRoleBasedGroup("verify-rbg", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	rbg.UID = "verify-claim-current-rbg-uid"

	// A foreign object squatting on the workload name: no controller ref, no group label.
	squatter := verifyClaimForeignLeftoverDeploy(rbg, &role)
	squatter.OwnerReferences = nil
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(squatter).Build()
	rec := NewDeploymentReconciler(scheme, c)

	status, err := rec.ConstructRoleStatus(ctx, rbg, &role)
	require.NoError(t, err)
	assert.Zero(t, status.ReadyReplicas,
		"a name-squatter without the group label must not feed the role status")
}

// TestVerifyClaim_P0_Unit_CheckWorkloadReadyNotSatisfiedByForeignWorkload is the same
// premise at the dependency gate: dependents must not be released on a foreign workload.
func TestVerifyClaim_P0_Unit_CheckWorkloadReadyNotSatisfiedByForeignWorkload(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	ctx := context.Background()

	role := wrappersv2.BuildStandaloneRole("worker").WithWorkload("apps/v1", "Deployment").Obj()
	rbg := wrappersv2.BuildBasicRoleBasedGroup("verify-rbg", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	rbg.UID = "verify-claim-current-rbg-uid"

	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(verifyClaimForeignLeftoverDeploy(rbg, &role)).Build()
	rec := NewDeploymentReconciler(scheme, c)

	ready, err := rec.CheckWorkloadReady(ctx, rbg, &role)
	require.NoError(t, err)
	assert.False(t, ready, "dependents must not be released on a workload the RBG does not control")
}
