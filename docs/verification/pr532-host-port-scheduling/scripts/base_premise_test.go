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

// Base-branch premise check for PR #532 (P0). This file is self-contained and
// deliberately base-compatible (it must not reference PortAllocation.Protocol,
// which only exists on the PR head). Copy it into pkg/port-allocator/ on the
// base commit and run:
//
//	go test ./pkg/port-allocator/ -run TestVerifyP0BasePremiseHostNetworkPodIsInvisibleToScheduler -v
//
// Expected on base: FAIL with "scheduler-visible host ports" empty — the premise.
// The head-side contract test TestVerifyP0HostNetworkPodDeclaresSchedulerVisibleHostPort
// (pkg/port-allocator/verify_hostport_scheduling_test.go on the verify branch)
// asserts the fixed behavior and passes on the PR head.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

const defaultBindAllHostIPBase = "0.0.0.0"

func baseSchedulerVisibleHostPorts(pod *corev1.Pod) [][3]string {
	var ports [][3]string
	if pod == nil {
		return ports
	}
	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if p.HostPort > 0 {
				ip, proto := p.HostIP, string(p.Protocol)
				if len(ip) == 0 {
					ip = defaultBindAllHostIPBase
				}
				if len(proto) == 0 {
					proto = string(corev1.ProtocolTCP)
				}
				ports = append(ports, [3]string{ip, proto, fmt.Sprintf("%d", p.HostPort)})
			}
		}
	}
	return ports
}

// TestVerifyP0BasePremiseHostNetworkPodIsInvisibleToScheduler reproduces the
// claimed problem on the base branch: two host-network pods share a RoleScoped
// allocated port, but the injected pod spec declares no ports at all, so
// kube-scheduler's NodePorts plugin (which only counts hostPort > 0) has nothing
// to filter on and can co-locate both pods on one node.
func TestVerifyP0BasePremiseHostNetworkPodIsInvisibleToScheduler(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-instance-worker-0", Namespace: "default"},
		Spec: corev1.PodSpec{
			HostNetwork: true,
			Containers:  []corev1.Container{{Name: "worker", Image: "busybox:1.36"}},
		},
	}
	config := &PortAllocatorConfig{
		Allocations: []PortAllocation{
			{Name: "shared", Env: "SHARED_PORT", Scope: RoleScoped},
		},
	}
	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-instance",
			Labels:      map[string]string{constants.RoleTypeLabelKey: string(constants.ComponentsTemplateType)},
			Annotations: map[string]string{FormatRoleScopedPortKey("worker", "shared"): "30000"},
		},
	}

	require.NoError(t, InjectPortsIntoPod(pod, instance, config, "worker"))

	visible := baseSchedulerVisibleHostPorts(pod)
	assert.NotEmpty(t, visible,
		"base branch: host-network pod exposes no scheduler-visible host ports — NodePorts cannot prevent co-placement of two pods sharing the allocated port")
}
