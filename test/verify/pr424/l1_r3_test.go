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

// Round 3 replacement for l1_defaulter_inert_test.go. 04ecd11c deleted
// RoleBasedGroupDefaulter, so the original tests no longer compile. The claim they
// encoded is now checked without it: nothing may make the deprecated field stop
// taking effect.
package pr424

import (
	"testing"

	"github.com/stretchr/testify/assert"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func lwpRole(legacy workloadsv1alpha2.RestartPolicyType) *workloadsv1alpha2.RoleSpec {
	return &workloadsv1alpha2.RoleSpec{
		Name: "role-1",
		Pattern: workloadsv1alpha2.Pattern{
			LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{
				RestartPolicy: legacy, //nolint:staticcheck // the field under test
			},
		},
	}
}

// F2 / L1 — CONTRACT, round 3. Resolution must be a pure read of the two fields, so
// repeated resolution never latches a value. This is what the round-1 defaulter broke
// by writing the resolved type into the field that outranks the deprecated one.
func TestL1_R3_DeprecatedFieldStaysAuthoritativeAcrossEdits(t *testing.T) {
	role := lwpRole(workloadsv1alpha2.RecreateRoleInstanceOnPodRestart)
	assert.Equal(t, workloadsv1alpha2.RecreateRoleInstanceOnPodRestart, role.GetRestartPolicy())

	for _, want := range []workloadsv1alpha2.RestartPolicyType{
		workloadsv1alpha2.RestartPolicyNone,
		workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
		workloadsv1alpha2.RestartPolicyNone,
	} {
		role.LeaderWorkerPattern.RestartPolicy = want //nolint:staticcheck
		// Resolve twice: a getter that mutated state would show up on the second call.
		_ = role.GetRestartPolicy()
		assert.Equal(t, want, role.GetRestartPolicy(),
			"the deprecated field must remain authoritative on every edit")
		assert.Nil(t, role.LeaderWorkerPattern.RestartPolicyConfig,
			"resolving must not materialize restartPolicyConfig")
	}
}

// F2 / L1 — CONTRACT, round 3. Precedence is still the documented one when the
// config carries a type explicitly.
func TestL1_R3_ExplicitConfigTypeStillWins(t *testing.T) {
	role := lwpRole(workloadsv1alpha2.RecreateRoleInstanceOnPodRestart)
	role.LeaderWorkerPattern.RestartPolicyConfig =
		&workloadsv1alpha2.RestartPolicyConfig{Type: workloadsv1alpha2.RestartPolicyNone}
	assert.Equal(t, workloadsv1alpha2.RestartPolicyNone, role.GetRestartPolicy())

	// A config without a type must fall through to the deprecated field.
	role.LeaderWorkerPattern.RestartPolicyConfig =
		&workloadsv1alpha2.RestartPolicyConfig{BaseDelaySeconds: nil}
	assert.Equal(t, workloadsv1alpha2.RecreateRoleInstanceOnPodRestart, role.GetRestartPolicy())
}
