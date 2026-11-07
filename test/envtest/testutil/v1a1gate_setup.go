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

// Reviewer verification harness for PR #556 (remove v1alpha1 admission
// webhooks). Additive only: no production code is modified.
//
// The suite this supports boots envtest the way a production 0.9.x cluster
// looks AFTER the PR: no v1alpha1 admission webhooks anywhere, the shipped
// v1alpha2 admission webhooks installed from config/webhook/manifests.yaml,
// and the CRD conversion webhook (strategy: Webhook, /convert) patched onto
// the rolebasedgroups/rolebasedgroupsets CRDs exactly as
// deploy/kubectl/manifests.yaml ships them. config/crd/bases carries no
// conversion stanza (it is a kustomize patch), so the harness patches the two
// CRDs after the API server starts, pointing at the manager's /convert
// endpoint.
package testutil

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
)

// GateWarningRecorder collects the Warning headers the API server returned to
// the suite's client, so specs can assert the on-the-wire deprecation warning
// instead of re-implementing it.
type GateWarningRecorder struct {
	mu       sync.Mutex
	warnings []string
}

// Reset drops all recorded warnings. Call it at the start of every spec.
func (r *GateWarningRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = nil
}

// HandleWarningHeader implements rest.WarningHandler.
func (r *GateWarningRecorder) HandleWarningHeader(code int, agent string, text string) {
	if code < 200 || code > 299 || text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, text)
}

// Containing returns every recorded warning containing substr.
func (r *GateWarningRecorder) Containing(substr string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, warning := range r.warnings {
		if strings.Contains(warning, substr) {
			out = append(out, warning)
		}
	}
	return out
}

// All returns a copy of every recorded warning.
func (r *GateWarningRecorder) All() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.warnings...)
}

// GateWarnings is shared by the v1a1gate suite's client.
var GateWarnings = &GateWarningRecorder{}

// V1alpha1GateNamespace is where the v1a1gate suite submits objects.
const V1alpha1GateNamespace = "v1a1-gate"

// SetupV1alpha1GateTestEnv boots a webhook-enabled envtest environment that
// mirrors a production cluster running the PR-head controller: the v1alpha2
// admission webhooks (which also serve /convert for the Hub types) are
// registered on the manager, and the two conversion CRDs are patched to call
// it. No validating/mutating webhook CONFIGURATION is installed yet, so the
// suite can seed a pre-gate legacy object first; call
// InstallShippedAdmissionWebhooks to switch the gate on.
func SetupV1alpha1GateTestEnv() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	Ctx, Cancel = context.WithCancel(context.TODO())

	By("bootstrapping the v1alpha1-gate test environment")

	// Register every type the suite serves before starting the environment so
	// the converter knows all spokes of the Hub types.
	err := workloadsv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = workloadsv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = admissionregistrationv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = apiextensionsv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	_, currentFile, _, _ := runtime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "config", "crd", "bases")
	webhookManifestPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "config", "webhook", "manifests.yaml")

	TestEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}

	Cfg, err = TestEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(Cfg).NotTo(BeNil())

	// Route the suite client through the warning recorder so the specs can
	// assert the warnings the API server actually returned.
	clientCfg := rest.CopyConfig(Cfg)
	clientCfg.WarningHandler = GateWarnings
	K8sClient, err = client.New(clientCfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(K8sClient).NotTo(BeNil())

	By("registering the production v1alpha2 webhook handlers")
	TestMgr, err = ctrl.NewManager(Cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: server.Options{
			BindAddress: "0",
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    TestEnv.WebhookInstallOptions.LocalServingHost,
			Port:    TestEnv.WebhookInstallOptions.LocalServingPort,
			CertDir: TestEnv.WebhookInstallOptions.LocalServingCertDir,
		}),
	})
	Expect(err).NotTo(HaveOccurred())

	// Same registrations and flag values as cmd/rbgs main.go at the PR head,
	// minus the removed v1alpha1 deprecation webhooks. Registering the Hub
	// types also serves the /convert conversion endpoint.
	Expect((&workloadsv1alpha2.RoleBasedGroup{}).SetupWebhookWithManager(TestMgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleBasedGroupSet{}).SetupWebhookWithManager(TestMgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.CoordinatedPolicy{}).SetupWebhookWithManager(TestMgr, false)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleInstanceSet{}).SetupWebhookWithManager(TestMgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		err := TestMgr.Start(Ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	// The webhook server must be serving before the CRDs point conversion at it.
	Eventually(func() error {
		conn, err := net.DialTimeout(
			"tcp",
			fmt.Sprintf(
				"%s:%d",
				TestEnv.WebhookInstallOptions.LocalServingHost,
				TestEnv.WebhookInstallOptions.LocalServingPort,
			),
			250*time.Millisecond,
		)
		if err != nil {
			return err
		}
		return conn.Close()
	}, 10*time.Second, 100*time.Millisecond).Should(Succeed())

	By("patching the shipped CRD conversion stanzas onto the test CRDs")
	patchConversionStanza()

	By("creating the gate namespace")
	CreateNamespace(V1alpha1GateNamespace)

	By("waiting for the conversion endpoint to answer through the API server")
	waitForConversion()

	gateManifestPath = webhookManifestPath
}

var gateManifestPath string

// conversionCRDs are the two CRDs whose shipped form carries
// spec.conversion.strategy=Webhook (see deploy/kubectl/manifests.yaml).
var conversionCRDs = []string{
	"rolebasedgroups.workloads.x-k8s.io",
	"rolebasedgroupsets.workloads.x-k8s.io",
}

// patchConversionStanza points the two conversion CRDs at the test webhook
// server's /convert endpoint, mirroring the shipped deploy manifests.
func patchConversionStanza() {
	opts := TestEnv.WebhookInstallOptions
	url := fmt.Sprintf("https://%s/convert", net.JoinHostPort(opts.LocalServingHost, fmt.Sprint(opts.LocalServingPort)))
	for _, name := range conversionCRDs {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		Expect(K8sClient.Get(Ctx, client.ObjectKey{Name: name}, crd)).To(Succeed())
		crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
			Strategy: apiextensionsv1.WebhookConverter,
			Webhook: &apiextensionsv1.WebhookConversion{
				ClientConfig: &apiextensionsv1.WebhookClientConfig{
					URL:      &url,
					CABundle: opts.LocalServingCAData,
				},
				ConversionReviewVersions: []string{"v1"},
			},
		}
		Expect(K8sClient.Update(Ctx, crd)).To(Succeed())
	}
}

