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

// Package deprecationv1a1typeexact is a reviewer-built verification harness
// for PR #524 round-4 commit fb3aa3d9 ("make update-path grandfathering
// type-exact"): under the compatibility flag, an existing role keeps its own
// deprecated workload type across updates, but switching between deprecated
// types (e.g. StatefulSet -> Deployment) is rejected as a new use.
//
// The semantics are pinned end-to-end through the real admission chain: the
// production webhook registrations from cmd/rbgs/main.go and the shipped
// config/webhook/manifests.yaml, with grandfathered objects seeded BEFORE the
// webhook configuration exists (the only moment a deprecated-type object can
// legally be created). Both API versions are exercised — v1alpha2 (the hub,
// enforcing endpoint) and v1alpha1 (via the production conversion), so the
// two validators cannot diverge silently.
//
// Polarity: CONTRACT. All four specs are green only when the type-exact rule
// is live on both validators. Reverting fb3aa3d9's one-line change (lenient
// name-only grandfathering) flips the two type-switch specs red.
package deprecationv1a1typeexact

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

const typeExactNamespace = "deprecation-typeexact"

var (
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	mgr       ctrl.Manager
	cancelCtx context.CancelFunc
	ctxValue  context.Context
)

func TestV1alpha1TypeExactGrandfathering(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "v1alpha1 Type-Exact Grandfathering Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctxValue, cancelCtx = context.WithCancel(context.TODO())

	_, currentFile, _, _ := goruntime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(currentFile)))))
	crdPath := filepath.Join(repoRoot, "config", "crd", "bases")
	webhookManifestPath := filepath.Join(repoRoot, "config", "webhook", "manifests.yaml")

	// Register every type the suite serves BEFORE starting the environment:
	// envtest decides at startup — from this scheme — which multi-version CRDs
	// get their conversion webhook enabled (advisory C4431130427), and the
	// v1alpha1 specs below need the production conversion path.
	Expect(workloadsv1alpha1.AddToScheme(clientgoscheme.Scheme)).To(Succeed())
	Expect(workloadsv1alpha2.AddToScheme(clientgoscheme.Scheme)).To(Succeed())
	Expect(admissionregistrationv1.AddToScheme(clientgoscheme.Scheme)).To(Succeed())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	mgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: clientgoscheme.Scheme,
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

	// Register exactly what cmd/rbgs/main.go registers, compatibility mode on.
	Expect((&workloadsv1alpha2.RoleBasedGroup{}).SetupWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleBasedGroupSet{}).SetupWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha2.CoordinatedPolicy{}).SetupWebhookWithManager(mgr, false)).To(Succeed())
	Expect((&workloadsv1alpha2.RoleInstanceSet{}).SetupWebhookWithManager(mgr)).To(Succeed())
	Expect((&workloadsv1alpha1.RoleBasedGroup{}).SetupDeprecationWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha1.RoleBasedGroupSet{}).SetupDeprecationWebhookWithManager(mgr, true)).To(Succeed())
	Expect((&workloadsv1alpha1.InstanceSet{}).SetupDeprecationWebhookWithManager(mgr)).To(Succeed())
	Expect((&workloadsv1alpha1.Instance{}).SetupDeprecationWebhookWithManager(mgr)).To(Succeed())
	Expect((&workloadsv1alpha1.RoleBasedGroupScalingAdapter{}).SetupDeprecationWebhookWithManager(mgr)).To(Succeed())
	Expect((&workloadsv1alpha1.ClusterEngineRuntimeProfile{}).SetupDeprecationWebhookWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctxValue)).To(Succeed())
	}()

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

	Expect(k8sClient.Create(ctxValue, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: typeExactNamespace},
	})).To(Succeed())

	By("seeding grandfathered deprecated-type objects before any webhook configuration exists")
	seedLegacyV2RBG()
	seedLegacyV1RBG()

	By("installing the production webhook configurations")
	installProductionWebhookConfigurations(webhookManifestPath)

	waitForGate()
})

var _ = AfterSuite(func() {
	cancelCtx()
	Expect(testEnv.Stop()).To(Succeed())
})

