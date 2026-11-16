/*
Copyright 2026.

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

// Round-3 harness (reviewer A): contract test for F-UPDATE-WEDGE.
//
// Claim: an RBG stored with an invalid engine-runtime reference during a
// webhook-absent window (targetContainers without component — the round-2
// wedge shape) must not have its unrelated updates rejected once the webhook
// returns, while an update that newly introduces (or changes) the violation
// is still rejected. Polarity: contract — RED on f3d4849a (label-only update
// rejected), GREEN on c87a2e1e (8f9da58d old-vs-new exemption).
package v1alpha2

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func reviewWedgedRBG() *RoleBasedGroup {
	return &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "review-rbg", Namespace: "default"},
		Spec: RoleBasedGroupSpec{Roles: []RoleSpec{{
			Name:    "lg",
			Pattern: Pattern{LeaderWorkerPattern: &LeaderWorkerPattern{}},
			EngineRuntimes: []EngineRuntime{{
				ProfileName:      "vllm-runtime",
				TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}},
			}},
		}}},
	}
}

func TestReview_UpdateWedgeExemption(t *testing.T) {
	v := &RoleBasedGroupValidator{
		Client:                        newFakeValidatorClient(t),
		EnableDeprecatedWorkloadTypes: true,
	}

	t.Run("label-only update on the wedged object passes", func(t *testing.T) {
		newObj := reviewWedgedRBG()
		newObj.Labels = map[string]string{"app": "review"}
		_, err := v.ValidateUpdate(context.Background(), reviewWedgedRBG(), newObj)
		assert.NoError(t, err,
			"an update that does not newly introduce the stored violation must not wedge (F-UPDATE-WEDGE)")
	})

	t.Run("newly introduced violation is still rejected", func(t *testing.T) {
		newObj := reviewWedgedRBG()
		newObj.Spec.Roles[0].EngineRuntimes = append(newObj.Spec.Roles[0].EngineRuntimes,
			EngineRuntime{ProfileName: "p2", TargetContainers: []EngineRuntimeTargetContainer{{Name: "c"}}})
		_, err := v.ValidateUpdate(context.Background(), reviewWedgedRBG(), newObj)
		require.Error(t, err, "the exemption must not admit violations the update itself introduces")
		assert.Contains(t, err.Error(), `reference "p2"`)
	})

	t.Run("changed surface on the wedged role is still rejected", func(t *testing.T) {
		newObj := reviewWedgedRBG()
		newObj.Spec.Roles[0].EngineRuntimes[0].ProfileName = "renamed"
		_, err := v.ValidateUpdate(context.Background(), reviewWedgedRBG(), newObj)
		require.Error(t, err, "touching the engine-runtime surface revalidates the role")
		assert.Contains(t, err.Error(), `reference "renamed"`)
	})
}

func TestReview_UpdateWedgeExemptionRBGSet(t *testing.T) {
	wedged := func() *RoleBasedGroupSet {
		return &RoleBasedGroupSet{
			ObjectMeta: metav1.ObjectMeta{Name: "review-rbgs", Namespace: "default"},
			Spec: RoleBasedGroupSetSpec{GroupTemplate: RoleBasedGroupTemplateSpec{
				Spec: RoleBasedGroupSpec{Roles: []RoleSpec{{
					Name:    "lg",
					Pattern: Pattern{LeaderWorkerPattern: &LeaderWorkerPattern{}},
					EngineRuntimes: []EngineRuntime{{
						ProfileName:      "vllm-runtime",
						TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}},
					}},
				}}},
			}},
		}
	}
	v := &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: true}

	t.Run("label-only update on the wedged object passes", func(t *testing.T) {
		newObj := wedged()
		newObj.Labels = map[string]string{"app": "review"}
		_, err := v.ValidateUpdate(context.Background(), wedged(), newObj)
		assert.NoError(t, err, "the RoleBasedGroupSet twin must not wedge either (F-UPDATE-WEDGE)")
	})

	t.Run("newly introduced violation is still rejected", func(t *testing.T) {
		newObj := wedged()
		newObj.Spec.GroupTemplate.Spec.Roles[0].EngineRuntimes = append(
			newObj.Spec.GroupTemplate.Spec.Roles[0].EngineRuntimes,
			EngineRuntime{ProfileName: "p2", TargetContainers: []EngineRuntimeTargetContainer{{Name: "c"}}})
		_, err := v.ValidateUpdate(context.Background(), wedged(), newObj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `reference "p2"`)
	})
}
