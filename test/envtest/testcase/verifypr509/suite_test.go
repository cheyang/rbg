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

// Package verifypr509 is the PR #509 review harness
// (verify/pr-509-deprecation-window-codex). It stands up envtest with the CRDs
// and the PR's own admission webhooks (v1alpha2 gate + v1alpha1 deprecation
// webhooks, compat mode), plus the conversion webhook, and exercises the
// compatibility-window behavior against a real API server.
//
// Additive review evidence only; no production code is touched.
package verifypr509

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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
)

var (
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	mgr       ctrl.Manager
	ctx       context.Context
	cancel    context.CancelFunc

	webhookURL string
	caData     []byte

	// oldObjProbe captures the raw oldObject of every admission request that
	// reaches /probe-pr509 (registered by probe_test's handler).
	probe = &captureProbe{}
)

func TestVerifyPR509(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PR509 Deprecation Window Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.TODO())

	_, currentFile, _, _ := runtime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "config", "crd", "bases")

	// No WebhookInstallOptions.Paths on purpose: the 0.9.0 validating webhooks
	// must NOT be installed yet, so the suite can first seed the pre-upgrade
	// objects (a legacy RoleBasedGroupSet, a defaulted v1alpha1 RoleBasedGroup)
	// that the compat gate would otherwise refuse to create. The webhook
	// configuration is created dynamically in the specs, pointed at the local
	// server, exactly like the shipped manifests but with envtest's CA.
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}
	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	Expect(workloadsv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(workloadsv1alpha2.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(apiextensionsv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(admissionregistrationv1.AddToScheme(scheme.Scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	host := testEnv.WebhookInstallOptions.LocalServingHost
	port := testEnv.WebhookInstallOptions.LocalServingPort
	certDir := testEnv.WebhookInstallOptions.LocalServingCertDir
	caData = testEnv.WebhookInstallOptions.LocalServingCAData
	webhookURL = fmt.Sprintf("https://%s:%d", host, port)
	Expect(caData).NotTo(BeEmpty())

	mgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    host,
			Port:    port,
			CertDir: certDir,
		}),
	})
	Expect(err).NotTo(HaveOccurred())

	// Register exactly what cmd/rbgs/main.go registers in compat mode
	// (--enable-deprecated-workload-types=true): the v1alpha2 admission
	// webhooks and the new v1alpha1 deprecation webhooks.
	Expect((&workloadsv1alpha2.RoleBasedGroup{}).SetupWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleBasedGroupSet{}).SetupWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleInstanceSet{}).SetupWebhookWithManager(mgr)).To(Succeed())
	Expect((&workloadsv1alpha1.RoleBasedGroup{}).SetupDeprecationWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha1.RoleBasedGroupSet{}).SetupDeprecationWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha1.InstanceSet{}).SetupDeprecationWebhookWithManager(mgr)).To(Succeed())
	Expect((&workloadsv1alpha1.Instance{}).SetupDeprecationWebhookWithManager(mgr)).To(Succeed())
	Expect((&workloadsv1alpha1.RoleBasedGroupScalingAdapter{}).SetupDeprecationWebhookWithManager(mgr)).To(Succeed())
	Expect((&workloadsv1alpha1.ClusterEngineRuntimeProfile{}).SetupDeprecationWebhookWithManager(mgr)).To(Succeed())

	// Probe endpoint: records oldObject of v1alpha1 RoleBasedGroup admission
	// requests so the suite can check whether oldObj carries schema defaults.
	mgr.GetWebhookServer().Register("/probe-pr509", probe)

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()

	// Wait for the webhook listener before any admission request is made.
	Eventually(func() error {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 250*time.Millisecond)
		if err != nil {
			return err
		}
		return conn.Close()
	}, 15*time.Second, 100*time.Millisecond).Should(Succeed())

	// Enable the conversion webhook on the two CRDs that have it in
	// production (config/crd/patches/webhook_in_*.yaml), pointed at the local
	// server so v1alpha1 <-> v1alpha2 conversion really runs.
	for _, crdName := range []string{
		"rolebasedgroups.workloads.x-k8s.io",
		"rolebasedgroupsets.workloads.x-k8s.io",
	} {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: crdName}, crd)).To(Succeed())
		crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
			Strategy: apiextensionsv1.WebhookConverter,
			Webhook: &apiextensionsv1.WebhookConversion{
				ConversionReviewVersions: []string{"v1"},
				ClientConfig: &apiextensionsv1.WebhookClientConfig{
					URL:      ptrTo(webhookURL + "/convert"),
					CABundle: caData,
				},
			},
		}
		Expect(k8sClient.Update(ctx, crd)).To(Succeed())
	}

	// Wait until conversion actually works before running v1alpha1 traffic.
	Eventually(func() error {
		list := &workloadsv1alpha1.RoleBasedGroupList{}
		return k8sClient.List(ctx, list)
	}, 15*time.Second, 250*time.Millisecond).Should(Succeed())
})

