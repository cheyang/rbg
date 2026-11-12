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

package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

// ChartContractNamespace is the namespace the chart-contract suites submit
// objects into.
const ChartContractNamespace = "deprecation-chart-contract"

// SetupChartContractTestEnv boots envtest and installs the *chart-rendered*
// webhook configurations (deploy/helm/rbgs rendered with `helm template`),
// rewritten to the local test server.
//
// Round 3 (head 69fc06c1, base ea5e82ae): #556 removed the v1alpha1
// admission webhooks from the serving binary, and this PR removed the six
// v1alpha1 deprecation entries from the chart template. The composition under
// test is therefore the production post-#556 one: the manager registers only
// the four v1alpha2 webhooks (validators + defaulters + conversion), and the
// chart-rendered Validating/MutatingWebhookConfigurations route writes to
// them. v1alpha1 writes traverse v1alpha1 -> conversion -> v1alpha2
// validator (matchPolicy: Equivalent) and the CRD deprecationWarning is the
// sole warning layer.
//
// seed, when non-nil, runs after the namespace exists but BEFORE the webhook
// configurations are installed, so tests can persist pre-upgrade state (e.g.
// a role that already carries a deprecated workload type) through the plain
// API server — the same technique the #524 deprecation suite uses.
func SetupChartContractTestEnv(seed func()) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	Ctx, Cancel = context.WithCancel(context.TODO())

	By("bootstrapping the chart contract test environment")

	_, currentFile, _, _ := runtime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "config", "crd", "bases")
	chartPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "deploy", "helm", "rbgs")

	// Register every type the suite serves before starting the environment, so
	// envtest enables CRD conversion for both API versions (same reason as the
	// repo's v1a1gate setup: controller-runtime's Start() PrepWithoutInstalling()
	// patches Webhook conversion onto the convertible GroupKinds).
	err := workloadsv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = workloadsv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = admissionregistrationv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

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
	clientCfg.WarningHandler = ContractWarnings
	K8sClient, err = client.New(clientCfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(K8sClient).NotTo(BeNil())

	By("registering the webhook handlers the serving binary registers (post-#556)")
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

	registerProductionWebhooks(TestMgr)

	go func() {
		defer GinkgoRecover()
		err = TestMgr.Start(Ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	// The webhook server must be serving before the chart-rendered
	// configurations that point at it are installed.
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

	CreateNamespace(ChartContractNamespace)

	if seed != nil {
		By("seeding pre-gate state before the chart webhooks are installed")
		seed()
	}

	By("installing the chart-rendered webhook configurations")
	installChartWebhookConfigurations(chartPath)

	// Readiness barrier: the gate must actively reject a new deprecated use
	// through the real admission chain (v1alpha1 -> conversion -> v1alpha2
	// validator) before specs run. A server-side dry-run create traverses
	// admission without persisting anything.
	waitForChartGate()
}

// registerProductionWebhooks registers exactly the webhook endpoints the
// post-#556 binary serves (mirrors cmd/rbgs): the four v1alpha2 admission
// and defaulting endpoints. The manager serves CRD conversion automatically
// because both API versions are in the scheme. No v1alpha1 deprecation
// endpoints exist anymore — #556 removed them.
func registerProductionWebhooks(mgr ctrl.Manager) {
	Expect((&workloadsv1alpha2.RoleBasedGroup{}).SetupWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleBasedGroupSet{}).SetupWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.CoordinatedPolicy{}).SetupWebhookWithManager(mgr, false)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleInstanceSet{}).SetupWebhookWithManager(mgr)).To(Succeed())
}

