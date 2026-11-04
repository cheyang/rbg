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

package deprecationv1a1helm

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
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
)

const (
	// helmContractNamespace is where the suite submits its probe objects.
	helmContractNamespace = "deprecation-v1a1-helm-contract"
	// seededRBGName is the pre-gate object used to prove type-exact
	// grandfathering through the chart-rendered gate.
	seededRBGName = "legacy-seed"
)

func suiteMode() string {
	if m := os.Getenv("RBGS_HELM_SUITE_MODE"); m != "" {
		return m
	}
	return "contract"
}

func chartDir() string {
	if d := os.Getenv("RBGS_CHART_DIR"); d != "" {
		return d
	}
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "deploy", "helm", "rbgs")
}

// setupHelmChartContractEnv boots envtest, registers the production webhook
// handlers exactly as cmd/rbgs does, seeds a pre-gate v1alpha1 RoleBasedGroup
// that uses a deprecated workload type, then renders the Helm chart and installs
// the chart's webhook configurations against the local test server.
func setupHelmChartContractEnv() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	testutil.Ctx, testutil.Cancel = context.WithCancel(context.TODO())

	By(fmt.Sprintf("bootstrapping the helm chart contract environment (mode=%s, chart=%s)", suiteMode(), chartDir()))

	_, currentFile, _, _ := runtime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "config", "crd", "bases")

	Expect(workloadsv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(workloadsv1alpha2.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(admissionregistrationv1.AddToScheme(scheme.Scheme)).To(Succeed())

	testutil.TestEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	testutil.Cfg, err = testutil.TestEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(testutil.Cfg).NotTo(BeNil())

	clientCfg := rest.CopyConfig(testutil.Cfg)
	clientCfg.WarningHandler = testutil.ContractWarnings
	testutil.K8sClient, err = client.New(clientCfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	By("registering the production webhook handlers (compat mode, as cmd/rbgs does)")
	testutil.TestMgr, err = ctrl.NewManager(testutil.Cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    testutil.TestEnv.WebhookInstallOptions.LocalServingHost,
			Port:    testutil.TestEnv.WebhookInstallOptions.LocalServingPort,
			CertDir: testutil.TestEnv.WebhookInstallOptions.LocalServingCertDir,
		}),
	})
	Expect(err).NotTo(HaveOccurred())

	// Registration order and flag values mirror cmd/rbgs with the 0.9.0 default
	// --enable-deprecated-workload-types=true.
	Expect((&workloadsv1alpha2.RoleBasedGroup{}).SetupWebhookWithManager(testutil.TestMgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleBasedGroupSet{}).SetupWebhookWithManager(testutil.TestMgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.CoordinatedPolicy{}).SetupWebhookWithManager(testutil.TestMgr, false)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleInstanceSet{}).SetupWebhookWithManager(testutil.TestMgr)).To(Succeed())

	Expect((&workloadsv1alpha1.RoleBasedGroup{}).SetupDeprecationWebhookWithManager(testutil.TestMgr, true)).To(Succeed())
	Expect((&workloadsv1alpha1.RoleBasedGroupSet{}).SetupDeprecationWebhookWithManager(testutil.TestMgr, true)).To(Succeed())
	Expect((&workloadsv1alpha1.InstanceSet{}).SetupDeprecationWebhookWithManager(testutil.TestMgr)).To(Succeed())
	Expect((&workloadsv1alpha1.Instance{}).SetupDeprecationWebhookWithManager(testutil.TestMgr)).To(Succeed())
	Expect((&workloadsv1alpha1.RoleBasedGroupScalingAdapter{}).SetupDeprecationWebhookWithManager(testutil.TestMgr)).To(Succeed())
	Expect((&workloadsv1alpha1.ClusterEngineRuntimeProfile{}).SetupDeprecationWebhookWithManager(testutil.TestMgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(testutil.TestMgr.Start(testutil.Ctx)).To(Succeed())
	}()

	By("waiting for the webhook server to listen")
	Eventually(func() error {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d",
			testutil.TestEnv.WebhookInstallOptions.LocalServingHost,
			testutil.TestEnv.WebhookInstallOptions.LocalServingPort), 250*time.Millisecond)
		if err != nil {
			return err
		}
		return conn.Close()
	}, 10*time.Second, 100*time.Millisecond).Should(Succeed())

	testutil.CreateNamespace(helmContractNamespace)

	By("seeding a pre-gate v1alpha1 RoleBasedGroup with a deprecated workload type")
	seed := buildV1alpha1RBG(seededRBGName)
	seed.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"}
	Expect(testutil.K8sClient.Create(testutil.Ctx, seed)).To(Succeed(),
		"the pre-gate seed must be admitted before any webhook configuration is installed")

	By("rendering the Helm chart and installing its webhook configurations")
	installChartWebhookConfigurations()

	if suiteMode() == "premise" {
		verifyGateAbsent()
		return
	}
	waitForHelmGate()
}

func teardownEnv() {
	if testutil.Cancel != nil {
		testutil.Cancel()
	}
	if testutil.TestEnv != nil {
		_ = testutil.TestEnv.Stop()
	}
}

// renderChartWebhookManifests runs `helm template` and returns the rendered
// stream. Only stdout is consumed: helm writes kubeconfig permission warnings to
// stderr, and merging the streams corrupts the YAML doc stream.
func renderChartWebhookManifests() string {
	helmBin, err := exec.LookPath("helm")
	Expect(err).NotTo(HaveOccurred(), "helm is required to render the chart under test")
	cmd := exec.Command(helmBin, "template", "rbgs", chartDir(), "--namespace", helmContractNamespace)
	out, err := cmd.Output()
	Expect(err).NotTo(HaveOccurred(), "helm template must render the chart")
	return string(out)
}

