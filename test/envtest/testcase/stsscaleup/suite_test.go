/*
Verification harness for PR #553 (issue #547) — Reviewer B (Codex).
Additive only; does not modify production code.

This suite is separate from testutil.SetupTestEnv because the shared helper
registers the RBG controller with enableDeprecatedWorkloadTypes=false, which
means it never Watches owned StatefulSets. Production defaults that flag to
true (cmd/rbgs/main.go --enable-deprecated-workload-types, default true), and
the StatefulSet watch is what enqueues the reconcile after an external
`kubectl scale`. This suite reproduces that production wiring.
*/

package stsscaleup

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	workloadscontroller "sigs.k8s.io/rbgs/internal/controller/workloads"
	"sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/sync"
	"sigs.k8s.io/rbgs/pkg/scheduler"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestPR553StatefulSetScaleUp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PR553 StatefulSet Scale-Up Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	_, currentFile, _, _ := runtime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "config", "crd", "bases")

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}
	var err error
	cfg, err = env.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	testEnv = env

	Expect(workloadsv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(workloadsv1alpha2.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(apiextensionsv1.AddToScheme(scheme.Scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())

	// enableDeprecatedWorkloadTypes=true: production default; registers the
	// Owns(&appsv1.StatefulSet{}) watch that fires the reconcile when the live
	// sts is scaled externally.
	rbgReconciler, err := workloadscontroller.NewRoleBasedGroupReconciler(
		mgr, scheduler.KubeSchedulerPlugin, "", sync.NewNodeBindingStore())
	Expect(err).NotTo(HaveOccurred())
	Expect(rbgReconciler.SetupWithManager(mgr, controller.Options{}, true)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()

	// wait for the manager cache to sync
	time.Sleep(2 * time.Second)
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

// keep schema import used (GVK debugging helper for future rounds)
var _ = schema.GroupVersionKind{}
