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

package v1alpha1

// Reviewer-B verification harness for PR #524 (additive; production code
// untouched). F3 carried from the PR #509 review: the v1alpha1 validator must
// trust the admission request's oldObj instead of re-reading persisted state.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func pr524V1alpha1RBG(workload WorkloadSpec) *RoleBasedGroup {
	return &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec: RoleBasedGroupSpec{Roles: []RoleSpec{{
			Name:     "worker",
			Replicas: ptr.To(int32(1)),
			Workload: workload,
		}}},
	}
}

// The validator carries no client at all, so a stale-cache exemption is
// structurally impossible: the request's oldObj decides.
func TestPR524Contract_V1alpha1UpdateTrustsRequestOldObj(t *testing.T) {
	v := &deprecatedWorkloadValidator{EnableDeprecatedWorkloadTypes: true}
	sts := WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"}
	ris := WorkloadSpec{APIVersion: "workloads.x-k8s.io/v1alpha2", Kind: "RoleInstanceSet"}

	t.Run("grandfathered role admitted from request oldObj", func(t *testing.T) {
		_, err := v.ValidateUpdate(context.Background(), pr524V1alpha1RBG(sts), pr524V1alpha1RBG(sts))
		assert.NoError(t, err)
	})

	t.Run("re-introduction after migration rejected", func(t *testing.T) {
		_, err := v.ValidateUpdate(context.Background(), pr524V1alpha1RBG(ris), pr524V1alpha1RBG(sts))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "new uses are not accepted")
	})
}
