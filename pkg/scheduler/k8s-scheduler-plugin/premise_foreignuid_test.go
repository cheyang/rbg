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

package kubeschedulerplugin

// Reviewer harness (Claude) for PR #500.
//
// TestP0* — premise contract test: a PodGroup still controlled by the UID of a
// deleted same-named RBG must not be adopted/rewritten. FAILS on base 56c4953
// (the premise reproduction: base silently updated the foreign PodGroup's spec),
// PASSES on PR head d2915172. Kept as the regression suite for the fix.
//
// TestF1Plugin_* — finding F1 contract test: an ORPHANED PodGroup whose spec
// already matches the desired spec must still be re-attached (adopted) so the
// IsControlledBy-gated deletePodGroup can clean it up on RBG deletion. As of the
// PR head d2915172 this FAILS: the adoption lives inside the spec-drift branch,
// so a spec-matching orphan is silently reused without an owner reference and
// leaks forever. The PR body claims "update and adopt orphans so the existing
// IsControlledBy-gated delete can clean them up later" — that claim does not
// hold when spec matches. This test documents the gap; it must pass once fixed.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scheduler/common"
	schedv1alpha1 "sigs.k8s.io/scheduler-plugins/apis/scheduling/v1alpha1"
)

// TestP0Plugin_ForeignPodGroupRejected: createOrUpdate must refuse to manage a
// PodGroup whose controller reference points at a different (predecessor) UID,
// regardless of whether the spec matches.
func TestP0Plugin_ForeignPodGroupRejected(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, schedv1alpha1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	rbg := testRBG()
	rbg.UID = "new-rbg-uid"
	oldRBG := rbg.DeepCopy()
	oldRBG.UID = "old-rbg-uid"

	for _, minMember := range []int32{2, 1} { // same spec and drifted spec
		pg := &schedv1alpha1.PodGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:            rbg.Name,
				Namespace:       rbg.Namespace,
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(oldRBG, workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))},
			},
			Spec: schedv1alpha1.PodGroupSpec{
				MinMember:              minMember,
				ScheduleTimeoutSeconds: ptr.To(defaultScheduleTimeoutSeconds),
			},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg).Build()

		err := New(c, "").createOrUpdate(context.Background(), rbg, &common.GangStrategy{})
		assert.Error(t, err, "foreign-controlled PodGroup (spec MinMember=%d) must be rejected", minMember)

		after := &schedv1alpha1.PodGroup{}
		require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pg), after))
		assert.Equal(t, minMember, after.Spec.MinMember, "foreign PodGroup spec must not be rewritten")
	}
}

// TestF1Plugin_OrphanMatchingSpec_AdoptionContract: an orphaned PodGroup
// (controller ref stripped by cascade=orphan) whose spec already matches the
// desired spec must be adopted so deletePodGroup can later clean it up.
func TestF1Plugin_OrphanMatchingSpec_AdoptionContract(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, schedv1alpha1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	rbg := testRBG()
	rbg.UID = "new-rbg-uid"

	pg := &schedv1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{Name: rbg.Name, Namespace: rbg.Namespace},
		// Spec deliberately matches what createOrUpdate would desire: gang size
		// of testRBG() (prefill x2) and the default schedule timeout.
		Spec: schedv1alpha1.PodGroupSpec{
			MinMember:              2,
			ScheduleTimeoutSeconds: ptr.To(defaultScheduleTimeoutSeconds),
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg).Build()

	require.NoError(t, New(c, "").createOrUpdate(context.Background(), rbg, &common.GangStrategy{}))

	after := &schedv1alpha1.PodGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pg), after))
	assert.True(t, metav1.IsControlledBy(after, rbg),
		"a spec-matching orphan PodGroup must be adopted, otherwise deletePodGroup's IsControlledBy gate leaks it forever")
}
