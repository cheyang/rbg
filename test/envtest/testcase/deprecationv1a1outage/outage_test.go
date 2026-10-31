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

// Package deprecationv1a1outage is a reviewer-built verification harness for
// PR #524 finding F2 (round 2): the four warning-only v1alpha1 deprecation
// webhooks must not be able to block writes.
//
// It boots envtest WITHOUT any webhook server, installs the four warning-only
// webhook entries taken from the shipped config/webhook/manifests.yaml with
// their clientConfig pointed at a dead port, and asserts:
//
//   - with the SHIPPED failurePolicy (Ignore since 22acc570) a v1alpha1
//     InstanceSet write is still admitted (contract; red when the policy was
//     Fail, green after the fix);
//   - the same dead endpoint with failurePolicy flipped to Fail rejects the
//     write — a mechanism control proving the API server really consults the
//     dead endpoint, so the first assertion passes because of Ignore, not
//     because the webhook failed to match.
package deprecationv1a1outage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
)

// parseWebhookManifests decodes the first ValidatingWebhookConfiguration in the
// shipped manifest bytes and returns its webhook entries.
func parseWebhookManifests(data []byte) ([]admissionregistrationv1.ValidatingWebhook, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		var raw map[string]interface{}
		err := decoder.Decode(&raw)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if raw == nil {
			continue
		}
		if raw["kind"] != "ValidatingWebhookConfiguration" {
			continue
		}
		config := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, config); err != nil {
			return nil, err
		}
		return config.Webhooks, nil
	}
	return nil, fmt.Errorf("no ValidatingWebhookConfiguration found")
}

// warningOnlyWebhookNames are the four v1alpha1 deprecation endpoints whose
// handlers can never reject (they only emit a warning).
var warningOnlyWebhookNames = map[string]bool{
	"vclusterengineruntimeprofile-v1alpha1-deprecation.kb.io":  true,
	"vinstance-v1alpha1-deprecation.kb.io":                     true,
	"vinstanceset-v1alpha1-deprecation.kb.io":                  true,
	"vrolebasedgroupscalingadapter-v1alpha1-deprecation.kb.io": true,
}

var (
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	cancelCtx context.CancelFunc
	ctxValue  context.Context
)

func TestV1alpha1WarningOnlyOutage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "v1alpha1 Warning-Only Webhook Outage Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctxValue, cancelCtx = context.WithCancel(context.TODO())

	_, currentFile, _, _ := goruntime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(currentFile)))))
	crdPath := filepath.Join(repoRoot, "config", "crd", "bases")

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(workloadsv1alpha1.AddToScheme(scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	// No manager, no webhook server: every webhook clientConfig below points
	// at a port with nothing listening, which is the outage under test.

	By("creating the outage namespaces")
	Expect(k8sClient.Create(ctxValue, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "outage-ignore"},
	})).To(Succeed())
	Expect(k8sClient.Create(ctxValue, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "outage-control",
			Labels: map[string]string{"outage-control": "true"},
		},
	})).To(Succeed())

	By("installing the shipped warning-only webhooks against a dead endpoint")
	deadHostPort := deadAddress()
	installFromShippedManifest(repoRoot, deadHostPort)

	// The API server's webhook configuration watcher is eventually consistent;
	// wait until the Fail-policy control webhook observably rejects.
	Eventually(func() bool {
		return createInstanceSet("outage-control", "readiness-control") != nil
	}, 20*time.Second, 250*time.Millisecond).Should(BeTrue(),
		"the dead Fail-policy control webhook never became active")
})

var _ = AfterSuite(func() {
	cancelCtx()
	Expect(testEnv.Stop()).To(Succeed())
})

