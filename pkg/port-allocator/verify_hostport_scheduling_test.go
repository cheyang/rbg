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

package port_allocator

// Verification harness for PR #532 (feat(port-allocator): expose host-network
// ports to the scheduler). Reviewer-branch only; production code untouched.
//
// Claims (see docs/verification/pr532-host-port-scheduling/verify-manifest.json):
//
//	P0  premise, differential: on the base branch InjectPortsIntoPod leaves a
//	    host-network Pod with zero scheduler-visible host ports, so kube-scheduler's
//	    NodePorts plugin has nothing to filter on. Contract test: on the PR head the
//	    injected Pod must declare hostPort == containerPort == allocated port.
//	F1  contract: injected Pod specs pass the real kube-apiserver Pod validation
//	    (duplicate host ports, hostNetwork hostPort==containerPort rule).
//	F2  contract: re-injecting the same allocations is idempotent (one declaration
//	    per (port, protocol)) and stays API-valid.
//	F3  canary: a legacy Pod (created by the pre-PR controller: env var only, no
//	    port declaration) is invisible to NodePorts — documents the upgrade gap.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	core "k8s.io/kubernetes/pkg/apis/core"
	corevalidation "k8s.io/kubernetes/pkg/apis/core/validation"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// ---------------------------------------------------------------------------
// scheduler-visibility helpers
//
// Mirrors of two upstream Kubernetes functions (the repo vendors k8s.io/kubernetes,
// but not pkg/scheduler/..., so the few lines below are ported verbatim with
// attribution; the assertion target is the Pod-spec shape, not scheduler internals):
//   - k8s.io/kubernetes/pkg/scheduler/util/utils.go GetHostPorts
//   - k8s.io/kubernetes/pkg/scheduler/framework/types.go HostPortInfo.Add/CheckConflict
//     (v1.34: staging/src/k8s.io/kube-scheduler/framework/hostport_info.go)
// ---------------------------------------------------------------------------

const defaultBindAllHostIP = "0.0.0.0"

func sanitizeHostPortArgs(ip, protocol *string) {
	if len(*ip) == 0 {
		*ip = defaultBindAllHostIP
	}
	if len(*protocol) == 0 {
		*protocol = string(corev1.ProtocolTCP)
	}
}

// schedulerVisibleHostPorts returns (ip, protocol, hostPort) triples exactly the
// way the NodePorts PreFilter/Filter plugins collect "wantPorts" for a Pod.
func schedulerVisibleHostPorts(pod *corev1.Pod) [][3]string {
	var ports [][3]string
	if pod == nil {
		return ports
	}
	for _, c := range pod.Spec.InitContainers {
		// Only initContainers that run for the entire Pod lifetime count.
		if c.RestartPolicy == nil || *c.RestartPolicy != corev1.ContainerRestartPolicyAlways {
			continue
		}
		for _, p := range c.Ports {
			if p.HostPort > 0 {
				ip, proto := p.HostIP, string(p.Protocol)
				sanitizeHostPortArgs(&ip, &proto)
				ports = append(ports, [3]string{ip, proto, fmt.Sprintf("%d", p.HostPort)})
			}
		}
	}
	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if p.HostPort > 0 {
				ip, proto := p.HostIP, string(p.Protocol)
				sanitizeHostPortArgs(&ip, &proto)
				ports = append(ports, [3]string{ip, proto, fmt.Sprintf("%d", p.HostPort)})
			}
		}
	}
	return ports
}

// nodePortsConflict mirrors HostPortInfo.CheckConflict: a want-port conflicts with
// a used-port when the (protocol, port) pair matches on the same IP, or either
// side is the bind-all IP 0.0.0.0.
func nodePortsConflict(want, used [3]string) bool {
	wantIP, wantProto, wantPort := want[0], want[1], want[2]
	if wantIP == defaultBindAllHostIP {
		for _, u := range [][3]string{used} {
			if u[1] == wantProto && u[2] == wantPort {
				return true
			}
		}
		return false
	}
	for _, key := range []string{defaultBindAllHostIP, wantIP} {
		if used[0] == key && used[1] == wantProto && used[2] == wantPort {
			return true
		}
	}
	return false
}

