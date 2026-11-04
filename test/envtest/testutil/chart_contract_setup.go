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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/ptr"
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
// rewritten to the local test server — instead of the shipped
// config/webhook/manifests.yaml the v1alpha1 contract suite uses.
//
// legacyBinary selects which handler set the manager registers:
//   - false: the full production set (what a post-#524 image serves);
//   - true: only the pre-#524 set — the v1alpha2 admission/mutating endpoints
//     and conversion, WITHOUT the six v1alpha1 deprecation endpoints. This is
//     the set the chart's pinned default image v0.9.0-d88b3186 serves: that
//     image predates #524, so a chart install with default values points the
//     API server at six endpoints the running binary does not register.
//
// seed, when non-nil, runs after the namespace exists but BEFORE the webhook
// configurations are installed, so tests can persist pre-upgrade state (e.g. a
// role that already carries a deprecated workload type) through the plain API
// server — the same technique the #524 deprecation suite uses.
//
// The specs in test/envtest/testcase/deprecationchartlegacy then observe what
// the API server does to v1alpha1 writes in that state.
func SetupChartContractTestEnv(legacyBinary bool, seed func()) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	Ctx, Cancel = context.WithCancel(context.TODO())

	By("bootstrapping the chart contract test environment")

	_, currentFile, _, _ := runtime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "config", "crd", "bases")
	chartPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "deploy", "helm", "rbgs")

	// Register every type the suite serves before starting the environment, so
	// envtest enables CRD conversion for both API versions (same reason as the
	// v1alpha1 contract setup).
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

	By("registering the webhook handlers the serving binary would register")
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

	if legacyBinary {
		registerPreGateWebhooks(TestMgr)
	} else {
		registerProductionWebhooks(TestMgr)
	}

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

	if !legacyBinary {
		// Same readiness barrier as the shipped-manifest suite, probing this
		// suite's namespace: the v1alpha1 gate must actively reject a new
		// deprecated use through the real admission chain before specs run.
		waitForChartGate()
	}
}

// waitForChartGate blocks until the chart-rendered v1alpha1 webhook actually
// rejects a new deprecated workload use through the real admission chain. A
// server-side dry-run create traverses admission without persisting anything.
func waitForChartGate() {
	Eventually(func() bool {
		rbg := &workloadsv1alpha1.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "gate-readiness-chart", Namespace: ChartContractNamespace},
			Spec: workloadsv1alpha1.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha1.RoleSpec{{
					Name:     "worker",
					Replicas: ptr.To(int32(1)),
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
	}, 10*time.Second, 250*time.Millisecond).Should(BeTrue(), "the chart-rendered v1alpha1 gate did not become active")
}

// registerPreGateWebhooks registers only the webhook endpoints a binary built
// before #524 serves: the v1alpha2 admission/defaulting endpoints (and CRD
// conversion, which the manager serves automatically). The six v1alpha1
// deprecation endpoints are deliberately NOT registered.
func registerPreGateWebhooks(mgr ctrl.Manager) {
	Expect((&workloadsv1alpha2.RoleBasedGroup{}).SetupWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleBasedGroupSet{}).SetupWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.CoordinatedPolicy{}).SetupWebhookWithManager(mgr, false)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleInstanceSet{}).SetupWebhookWithManager(mgr)).To(Succeed())
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
				rewriteClientConfigV1(&config.Webhooks[i].ClientConfig, chartHostPort(), TestEnv.WebhookInstallOptions.LocalServingCAData)
			}
			Expect(K8sClient.Create(Ctx, config)).To(Succeed())
			installed++
		case "MutatingWebhookConfiguration":
			config := &admissionregistrationv1.MutatingWebhookConfiguration{}
			Expect(k8sruntime.DefaultUnstructuredConverter.FromUnstructured(raw, config)).To(Succeed())
			for i := range config.Webhooks {
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
