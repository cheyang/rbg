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

// Reviewer verification harness, round 3 (PR head bd9ee4dd). Admission layer.
// Contract tests keyed to round-2 findings: they assert the fixed behavior and
// must PASS on the reworked head. See docs/verification/pr434-gang-scheduling/.

package v1alpha2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifyR3_F5_AdmissionRejectsNonPositiveMinimums covers the admission half
// of round-2 F5: zero and negative minReplicas used to pass through unvalidated.
func TestVerifyR3_F5_AdmissionRejectsNonPositiveMinimums(t *testing.T) {
	for _, minimum := range []int32{0, -1} {
		policy := gangPolicy([]string{"prefill"}, map[string]int32{"prefill": minimum})
		err := ValidateCoordinatedPolicyGang(policy, true)
		require.Error(t, err, "minReplicas=%d must be rejected at admission", minimum)
		assert.Contains(t, err.Error(), "must be at least 1")
	}

	positive := gangPolicy([]string{"prefill"}, map[string]int32{"prefill": 1})
	require.NoError(t, ValidateCoordinatedPolicyGang(positive, true))
}

// TestVerifyR3_F6_AdmissionRejectsOutOfScopeMinimums covers the admission half
// of round-2 F6: a minimum for a role the declaring rule does not list used to be
// silently ignored downstream; admission now rejects it up front.
func TestVerifyR3_F6_AdmissionRejectsOutOfScopeMinimums(t *testing.T) {
	policy := gangPolicy([]string{"prefill"}, map[string]int32{"prefill": 1, "decode": 2})
	err := ValidateCoordinatedPolicyGang(policy, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
	assert.Contains(t, err.Error(), "not listed")
}

// TestVerifyR3_SchedulerPluginsRejectsPerRoleMinimumsAtAdmission pins the
// capability boundary: per-role minimums only exist on Volcano's subGroupPolicy,
// so with any other scheduler they are rejected at admission rather than failing
// every reconcile.
func TestVerifyR3_SchedulerPluginsRejectsPerRoleMinimumsAtAdmission(t *testing.T) {
	policy := gangPolicy([]string{"prefill"}, map[string]int32{"prefill": 1})

	err := ValidateCoordinatedPolicyGang(policy, false)
	require.Error(t, err, "per-role minimums without Volcano must be rejected")
	assert.Contains(t, err.Error(), "volcano")

	// The whole-group gang (empty minReplicas) remains available on every scheduler.
	allOrNothing := gangPolicy([]string{"prefill", "decode"}, nil)
	require.NoError(t, ValidateCoordinatedPolicyGang(allOrNothing, false))
	require.NoError(t, ValidateCoordinatedPolicyGang(allOrNothing, true))
}
