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

// Verification harness for PR #532 (reviewer: codex). Additive only; touches no
// production code. Kept free of the PR-added `Protocol` field so the premise
// test (TestVerifyPR532_P0_*) also compiles and runs against the BASE branch,
// where it is expected to FAIL (that failure IS the premise reproduction).

package port_allocator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func verifyPR532Pod(name string, hostNetwork bool, ports ...corev1.ContainerPort) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PodSpec{
			HostNetwork: hostNetwork,
			Containers:  []corev1.Container{{Name: "worker", Image: "worker:latest", Ports: ports}},
		},
	}
}

func verifyPR532Instance(podName, portValue string) *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "verify-instance",
			Labels:      map[string]string{constants.RoleTypeLabelKey: string(constants.ComponentsTemplateType)},
			Annotations: map[string]string{FormatPodScopedPortKey(podName, "metrics"): portValue},
		},
	}
}

func verifyPR532Config() *PortAllocatorConfig {
	return &PortAllocatorConfig{
		Allocations: []PortAllocation{{Name: "metrics", Env: "METRICS_PORT", Scope: PodScoped}},
	}
}

// TestVerifyPR532_P0_CollidingHostNetworkPodsDeclareHostPort is the premise
// check (P0, runs against BASE) and the fix contract in one:
//
//   - The premise: the random allocator guarantees uniqueness only within one
//     AllocateBatch call, so two pods (two calls) CAN be assigned the same port.
//     We force that deterministically with a 1-port range. For hostNetwork pods
//     the port occupies the node network namespace, so co-located colliding pods
//     conflict at runtime; the only scheduler-visible signal is a declared
//     hostPort (kube-scheduler NodePorts plugin ignores hostPort == 0).
//   - On BASE: injection declares no container ports at all -> the assertions
//     below FAIL -> premise confirmed (problem exists and is invisible to the
//     scheduler).
//   - On HEAD: both colliding pods declare hostPort == containerPort == 30000,
//     which is exactly what NodePorts filters on -> PASS.
//
// Polarity: contract (red on base, green on head).
func TestVerifyPR532_P0_CollidingHostNetworkPodsDeclareHostPort(t *testing.T) {
	alloc, err := newRandomAllocator(30000, 1)
	require.NoError(t, err)

	// Two separate AllocateBatch calls == two different pods' PodScoped
	// allocations (AllocatePortsForInstance calls AllocatePodScopedPorts per pod).
	batchA, err := alloc.AllocateBatch(1)
	require.NoError(t, err)
	batchB, err := alloc.AllocateBatch(1)
	require.NoError(t, err)
	require.Equal(t, batchA[0], batchB[0],
		"premise setup: two AllocateBatch calls must be able to return the same port")

	podA := verifyPR532Pod("verify-instance-worker-0", true)
	podB := verifyPR532Pod("verify-instance-worker-1", true)

	require.NoError(t, InjectPortsIntoPod(podA, verifyPR532Instance(podA.Name, "30000"), verifyPR532Config(), "worker"))
	require.NoError(t, InjectPortsIntoPod(podB, verifyPR532Instance(podB.Name, "30000"), verifyPR532Config(), "worker"))

	for _, pod := range []*corev1.Pod{podA, podB} {
		require.Len(t, pod.Spec.Containers[0].Ports, 1,
			"host-network pod %s must declare the colliding port so the scheduler can spread them", pod.Name)
		declared := pod.Spec.Containers[0].Ports[0]
		assert.Equal(t, int32(30000), declared.ContainerPort, "pod %s", pod.Name)
		assert.Equal(t, int32(30000), declared.HostPort,
			"pod %s: hostPort must equal the allocated port for the NodePorts plugin to see it", pod.Name)
	}
}

// TestVerifyPR532_AC2_NonHostNetworkOmitsHostPort is a contract test for the
// PR's second claim: without hostNetwork the allocated value is declared as
// containerPort only (hostPort stays 0 so no node port is reserved and the
// NodePorts plugin ignores it).
func TestVerifyPR532_AC2_NonHostNetworkOmitsHostPort(t *testing.T) {
	pod := verifyPR532Pod("verify-instance-worker-0", false)
	require.NoError(t, InjectPortsIntoPod(pod, verifyPR532Instance(pod.Name, "30000"), verifyPR532Config(), "worker"))

	require.Len(t, pod.Spec.Containers[0].Ports, 1)
	declared := pod.Spec.Containers[0].Ports[0]
	assert.Equal(t, int32(30000), declared.ContainerPort)
	assert.Zero(t, declared.HostPort, "non-host-network pods must not reserve a node port")
}

// TestVerifyPR532_AC4_CrossBatchCollisionSamePodSameProtocol probes the reuse
// path when two allocations (e.g. one PodScoped batch and one RoleScoped batch,
// which are drawn from separate AllocateBatch calls) collide on the same port
// value and protocol for the SAME pod: injection must stay idempotent (single
// declaration) instead of producing a duplicate hostPort entry.
func TestVerifyPR532_AC4_CrossBatchCollisionSamePodSameProtocol(t *testing.T) {
	pod := verifyPR532Pod("verify-instance-worker-0", true)
	config := &PortAllocatorConfig{
		Allocations: []PortAllocation{
			{Name: "metrics", Env: "METRICS_PORT", Scope: PodScoped},
			{Name: "shared", Env: "SHARED_PORT", Scope: RoleScoped},
		},
	}
	instance := verifyPR532Instance(pod.Name, "30000")
	// RoleScoped key uses the component name; force the same value (collision).
	instance.Annotations[FormatRoleScopedPortKey("worker", "shared")] = "30000"

	require.NoError(t, InjectPortsIntoPod(pod, instance, config, "worker"))

	count := 0
	for _, p := range pod.Spec.Containers[0].Ports {
		if p.ContainerPort == 30000 {
			count++
			assert.Equal(t, int32(30000), p.HostPort)
		}
	}
	assert.Equal(t, 1, count, "colliding allocations for one pod must reuse a single port declaration")
	// Both env vars still carry the (identical) value.
	envNames := map[string]bool{}
	for _, e := range pod.Spec.Containers[0].Env {
		envNames[e.Name] = true
		assert.Equal(t, "30000", e.Value)
	}
	assert.True(t, envNames["METRICS_PORT"] && envNames["SHARED_PORT"])
}

// TestVerifyPR532_AC4_ConflictingUserHostPortFailsClosed documents the PR's
// deliberate fail-closed behavior: a template that already binds host port
// 30000 for a different container port cannot also receive an allocation of
// 30000 on a host-network pod, so pod creation errors instead of silently
// dropping the scheduling guarantee.
func TestVerifyPR532_AC4_ConflictingUserHostPortFailsClosed(t *testing.T) {
	pod := verifyPR532Pod("verify-instance-worker-0", true,
		corev1.ContainerPort{ContainerPort: 8080, HostPort: 30000, Protocol: corev1.ProtocolTCP})

	err := InjectPortsIntoPod(pod, verifyPR532Instance(pod.Name, "30000"), verifyPR532Config(), "worker")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host port 30000")
}
