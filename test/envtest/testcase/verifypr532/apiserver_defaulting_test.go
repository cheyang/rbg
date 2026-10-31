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

package verifypr532

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// TestVerifyPR532_APIServerDefaultsHostNetworkHostPort documents the platform
// behavior that PR #532's explicit hostPort assignment overlaps with: the
// kube-apiserver (pkg/apis/core/v1/defaults.go defaultHostNetworkPorts, gated
// on pod.spec.hostNetwork) fills hostPort = containerPort for every declared
// container port of a host-network pod. Verified here against a real 1.31
// API server: we create a hostNetwork pod declaring ONLY containerPort and
// read back hostPort == containerPort, while a non-hostNetwork pod keeps
// hostPort == 0.
//
// Consequences for the PR:
//   - The scheduler-visible spreading for hostNetwork pods follows from the
//     containerPort declaration alone; the controller's explicit hostPort is
//     belt-and-braces (and the e2e assertions on persisted pods cannot tell
//     the two apart — only the pre-create, in-memory assertions can).
//   - For non-hostNetwork pods no defaulting applies, so the PR's
//     "hostPort stays unset" contract holds.
func testAPIServerDefaultsHostNetworkHostPort(t *testing.T) {
	createNamespace(t, "verify-pr532-defaulting")

	cases := []struct {
		name        string
		hostNetwork bool
		wantHost    int32
	}{
		{name: "hostnetwork", hostNetwork: true, wantHost: 30000},
		{name: "regular", hostNetwork: false, wantHost: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "verify-pr532-defaulting-" + tc.name, Namespace: "verify-pr532-defaulting"},
				Spec: corev1.PodSpec{
					HostNetwork: tc.hostNetwork,
					Containers: []corev1.Container{{
						Name:  "worker",
						Image: "worker:latest",
						Ports: []corev1.ContainerPort{{ContainerPort: 30000, Protocol: corev1.ProtocolTCP}},
					}},
				},
			}
			if err := testClient.Create(testCtx, pod); err != nil {
				t.Fatalf("create: %v", err)
			}
			t.Cleanup(func() { _ = testClient.Delete(testCtx, pod) })

			got := &corev1.Pod{}
			if err := testClient.Get(testCtx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, got); err != nil {
				t.Fatalf("get: %v", err)
			}
			gotHP := got.Spec.Containers[0].Ports[0].HostPort
			if gotHP != tc.wantHost {
				t.Fatalf("hostNetwork=%v: hostPort after admission = %d, want %d", tc.hostNetwork, gotHP, tc.wantHost)
			}
			t.Logf("hostNetwork=%v: apiserver returned hostPort=%d", tc.hostNetwork, gotHP)
		})
	}
}
