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

// Round 3, follow-up question: does renaming the field inside roleInstanceTemplate
// change the RoleInstanceSet's ControllerRevision hash? If it does, a controller
// upgrade rolls every existing RoleInstance even though nobody edited a spec.
package pr424

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	risrevision "sigs.k8s.io/rbgs/pkg/reconciler/roleinstanceset/statelessmode/revision"
)

// The revision control encodes through k8s.io/client-go/kubernetes/scheme, so the
// project types have to be registered there for NewRevision to work. cmd/rbgs does
// the same thing at startup.
func init() {
	utilruntime.Must(workloadsv1alpha2.AddToScheme(clientgoscheme.Scheme))
}

func risWith(mut func(*workloadsv1alpha2.RoleInstanceSet)) *workloadsv1alpha2.RoleInstanceSet {
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ris", Namespace: "default", UID: "fixed-uid"},
		Spec: workloadsv1alpha2.RoleInstanceSetSpec{
			Replicas: ptr.To(int32(1)),
			RoleInstanceTemplate: workloadsv1alpha2.RoleInstanceTemplate{
				RoleInstanceSpec: workloadsv1alpha2.RoleInstanceSpec{
					Components: []workloadsv1alpha2.RoleInstanceComponent{{
						Name: "main",
						Size: ptr.To(int32(1)),
					}},
				},
			},
		},
	}
	mut(set)
	return set
}

func revisionHashOf(t *testing.T, set *workloadsv1alpha2.RoleInstanceSet) (string, string) {
	t.Helper()
	cr, err := risrevision.NewRevisionControl().NewRevision(set, 1, ptr.To(int32(0)))
	require.NoError(t, err)
	return cr.Name, string(cr.Data.Raw)
}

// R3-5 / L1. Does the restart policy participate in the revision at all?
// SetRevisionTemplate copies roleInstanceTemplate wholesale, so it should.
func TestL1_R3_RestartPolicyIsPartOfTheRevisionData(t *testing.T) {
	set := risWith(func(s *workloadsv1alpha2.RoleInstanceSet) {
		s.Spec.RoleInstanceTemplate.RestartPolicyConfig = &workloadsv1alpha2.RestartPolicyConfig{
			Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			BaseDelaySeconds: ptr.To(int32(30)),
			MaxDelaySeconds:  ptr.To(int32(600)),
		}
	})
	name, data := revisionHashOf(t, set)
	t.Logf("revision name: %s", name)
	t.Logf("revision data: %s", data)

	assert.Contains(t, data, "restartPolicyConfig",
		"the restart policy is copied verbatim into the revision, so it is hashed")
}

// R3-6 / L1 — CANARY. This is the upgrade question. The stored roleInstanceTemplate
// written by the CURRENT release carries the policy under "restartPolicy"; this
// release writes the same values under "restartPolicyConfig". Nothing else about the
// workload changes. If the two produce different revision names, then simply rolling
// the controller makes UpdateRevision != CurrentRevision for every existing
// RoleInstanceSet, which rolls every RoleInstance.
//
// Both shapes are built as raw template maps because the new Go type cannot express
// the old one.
func TestL1_R3_Canary_FieldRenameChangesTheRevisionHash(t *testing.T) {
	// What this release produces.
	newSet := risWith(func(s *workloadsv1alpha2.RoleInstanceSet) {
		s.Spec.RoleInstanceTemplate.RestartPolicyConfig = &workloadsv1alpha2.RestartPolicyConfig{
			Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			BaseDelaySeconds: ptr.To(int32(30)),
			MaxDelaySeconds:  ptr.To(int32(600)),
		}
	})
	newName, newData := revisionHashOf(t, newSet)

	// The same values reached through the deprecated string field, which is what a
	// v0.7.0-shaped RoleInstanceSet resolves to. Different serialization, identical
	// intent.
	legacySet := risWith(func(s *workloadsv1alpha2.RoleInstanceSet) {
		s.Spec.RoleInstanceTemplate.RestartPolicy = //nolint:staticcheck
			workloadsv1alpha2.RecreateRoleInstanceOnPodRestart
	})
	legacyName, legacyData := revisionHashOf(t, legacySet)

	t.Logf("restartPolicyConfig shape -> %s", newName)
	t.Logf("   data: %s", newData)
	t.Logf("deprecated-string shape   -> %s", legacyName)
	t.Logf("   data: %s", legacyData)

	assert.NotEqual(t, legacyName, newName,
		"CANARY: the same effective policy under a different field name yields a different "+
			"revision, so a controller upgrade alone changes UpdateRevision")
}

// R3-7 / L1 — CONTROL. Sanity check that the hash is stable for identical input, so
// the difference above is attributable to the field change and not to nondeterminism.
func TestL1_R3_Control_RevisionHashIsDeterministic(t *testing.T) {
	build := func() *workloadsv1alpha2.RoleInstanceSet {
		return risWith(func(s *workloadsv1alpha2.RoleInstanceSet) {
			s.Spec.RoleInstanceTemplate.RestartPolicyConfig = &workloadsv1alpha2.RestartPolicyConfig{
				Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
				BaseDelaySeconds: ptr.To(int32(30)),
				MaxDelaySeconds:  ptr.To(int32(600)),
			}
		})
	}
	a, _ := revisionHashOf(t, build())
	b, _ := revisionHashOf(t, build())
	assert.Equal(t, a, b, "identical input must hash identically")
}

// R3-8 / L1 — CANARY. The reconciler now always writes baseDelaySeconds and
// maxDelaySeconds into the template, where before it only set them when the user had
// configured them. Check whether that alone moves the hash, independent of the rename.
func TestL1_R3_Canary_AlwaysWritingBackoffDefaultsChangesTheHash(t *testing.T) {
	typeOnly := risWith(func(s *workloadsv1alpha2.RoleInstanceSet) {
		s.Spec.RoleInstanceTemplate.RestartPolicyConfig = &workloadsv1alpha2.RestartPolicyConfig{
			Type: workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
		}
	})
	withDefaults := risWith(func(s *workloadsv1alpha2.RoleInstanceSet) {
		s.Spec.RoleInstanceTemplate.RestartPolicyConfig = &workloadsv1alpha2.RestartPolicyConfig{
			Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			BaseDelaySeconds: ptr.To(int32(30)),
			MaxDelaySeconds:  ptr.To(int32(600)),
		}
	})
	a, aData := revisionHashOf(t, typeOnly)
	b, bData := revisionHashOf(t, withDefaults)
	t.Logf("type only        -> %s : %s", a, aData)
	t.Logf("type + defaults  -> %s : %s", b, bData)

	assert.NotEqual(t, a, b,
		"CANARY: materializing the backoff defaults is a second, independent source of "+
			"revision churn")
}
