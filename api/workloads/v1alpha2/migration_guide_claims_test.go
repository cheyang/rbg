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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/rbgs/api/workloads/constants"
)

// These tests pin the runtime behavior that PR #586's migration guides
// (doc/migration-v1alpha1-to-v1alpha2.md and the zh translation) describe, so a
// later change to either the code or the docs is caught here.
//
// Polarity:
//   - CONTRACT tests assert the behavior the guide documents correctly; they
//     fail if the code regresses away from the documented behavior.
//   - F1 is a finding against the DOC: the Rollback section claims the
//     validator rejects restoring an InstanceSet selector, which the code-pin
//     tests below disprove. TestMigrationGuide_F1_RollbackDocClaim is a
//     CONTRACT test against the doc: it is RED on the PR head (the
//     reproduction) and goes GREEN once the sentence is corrected. The
//     InstanceSet behavior tests are code-pins (canaries): they document what
//     the code actually does today and flip if the validator is ever extended
//     to reject InstanceSet restores.

// F1 (doc finding, contract test): the guide's Rollback section currently says
// "restoring the saved Deployment, StatefulSet, LeaderWorkerSet, or InstanceSet
// selector is a new deprecated use. The validator rejects it even while
// controller.deprecatedWorkloadTypes.enabled=true".
//
// For Deployment/StatefulSet/LeaderWorkerSet that is true. For InstanceSet it
// is NOT (see the code-pin tests below: the validator accepts the restore).
// This test is the reproduction: RED while the inaccurate sentence is present,
// GREEN once the Rollback section stops claiming write-time rejection for
// InstanceSet. It pins the known-bad sentence, so a reworded fix must remove
// the claim, not merely move it.
func TestMigrationGuide_F1_RollbackDocClaim(t *testing.T) {
	for _, tc := range []struct {
		file string
		bad  []string
	}{
		{
			file: "doc/migration-v1alpha1-to-v1alpha2.md",
			// One spanning fragment: the false claim is the *conjunction* —
			// InstanceSet listed among the types whose restore "the validator
			// rejects". A fix must either drop InstanceSet from the list or
			// explicitly split its enforcement from the three deprecated
			// types, both of which break this fragment. Keeping the rejection
			// sentence scoped to Deployment/StatefulSet/LeaderWorkerSet alone
			// is fine and stays green.
			bad: []string{
				"LeaderWorkerSet, or InstanceSet selector is a new deprecated use. The validator rejects it even while",
			},
		},
		{
			file: "doc/zh/migration-v1alpha1-to-v1alpha2.md",
			bad: []string{
				"LeaderWorkerSet 或 InstanceSet 选择器就是一次新的弃用使用。即使 `controller.deprecatedWorkloadTypes.enabled=true`，validator 也会拒绝该写入",
			},
		},
	} {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "..", tc.file))
			require.NoError(t, err, "migration guide must exist")
			text := string(data)
			for _, frag := range tc.bad {
				assert.NotContains(t, text, frag,
					"Rollback section claims the validator rejects an InstanceSet restore; the code-pin tests in this file show the write is accepted (see F1)")
			}
		})
	}
}

