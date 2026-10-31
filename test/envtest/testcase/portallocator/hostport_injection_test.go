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

package portallocator

// L2 (integration) verification harness for PR #532. Runs the real
// RoleBasedGroup -> RoleInstanceSet -> RoleInstance -> Pod chain against a real
// kube-apiserver (envtest), with the port allocator enabled the way
// cmd/rbgs/main.go enables it. The apiserver's create-time validation and
// defaulting are live, so a created Pod proves the injected spec is accepted
// upstream (not just by an offline validator).
//
// Claim coverage (see docs/verification/pr532-host-port-scheduling/verify-manifest.json):
//
//	V4  contract: host-network pods created by the controller declare
//	    containerPort == hostPort == the RoleScoped allocated port, with the env
//	    var and pod annotation injected; the two pods share one port value.
//	V5  contract: non-host-network pods declare containerPort with hostPort == 0
//	    (declarative only, no node-port reservation).

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	portallocator "sigs.k8s.io/rbgs/pkg/port-allocator"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

const (
	timeout  = 60 * time.Second
	interval = 250 * time.Millisecond
)

var _ = Describe("Port allocator host port injection", func() {
	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("test-portallocator-%d", time.Now().UnixNano())
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	It("declares scheduler-visible host ports on host-network pods and keeps plain pods hostPort-free", func() {
		rbgName := "hostport-inject"
		rbg := newPortAllocatorRBG(rbgName, testNs)
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())

		By("waiting for the two host-network worker pods and the plain pod")
		pods := listRBGPods(rbgName, testNs)
		workers := podsByComponent(pods, "worker")
		plains := podsByComponent(pods, "plain")
		Expect(len(workers)).To(Equal(2), "expected two host-network worker pods")
		Expect(len(plains)).To(Equal(1), "expected one non-host-network plain pod")

		By("host-network pods share one RoleScoped allocated port")
		p0 := workers[0].Annotations["test/shared-port"]
		p1 := workers[1].Annotations["test/shared-port"]
		Expect(p0).NotTo(BeEmpty(), "worker pod must carry the shared port annotation")
		Expect(p1).NotTo(BeEmpty(), "worker pod must carry the shared port annotation")
		Expect(p1).To(Equal(p0), "both workers must share the RoleScoped port")

		By("host-network pods declare containerPort == hostPort == allocated port (scheduler-visible)")
		assertHostPortDeclaration(workers[0], p0)
		assertHostPortDeclaration(workers[1], p0)

		By("the shared port env var is injected into all containers of the host-network pods")
		assertEnvVar(workers[0], "SHARED_PORT", p0)
		assertEnvVar(workers[1], "SHARED_PORT", p0)

		By("non-host-network pods declare containerPort without reserving a host port")
		plain := plains[0]
		metrics := envVarValue(plain, "METRICS_PORT")
		Expect(metrics).NotTo(BeEmpty(), "plain pod must carry the METRICS_PORT env var")
		Expect(plain.Spec.HostNetwork).To(BeFalse())
		foundPlainPort := false
		for _, c := range plain.Spec.Containers {
			for _, p := range c.Ports {
				if fmt.Sprintf("%d", p.ContainerPort) == metrics {
					Expect(p.HostPort).To(Equal(int32(0)),
						"non-host-network pods must not reserve a node port")
					Expect(string(p.Protocol)).To(Equal(string(corev1.ProtocolTCP)))
					foundPlainPort = true
				}
			}
		}
		Expect(foundPlainPort).To(BeTrue(),
			"plain pod must declare the PodScoped allocated port as containerPort")
	})
})

func listRBGPods(rbgName, ns string) []corev1.Pod {
	var pods []corev1.Pod
	Eventually(func() (int, error) {
		podList := &corev1.PodList{}
		err := testutil.K8sClient.List(testutil.Ctx, podList,
			client.InNamespace(ns),
			client.MatchingLabels{constants.GroupNameLabelKey: rbgName},
		)
		if err != nil {
			return 0, err
		}
		pods = podList.Items
		return len(pods), nil
	}, timeout, interval).Should(Equal(3), "all three pods of the RBG should be created")
	return pods
}

func podsByComponent(pods []corev1.Pod, component string) []*corev1.Pod {
	var out []*corev1.Pod
	for i := range pods {
		if pods[i].Labels[constants.ComponentNameLabelKey] == component {
			out = append(out, &pods[i])
		}
	}
	return out
}

func assertHostPortDeclaration(pod *corev1.Pod, port string) {
	Expect(pod.Spec.HostNetwork).To(BeTrue())
	Expect(pod.Spec.Containers).NotTo(BeEmpty())
	found := false
	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if fmt.Sprintf("%d", p.HostPort) == port {
				Expect(fmt.Sprintf("%d", p.ContainerPort)).To(Equal(port),
					"host-network port declarations must satisfy hostPort == containerPort")
				Expect(string(p.Protocol)).To(Equal(string(corev1.ProtocolTCP)), "protocol defaults to TCP")
				found = true
			}
		}
	}
	Expect(found).To(BeTrue(),
		fmt.Sprintf("host-network pod %s must declare hostPort %s for the scheduler", pod.Name, port))
}

func assertEnvVar(pod *corev1.Pod, name, value string) {
	for _, c := range pod.Spec.Containers {
		found := false
		for _, env := range c.Env {
			if env.Name == name {
				Expect(env.Value).To(Equal(value))
				found = true
			}
		}
		Expect(found).To(BeTrue(), fmt.Sprintf("container %s of pod %s must have env %s", c.Name, pod.Name, name))
	}
}

func envVarValue(pod *corev1.Pod, name string) string {
	for _, c := range pod.Spec.Containers {
		for _, env := range c.Env {
			if env.Name == name {
				return env.Value
			}
		}
	}
	return ""
}

func newPortAllocatorRBG(name, namespace string) *workloadsv1alpha2.RoleBasedGroup {
	workerTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				portallocator.PortAllocatorAnnotationKey: `{
					"allocations": [
						{
							"name": "shared",
							"env": "SHARED_PORT",
							"annotationKey": "test/shared-port",
							"scope": "RoleScoped"
						}
					]
				}`,
			},
		},
		Spec: corev1.PodSpec{
			HostNetwork: true,
			Containers: []corev1.Container{
				{Name: "main", Image: "busybox:1.36"},
			},
		},
	}

	plainTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				portallocator.PortAllocatorAnnotationKey: `{
					"allocations": [
						{
							"name": "metrics",
							"env": "METRICS_PORT",
							"scope": "PodScoped"
						}
					]
				}`,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "main", Image: "busybox:1.36"},
			},
		},
	}

	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{
					Name:     "prefill",
					Replicas: ptr.To[int32](1),
					Pattern: workloadsv1alpha2.Pattern{
						CustomComponentsPattern: &workloadsv1alpha2.CustomComponentsPattern{
							Components: []workloadsv1alpha2.InstanceComponent{
								{
									Name:     "worker",
									Size:     ptr.To[int32](2),
									Template: workerTemplate,
								},
								{
									Name:     "plain",
									Size:     ptr.To[int32](1),
									Template: plainTemplate,
								},
							},
						},
					},
				},
			},
		},
	}
}