// podFitsNodePorts reports whether `pod` would pass the NodePorts Filter against a
// node whose used host ports are `used` (the same reduction nodeports.Fits makes).
func podFitsNodePorts(pod *corev1.Pod, used [][3]string) bool {
	for _, want := range schedulerVisibleHostPorts(pod) {
		for _, u := range used {
			if nodePortsConflict(want, u) {
				return false
			}
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// shared fixtures
// ---------------------------------------------------------------------------

func verifyTestPod(hostNetwork bool, ports []corev1.ContainerPort, containers int) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-instance-worker-0",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			HostNetwork: hostNetwork,
		},
	}
	for i := 0; i < containers; i++ {
		pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
			Name:  fmt.Sprintf("c%d", i),
			Image: "busybox:1.36",
		})
	}
	pod.Spec.Containers[0].Ports = ports
	return pod
}

func verifyTestInstance(podName string, annotations map[string]string) *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-instance",
			Labels:      map[string]string{constants.RoleTypeLabelKey: string(constants.ComponentsTemplateType)},
			Annotations: annotations,
		},
	}
}

// validateWithRealAPIServer runs the actual kube-apiserver create-time validation
// on an injected Pod: versioned defaulting (as the apiserver does on decode),
// v1->internal conversion, then ValidatePodCreate with ResourceIsPod semantics.
func validateWithRealAPIServer(t *testing.T, pod *corev1.Pod) error {
	t.Helper()

	defaulted := pod.DeepCopy()
	legacyscheme.Scheme.Default(defaulted)

	internal := &core.Pod{}
	if err := legacyscheme.Scheme.Convert(defaulted, internal, nil); err != nil {
		return fmt.Errorf("conversion to internal failed: %w", err)
	}

	errs := corevalidation.ValidatePodCreate(internal, corevalidation.PodValidationOptions{
		ResourceIsPod: true,
	})
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", errs.ToAggregate())
}

// ---------------------------------------------------------------------------
// P0 — premise (differential: fails on base 7894133, passes on PR head)
// ---------------------------------------------------------------------------

// TestVerifyP0HostNetworkPodDeclaresSchedulerVisibleHostPort is the premise
// check: the PR claims the port allocator hands the same port to different
// host-network Pods and nothing told the scheduler. On the base branch the
// injected Pod declares no ports at all (GetHostPorts == 0), so NodePorts
// cannot prevent co-placement of two Pods that share a RoleScoped port.
func TestVerifyP0HostNetworkPodDeclaresSchedulerVisibleHostPort(t *testing.T) {
	pod := verifyTestPod(true, nil, 1)
	config := &PortAllocatorConfig{
		Allocations: []PortAllocation{
			{Name: "shared", Env: "SHARED_PORT", Scope: RoleScoped},
		},
	}
	instance := verifyTestInstance(pod.Name, map[string]string{
		FormatRoleScopedPortKey("worker", "shared"): "30000",
	})

	require.NoError(t, InjectPortsIntoPod(pod, instance, config, "worker"))

	visible := schedulerVisibleHostPorts(pod)
	require.NotEmpty(t, visible,
		"host-network Pod must expose the allocated port to the scheduler (NodePorts only filters on hostPort > 0)")
	assert.Contains(t, visible, [3]string{defaultBindAllHostIP, "TCP", "30000"})

	// The declared port must actually drive NodePorts: a second Pod with the same
	// (protocol, port) must no longer fit a node that already runs this Pod.
	used := schedulerVisibleHostPorts(pod)
	other := verifyTestPod(true, nil, 1)
	require.NoError(t, InjectPortsIntoPod(other, instance, config, "worker"))
	assert.False(t, podFitsNodePorts(other, used),
		"a second Pod sharing the allocated host port must be filtered out of this node by NodePorts")
}

