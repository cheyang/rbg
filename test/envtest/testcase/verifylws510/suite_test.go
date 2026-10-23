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

// Package verifylws510 is the PR #510 verification harness (finding F1)
// integration layer. It boots a dedicated envtest control plane that also
// installs the LeaderWorkerSet CRD (resolved from the Go module cache), runs
// the real RoleBasedGroup controller against it, and simulates the real LWS
// v0.7.0 controller's status writes.
package verifylws510

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	lwsv1 "sigs.k8s.io/lws/api/leaderworkerset/v1"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	workloadscontroller "sigs.k8s.io/rbgs/internal/controller/workloads"
	instancesync "sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/sync"
	"sigs.k8s.io/rbgs/pkg/scheduler"
	"sigs.k8s.io/rbgs/pkg/utils/fieldindex"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestVerifyLWSPR510(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PR510 LWS status verification suite")
}

// lwsCRDDir resolves the directory containing the LeaderWorkerSet CRD from the
// Go module cache. "go mod download -json" reports the extracted module dir and
// works even when the main module builds in vendor mode (where "go list -m -f
// {{.Dir}}" returns empty).
func lwsCRDDir() string {
	out, err := exec.Command("go", "mod", "download", "-json", "sigs.k8s.io/lws").Output()
	Expect(err).NotTo(HaveOccurred())
	var info struct {
		Dir string
	}
	Expect(json.Unmarshal(out, &info)).To(Succeed())
	Expect(info.Dir).NotTo(BeEmpty())
	return filepath.Join(info.Dir, "config", "crd", "bases")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.TODO())

	repoRootOut, repoErr := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "sigs.k8s.io/rbgs").Output()
	Expect(repoErr).NotTo(HaveOccurred())
	repoCRDPath := filepath.Join(strings.TrimSpace(string(repoRootOut)), "config", "crd", "bases")

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{repoCRDPath, lwsCRDDir()},
		ErrorIfCRDPathMissing: true,
	}
	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	Expect(workloadsv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(workloadsv1alpha2.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(apiextensionsv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(lwsv1.AddToScheme(scheme.Scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(fieldindex.RegisterFieldIndexes(mgr.GetCache())).To(Succeed())

	nodeBindings := instancesync.NewNodeBindingStore()
	rbgReconciler, err := workloadscontroller.NewRoleBasedGroupReconciler(mgr, scheduler.KubeSchedulerPlugin, "", nodeBindings)
	Expect(err).NotTo(HaveOccurred())
	// Deprecated workload types enabled so the LWS watch is registered, exactly
	// like the e2e deployment that hits the bug.
	Expect(rbgReconciler.SetupWithManager(mgr, controller.Options{}, true)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()
	// Give the manager caches a moment to start.
	time.Sleep(500 * time.Millisecond)
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})
