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

// Verification harness for PR #487 — head-only unit claims (F2, F3).
//
// These tests exercise the PR's own checkWorkloadClaimable, so they compile only on the
// PR head (and later, on the fixed code). They are NOT part of the base-branch premise
// run; see workload_claim_verify_p0_test.go for the base-compatible premise tests.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// verifyClaimScheme is the harness's own scheme builder: the harness must not depend on the
// PR's test helpers, so it still compiles if the PR's own tests are refactored away.
func verifyClaimScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

// TestVerifyClaim_F2_Unit_OrphanReadinessInheritedBeforeAdoption is a BUG-CANARY: it
// documents the residual of the original bug the PR fixes. An orphan that carries the
// group-name label is claimable, so ConstructRoleStatus still reads its ready replicas
// even though the RBG has not adopted it yet (no controller reference). On the PR head
// this PASSES — it is the current behavior. If a later fix stops the status from being
// read before adoption, this test FLIPS TO RED and must then be inverted into a
// contract test.
func TestVerifyClaim_F2_Unit_OrphanReadinessInheritedBeforeAdoption(t *testing.T) {
	scheme := verifyClaimScheme(t)
	ctx := context.Background()

	role := wrappersv2.BuildStandaloneRole("worker").WithWorkload("apps/v1", "Deployment").Obj()
	rbg := wrappersv2.BuildBasicRoleBasedGroup("verify-rbg", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	rbg.UID = "verify-claim-current-rbg-uid"

	orphan := verifyClaimForeignLeftoverDeploy(rbg, &role)
	orphan.OwnerReferences = nil
	orphan.Labels = map[string]string{constants.GroupNameLabelKey: rbg.Name}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(orphan).Build()
	rec := NewDeploymentReconciler(scheme, c)

	status, err := rec.ConstructRoleStatus(ctx, rbg, &role)
	require.NoError(t, err)
	assert.Equal(t, int32(1), status.ReadyReplicas,
		"canary: on the PR head an unadopted orphan still feeds readiness into the role status")
}

// TestVerifyClaim_F3_Unit_WorkloadNameCollision demonstrates the mechanism behind the
// orphan-adoption risk: GetWorkloadName truncates to 63 characters, so two distinct role
// names of the same RBG can map to ONE workload name, and checkWorkloadClaimable's
// orphan branch only inspects the group-name label — not the role label — so role A's
// leftover is claimable by role B.
func TestVerifyClaim_F3_Unit_WorkloadNameCollision(t *testing.T) {
	rbg := wrappersv2.BuildBasicRoleBasedGroup("verify-rbg", "default").Obj()

	// "verify-rbg-" is 11 chars; two role names sharing their first 52 characters both
	// truncate to the same 63-char workload name.
	base := strings.Repeat("r", 52)
	roleA := wrappersv2.BuildStandaloneRole(base + "aaaa").Obj()
	roleB := wrappersv2.BuildStandaloneRole(base + "bbbb").Obj()

	assert.NotEqual(t, roleA.Name, roleB.Name)
	assert.Equal(t, rbg.GetWorkloadName(&roleA), rbg.GetWorkloadName(&roleB),
		"two distinct roles must not silently share one workload name, yet they do after truncation")
	assert.LessOrEqual(t, len(rbg.GetWorkloadName(&roleA)), 63)

	// roleA's orphan — carrying roleA's role label — is claimable for roleB's slot:
	// the orphan branch checks only the group-name label.
	orphan := verifyClaimForeignLeftoverDeploy(rbg, &roleA)
	orphan.OwnerReferences = nil
	orphan.Name = rbg.GetWorkloadName(&roleB)
	orphan.Labels = map[string]string{
		constants.GroupNameLabelKey: rbg.Name,
		constants.RoleNameLabelKey:  roleA.Name, // a different role than the one reconciling
	}
	assert.NoError(t, checkWorkloadClaimable(orphan, rbg),
		"an orphan labeled for a different role is still claimable: the role label is not checked")
}