// waitForConversion blocks until a v1alpha1 write demonstrably traverses the
// conversion webhook: before the stanzas are live the API server answers a
// version-relabeled object instead. A dry-run create keeps the probe free of
// persisted state and admission side effects (no gate is installed yet).
func waitForConversion() {
	Eventually(func() error {
		role := wrappersv1.BuildBasicRole("worker").Obj() // RoleInstanceSet workload + pod template
		probe := &workloadsv1alpha1.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "conversion-probe", Namespace: V1alpha1GateNamespace},
			Spec:       workloadsv1alpha1.RoleBasedGroupSpec{Roles: []workloadsv1alpha1.RoleSpec{role}},
		}
		return K8sClient.Create(Ctx, probe, &client.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	}, 15*time.Second, 250*time.Millisecond).Should(Succeed(), "conversion webhook did not become active")
}

// InstallShippedAdmissionWebhooks installs the shipped
// config/webhook/manifests.yaml (rewritten to the local test server), exactly
// the way the removed v1alpha1 contract suite did: the webhook rules, paths,
// failurePolicy and matchPolicy values under test are the bytes release
// rendering consumes.
func InstallShippedAdmissionWebhooks() {
	data, err := os.ReadFile(gateManifestPath)
	Expect(err).NotTo(HaveOccurred())

	opts := TestEnv.WebhookInstallOptions
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
				rewriteGateClientConfig(&config.Webhooks[i].ClientConfig, hostPort, opts.LocalServingCAData)
			}
			Expect(K8sClient.Create(Ctx, config)).To(Succeed())
			installed++
		case "MutatingWebhookConfiguration":
			config := &admissionregistrationv1.MutatingWebhookConfiguration{}
			Expect(k8sruntime.DefaultUnstructuredConverter.FromUnstructured(raw, config)).To(Succeed())
			for i := range config.Webhooks {
				rewriteMutatingClientConfig(&config.Webhooks[i].ClientConfig, hostPort, opts.LocalServingCAData)
			}
			Expect(K8sClient.Create(Ctx, config)).To(Succeed())
			installed++
		default:
			Fail(fmt.Sprintf("unexpected kind in shipped webhook manifests: %v", raw["kind"]))
		}
	}
	Expect(installed).To(Equal(2), "the shipped manifests must contain exactly two webhook configurations")
}

func rewriteGateClientConfig(cc *admissionregistrationv1.WebhookClientConfig, hostPort string, caData []byte) {
	if cc.Service != nil && cc.Service.Path != nil {
		url := fmt.Sprintf("https://%s%s", hostPort, *cc.Service.Path)
		cc.URL = &url
		cc.Service = nil
	}
	cc.CABundle = caData
}

func rewriteMutatingClientConfig(cc *admissionregistrationv1.WebhookClientConfig, hostPort string, caData []byte) {
	if cc.Service != nil && cc.Service.Path != nil {
		url := fmt.Sprintf("https://%s%s", hostPort, *cc.Service.Path)
		cc.URL = &url
		cc.Service = nil
	}
	cc.CABundle = caData
}

// WaitForV1alpha2Gate blocks until the shipped v1alpha2 validators reject a
// new deprecated workload use submitted through the V1ALPHA1 API — i.e. the
// request matched the Equivalent v1alpha2 rule and was converted. A dry-run
// create traverses admission without persisting anything.
func WaitForV1alpha2Gate() {
	Eventually(func() bool {
		role := wrappersv1.BuildBasicRole("worker").Obj()
		role.Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		}
		rbg := &workloadsv1alpha1.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "gate-readiness-v1a1", Namespace: V1alpha1GateNamespace},
			Spec:       workloadsv1alpha1.RoleBasedGroupSpec{Roles: []workloadsv1alpha1.RoleSpec{role}},
		}
		err := K8sClient.Create(Ctx, rbg, &client.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		return err != nil && strings.Contains(err.Error(), "new uses are not accepted")
	}, 15*time.Second, 250*time.Millisecond).Should(BeTrue(),
		"the shipped v1alpha2 gate did not become active for v1alpha1 writes")
}
