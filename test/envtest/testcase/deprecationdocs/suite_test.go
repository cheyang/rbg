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

// Package deprecationdocs is the PR #552 round-3 (reviewer: codex) integration
// harness. The PR is now documentation-only, so what needs proving is that the
// docs' behavioral claims are TRUE of the merged state (base ea5e82ae, which
// includes #556's removal of the v1alpha1 admission webhooks):
//
//   - compat mode: new deprecated uses rejected (explicit, schema-defaulted,
//     and type-switches), existing same-type uses stay writable, RBGSet
//     legacy-template children are exempt;
//   - strict mode: every remaining deprecated use rejected, RoleInstanceSet
//     accepted;
//   - every v1alpha1 request returns the CRD deprecation warning naming the
//     0.10.0 removal, and v1alpha2 requests are not warned;
//   - the chart README's inventory command, executed verbatim, lists exactly
//     the deprecated roles.
//
// Modes (RBGS_DOCS_SUITE_MODE): "compat" (default), "strict", "nowebhook"
// (control: no ValidatingWebhookConfiguration installed; the schema-defaulted
// create must be ADMITTED, proving the rejections above come from the gate).
package deprecationdocs

import (
	"context"
	"fmt"
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
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
)

const docsNamespace = "deprecation-docs"

const (
	seededRBGName    = "legacy-seed"
	seededRBGSetName = "legacy-seed-set"
	seededModernName = "modern-seed"
	stsWorkload      = "apps/v1/StatefulSet"
	deployWorkload   = "apps/v1/Deployment"
	risWorkload      = "workloads.x-k8s.io/v1alpha2/RoleInstanceSet"
	workloadAnn      = "rbg.workloads.x-k8s.io/role-workload-type"
	v1a2RBGPath      = "/validate-workloads-x-k8s-io-v1alpha2-rolebasedgroup"
	v1a2RBGSetPath   = "/validate-workloads-x-k8s-io-v1alpha2-rolebasedgroupset"
)

var (
	ctx        context.Context
	cancel     context.CancelFunc
	testEnv    *envtest.Environment
	cfg        *rest.Config
	k8sClient  client.Client // warning-recording client
	warnings   *warningRecorder
	strictMode bool
	noWebhook  bool
)

func suiteMode() string { return os.Getenv("RBGS_DOCS_SUITE_MODE") }

type warningRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *warningRecorder) Reset() { r.mu.Lock(); r.seen = nil; r.mu.Unlock() }

func (r *warningRecorder) HandleWarningHeader(code int, _ string, text string) {
	if code < 200 || code > 299 || text == "" {
		return
	}
	r.mu.Lock()
	r.seen = append(r.seen, text)
	r.mu.Unlock()
}

func (r *warningRecorder) All() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