// TestVerifyP0NonHostNetworkPodStaysPortReservationFree pins the documented
// non-hostNetwork contract: containerPort declarative, hostPort unset, so the
// scheduler does not reserve a node port.
func TestVerifyP0NonHostNetworkPodStaysPortReservationFree(t *testing.T) {
	pod := verifyTestPod(false, nil, 1)
	config := &PortAllocatorConfig{
		Allocations: []PortAllocation{
			{Name: "metrics", Env: "METRICS_PORT", Scope: PodScoped},
		},
	}
	instance := verifyTestInstance(pod.Name, map[string]string{
		FormatPodScopedPortKey(pod.Name, "metrics"): "30000",
	})

	require.NoError(t, InjectPortsIntoPod(pod, instance, config, "worker"))

	assert.Empty(t, schedulerVisibleHostPorts(pod),
		"non-host-network Pods must not declare hostPort (NodePorts must not reserve a node port)")
	require.Len(t, pod.Spec.Containers[0].Ports, 1)
	assert.Equal(t, int32(30000), pod.Spec.Containers[0].Ports[0].ContainerPort)
}

// ---------------------------------------------------------------------------
// F1 — injected Pod specs must pass real kube-apiserver validation
// ---------------------------------------------------------------------------

func TestVerifyInjectedPodSpecsPassRealAPIServerValidation(t *testing.T) {
	tests := []struct {
		name        string
		hostNetwork bool
		predeclared []corev1.ContainerPort
		containers  int
		allocations []PortAllocation
		annotations map[string]string
	}{
		{
			name:        "single TCP allocation on host-network pod",
			hostNetwork: true,
			containers:  1,
			allocations: []PortAllocation{{Name: "shared", Env: "SHARED_PORT", Scope: RoleScoped}},
			annotations: map[string]string{"worker.shared": "30000"},
		},
		{
			name:        "two TCP allocations on host-network pod",
			hostNetwork: true,
			containers:  1,
			allocations: []PortAllocation{
				{Name: "grpc", Env: "GRPC_PORT", Scope: RoleScoped},
				{Name: "http", Env: "HTTP_PORT", Scope: RoleScoped},
			},
			annotations: map[string]string{"worker.grpc": "30000", "worker.http": "30001"},
		},
		{
			name:        "UDP allocation on host-network pod",
			hostNetwork: true,
			containers:  1,
			allocations: []PortAllocation{{Name: "dns", Env: "DNS_PORT", Scope: RoleScoped, Protocol: corev1.ProtocolUDP}},
			annotations: map[string]string{"worker.dns": "30000"},
		},
		{
			name:        "SCTP allocation on host-network pod",
			hostNetwork: true,
			containers:  1,
			allocations: []PortAllocation{{Name: "sctp", Env: "SCTP_PORT", Scope: RoleScoped, Protocol: corev1.ProtocolSCTP}},
			annotations: map[string]string{"worker.sctp": "30000"},
		},
		{
			// Two allocations that received the SAME random port (the random
			// allocator does not guarantee cross-batch uniqueness) with the same
			// protocol must not produce duplicate hostPort declarations.
			name:        "two same-protocol allocations with the same port value",
			hostNetwork: true,
			containers:  1,
			allocations: []PortAllocation{
				{Name: "a", Env: "PORT_A", Scope: RoleScoped},
				{Name: "b", Env: "PORT_B", Scope: RoleScoped},
			},
			annotations: map[string]string{"worker.a": "30000", "worker.b": "30000"},
		},
		{
			// Same port, different protocols: two declarations are legitimate
			// (apiserver duplicate-hostPort key includes the protocol).
			name:        "same port with different protocols",
			hostNetwork: true,
			containers:  1,
			allocations: []PortAllocation{
				{Name: "tcp", Env: "TCP_PORT", Scope: RoleScoped},
				{Name: "udp", Env: "UDP_PORT", Scope: RoleScoped, Protocol: corev1.ProtocolUDP},
			},
			annotations: map[string]string{"worker.tcp": "30000", "worker.udp": "30000"},
		},
		{
			// User pre-declared the container port without hostPort; the injection
			// fills hostPort on the matching declaration.
			name:        "reuses user-declared matching container port",
			hostNetwork: true,
			predeclared: []corev1.ContainerPort{{ContainerPort: 30000, Protocol: corev1.ProtocolTCP}},
			containers:  1,
			allocations: []PortAllocation{{Name: "shared", Env: "SHARED_PORT", Scope: RoleScoped}},
			annotations: map[string]string{"worker.shared": "30000"},
		},
		{
			name: "port declared by a later container",
			hostNetwork: true,
			containers:  2,
			predeclared: nil,
			allocations: []PortAllocation{{Name: "shared", Env: "SHARED_PORT", Scope: RoleScoped}},
			annotations: map[string]string{"worker.shared": "30000"},
		},
		{
			name:        "non-host-network pod with user-declared hostPort",
			hostNetwork: false,
			predeclared: []corev1.ContainerPort{{ContainerPort: 8080, HostPort: 30001, Protocol: corev1.ProtocolTCP}},
			containers:  1,
			allocations: []PortAllocation{{Name: "metrics", Env: "METRICS_PORT", Scope: PodScoped}},
			annotations: map[string]string{"test-instance-worker-0.metrics": "30000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := verifyTestPod(tt.hostNetwork, tt.predeclared, tt.containers)
			if tt.containers > 1 {
				// Declare the same port in a later container, like a sidecar that
				// actually owns the listener.
				pod.Spec.Containers[1].Ports = []corev1.ContainerPort{{ContainerPort: 30000, Protocol: corev1.ProtocolTCP}}
			}
			config := &PortAllocatorConfig{Allocations: tt.allocations}
			instance := verifyTestInstance(pod.Name, tt.annotations)

			require.NoError(t, InjectPortsIntoPod(pod, instance, config, "worker"))

			err := validateWithRealAPIServer(t, pod)
			assert.NoError(t, err,
				"the injected Pod must be accepted by kube-apiserver create-time validation")
		})
	}
}

