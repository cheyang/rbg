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

// Package deprecation is the PR #509 verification harness (integration layer,
// envtest). It reproduces the exact admission topology the release installs:
// the v1alpha2 validating/mutating webhooks plus the new v1alpha1 deprecation
// webhooks, and the RoleBasedGroupSet controller that issues child
// RoleBasedGroup creates.
//
// "Legacy" objects (a role annotated with a deprecated workload type) cannot be
// created through admission — the very gate under test rejects them — so the
// harness seeds them by temporarily pointing every webhook's rules at a resource
// that never matches, creating the pre-0.9.0 state, and then restoring the
// production rules. That is the envtest stand-in for "this object already
// existed before the upgrade".
package deprecation

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	workloadscontroller "sigs.k8s.io/rbgs/internal/controller/workloads"
)

var (
	testEnv   *envtest.Environment
	testCtx   context.Context
	cancelCtx context.CancelFunc
	testMgr   ctrl.Manager
	k8sClient client.Client
)

func TestDeprecationCompatGate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "v1alpha1 Deprecation Compat Gate Suite")
}

// setupV1alpha1DeprecationWebhooks mirrors cmd/rbgs.setupV1alpha1DeprecationWebhooks.
func setupV1alpha1DeprecationWebhooks(mgr ctrl.Manager, enableDeprecatedWorkloadTypes bool) error {
	if err := (&workloadsv1alpha1.RoleBasedGroup{}).SetupDeprecationWebhookWithManager(mgr, enableDeprecatedWorkloadTypes); err != nil {
		return err
	}
	if err := (&workloadsv1alpha1.RoleBasedGroupSet{}).SetupDeprecationWebhookWithManager(mgr, enableDeprecatedWorkloadTypes); err != nil {
		return err
	}
	if err := (&workloadsv1alpha1.InstanceSet{}).SetupDeprecationWebhookWithManager(mgr); err != nil {
		return err
	}
	if err := (&workloadsv1alpha1.Instance{}).SetupDeprecationWebhookWithManager(mgr); err != nil {
		return err
	}
	if err := (&workloadsv1alpha1.RoleBasedGroupScalingAdapter{}).SetupDeprecationWebhookWithManager(mgr); err != nil {
		return err
	}
	if err := (&workloadsv1alpha1.ClusterEngineRuntimeProfile{}).SetupDeprecationWebhookWithManager(mgr); err != nil {
		return err
	}
	return nil
}

var _ = BeforeSuite(func() {
	// Logs go to GinkgoWriter (shown on failure) and stdout so a passing run
	// still captures the reconcile evidence in `go test -v` output.
	logf.SetLogger(zap.New(zap.WriteTo(io.MultiWriter(GinkgoWriter, os.Stdout)), zap.UseDevMode(true)))

	testCtx, cancelCtx = context.WithCancel(context.TODO())

	By("bootstrapping the webhook-enabled envtest environment")
	_, currentFile, _, _ := runtime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "config", "crd", "bases")
	webhookPath := filepath.Join(filepath.Dir(currentFile), "webhooks")

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{webhookPath},
		},
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	Expect(workloadsv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(workloadsv1alpha2.AddToScheme(scheme.Scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	By("setting up the manager with the production webhook set and the RBGSet controller")
	testMgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    testEnv.WebhookInstallOptions.LocalServingHost,
			Port:    testEnv.WebhookInstallOptions.LocalServingPort,
			CertDir: testEnv.WebhookInstallOptions.LocalServingCertDir,
		}),
	})
	Expect(err).NotTo(HaveOccurred())

	// The production admission set (cmd/rbgs/main.go), compat mode
	// (--enable-deprecated-workload-types=true, the 0.9.0 default).
	Expect((&workloadsv1alpha2.RoleBasedGroup{}).SetupWebhookWithManager(testMgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleBasedGroupSet{}).SetupWebhookWithManager(testMgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleInstanceSet{}).SetupWebhookWithManager(testMgr)).To(Succeed())
	Expect(setupV1alpha1DeprecationWebhooks(testMgr, true)).To(Succeed())

	// The controller whose scaleUp path issues child RoleBasedGroup creates.
	Expect(workloadscontroller.NewRoleBasedGroupSetReconciler(testMgr).
		SetupWithManager(testMgr, controller.Options{})).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(testMgr.Start(testCtx)).To(Succeed())
	}()

	By("waiting for the webhook server to serve and the cache to sync")
	Eventually(func() error {
		conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf(
			"%s:%d",
			testEnv.WebhookInstallOptions.LocalServingHost,
			testEnv.WebhookInstallOptions.LocalServingPort,
		), 250*time.Millisecond)
		if dialErr != nil {
			return dialErr
		}
		return conn.Close()
	}, 10*time.Second, 100*time.Millisecond).Should(Succeed())

	Eventually(func() bool {
		return testMgr.GetCache().WaitForCacheSync(testCtx)
	}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())
})

var _ = AfterSuite(func() {
	cancelCtx()
	By("tearing down the envtest environment")
	Expect(testEnv.Stop()).To(Succeed())
})

// noopRule matches a resource nothing ever writes, so a webhook carrying it is
// never invoked.
func noopRule() []admissionregistrationv1.RuleWithOperations {
	return []admissionregistrationv1.RuleWithOperations{{
		Operations: []admissionregistrationv1.OperationType{"CREATE", "UPDATE", "DELETE"},
		Rule: admissionregistrationv1.Rule{
			APIGroups:   []string{"workloads.x-k8s.io"},
			APIVersions: []string{"*"},
			Resources:   []string{"pr509-noop-resources"},
		},
	}}
}

// relaxAdmission points every validating webhook's rules at a never-matching
// resource so objects can be seeded as if they pre-dated the gate. It returns
// the function that restores the production rules.
func relaxAdmission() func() {
	const name = "validating-webhook-configuration"
	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	Expect(k8sClient.Get(testCtx, client.ObjectKey{Name: name}, vwc)).To(Succeed())

	original := make([][]admissionregistrationv1.RuleWithOperations, len(vwc.Webhooks))
	for i := range vwc.Webhooks {
		original[i] = vwc.Webhooks[i].Rules
		vwc.Webhooks[i].Rules = noopRule()
	}
	Expect(k8sClient.Update(testCtx, vwc)).To(Succeed())

	return func() {
		restored := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		Expect(k8sClient.Get(testCtx, client.ObjectKey{Name: name}, restored)).To(Succeed())
		for i := range restored.Webhooks {
			restored.Webhooks[i].Rules = original[i]
		}
		Expect(k8sClient.Update(testCtx, restored)).To(Succeed())
	}
}
