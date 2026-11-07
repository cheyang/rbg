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

// Package verifyv1a1 is the reviewer verification harness for the removal of
// the v1alpha1 deprecation admission webhooks (PR #556). It proves, against the
// PR head, the three claims the removal rests on:
//
//  1. v1alpha1 writes still reach the v1alpha2 deprecated-workload gate through
//     conversion (matchPolicy defaults to Equivalent), so enforcement is not
//     lost with the Exact-matched v1alpha1 webhooks.
//  2. A v1alpha1 write now returns exactly ONE Warning header — the CRD-level
//     deprecationWarning carrying the "will be removed in 0.10.0" timeline —
//     instead of two overlapping warnings.
//  3. Grandfathering semantics (an existing role keeps its own deprecated
//     workload type; switching between deprecated types is a new use) survive
//     the conversion round-trip.
//
// It boots envtest with the shipped CRDs, serves the production webhook
// handlers cmd/rbgs registers at this commit, and installs the shipped
// config/webhook/manifests.yaml rewritten to the local server — the same
// release bytes the deploy paths consume.
package verifyv1a1

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerifyV1alpha1DeprecationRemoval(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "v1alpha1 Deprecation Removal Verification Suite")
}

// verifyWarningRecorder collects the Warning headers the API server returned
// to this suite's client, so specs can assert the on-the-wire warnings rather
// than re-deriving them.
type verifyWarningRecorder struct {
	mu       sync.Mutex
	warnings []string
}

func (r *verifyWarningRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = nil
}

func (r *verifyWarningRecorder) HandleWarningHeader(code int, agent string, text string) {
	if code < 200 || code > 299 || text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, text)
}

func (r *verifyWarningRecorder) All() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.warnings...)
}

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	testMgr   ctrl.Manager
	warnings  = &verifyWarningRecorder{}
)

const verifyNamespace = "verify-v1a1-gate"

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping the v1alpha1 verification environment")

	_, currentFile, _, _ := runtime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "config", "crd", "bases")
	webhookManifestPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "config", "webhook", "manifests.yaml")

	// Register both versions before starting the environment: envtest decides
	// at startup — from this scheme — which multi-version CRDs get webhook
	// conversion enabled. Registering afterwards would leave the suite on
	// version-only conversion instead of the production v1alpha1 <-> v1alpha2
	// translation the manager serves.
	Expect(workloadsv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(workloadsv1alpha2.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(admissionregistrationv1.AddToScheme(scheme.Scheme)).To(Succeed())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// Route the suite client through the warning recorder.
	clientCfg := rest.CopyConfig(cfg)
	clientCfg.WarningHandler = warnings
	k8sClient, err = client.New(clientCfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	By("registering the production webhook handlers this commit serves")
	testMgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: server.Options{
			BindAddress: "0",
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    testEnv.WebhookInstallOptions.LocalServingHost,
			Port:    testEnv.WebhookInstallOptions.LocalServingPort,
			CertDir: testEnv.WebhookInstallOptions.LocalServingCertDir,
		}),
	})
	Expect(err).NotTo(HaveOccurred())

	// Mirrors cmd/rbgs registration at this commit: only the v1alpha2
	// admission webhooks remain; the v1alpha1 deprecation webhooks are gone.
	Expect((&workloadsv1alpha2.RoleBasedGroup{}).SetupWebhookWithManager(testMgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleBasedGroupSet{}).SetupWebhookWithManager(testMgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.CoordinatedPolicy{}).SetupWebhookWithManager(testMgr, false)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleInstanceSet{}).SetupWebhookWithManager(testMgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		err := testMgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	// The webhook server must be serving before configurations pointing at it
	// are installed (and before conversion-dependent writes are made).
	Eventually(func() error {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf(
			"%s:%d",
			testEnv.WebhookInstallOptions.LocalServingHost,
			testEnv.WebhookInstallOptions.LocalServingPort,
		), 250*time.Millisecond)
		if err != nil {
			return err
		}
		return conn.Close()
	}, 10*time.Second, 100*time.Millisecond).Should(Succeed())

	By("creating the test namespace")
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: verifyNamespace}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())

	By("seeding the pre-gate legacy state while no webhook configuration is installed")
	seedLegacyV1alpha1Objects()

	By("installing the shipped webhook manifests")
	installShippedWebhookConfigurations(webhookManifestPath)
	waitForGate()
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

