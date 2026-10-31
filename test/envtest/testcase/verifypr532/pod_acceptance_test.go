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
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	portallocator "sigs.k8s.io/rbgs/pkg/port-allocator"
)

func injectedInstance(podName, portValue string) *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "verify-instance",
			Labels:      map[string]string{constants.RoleTypeLabelKey: string(constants.ComponentsTemplateType)},
			Annotations: map[string]string{portallocator.FormatPodScopedPortKey(podName, "metrics"): portValue},
		},
	}
}

// testInjectedPodSpecsAccepted runs the real InjectPortsIntoPod and submits the
// resulting pods to a real API server (envtest), proving the injected shapes —
// hostNetwork + hostPort == containerPort across TCP/UDP/SCTP, and the
// hostPort-less non-hostNetwork shape — pass API validation and round-trip.
func testInjectedPodSpecsAccepted(t *testing.T) {
	createNamespace(t, "verify-pr532")

	cases := []struct {
		name         string
		hostNetwork  bool
		protocol     corev1.Protocol
		wantHostPort int32
	}{
		{name: "hostnetwork-tcp", hostNetwork: true, protocol: corev1.ProtocolTCP, wantHostPort: 30000},
		{name: "hostnetwork-udp", hostNetwork: true, protocol: corev1.ProtocolUDP, wantHostPort: 30000},
		{name: "hostnetwork-sctp", hostNetwork: true, protocol: corev1.ProtocolSCTP, wantHostPort: 30000},
		{name: "regular-tcp", hostNetwork: false, protocol: corev1.ProtocolTCP, wantHostPort: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "verify-pr532-" + tc.name, Namespace: "verify-pr532"},
				Spec: corev1.PodSpec{
					HostNetwork: tc.hostNetwork,
					Containers:  []corev1.Container{{Name: "worker", Image: "worker:latest"}},
				},
			}
			config := &portallocator.PortAllocatorConfig{
				Allocations: []portallocator.PortAllocation{
					{Name: "metrics", Env: "METRICS_PORT", Scope: portallocator.PodScoped, Protocol: tc.protocol},
				},
			}

			if err := portallocator.InjectPortsIntoPod(pod, injectedInstance(pod.Name, "30000"), config, "worker"); err != nil {
				t.Fatalf("InjectPortsIntoPod failed: %v", err)
			}

			// Controller contract (in-memory, pre-admission): the injected
			// declaration must carry the expected hostPort. NOTE: this is the
			// only assertion that can see the controller's hostPort logic —
			// after submission the kube-apiserver defaultHostNetworkPorts
			// defaulting fills hostPort=containerPort for hostNetwork pods,
			// so post-create assertions cannot distinguish controller-set
			// from apiserver-defaulted hostPort.
			pre := pod.Spec.Containers[0].Ports
			if len(pre) != 1 || pre[0].ContainerPort != 30000 || pre[0].HostPort != tc.wantHostPort || pre[0].Protocol != tc.protocol {
				t.Fatalf("injected port mismatch (pre-create): got %+v, want containerPort=30000 hostPort=%d protocol=%s",
					pre, tc.wantHostPort, tc.protocol)
			}

			if err := testClient.Create(testCtx, pod); err != nil {
				t.Fatalf("API server rejected injected pod spec: %v", err)
			}
			t.Cleanup(func() { _ = testClient.Delete(testCtx, pod) })

			got := &corev1.Pod{}
			if err := testClient.Get(testCtx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, got); err != nil {
				t.Fatalf("failed to read back pod: %v", err)
			}
			if len(got.Spec.Containers[0].Ports) != 1 {
				t.Fatalf("expected exactly 1 declared port after round-trip, got %d", len(got.Spec.Containers[0].Ports))
			}
			p := got.Spec.Containers[0].Ports[0]
			// Post-create the apiserver may have defaulted hostPort for
			// hostNetwork pods; the stable post-admission invariants are
			// containerPort and protocol, plus hostPort == containerPort when
			// host networking is on.
			if p.ContainerPort != 30000 || p.Protocol != tc.protocol || (tc.hostNetwork && p.HostPort != p.ContainerPort) {
				t.Fatalf("round-tripped port mismatch: got %+v, want containerPort=30000 hostPort=%d protocol=%s",
					p, tc.wantHostPort, tc.protocol)
			}
		})
	}
}