// F1 code-pin (canary): InstanceSet restores are accepted by the validator.
func TestMigrationGuide_RollbackRestore_InstanceSetNotRejectedByValidator(t *testing.T) {
	rbgWithRole := func(role RoleSpec) *RoleBasedGroup {
		return &RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "rollback-rbg", Namespace: "default"},
			Spec:       RoleBasedGroupSpec{Roles: []RoleSpec{role}},
		}
	}

	// Role as it looks after a completed migration to RoleInstanceSet (the
	// role-workload-type annotation was removed).
	migratedRole := RoleSpec{Name: "engine", Replicas: ptr.To(int32(1))}

	// Role as it would look after "restoring the saved InstanceSet selector"
	// through the v1alpha2 write path (annotation written back).
	restoredInstanceSet := RoleSpec{
		Name:        "engine",
		Replicas:    ptr.To(int32(1)),
		Annotations: map[string]string{
			constants.RoleWorkloadTypeAnnotationKey: "workloads.x-k8s.io/v1alpha1/InstanceSet",
		},
	}

	t.Run("compat mode enabled: restore is accepted (guide says rejected)", func(t *testing.T) {
		v := &RoleBasedGroupValidator{
			Client:                        newFakeValidatorClient(t),
			EnableDeprecatedWorkloadTypes: true,
		}
		oldRBG := rbgWithRole(migratedRole)
		newRBG := rbgWithRole(restoredInstanceSet)
		_, err := v.ValidateUpdate(context.Background(), oldRBG, newRBG)
		// Current behavior: no validation error. The write is accepted and the
		// failure only surfaces at reconcile time.
		assert.NoError(t, err)
	})

	t.Run("compat mode disabled: restore is also accepted (guide says rejected)", func(t *testing.T) {
		v := &RoleBasedGroupValidator{
			Client:                        newFakeValidatorClient(t),
			EnableDeprecatedWorkloadTypes: false,
		}
		oldRBG := rbgWithRole(migratedRole)
		newRBG := rbgWithRole(restoredInstanceSet)
		_, err := v.ValidateUpdate(context.Background(), oldRBG, newRBG)
		assert.NoError(t, err)
	})
}

// CONTRACT: the guide's Rollback statement is accurate for the three deprecated
// workload types — restoring one after migration is rejected even while the
// compatibility flag is enabled.
func TestMigrationGuide_RollbackRestore_DeprecatedTypesRejected(t *testing.T) {
	for _, wt := range []string{
		constants.DeploymentWorkloadType,
		constants.StatefulSetWorkloadType,
		constants.LeaderWorkerSetWorkloadType,
	} {
		t.Run(wt, func(t *testing.T) {
			migrated := &RoleBasedGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "rollback-rbg", Namespace: "default"},
				Spec: RoleBasedGroupSpec{Roles: []RoleSpec{
					{Name: "engine", Replicas: ptr.To(int32(1))},
				}},
			}
			restored := migrated.DeepCopy()
			restored.Spec.Roles[0].Annotations = map[string]string{
				constants.RoleWorkloadTypeAnnotationKey: wt,
			}

			v := &RoleBasedGroupValidator{
				Client:                        newFakeValidatorClient(t),
				EnableDeprecatedWorkloadTypes: true,
			}
			_, err := v.ValidateUpdate(context.Background(), migrated, restored)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "new uses are not accepted")
		})
	}
}

// CONTRACT: the guide's "type-exact compatibility" claim — an existing role
// keeps its own deprecated workload type while compatibility is enabled, but
// switching a role from one deprecated type to another is rejected.
func TestMigrationGuide_TypeExactCompat(t *testing.T) {
	v := &RoleBasedGroupValidator{
		Client:                        newFakeValidatorClient(t),
		EnableDeprecatedWorkloadTypes: true,
	}
	oldRBG := &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "typeexact-rbg", Namespace: "default"},
		Spec: RoleBasedGroupSpec{Roles: []RoleSpec{{
			Name:        "engine",
			Replicas:    ptr.To(int32(1)),
			Annotations: map[string]string{constants.RoleWorkloadTypeAnnotationKey: constants.StatefulSetWorkloadType},
		}}},
	}

	t.Run("keeping the same deprecated type is accepted", func(t *testing.T) {
		newRBG := oldRBG.DeepCopy()
		newRBG.Spec.Roles[0].Replicas = ptr.To(int32(3))
		_, err := v.ValidateUpdate(context.Background(), oldRBG, newRBG)
		assert.NoError(t, err)
	})

	t.Run("switching to another deprecated type is rejected", func(t *testing.T) {
		newRBG := oldRBG.DeepCopy()
		newRBG.Spec.Roles[0].Annotations[constants.RoleWorkloadTypeAnnotationKey] = constants.DeploymentWorkloadType
		_, err := v.ValidateUpdate(context.Background(), oldRBG, newRBG)
		assert.Error(t, err)
	})
}
