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

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// Round-3 harness (PR #538, head 2c347c47). Commit 5fc8c972 ("exclude
// requested ordinals from new rollout surge") fixes F3 by mirroring the
// role's exclusion input E onto the child RIS annotations so that
// statefulmode's computeTopology can keep new surge slots off excluded
// ordinals. This test pins the mirror mechanism itself: if the writer stops
// mirroring (or starts leaking E somewhere it does not belong), the F3
// contract test in statefulmode would silently keep passing on a stale
// mirror written earlier.
//
// polarity: contract — the child must carry E exactly when the role does,
// in the same conditional write that commits replicas and the record, and
// must drop it again once the role input is removed.
func TestClaudeWriterMirrorsExcludedOrdinalsForSurge(t *testing.T) {
	ctx := context.Background()
	group, child, scheme := selectiveTestObjects(t, 3, "1,3,5")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(group, child).Build()
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(group), group))
	r := NewRoleInstanceSetReconciler(scheme, c)

	// Commit a selective scale-down with an exclusion above the last member.
	require.NoError(t, r.reconcileRoleInstanceSet(ctx, group, &group.Spec.Roles[0], nil, "stable-hash"))
	actual := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: child.Namespace, Name: child.Name}, actual))
	require.Equal(t, "1,3", actual.Annotations[constants.CommittedSkipOrdinalsAnnotationKey],
		"record must still encode only the holes below the last member")
	require.Equal(t, "1,3,5", actual.Annotations[constants.ExcludedOrdinalsAnnotationKey],
		"the writer must mirror the current E onto the child for surge allocation")

	// Editing E updates the mirror without touching committed membership.
	latest := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(group), latest))
	latest.Spec.Roles[0].Annotations[constants.ExcludedOrdinalsAnnotationKey] = "1,3,5,7"
	require.NoError(t, c.Update(ctx, latest))
	require.NoError(t, r.reconcileRoleInstanceSet(ctx, latest, &latest.Spec.Roles[0], nil, "stable-hash"))
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: child.Namespace, Name: child.Name}, actual))
	require.Equal(t, "1,3,5,7", actual.Annotations[constants.ExcludedOrdinalsAnnotationKey])
	require.Equal(t, "1,3", actual.Annotations[constants.CommittedSkipOrdinalsAnnotationKey])

	// Removing E drops the mirror while the record and committed membership stay.
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(group), latest))
	delete(latest.Spec.Roles[0].Annotations, constants.ExcludedOrdinalsAnnotationKey)
	require.NoError(t, c.Update(ctx, latest))
	require.NoError(t, r.reconcileRoleInstanceSet(ctx, latest, &latest.Spec.Roles[0], nil, "stable-hash"))
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: child.Namespace, Name: child.Name}, actual))
	require.Empty(t, actual.Annotations[constants.ExcludedOrdinalsAnnotationKey],
		"removing the role input must remove the child mirror")
	require.Equal(t, "1,3", actual.Annotations[constants.CommittedSkipOrdinalsAnnotationKey])
	members, err := actual.CommittedOrdinals()
	require.NoError(t, err)
	require.Equal(t, []int{0, 2, 4}, members)
}

// The mirror must ride in the same conditional write as replicas and the
// record (AC-1 atomicity extended by 5fc8c972): a conflicting patch must not
// leave a half-applied state (e.g. new E with the old record/replica pair).
func TestClaudeMirrorIsAtomicWithRecordAndReplicas(t *testing.T) {
	ctx := context.Background()
	group, child, scheme := selectiveTestObjects(t, 3, "1,3")
	c := &membershipPatchClient{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(group, child).Build(), fail: true}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(group), group))
	r := NewRoleInstanceSetReconciler(scheme, c)

	// E edited together with the scale-down; the conditional patch conflicts.
	group.Spec.Roles[0].Annotations[constants.ExcludedOrdinalsAnnotationKey] = "1,3,5"
	require.NoError(t, c.Update(ctx, group))
	require.Error(t, r.reconcileRoleInstanceSet(ctx, group, &group.Spec.Roles[0], nil, "stable-hash"))

	actual := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(child), actual))
	require.Equal(t, int32(5), *actual.Spec.Replicas)
	require.Empty(t, actual.Annotations[constants.CommittedSkipOrdinalsAnnotationKey])
	require.Empty(t, actual.Annotations[constants.ExcludedOrdinalsAnnotationKey],
		"a rejected patch must not leave the E mirror applied without the record")

	// The recorded patch carries replicas, record, mirror and RV together.
	require.Len(t, c.patches, 1)
	patch := c.patches[0]
	metadata := patch["metadata"].(map[string]interface{})
	annotations := metadata["annotations"].(map[string]interface{})
	require.EqualValues(t, 3, patch["spec"].(map[string]interface{})["replicas"])
	require.Equal(t, "1,3", annotations[constants.CommittedSkipOrdinalsAnnotationKey])
	require.Equal(t, "1,3,5", annotations[constants.ExcludedOrdinalsAnnotationKey])
}