var _ = Describe("type-exact update-path grandfathering", func() {
	It("admits a v1alpha2 update that keeps the role's deprecated workload type", func() {
		rbg := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(k8sClient.Get(ctxValue, types.NamespacedName{
			Name: "legacy-v2-rbg", Namespace: typeExactNamespace,
		}, rbg)).To(Succeed())

		rbg.Spec.Roles[0].Replicas = ptr.To(int32(3))
		Expect(k8sClient.Update(ctxValue, rbg)).To(Succeed(),
			"a grandfathered role keeping its own deprecated workload type must stay writable")
	})

	It("rejects a v1alpha2 update switching between deprecated workload types", func() {
		rbg := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(k8sClient.Get(ctxValue, types.NamespacedName{
			Name: "legacy-v2-rbg", Namespace: typeExactNamespace,
		}, rbg)).To(Succeed())

		rbg.Spec.Roles[0].Annotations[constants.RoleWorkloadTypeAnnotationKey] = constants.DeploymentWorkloadType
		err := k8sClient.Update(ctxValue, rbg)
		Expect(err).To(HaveOccurred(),
			"switching StatefulSet -> Deployment provisions a new deprecated workload and is a new use")
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("admits a v1alpha1 update that keeps the role's deprecated workload type", func() {
		rbg := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(k8sClient.Get(ctxValue, types.NamespacedName{
			Name: "legacy-v1-rbg", Namespace: typeExactNamespace,
		}, rbg)).To(Succeed())

		rbg.Spec.Roles[0].Replicas = ptr.To(int32(3))
		Expect(k8sClient.Update(ctxValue, rbg)).To(Succeed(),
			"a grandfathered v1alpha1 role keeping its own deprecated workload type must stay writable")
	})

	It("rejects a v1alpha1 update switching between deprecated workload types", func() {
		rbg := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(k8sClient.Get(ctxValue, types.NamespacedName{
			Name: "legacy-v1-rbg", Namespace: typeExactNamespace,
		}, rbg)).To(Succeed())

		rbg.Spec.Roles[0].Workload.Kind = "Deployment"
		err := k8sClient.Update(ctxValue, rbg)
		Expect(err).To(HaveOccurred(),
			"switching StatefulSet -> Deployment through the v1alpha1 API is a new use")
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})
})

func v2StatefulRole(name string) workloadsv1alpha2.RoleSpec {
	return workloadsv1alpha2.RoleSpec{
		Name:        name,
		Replicas:    ptr.To(int32(1)),
		Annotations: map[string]string{constants.RoleWorkloadTypeAnnotationKey: constants.StatefulSetWorkloadType},
		Pattern: workloadsv1alpha2.Pattern{
			StandalonePattern: &workloadsv1alpha2.StandalonePattern{
				TemplateSource: workloadsv1alpha2.TemplateSource{
					Template: ptr.To(corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}}},
					}),
				},
			},
		},
	}
}

// seedLegacyV2RBG stores a grandfathered v1alpha2 RBG whose role carries the
// deprecated StatefulSet workload type. This must run before the webhook
// configurations are installed, because the compat gate rejects creating such
// an object.
func seedLegacyV2RBG() {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-v2-rbg", Namespace: typeExactNamespace},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{v2StatefulRole("worker")},
		},
	}
	Expect(k8sClient.Create(ctxValue, rbg)).To(Succeed())
}

// seedLegacyV1RBG stores a grandfathered object in the pre-0.9.0 shape: a
// v1alpha1 RoleBasedGroup whose role names a deprecated workload explicitly.
// The production conversion persists it as v1alpha2 with the
// role-workload-type annotation.
func seedLegacyV1RBG() {
	rbg := &workloadsv1alpha1.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-v1-rbg", Namespace: typeExactNamespace},
		Spec: workloadsv1alpha1.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha1.RoleSpec{v1StatefulRole("worker")},
		},
	}
	Expect(k8sClient.Create(ctxValue, rbg)).To(Succeed())
}

func v1StatefulRole(name string) workloadsv1alpha1.RoleSpec {
	return workloadsv1alpha1.RoleSpec{
		Name:     name,
		Replicas: ptr.To(int32(1)),
		Workload: workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		TemplateSource: workloadsv1alpha1.TemplateSource{
			Template: ptr.To(corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}}},
			}),
		},
	}
}

// installProductionWebhookConfigurations installs the shipped webhook
// manifests against the local test server.
func installProductionWebhookConfigurations(path string) {
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())

	opts := testEnv.WebhookInstallOptions
	hostPort := net.JoinHostPort(opts.LocalServingHost, fmt.Sprint(opts.LocalServingPort))

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
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
			Expect(k8sClient.Create(ctxValue, config)).To(Succeed())
		case "MutatingWebhookConfiguration":
			config := &admissionregistrationv1.MutatingWebhookConfiguration{}
			Expect(k8sruntime.DefaultUnstructuredConverter.FromUnstructured(raw, config)).To(Succeed())
			for i := range config.Webhooks {
				rewriteClientConfig(&config.Webhooks[i].ClientConfig, hostPort, opts.LocalServingCAData)
			}
			Expect(k8sClient.Create(ctxValue, config)).To(Succeed())
		default:
			Fail(fmt.Sprintf("unexpected kind in shipped webhook manifests: %v", raw["kind"]))
		}
	}
}

func rewriteClientConfig(cc *admissionregistrationv1.WebhookClientConfig, hostPort string, caData []byte) {
	if cc.Service != nil && cc.Service.Path != nil {
		url := fmt.Sprintf("https://%s%s", hostPort, *cc.Service.Path)
		cc.URL = &url
		cc.Service = nil
	}
	cc.CABundle = caData
}

// waitForGate blocks until the v1alpha2 compat gate rejects a new deprecated
// use through the real admission chain.
func waitForGate() {
	Eventually(func() bool {
		child := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "gate-readiness", Namespace: typeExactNamespace},
			Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{v2StatefulRole("worker")}},
		}
		err := k8sClient.Create(ctxValue, child)
		return err != nil && strings.Contains(err.Error(), "new uses are not accepted")
	}, 10*time.Second, 250*time.Millisecond).Should(BeTrue(), "the compat gate did not become active")
}
