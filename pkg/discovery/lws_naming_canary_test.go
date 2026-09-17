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

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestLWS_NamingContract is a CONTRACT test (bug-canary for finding F1). It
// encodes the LeaderWorkerSet pod-naming contract as documented in the vendored
// LWS API (vendor/sigs.k8s.io/lws/api/leaderworkerset/v1/leaderworkerset_types.go,
// LeaderWorkerSetSpec doc):
//
//	leader pod : leaderWorkerSetName-{leaderIndex}            (leaderIndex in [0, Replicas-1])
//	worker pod : leaderWorkerSetName-{leaderIndex}-{workerIndex} (workerIndex in [1, Size-1])
//
// so for Replicas=2, Size=2 the four pods are {w}-0, {w}-0-1, {w}-1, {w}-1-1.
//
// The PR's buildInstances LWS branch instead emits contiguous ordinals
// {w}-0, {w}-1, {w}-2, {w}-3 — wrong leader index for size>1 and wrong
// (non-dashed) worker names. This test therefore FAILS on the PR head, which
// IS the reproduction. When fixed, it must go green.
func TestLWS_NamingContract(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{
					Name:     "prefill",
					Replicas: ptr.To(int32(2)),
					Annotations: map[string]string{
						constants.RoleWorkloadTypeAnnotationKey: "leaderworkerset.x-k8s.io/v1/LeaderWorkerSet",
					},
					Pattern: workloadsv1alpha2.Pattern{
						LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{
							Size: ptr.To(int32(2)),
						},
					},
				},
			},
		},
	}
	schema := runtime.NewScheme()
	_ = corev1.AddToScheme(schema)
	b := &ConfigBuilder{
		client: fake.NewClientBuilder().WithScheme(schema).Build(),
		rbg:    rbg,
		role: &workloadsv1alpha2.RoleSpec{
			Name:     "prefill",
			Replicas: ptr.To(int32(2)),
		},
	}
	got, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := `group:
  name: test-cluster
  roles:
  - prefill
  size: 1
roles:
  prefill:
    instances:
    - address: test-cluster-prefill-0.s-test-cluster-prefill
    - address: test-cluster-prefill-0-1.s-test-cluster-prefill
    - address: test-cluster-prefill-1.s-test-cluster-prefill
    - address: test-cluster-prefill-1-1.s-test-cluster-prefill
    size: 4
`
	if diff := cmp.Diff(want, string(got)); diff != "" {
		t.Errorf("LWS naming contract violated (-want +got):\n%s\n"+
			"Per vendored LWS API docs: leader={name}-{leaderIndex}, worker={name}-{leaderIndex}-{workerIndex}.\n"+
			"PR emits contiguous ordinals instead.", diff)
	}
}
