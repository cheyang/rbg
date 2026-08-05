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

package v1alpha2

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Round-3 harness for PR #416 -- pinning the DESIGN REVERSAL at aac6056d.
//
// The delta dcc7104a..aac6056d removes validateNoNewDeprecatedWorkloadTypes and
// routes ValidateUpdate back through the strict whole-object check, for both
// RoleBasedGroup and RoleBasedGroupSet. Grandfathering -- which round 2 introduced
// and this harness guarded -- is gone.
//
// That is a deliberate scope decision, not a regression: the PR description, README,
// chart README, values.yaml, NOTES.txt and the flag's own help text now all say the
// same thing -- `false` is for a FRESH INSTALLATION only, with no exemption for
// objects that already use a deprecated workload type. Under that premise the
// denials round 1 reported (F2a/F2c/F9/F10, R2-F13) are unreachable rather than
// fixed, because no such object can exist.
//
// This file therefore does NOT re-report those denials as bugs. It pins the new
// contract, so that:
//
//   - a future round cannot flip the design a third time without the harness
//     saying so, and
//   - the denials stay attributable to the design rather than to a defect.
//
// Whether the "fresh installation only" premise actually holds is a separate
// question, and the answer is no: see R3-F22 and
// scripts/10-fresh-install-invariant.sh. Nothing in the chart prevents
// `helm install --set controller.deprecatedWorkloadTypes.enabled=false` over
// objects that survived an uninstall -- which is the update procedure the repo
// itself documents in four places. These pins are what make that finding precise:
// they show exactly what happens to such an object once it exists.

// TestPR416R3_WholeObjectRejectionOnUpdate pins the reverted contract for
// RoleBasedGroup: an update is judged on the whole object, so a pre-existing role
// carrying a deprecated type is refused even when the update does not touch it.
// Each denial is paired with an ENABLED control, so the test cannot pass vacuously.
//
// GREEN at aac6056d. If it goes RED, the update path was narrowed again and the
// round-2 grandfathering findings become live once more.
func TestPR416R3_WholeObjectRejectionOnUpdate(t *testing.T) {
	ctx := context.Background()

	for _, wt := range r2DeprecatedTypes {
		t.Run(wt, func(t *testing.T) {
			old := r2RBG(r2Role("worker", wt))

			// A no-op re-apply: identical old and new. This is the shape the
			// controller's own annotation patch presents to admission.
			_, err := r2Validator(false).ValidateUpdate(ctx, old.DeepCopy(), old.DeepCopy())
			require.Error(t, err,
				"design pin: a no-op update of a pre-existing %s role must be REFUSED "+
					"when the deprecated types are off (whole-object check)", wt)
			assert.Contains(t, err.Error(), "deprecated",
				"the refusal must name the deprecated workload type")

			// ENABLED control: the very same update is accepted with the toggle on,
			// so the denial above is attributable to the toggle and nothing else.
			_, err = r2Validator(true).ValidateUpdate(ctx, old.DeepCopy(), old.DeepCopy())
			require.NoError(t, err,
				"control: the same update must be ACCEPTED with the deprecated types on")
		})
	}
}

// TestPR416R3_WholeObjectRejectionOnUpdate_RBGSet pins the same reversal for
// RoleBasedGroupSet, whose ValidateUpdate now ignores oldObj entirely.
func TestPR416R3_WholeObjectRejectionOnUpdate_RBGSet(t *testing.T) {
	ctx := context.Background()

	for _, wt := range r2DeprecatedTypes {
		t.Run(wt, func(t *testing.T) {
			old := r2RBGSet(r2Role("worker", wt))

			_, err := r2SetValidator(false).ValidateUpdate(ctx, old.DeepCopy(), old.DeepCopy())
			require.Error(t, err,
				"design pin: a no-op update of a pre-existing RBGSet whose template uses "+
					"%s must be REFUSED", wt)
			assert.Contains(t, err.Error(), "spec.groupTemplate.spec.roles",
				"the refusal must point at the template path")

			_, err = r2SetValidator(true).ValidateUpdate(ctx, old.DeepCopy(), old.DeepCopy())
			require.NoError(t, err, "control: accepted with the deprecated types on")
		})
	}
}

// TestPR416R3_CreateAndUpdateNowAgree records the one thing the reversal genuinely
// buys: create and update are the same check again, so R2-F13 (the RBGSet's own
// update was exempt but the child RBG it must CREATE was not) is closed by
// construction rather than by a second special case. An asymmetry here was the whole
// substance of R2-F13, so this is the pin that keeps it closed.
func TestPR416R3_CreateAndUpdateNowAgree(t *testing.T) {
	ctx := context.Background()

	for _, wt := range r2DeprecatedTypes {
		t.Run(wt, func(t *testing.T) {
			obj := r2RBG(r2Role("worker", wt))

			_, createErr := r2Validator(false).ValidateCreate(ctx, obj.DeepCopy())
			_, updateErr := r2Validator(false).ValidateUpdate(ctx, obj.DeepCopy(), obj.DeepCopy())

			require.Error(t, createErr, "create must refuse %s", wt)
			require.Error(t, updateErr, "update must refuse %s", wt)
			assert.Equal(t, createErr.Error(), updateErr.Error(),
				"design pin: create and update must produce the SAME verdict and the same "+
					"message; any divergence reopens R2-F13, where a parent update was "+
					"exempt while the child create it triggers was not")
		})
	}
}