func (r *warningRecorder) containSubstring(sub string) bool {
	for _, w := range r.All() {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestDeprecationDocs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Deprecation docs contract suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.TODO())
	strictMode = suiteMode() == "strict"
	noWebhook = suiteMode() == "nowebhook"

	By(fmt.Sprintf("bootstrapping envtest with conversion webhooks (mode=%s)", func() string {
		if suiteMode() == "" {
			return "compat"
		}
		return suiteMode()
	}()))

	_, thisFile, _, _ := runtime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "config", "crd", "bases")

	Expect(workloadsv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(workloadsv1alpha2.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(admissionregistrationv1.AddToScheme(scheme.Scheme)).To(Succeed())

	testEnv = &envtest.Environment{CRDDirectoryPaths: []string{crdPath}, ErrorIfCRDPathMissing: true}
	// Enable the conversion-webhook wiring on the CRDs before Start installs them.
	Expect(testEnv.WebhookInstallOptions.PrepWithoutInstalling()).To(Succeed())

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	warnings = &warningRecorder{}
	clientCfg := rest.CopyConfig(cfg)
	clientCfg.WarningHandler = warnings
	k8sClient, err = client.New(clientCfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	By("registering the production v1alpha2 webhooks as cmd/rbgs does (flag=" + fmt.Sprint(!strictMode) + ")")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    testEnv.WebhookInstallOptions.LocalServingHost,
			Port:    testEnv.WebhookInstallOptions.LocalServingPort,
			CertDir: testEnv.WebhookInstallOptions.LocalServingCertDir,
		}),
	})
	Expect(err).NotTo(HaveOccurred())
	Expect((&workloadsv1alpha2.RoleBasedGroup{}).SetupWebhookWithManager(mgr, !strictMode)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleBasedGroupSet{}).SetupWebhookWithManager(mgr, !strictMode)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleInstanceSet{}).SetupWebhookWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()

	By("waiting for the webhook server to listen")
	Eventually(func() error {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d",
			testEnv.WebhookInstallOptions.LocalServingHost,
			testEnv.WebhookInstallOptions.LocalServingPort), 250*time.Millisecond)
		if err != nil {
			return err
		}
		return conn.Close()
	}, 10*time.Second, 100*time.Millisecond).Should(Succeed())

	Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: docsNamespace}})).To(Succeed())

	By("seeding pre-gate objects (deprecated RBG, deprecated RBGSet, modern control)")
	seedRBG := wrappersv1.BuildBasicRoleBasedGroup(seededRBGName, docsNamespace).Obj()
	seedRBG.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"}
	Expect(k8sClient.Create(ctx, seedRBG)).To(Succeed())

	seedSet := wrappersv1.BuildBasicRoleBasedGroupSet(seededRBGSetName, docsNamespace).Obj()
	seedSet.Spec.Template.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"}
	Expect(k8sClient.Create(ctx, seedSet)).To(Succeed())

	seedModern := wrappersv1.BuildBasicRoleBasedGroup(seededModernName, docsNamespace).Obj()
	seedModern.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{APIVersion: "workloads.x-k8s.io/v1alpha2", Kind: "RoleInstanceSet"}
	Expect(k8sClient.Create(ctx, seedModern)).To(Succeed())

	if noWebhook {
		By("control mode: no ValidatingWebhookConfiguration installed")
		return
	}

	By("installing the v1alpha2 validating webhook configuration (Equivalent matchPolicy, as shipped)")
	installGateWebhooks()

	By("waiting for the gate to become active")
	Eventually(func() bool {
		probe := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "gate-readiness-probe", Namespace: docsNamespace},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{{
				Name:        "worker",
				Annotations: map[string]string{workloadAnn: stsWorkload},
			}}},
		}
		err := k8sClient.Create(ctx, probe, &client.CreateOptions{DryRun: []string{"All"}})
		return err != nil && strings.Contains(err.Error(), "deprecated")
	}, 10*time.Second, 250*time.Millisecond).Should(BeTrue(), "the v1alpha2 gate did not become active")
})

var _ = AfterSuite(func() {
	if cancel != nil {
		cancel()
	}
	if testEnv != nil {
		_ = testEnv.Stop()
	}
})

func installGateWebhooks() {
	opts := testEnv.WebhookInstallOptions
	fail := admissionregistrationv1.Fail
	none := admissionregistrationv1.SideEffectClassNone
	mk := func(path, resource string) admissionregistrationv1.ValidatingWebhook {
		url := fmt.Sprintf("https://%s%s", net.JoinHostPort(opts.LocalServingHost, fmt.Sprintf("%d", opts.LocalServingPort)), path)
		return admissionregistrationv1.ValidatingWebhook{
			Name:                    "docs-" + resource + ".kb.io",
			AdmissionReviewVersions: []string{"v1"},
			SideEffects:             &none,
			FailurePolicy:           &fail,
			ClientConfig:            admissionregistrationv1.WebhookClientConfig{URL: &url, CABundle: opts.LocalServingCAData},
			// matchPolicy left unset: Equivalent is the API default, so v1alpha1
			// writes are routed through conversion to this v1alpha2 gate — the
			// production composition after #556.
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{"CREATE", "UPDATE"},
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{"workloads.x-k8s.io"},
					APIVersions: []string{"v1alpha2"},
					Resources:   []string{resource},
				},
			}},
		}
	}
	config := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "docs-gate-validating"},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			mk(v1a2RBGPath, "rolebasedgroups"),
			mk(v1a2RBGSetPath, "rolebasedgroupsets"),
		},
	}
	Expect(k8sClient.Create(ctx, config)).To(Succeed())
}
