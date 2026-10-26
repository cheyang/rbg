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

// Premise check (P0) for PR #524 — RUNS AGAINST THE BASE BRANCH.
// Copy this file into api/workloads/v1alpha2/ of a base checkout and run:
//   go test ./api/workloads/v1alpha2/ -run TestP0 -v
// Expected on base: PASS — a brand-new deprecated workload use is silently
// admitted in the default (enableDeprecatedWorkloadTypes=true) mode, i.e. the
// missing-gate problem the PR solves is real.
// On the PR head the same test FAILS — the new-use gate rejects — which
// doubles as the polarity/bites control.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
)

func TestP0_BaseAdmitsNewDeprecatedUseSilently(t *testing.T) {
	v := &RoleBasedGroupValidator{EnableDeprecatedWorkloadTypes: true}
	rbg := &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "new-deprecated-use"},
		Spec: RoleBasedGroupSpec{Roles: []RoleSpec{{
			Name:     "worker",
			Replicas: ptr.To(int32(1)),
			Annotations: map[string]string{
				constants.RoleWorkloadTypeAnnotationKey: constants.StatefulSetWorkloadType,
			},
		}}},
	}
	warnings, err := v.ValidateCreate(context.Background(), rbg)
	assert.NoError(t, err, "premise: base admits a new deprecated workload use (no gate exists)")
	assert.Empty(t, warnings, "premise: base returns no warning either")
}