// TestVerifyControlApIServerHostNetworkPortSemantics pins two apiserver controls
// used to attribute (or not attribute) errors to this PR:
//
//   - kube-apiserver already defaults hostPort = containerPort for every port of a
//     hostNetwork Pod (pkg/apis/core/v1/defaults.go SetDefaults_Pod), so the PR's
//     explicit hostPort=containerPort matches upstream semantics, and a bare
//     containerPort declaration on a hostNetwork Pod is scheduler-visible anyway.
//   - a user declaration with an explicit mismatched hostPort (containerPort: 8080,
//     hostPort: 9090) is rejected with or without injection — pre-existing user
//     error, not attributable to this PR.
func TestVerifyControlApIServerHostNetworkPortSemantics(t *testing.T) {
	// Control 1: host-network defaulting mirrors the injection's explicit hostPort.
	bare := verifyTestPod(true, []corev1.ContainerPort{{ContainerPort: 8080, Protocol: corev1.ProtocolTCP}}, 1)
	defaulted := bare.DeepCopy()
	legacyscheme.Scheme.Default(defaulted)
	require.Equal(t, int32(8080), defaulted.Spec.Containers[0].Ports[0].HostPort,
		"apiserver defaulting must fill hostPort = containerPort for hostNetwork Pods")
	assert.NoError(t, validateWithRealAPIServer(t, bare))

	// Control 2: an explicitly mismatched user declaration fails without injection.
	mismatched := verifyTestPod(true, []corev1.ContainerPort{{ContainerPort: 8080, HostPort: 9090, Protocol: corev1.ProtocolTCP}}, 1)
	bareErr := validateWithRealAPIServer(t, mismatched)
	require.Error(t, bareErr, "hostNetwork Pod with explicit hostPort != containerPort is invalid even without injection")
	assert.Contains(t, bareErr.Error(), "must match `containerPort`")

	// And fails the same way when an unrelated port is injected alongside it.
	injected := verifyTestPod(true, []corev1.ContainerPort{{ContainerPort: 8080, HostPort: 9090, Protocol: corev1.ProtocolTCP}}, 1)
	config := &PortAllocatorConfig{
		Allocations: []PortAllocation{{Name: "shared", Env: "SHARED_PORT", Scope: RoleScoped}},
	}
	instance := verifyTestInstance(injected.Name, map[string]string{"worker.shared": "30000"})
	require.NoError(t, InjectPortsIntoPod(injected, instance, config, "worker"))

	injectedErr := validateWithRealAPIServer(t, injected)
	require.Error(t, injectedErr, "the pre-existing user declaration stays invalid after injection")
	assert.Contains(t, injectedErr.Error(), "must match `containerPort`",
		"the remaining error must point at the user's own declaration, not the injected one")
}