// waitForChartGate blocks until the chart-rendered v1alpha2 webhook chain
// actually rejects a new deprecated workload use submitted as v1alpha1
// (through conversion with matchPolicy: Equivalent).
func waitForChartGate() {
	Eventually(func() bool {
		rbg := &workloadsv1alpha1.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "gate-readiness-chart", Namespace: ChartContractNamespace},
			Spec: workloadsv1alpha1.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha1.RoleSpec{{
					Name:     "worker",
					Replicas: func() *int32 { r := int32(1); return &r }(),
					Workload: workloadsv1alpha1.WorkloadSpec{
						APIVersion: "apps/v1",
						Kind:       "StatefulSet",
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
		err := K8sClient.Create(Ctx, rbg, &client.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		return err != nil && strings.Contains(err.Error(), "new uses are not accepted")
	}, 10*time.Second, 250*time.Millisecond).Should(BeTrue(), "the chart-rendered v1alpha2 gate did not become active")
}

// installChartWebhookConfigurations renders the chart with `helm template` and
// creates its webhook configurations against the test server, rewriting the
// chart's service clientConfig (name/namespace + empty caBundle) exactly the
// way the shipped-manifest setup rewrites the kustomize placeholders.
func installChartWebhookConfigurations(chartPath string) {
	manifest := chartWebhookManifests(chartPath)
	Expect(manifest).NotTo(BeEmpty())
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(manifest)), 4096)
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
				vwhookSanity = append(vwhookSanity, sanityWebhookOf(config.Webhooks[i]))
				rewriteClientConfigV1(&config.Webhooks[i].ClientConfig, chartHostPort(), TestEnv.WebhookInstallOptions.LocalServingCAData)
			}
			Expect(K8sClient.Create(Ctx, config)).To(Succeed())
			installed++
		case "MutatingWebhookConfiguration":
			config := &admissionregistrationv1.MutatingWebhookConfiguration{}
			Expect(k8sruntime.DefaultUnstructuredConverter.FromUnstructured(raw, config)).To(Succeed())
			for i := range config.Webhooks {
				vwhookSanity = append(vwhookSanity, sanityWebhookOfMutating(config.Webhooks[i]))
				rewriteClientConfigV1(&config.Webhooks[i].ClientConfig, chartHostPort(), TestEnv.WebhookInstallOptions.LocalServingCAData)
			}
			Expect(K8sClient.Create(Ctx, config)).To(Succeed())
			installed++
		default:
			// The rendered chart carries every template; only the webhook
			// configurations are interesting here.
			continue
		}
	}
	Expect(installed).To(Equal(2), "the rendered chart must contain exactly two webhook configurations")

	// Round-3 contract: the chart must render NO v1alpha1 deprecation
	// webhook entries — #556 removed the handlers, so a rendered entry would
	// point the API server at an endpoint nothing serves.
	for _, wh := range vwhookSanity {
		assertNoV1alpha1DeprecationWebhook(wh.name, wh.policy, wh.path)
	}
}

type sanityWebhook struct {
	name   string
	policy string
	path   string
}

var vwhookSanity []sanityWebhook

func assertNoV1alpha1DeprecationWebhook(name, policy, path string) {
	if strings.Contains(name, "v1alpha1") && strings.Contains(name, "deprecation") {
		Fail(fmt.Sprintf("chart renders v1alpha1 deprecation webhook entry %q (failurePolicy %s)", name, policy))
	}
	if strings.Contains(path, "v1alpha1") && strings.Contains(path, "deprecation") {
		Fail(fmt.Sprintf("chart webhook %q routes v1alpha1 deprecation path %q", name, path))
	}
}

func sanityWebhookOf(wh admissionregistrationv1.ValidatingWebhook) sanityWebhook {
	return sanityWebhook{name: wh.Name, policy: string(*wh.FailurePolicy), path: servicePathOf(wh.ClientConfig)}
}

func sanityWebhookOfMutating(wh admissionregistrationv1.MutatingWebhook) sanityWebhook {
	return sanityWebhook{name: wh.Name, policy: string(*wh.FailurePolicy), path: servicePathOf(wh.ClientConfig)}
}

func servicePathOf(cc admissionregistrationv1.WebhookClientConfig) string {
	if cc.Service != nil && cc.Service.Path != nil {
		return *cc.Service.Path
	}
	return ""
}

func chartHostPort() string {
	opts := TestEnv.WebhookInstallOptions
	return net.JoinHostPort(opts.LocalServingHost, fmt.Sprint(opts.LocalServingPort))
}

// chartWebhookManifests shells out to `helm template`. KUBECONFIG is pointed at
// a missing path so helm's kubeconfig-permission warnings (stderr) cannot
// pollute the rendered manifest.
func chartWebhookManifests(chartPath string) string {
	cmd := exec.Command("helm", "template", "rbgs", chartPath, "--namespace", ChartContractNamespace)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+filepath.Join(os.TempDir(), "rbgs-chart-contract-missing-kubeconfig"))
	out, err := cmd.Output()
	Expect(err).NotTo(HaveOccurred(), "helm template failed: %s\n%s", err, out)
	return string(out)
}

// ContractWarningRecorder collects Warning headers returned by the API server
// (both CRD deprecation warnings and admission warnings travel the same way).
type ContractWarningRecorder struct {
	mu       sync.Mutex
	warnings []string
}

// Reset drops all recorded warnings. Call it at the start of every spec.
func (r *ContractWarningRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = nil
}

// HandleWarningHeader implements rest.WarningHandler.
func (r *ContractWarningRecorder) HandleWarningHeader(code int, agent string, text string) {
	if code < 200 || code > 299 || text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, text)
}

// Containing returns every recorded warning containing substr.
func (r *ContractWarningRecorder) Containing(substr string) []string {
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
func (r *ContractWarningRecorder) All() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.warnings...)
}

// ContractWarnings is shared by the chart contract suite's client.
var ContractWarnings = &ContractWarningRecorder{}

// rewriteClientConfigV1 rewrites a webhook clientConfig to point at the local
// test server (same rewrite envtest applies to its own webhook manifests).
func rewriteClientConfigV1(cc *admissionregistrationv1.WebhookClientConfig, hostPort string, caData []byte) {
	if cc.Service != nil && cc.Service.Path != nil {
		url := fmt.Sprintf("https://%s%s", hostPort, *cc.Service.Path)
		cc.URL = &url
		cc.Service = nil
	}
	cc.CABundle = caData
}
