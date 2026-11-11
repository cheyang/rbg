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

package discovery

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F7 (reviewer A harness, PR #538): coverage for ConfigBuilder.memberOrdinals.
//
// The PR added memberOrdinals with five distinct branches (dense fallback,
// NotFound+absent, NotFound+present, owner-UID guard, committed record) but
// no test touched any of them — config_builder_test.go only gained a scheme
// registration line. These tests pin the intended behavior of each branch.
// They all PASS on the PR head (results/unit-head.out); they exist to close
// the coverage gap, not to demonstrate a defect.
func TestClaudeMemberOrdinalsBranches(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	newRole := func(e string) *workloadsv1alpha2.RoleSpec {
		role := &workloadsv1alpha2.RoleSpec{Name: "worker", Replicas: ptr.To[int32](3)}
		if e != "" {
			role.Annotations = map[string]string{constants.ExcludedOrdinalsAnnotationKey: e}
		}
		return role
	}
	newRBG := func() *workloadsv1alpha2.RoleBasedGroup {
		return &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "group", Namespace: "default", UID: "rbg-uid"},
		}
	}
	child := func(ownerUID string, record string) *workloadsv1alpha2.RoleInstanceSet {
		set := &workloadsv1alpha2.RoleInstanceSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "group-worker", Namespace: "default", UID: "child-uid",
				Annotations: map[string]string{},
			},
			Spec: workloadsv1alpha2.RoleInstanceSetSpec{Replicas: ptr.To[int32](3)},
		}
		if record != "" {
			set.Annotations[constants.CommittedSkipOrdinalsAnnotationKey] = record
		}
		if ownerUID != "" {
			set.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(
				&workloadsv1alpha2.RoleBasedGroup{ObjectMeta: metav1.ObjectMeta{Name: "group", UID: types.UID(ownerUID)}},
				workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))}
		}
		return set
	}

	t.Run("follows the committed child record", func(t *testing.T) {
		rbg := newRBG()
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(child("rbg-uid", "1,3")).Build()
		b := &ConfigBuilder{client: c, rbg: rbg}
		members, err := b.memberOrdinals(newRole("1,3"))
		require.NoError(t, err)
		require.Equal(t, []int{0, 2, 4}, members)
	})

	t.Run("follows the committed record even after the input is removed", func(t *testing.T) {
		rbg := newRBG()
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(child("rbg-uid", "1,3")).Build()
		b := &ConfigBuilder{client: c, rbg: rbg}
		members, err := b.memberOrdinals(newRole(""))
		require.NoError(t, err)
		require.Equal(t, []int{0, 2, 4}, members)
	})

	t.Run("rejects a child owned by another controller", func(t *testing.T) {
		rbg := newRBG()
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(child("other-uid", "1,3")).Build()
		b := &ConfigBuilder{client: c, rbg: rbg}
		_, err := b.memberOrdinals(newRole("1,3"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "different controller owner")
	})

	t.Run("initial planner before the child exists", func(t *testing.T) {
		rbg := newRBG()
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		b := &ConfigBuilder{client: c, rbg: rbg}
		members, err := b.memberOrdinals(newRole("1,3"))
		require.NoError(t, err)
		require.Equal(t, []int{0, 2, 4}, members)
	})

	t.Run("dense when no record and no input", func(t *testing.T) {
		rbg := newRBG()
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(child("rbg-uid", "")).Build()
		b := &ConfigBuilder{client: c, rbg: rbg}
		members, err := b.memberOrdinals(newRole(""))
		require.NoError(t, err)
		require.Equal(t, []int{0, 1, 2}, members)
	})

	// ---- Round 3 additions (head 2c347c47) ----
	// eec29c95 "allow discovery of claimable dense orphan children": the
	// owner-guard now accepts an ownerless dense child carrying the matching
	// group-name label (the same dense orphan the selective writer can claim),
	// and still rejects sparse orphans, foreign owners, and label mismatches.
	labelledChild := func(ownerUID, record string, labels map[string]string) *workloadsv1alpha2.RoleInstanceSet {
		set := child(ownerUID, record)
		if labels != nil {
			set.Labels = labels
		} else {
			set.Labels = map[string]string{}
		}
		return set
	}

	t.Run("accepts a claimable ownerless dense orphan", func(t *testing.T) {
		rbg := newRBG()
		ownerless := labelledChild("", "", map[string]string{constants.GroupNameLabelKey: "group"})
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ownerless).Build()
		b := &ConfigBuilder{client: c, rbg: rbg}
		members, err := b.memberOrdinals(newRole("1,3"))
		require.NoError(t, err, "dense orphan with matching label must be readable, not rejected")
		require.Equal(t, []int{0, 1, 2}, members)
	})

	t.Run("rejects an ownerless sparse orphan", func(t *testing.T) {
		rbg := newRBG()
		ownerless := labelledChild("", "1,3", map[string]string{constants.GroupNameLabelKey: "group"})
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ownerless).Build()
		b := &ConfigBuilder{client: c, rbg: rbg}
		_, err := b.memberOrdinals(newRole("1,3"))
		require.Error(t, err, "sparse membership must never be inherited from an ownerless child")
		require.Contains(t, err.Error(), "different controller owner")
	})

	t.Run("rejects an ownerless dense orphan with a foreign label", func(t *testing.T) {
		rbg := newRBG()
		ownerless := labelledChild("", "", map[string]string{constants.GroupNameLabelKey: "other-group"})
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ownerless).Build()
		b := &ConfigBuilder{client: c, rbg: rbg}
		_, err := b.memberOrdinals(newRole("1,3"))
		require.Error(t, err, "an ownerless child of another group must not be claimed")
		require.Contains(t, err.Error(), "different controller owner")
	})

	// e340f9f7 "isolate invalid selective scaling roles": invalid input must
	// not plan membership, while an existing child keeps publishing its
	// committed target (frozen role) and a missing child publishes nothing.
	t.Run("freezes discovery on the committed target for an invalid role", func(t *testing.T) {
		rbg := newRBG()
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(child("rbg-uid", "1,3")).Build()
		b := &ConfigBuilder{client: c, rbg: rbg}
		members, err := b.memberOrdinals(newRole("2-1"))
		require.NoError(t, err)
		require.Equal(t, []int{0, 2, 4}, members, "invalid input must retain the committed discovery target")
	})

	t.Run("publishes no members for an invalid role without a child", func(t *testing.T) {
		rbg := newRBG()
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		b := &ConfigBuilder{client: c, rbg: rbg}
		members, err := b.memberOrdinals(newRole("2-1"))
		require.NoError(t, err)
		require.Empty(t, members, "never plan new membership from invalid input")
	})
}