// installChartWebhookConfigurations decodes the rendered chart and creates its
// webhook configurations against the test server, rewriting clientConfig
// (service name/namespace + empty caBundle) the way envtest rewrites its own
// webhook manifests — the equivalent of the controller's runtime caBundle patch.
func installChartWebhookConfigurations() {
	data := renderChartWebhookManifests()

	opts := testutil.TestEnv.WebhookInstallOptions
	hostPort := net.JoinHostPort(opts.LocalServingHost, fmt.Sprint(opts.LocalServingPort))

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(data)), 4096)
	installed := map[string]int{}
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
		kind, _ := raw["kind"].(string)
		switch kind {
		case "ValidatingWebhookConfiguration":
			config := &admissionregistrationv1.ValidatingWebhookConfiguration{}
			Expect(k8sruntime.DefaultUnstructuredConverter.FromUnstructured(raw, config)).To(Succeed())
			for i := range config.Webhooks {
				rewriteClientConfig(&config.Webhooks[i].ClientConfig, hostPort, opts.LocalServingCAData)
			}
			Expect(testutil.K8sClient.Create(testutil.Ctx, config)).To(Succeed())
			installed[kind]++
		case "MutatingWebhookConfiguration":
			config := &admissionregistrationv1.MutatingWebhookConfiguration{}
			Expect(k8sruntime.DefaultUnstructuredConverter.FromUnstructured(raw, config)).To(Succeed())
			for i := range config.Webhooks {
				rewriteClientConfig(&config.Webhooks[i].ClientConfig, hostPort, opts.LocalServingCAData)
			}
			Expect(testutil.K8sClient.Create(testutil.Ctx, config)).To(Succeed())
			installed[kind]++
		}
	}
	Expect(installed["ValidatingWebhookConfiguration"]).To(Equal(1),
		"the rendered chart must contain exactly one ValidatingWebhookConfiguration")
	Expect(installed["MutatingWebhookConfiguration"]).To(Equal(1),
		"the rendered chart must contain exactly one MutatingWebhookConfiguration")
}

func rewriteClientConfig(cc *admissionregistrationv1.WebhookClientConfig, hostPort string, caData []byte) {
	if cc.Service != nil && cc.Service.Path != nil {
		url := fmt.Sprintf("https://%s%s", hostPort, *cc.Service.Path)
		cc.URL = &url
		cc.Service = nil
	}
	cc.CABundle = caData
}

// gateProbe submits a server-side dry-run create of a new v1alpha1 RoleBasedGroup
// with an explicit deprecated workload type and reports whether the admission
// chain rejected it as a new deprecated use.
func gateProbe() (rejected bool, err error) {
	probe := buildV1alpha1RBG("gate-probe")
	probe.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"}
	err = testutil.K8sClient.Create(testutil.Ctx, probe, &client.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	if err != nil && strings.Contains(err.Error(), "new uses are not accepted") {
		return true, err
	}
	return false, err
}

// warningProbe submits a dry-run v1alpha1 InstanceSet create and reports whether
// the on-the-wire v1alpha1 deprecation warning came back. The InstanceSet path
// is the cleanest probe for the chart's v1alpha1 entries: unlike RBG/RBGSet, no
// Equivalent-matching v1alpha2 validating webhook covers it, so the warning can
// only come from the chart's own v1alpha1 webhook entry.
func warningProbe() bool {
	testutil.ContractWarnings.Reset()
	probe := &workloadsv1alpha1.InstanceSet{
		ObjectMeta: metav1.ObjectMeta{Name: "gate-probe-warn", Namespace: helmContractNamespace},
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
	}
	err := testutil.K8sClient.Create(testutil.Ctx, probe, &client.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	if err != nil {
		return false
	}
	return len(testutil.ContractWarnings.Containing("v1alpha1 is deprecated")) > 0
}

// waitForHelmGate blocks until the chart-rendered gate rejects a new deprecated
// workload use through the real admission chain.
func waitForHelmGate() {
	Eventually(func() bool {
		rejected, _ := gateProbe()
		return rejected
	}, 10*time.Second, 250*time.Millisecond).Should(BeTrue(),
		"the chart-rendered v1alpha1 gate did not become active")
}

// verifyGateAbsent is positive evidence for the premise: with the BASE chart's
// webhook configuration installed, a v1alpha1 write traverses the whole
// admission chain and returns NO deprecation warning — the chart shipped no
// v1alpha1 webhook entries. (RBG/RBGSet new-use enforcement already exists on
// the base chart through the Equivalent-matching v1alpha2 validators; the
// warning plane is what is missing.)
func verifyGateAbsent() {
	Consistently(warningProbe, 3*time.Second, 250*time.Millisecond).Should(BeFalse(),
		"premise mode: the base chart must not produce the v1alpha1 deprecation warning")
}

func buildV1alpha1RBG(name string) *workloadsv1alpha1.RoleBasedGroup {
	return wrappersv1.BuildBasicRoleBasedGroup(name, helmContractNamespace).
		WithRoles([]workloadsv1alpha1.RoleSpec{
			wrappersv1.BuildBasicRole("worker").Obj(),
		}).Obj()
}