// ---------------------------------------------------------------------------
// F2 — idempotency across re-injection
// ---------------------------------------------------------------------------

func TestVerifyInjectionIsIdempotentAcrossReinjection(t *testing.T) {
	pod := verifyTestPod(true, nil, 1)
	config := &PortAllocatorConfig{
		Allocations: []PortAllocation{
			{Name: "shared", Env: "SHARED_PORT", Scope: RoleScoped},
		},
	}
	instance := verifyTestInstance(pod.Name, map[string]string{
		FormatRoleScopedPortKey("worker", "shared"): "30000",
	})

	require.NoError(t, InjectPortsIntoPod(pod, instance, config, "worker"))
	require.NoError(t, InjectPortsIntoPod(pod, instance, config, "worker"))
	require.NoError(t, InjectPortsIntoPod(pod, instance, config, "worker"))

	require.Len(t, pod.Spec.Containers[0].Ports, 1,
		"re-injection must not duplicate the host port declaration")
	assert.Equal(t, int32(30000), pod.Spec.Containers[0].Ports[0].HostPort)

	assert.NoError(t, validateWithRealAPIServer(t, pod),
		"the repeatedly-injected Pod must still pass kube-apiserver validation")
}

// ---------------------------------------------------------------------------
// F3 — canary: the upgrade/migration gap (minor, docs)
// ---------------------------------------------------------------------------

// TestVerifyCanaryLegacyPodWithoutDeclarationIsInvisibleToNodePorts is a BUG-CANARY.
// It asserts the CURRENT behavior: a Pod created by the pre-PR controller (env var
// only, no port declarations) exposes zero host ports to NodePorts, so after an
// upgrade the scheduler can still co-locate a NEW pod (which declares hostPort)
// on a node where a LEGACY pod of the same component already binds the shared
// port. Polarity: passes while the gap exists; if a future change backfills
// declarations (or recreates legacy pods), this flips to fail and must be
// inverted / promoted to a contract test.
func TestVerifyCanaryLegacyPodWithoutDeclarationIsInvisibleToNodePorts(t *testing.T) {
	legacy := verifyTestPod(true, nil, 1)
	legacy.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "SHARED_PORT", Value: "30000"}}

	assert.Empty(t, schedulerVisibleHostPorts(legacy),
		"legacy pod has no scheduler-visible host ports")

	// A new pod (injected, declaring hostPort) still "fits" the node the legacy
	// pod runs on, because the legacy pod reserves nothing in the scheduler's view.
	newPod := verifyTestPod(true, nil, 1)
	config := &PortAllocatorConfig{
		Allocations: []PortAllocation{{Name: "shared", Env: "SHARED_PORT", Scope: RoleScoped}},
	}
	instance := verifyTestInstance(newPod.Name, map[string]string{
		FormatRoleScopedPortKey("worker", "shared"): "30000",
	})
	require.NoError(t, InjectPortsIntoPod(newPod, instance, config, "worker"))

	legacyUsed := schedulerVisibleHostPorts(legacy)
	assert.True(t, podFitsNodePorts(newPod, legacyUsed),
		"canary: NodePorts lets the new pod land next to the legacy pod that already binds the port (upgrade gap)")
}