var _ = Describe("warning-only v1alpha1 webhooks during an outage", func() {
	It("admits a v1alpha1 InstanceSet write while the warning-only webhooks are unreachable", func() {
		// CONTRACT for F2: the four warning-only endpoints ship
		// failurePolicy=Ignore, so an unreachable webhook server must not
		// block writes to still-supported v1alpha1 kinds. On the pre-fix
		// head (Fail) this create was rejected with "failed calling
		// webhook ... connection refused".
		Expect(createInstanceSet("outage-ignore", "outage-ign")).To(Succeed())
	})

	It("rejects the same write when the identical dead endpoint carries failurePolicy=Fail", func() {
		// Mechanism control: proves the API server consults the dead
		// endpoint, so the spec above passes because of Ignore and not
		// because the webhook failed to match the request.
		err := createInstanceSet("outage-control", "outage-fail")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed calling webhook"))
	})
})

// installFromShippedManifest reads config/webhook/manifests.yaml, keeps the
// four warning-only v1alpha1 webhooks with their shipped failurePolicy and
// rules, and installs them twice: once verbatim (shipped policy) matching all
// namespaces, and once with the policy flipped to Fail and scoped by
// namespaceSelector to the outage-control namespace.
func installFromShippedManifest(repoRoot, deadHostPort string) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "config", "webhook", "manifests.yaml"))
	Expect(err).NotTo(HaveOccurred())

	manifests, err := parseWebhookManifests(data)
	Expect(err).NotTo(HaveOccurred())
	Expect(manifests).NotTo(BeEmpty())

	shipped := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "outage-shipped-warning-only"},
	}
	control := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "outage-control-fail"},
	}

	found := 0
	for _, wh := range manifests {
		if !warningOnlyWebhookNames[wh.Name] {
			continue
		}
		found++
		// The shipped bytes: failurePolicy as generated (Ignore since the
		// F2 fix, Fail before it).
		shippedWebhook := wh.DeepCopy()
		rewriteToDeadAddress(&shippedWebhook.ClientConfig, deadHostPort)
		shipped.Webhooks = append(shipped.Webhooks, *shippedWebhook)

		// The control: identical, but hard Fail and scoped to the labeled
		// control namespace.
		controlWebhook := wh.DeepCopy()
		fail := admissionregistrationv1.Fail
		controlWebhook.FailurePolicy = &fail
		controlWebhook.NamespaceSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{"outage-control": "true"},
		}
		rewriteToDeadAddress(&controlWebhook.ClientConfig, deadHostPort)
		control.Webhooks = append(control.Webhooks, *controlWebhook)
	}
	Expect(found).To(Equal(4),
		"the shipped manifest must contain exactly the four warning-only v1alpha1 webhooks")

	Expect(k8sClient.Create(ctxValue, shipped)).To(Succeed())
	Expect(k8sClient.Create(ctxValue, control)).To(Succeed())
}

// deadAddress reserves a local port and closes it again, so nothing is
// listening when the API server dials it.
func deadAddress() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	addr := listener.Addr().String()
	Expect(listener.Close()).To(Succeed())
	return addr
}

func rewriteToDeadAddress(cc *admissionregistrationv1.WebhookClientConfig, hostPort string) {
	Expect(cc.Service).NotTo(BeNil())
	Expect(cc.Service.Path).NotTo(BeNil())
	url := fmt.Sprintf("https://%s%s", hostPort, *cc.Service.Path)
	cc.URL = &url
	cc.Service = nil
	cc.CABundle = nil
}

func createInstanceSet(namespace, name string) error {
	return k8sClient.Create(ctxValue, &workloadsv1alpha1.InstanceSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: workloadsv1alpha1.InstanceSetSpec{
			Replicas: ptr.To(int32(1)),
			InstanceTemplate: workloadsv1alpha1.InstanceTemplate{
				InstanceSpec: workloadsv1alpha1.InstanceSpec{
					Components: []workloadsv1alpha1.InstanceComponent{{
						Name: "worker",
						Size: ptr.To(int32(1)),
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}},
							},
						},
					}},
				},
			},
		},
	})
}
