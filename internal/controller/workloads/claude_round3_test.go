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

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// Round-3 harness (PR #538, head 2c347c47). e340f9f7 "isolate invalid
// selective scaling roles" addresses round-2 F5: the Step-0 preCheck used to
// validate every role's exclusion input and hard-return, blocking the whole
// RBG reconcile (including Step-9 cleanup of removed roles). The fix moves
// the gate into reconcileRoles per role and keeps cleanup running.
//
// The preCheck-level "no hard return" property needs the full controller
// chain, so it is pinned by the envtest in
// test/envtest/testcase/rbg/claude_round3_test.go; this file pins the
// role-scoped validator the fix introduced.
//
// The role-scoped backstop must name exactly the offending role and pass a
// clean role even when a sibling role is invalid — that is what lets
// reconcileRoles isolate the blast radius to one role.
func TestClaudeValidateRoleExcludedOrdinalsScopesToTheOffendingRole(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-scope", Namespace: "default", UID: "rbg-uid"},
	}
	rbg.Spec.Roles = []workloadsv1alpha2.RoleSpec{
		{Name: "worker", Replicas: ptr.To[int32](3)},
		{Name: "healthy", Replicas: ptr.To[int32](1)},
	}
	rbg.Spec.Roles[0].Annotations = map[string]string{constants.ExcludedOrdinalsAnnotationKey: "2-1"}

	// The clean sibling validates nil even though the group as a whole is invalid.
	require.NoError(t, workloadsv1alpha2.ValidateRoleExcludedOrdinals(rbg, &rbg.Spec.Roles[1]))

	// The offending role is rejected, and the error names its role index.
	err := workloadsv1alpha2.ValidateRoleExcludedOrdinals(rbg, &rbg.Spec.Roles[0])
	require.Error(t, err)
	require.Contains(t, err.Error(), "roles[0]")

	// Whole-object validation still fails (admission and Ready=False keep the
	// complete-object semantics).
	require.Error(t, workloadsv1alpha2.ValidateExcludedOrdinals(rbg))
}
