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

package volcano

// Reviewer harness (Claude) for PR #500.
//
// TestP0Volcano_* — premise contract test: a PodGroup still controlled by the
// UID of a deleted same-named RBG must not be adopted/rewritten. FAILS on base
// 56c4953 (the premise reproduction: base rewrote the foreign PodGroup spec and
// kept the stale controller reference), PASSES on PR head d2915172. Kept as the
// regression suite for the fix.
//
// TestF1Volcano_* — finding F1 contract test: an ORPHANED PodGroup whose spec
// already matches the desired spec must still be re-attached (adopted) so the
// IsControlledBy-gated deletePodGroup can clean it up on RBG deletion. As of the
// PR head d2915172 this FAILS: the adoption lives inside the spec-drift branch,
// so a spec-matching orphan is silently reused without an owner reference and
// leaks forever — exactly the leak the PR body says it fixes for the Volcano
// path ("the Volcano path also never adopted orphans, leaking them past
// deletePodGroup"). This test documents the gap; it must pass once fixed.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scheduler/common"
	volcanoschedulingv1beta1 "volcano.sh/apis/pkg/apis/scheduling/v1beta1"
)

// TestP0Volcano_ForeignPodGroupRejected: createOrUpdate must refuse to manage a
// PodGroup whose controller reference points at a different (predecessor) UID,
// regardless of whether the spec matches.
func TestP0Volcano_ForeignPodGroupRejected(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, volcanoschedulingv1beta1.AddToScheme(scheme))

	rbg := rbgWithRoles(standaloneRole("prefill", 2, ""))
	rbg.UID = "new-rbg-uid"
	oldRBG := rbg.DeepCopy()
	oldRBG.UID = "old-rbg-uid"
	strategy := &common.GangStrategy{Roles: sets.New("prefill")}

	for _, minMember := range []int32{2, 1} { // same spec and drifted spec
		pg := &volcanoschedulingv1beta1.PodGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:            rbg.Name,
				Namespace:       rbg.Namespace,
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(oldRBG, workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))},
			},
			Spec: volcanoschedulingv1beta1.PodGroupSpec{MinMember: minMember},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg).Build()

		err := New(c).createOrUpdate(context.Background(), rbg, strategy, c)
		assert.Error(t, err, "foreign-controlled PodGroup (spec MinMember=%d) must be rejected", minMember)

		after := &volcanoschedulingv1beta1.PodGroup{}
		require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pg), after))
		assert.Equal(t, minMember, after.Spec.MinMember, "foreign PodGroup spec must not be rewritten")
	}
}

// TestF1Volcano_OrphanMatchingSpec_AdoptionContract: an orphaned PodGroup
// (controller ref stripped by cascade=orphan) whose spec already matches the
// desired spec must be adopted so deletePodGroup can later clean it up.
func TestF1Volcano_OrphanMatchingSpec_AdoptionContract(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, volcanoschedulingv1beta1.AddToScheme(scheme))

	rbg := rbgWithRoles(standaloneRole("prefill", 2, ""))
	rbg.UID = "new-rbg-uid"
	strategy := &common.GangStrategy{Roles: sets.New("prefill")}

	pg := &volcanoschedulingv1beta1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{Name: rbg.Name, Namespace: rbg.Namespace},
		// Spec deliberately matches what createOrUpdate would desire for
		// prefill x2 with no queue/priority/subGroupPolicy configured.
		Spec: volcanoschedulingv1beta1.PodGroupSpec{MinMember: 2},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg).Build()

	require.NoError(t, New(c).createOrUpdate(context.Background(), rbg, strategy, c))

	after := &volcanoschedulingv1beta1.PodGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pg), after))
	assert.True(t, metav1.IsControlledBy(after, rbg),
		"a spec-matching orphan PodGroup must be adopted, otherwise deletePodGroup's IsControlledBy gate leaks it forever")
}
