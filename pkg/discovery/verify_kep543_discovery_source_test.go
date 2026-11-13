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

// Verification harness for the review of KEP-543 (PR #569), round 2.
// Additive only; no production code is changed by this file.
//
// Claim under test (finding F4): the KEP's Complete Segment Invariants section
// states that "[d]iscovery derives the address table from the committed base
// target — RBG role replicas before a child exists, otherwise the child RIS
// committed ordinals". These tests characterize what memberOrdinals actually
// does for a Segment-shaped member (dense membership: no excluded-ordinals on
// the role, no committed-skip-ordinals on the child — both forbidden by the
// KEP for Segment members).
//
// Polarity: GROUND-TRUTH CANARY — PASSES on the code under review, proving
// that for the only Segment-reachable path the builder enumerates the RBG
// *spec* replicas (dense), not the child RIS committed ordinals. The doc fix
// is text-side (the code must not change); if the builder is ever changed to
// follow the child committed target, the first test flips and must be
// re-inverted.

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

func keps543RBG(roleReplicas int32) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "seg-demo",
			Namespace: "default",
			UID:       types.UID("rbg-uid-1"),
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{
					// Default workload type = RoleInstanceSet, default pattern
					// Stateful — exactly what the KEP requires of Segment members.
					Name:     "prefill",
					Replicas: ptr.To(roleReplicas),
				},
			},
		},
	}
}

func keps543ChildRIS(rbg *workloadsv1alpha2.RoleBasedGroup, role *workloadsv1alpha2.RoleSpec, replicas int32, annotations map[string]string) *workloadsv1alpha2.RoleInstanceSet {
	return &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        rbg.GetWorkloadName(role),
			Namespace:   rbg.Namespace,
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "workloads.x-k8s.io/v1alpha2",
					Kind:       "RoleBasedGroup",
					Name:       rbg.Name,
					UID:        rbg.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: workloadsv1alpha2.RoleInstanceSetSpec{
			Replicas: ptr.To(replicas),
		},
	}
}

// Misaligned-window scenario: RBG spec still carries the (invalid) desired
// replicas P=5 while the child RIS retains the last aligned target of 4.
// A Segment member has no excluded-ordinals and the child has no
// committed-skip-ordinals, so only the default path is reachable.
func TestVerifyKEP543_DiscoveryEnumeratesRBGSpecNotChildCommitted(t *testing.T) {
	schema := runtime.NewScheme()
	_ = corev1.AddToScheme(schema)
	_ = workloadsv1alpha2.AddToScheme(schema)

	rbg := keps543RBG(5)
	role := &rbg.Spec.Roles[0]
	child := keps543ChildRIS(rbg, role, 4, nil)

	b := NewConfigBuilder(
		fake.NewClientBuilder().WithScheme(schema).WithRuntimeObjects(child).Build(),
		rbg, nil)

	got, err := b.memberOrdinals(role)
	require.NoError(t, err)
	// The doc claims the source is "the child RIS committed ordinals" once a
	// child exists; that would be [0,1,2,3] here. Observed: dense [0..4] from
	// RBG spec replicas. The misaligned-window guard therefore cannot be the
	// enumeration source — it is the new fail-fast ordering that stops
	// reconciliation before the discovery ConfigMap is rebuilt.
	require.Equal(t, []int{0, 1, 2, 3, 4}, got,
		"clean Segment-shaped member: memberOrdinals must enumerate RBG spec replicas, not child committed ordinals")
	t.Logf("observed: clean member with child RIS at spec.replicas=4 and RBG spec replicas=5 enumerates %v (RBG spec), not [0 1 2 3] (child)", got)
}

// Control: the child-committed-ordinals path exists, but only when the child
// carries a committed-skip-ordinals annotation — precisely the annotation the
// KEP forbids for Segment members (dense membership prerequisite).
func TestVerifyKEP543_ChildCommittedOrdinalsOnlyOnAnnotatedPath(t *testing.T) {
	schema := runtime.NewScheme()
	_ = corev1.AddToScheme(schema)
	_ = workloadsv1alpha2.AddToScheme(schema)

	rbg := keps543RBG(5)
	role := &rbg.Spec.Roles[0]
	child := keps543ChildRIS(rbg, role, 4, map[string]string{
		constants.CommittedSkipOrdinalsAnnotationKey: "0",
	})

	b := NewConfigBuilder(
		fake.NewClientBuilder().WithScheme(schema).WithRuntimeObjects(child).Build(),
		rbg, nil)

	got, err := b.memberOrdinals(role)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3, 4}, got,
		"annotated path: memberOrdinals follows child CommittedOrdinals (4 members from child spec.replicas=4, skip 0)")
	t.Logf("observed: child committed-skip-ordinals=0 switches enumeration to child CommittedOrdinals %v", got)
}