var _ = AfterSuite(func() {
	if cancel != nil {
		cancel()
	}
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})

func ptrTo[T any](v T) *T { return &v }

// installShippedValidatingWebhooks creates the ValidatingWebhookConfiguration
// carrying the validating entries from config/webhook/manifests.yaml (the
// 0.9.0 set: three v1alpha2 gates plus six v1alpha1 deprecation guards),
// repointed at the envtest webhook server, plus the fail-open oldObj probe.
func installShippedValidatingWebhooks() {
	svc := func(path string) admissionregistrationv1.WebhookClientConfig {
		return admissionregistrationv1.WebhookClientConfig{
			URL:      ptrTo(webhookURL + path),
			CABundle: caData,
		}
	}
	fail := admissionregistrationv1.Fail
	ignore := admissionregistrationv1.Ignore
	exact := admissionregistrationv1.Exact
	none := admissionregistrationv1.SideEffectClassNone
	mk := func(name, path, version, resource string, match *admissionregistrationv1.MatchPolicyType, ops ...admissionregistrationv1.OperationType) admissionregistrationv1.ValidatingWebhook {
		return admissionregistrationv1.ValidatingWebhook{
			Name:                    name,
			AdmissionReviewVersions: []string{"v1"},
			SideEffects:             &none,
			FailurePolicy:           &fail,
			MatchPolicy:             match,
			ClientConfig:            svc(path),
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: ops,
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{"workloads.x-k8s.io"},
					APIVersions: []string{version},
					Resources:   []string{resource},
				},
			}},
		}
	}
	cud := []admissionregistrationv1.OperationType{admissionregistrationv1.Create, admissionregistrationv1.Update, admissionregistrationv1.Delete}
	cu := []admissionregistrationv1.OperationType{admissionregistrationv1.Create, admissionregistrationv1.Update}

	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-pr509-validating"},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			mk("vrolebasedgroup.kb.io", "/validate-workloads-x-k8s-io-v1alpha2-rolebasedgroup", "v1alpha2", "rolebasedgroups", nil, cu...),
			mk("vrolebasedgroupset.kb.io", "/validate-workloads-x-k8s-io-v1alpha2-rolebasedgroupset", "v1alpha2", "rolebasedgroupsets", nil, cu...),
			mk("vrolebasedgroup-v1alpha1-deprecation.kb.io", "/validate-workloads-x-k8s-io-v1alpha1-rolebasedgroup", "v1alpha1", "rolebasedgroups", &exact, cud...),
			mk("vrolebasedgroupset-v1alpha1-deprecation.kb.io", "/validate-workloads-x-k8s-io-v1alpha1-rolebasedgroupset", "v1alpha1", "rolebasedgroupsets", &exact, cud...),
			mk("vinstanceset-v1alpha1-deprecation.kb.io", "/validate-workloads-x-k8s-io-v1alpha1-instanceset", "v1alpha1", "instancesets", &exact, cud...),
			mk("vinstance-v1alpha1-deprecation.kb.io", "/validate-workloads-x-k8s-io-v1alpha1-instance", "v1alpha1", "instances", &exact, cud...),
			mk("vrolebasedgroupscalingadapter-v1alpha1-deprecation.kb.io", "/validate-workloads-x-k8s-io-v1alpha1-rolebasedgroupscalingadapter", "v1alpha1", "rolebasedgroupscalingadapters", &exact, cud...),
			mk("vclusterengineruntimeprofile-v1alpha1-deprecation.kb.io", "/validate-workloads-x-k8s-io-v1alpha1-clusterengineruntimeprofile", "v1alpha1", "clusterengineruntimeprofiles", &exact, cud...),
			{
				Name:                    "probe-pr509.kb.io",
				AdmissionReviewVersions: []string{"v1"},
				SideEffects:             &none,
				FailurePolicy:           &ignore, // probe must never block
				MatchPolicy:             &exact,
				ClientConfig:            svc("/probe-pr509"),
				Rules: []admissionregistrationv1.RuleWithOperations{{
					Operations: cud,
					Rule: admissionregistrationv1.Rule{
						APIGroups:   []string{"workloads.x-k8s.io"},
						APIVersions: []string{"v1alpha1"},
						Resources:   []string{"rolebasedgroups"},
					},
				}},
			},
		},
	}
	Expect(k8sClient.Create(ctx, vwc)).To(Succeed())

	// Wait until the gate is actually enforced before relying on rejections.
	Eventually(func() bool {
		probe := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "gate-probe", Namespace: "default"},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{legacyAnnotatedRole("probe")},
			},
		}
		err := k8sClient.Create(ctx, probe)
		return err != nil && apiLikeRejection(err)
	}, 15*time.Second, 250*time.Millisecond).Should(BeTrue(), "validating webhook gate never became active")
}

func apiLikeRejection(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "new uses are not accepted") ||
		strings.Contains(err.Error(), "failed calling webhook"))
}