// installShippedWebhookConfigurations decodes the shipped webhook manifests and
// creates them against the local test server, rewriting the Kustomize
// placeholder clientConfig the way envtest rewrites its own manifests.
func installShippedWebhookConfigurations(path string) {
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())

	opts := testEnv.WebhookInstallOptions
	hostPort := net.JoinHostPort(opts.LocalServingHost, fmt.Sprint(opts.LocalServingPort))

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	installed := 0
	for {
		var raw map[string]interface{}
		err := decoder.Decode(&raw)
		if err == io.EOF {
			break
		}
		Expect(err).NotTo(HaveOccurred())
		if raw == nil {
			continue
		}

		switch raw["kind"].(string) {
		case "ValidatingWebhookConfiguration":
			config := &admissionregistrationv1.ValidatingWebhookConfiguration{}
			Expect(k8sruntime.DefaultUnstructuredConverter.FromUnstructured(raw, config)).To(Succeed())
			for i := range config.Webhooks {
				rewriteClientConfig(&config.Webhooks[i].ClientConfig, hostPort, opts.LocalServingCAData)
			}
			Expect(k8sClient.Create(ctx, config)).To(Succeed())
			installed++
		case "MutatingWebhookConfiguration":
			config := &admissionregistrationv1.MutatingWebhookConfiguration{}
			Expect(k8sruntime.DefaultUnstructuredConverter.FromUnstructured(raw, config)).To(Succeed())
			for i := range config.Webhooks {
				rewriteClientConfig(&config.Webhooks[i].ClientConfig, hostPort, opts.LocalServingCAData)
			}
			Expect(k8sClient.Create(ctx, config)).To(Succeed())
			installed++
		default:
			Fail(fmt.Sprintf("unexpected kind in shipped webhook manifests: %v", raw["kind"]))
		}
	}
	Expect(installed).To(Equal(2), "the shipped manifests must contain exactly two webhook configurations")
}

func rewriteClientConfig(cc *admissionregistrationv1.WebhookClientConfig, hostPort string, caData []byte) {
	if cc.Service != nil && cc.Service.Path != nil {
		url := fmt.Sprintf("https://%s%s", hostPort, *cc.Service.Path)
		cc.URL = &url
		cc.Service = nil
	}
	cc.CABundle = caData
}

// waitForGate blocks until the v1alpha2 gate — reached through conversion from
// a v1alpha1 request — actually rejects a new deprecated workload use. The
// probe is a server-side dry-run create, so it traverses admission without
// persisting anything.
func waitForGate() {
	Eventually(func() bool {
		rbg := legacyV1alpha1RBG("gate-readiness-v1a1", "worker", "apps/v1", "StatefulSet")
		err := k8sClient.Create(ctx, rbg, &client.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		return err != nil && strings.Contains(err.Error(), "new uses are not accepted")
	}, 10*time.Second, 250*time.Millisecond).Should(BeTrue(),
		"the conversion-mediated v1alpha2 gate did not become active for v1alpha1 requests")
}

// legacyV1alpha1RBG builds a v1alpha1 RoleBasedGroup with a single role using
// the given workload type.
func legacyV1alpha1RBG(name, roleName, apiVersion, kind string) *workloadsv1alpha1.RoleBasedGroup {
	return &workloadsv1alpha1.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: verifyNamespace},
		Spec: workloadsv1alpha1.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha1.RoleSpec{{
				Name:     roleName,
				Replicas: ptr.To(int32(1)),
				Workload: workloadsv1alpha1.WorkloadSpec{
					APIVersion: apiVersion,
					Kind:       kind,
				},
				TemplateSource: workloadsv1alpha1.TemplateSource{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}},
						},
					},
				},
			}},
		},
	}
}

// seedLegacyV1alpha1Objects creates, with no admission gate installed yet, the
// pre-upgrade objects the grandfathering specs update afterwards: a v1alpha1
// RoleBasedGroup whose role uses a deprecated workload type.
func seedLegacyV1alpha1Objects() {
	legacy := legacyV1alpha1RBG("legacy-rbg", "worker", "apps/v1", "StatefulSet")
	Expect(k8sClient.Create(ctx, legacy)).To(Succeed())
}
