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

// Package deprecationv1a1spoof is a reviewer-built verification harness for
// PR #524 finding F3 (carried to round 2): the v1alpha2 replacement-child
// exemption is claimable by any writer that can read the parent set's name
// and UID, while the docstring claims a forged owner reference grants nothing.
//
// It seeds a v1alpha2 RoleBasedGroupSet whose template carries a deprecated
// (StatefulSet) role BEFORE any webhook configuration is installed — the only
// moment such an object can legally exist — then installs the production
// webhook configuration from config/webhook/manifests.yaml and exercises the
// exemption as an ordinary writer.
package deprecationv1a1spoof

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
	"k8s.io/apimachinery/pkg/runtime"
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

const spoofNamespace = "deprecation-spoof"

var (
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	mgr       ctrl.Manager
	cancelCtx context.CancelFunc
	ctxValue  context.Context
)

func TestV1alpha1SpoofExemption(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "v1alpha1 Replacement-Child Exemption Spoof Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctxValue, cancelCtx = context.WithCancel(context.TODO())

	_, currentFile, _, _ := goruntime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(currentFile)))))
	crdPath := filepath.Join(repoRoot, "config", "crd", "bases")
	webhookManifestPath := filepath.Join(repoRoot, "config", "webhook", "manifests.yaml")

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(workloadsv1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(workloadsv1alpha2.AddToScheme(scheme)).To(Succeed())
	Expect(admissionregistrationv1.AddToScheme(scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	mgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
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
		ObjectMeta: metav1.ObjectMeta{Name: spoofNamespace},
	})).To(Succeed())

	By("seeding the legacy v1alpha2 set before any webhook configuration exists")
	seedLegacyV2Set()

	By("installing the production webhook configurations")
	installProductionWebhookConfigurations(webhookManifestPath)

	waitForGate()
})

var _ = AfterSuite(func() {
	cancelCtx()
	Expect(testEnv.Stop()).To(Succeed())
})

var _ = Describe("replacement-child exemption (v1alpha2 gate)", func() {
	// BUG-CANARY for finding F3: pins the CURRENT behavior. The exemption is
	// decided only from the owner reference plus a live parent read, so ANY
	// writer that can read the set's name and UID can create a
	// deprecated-type child, not just the set controller. If the gate is
	// tightened (e.g. a caller-identity check), this spec flips red and must
	// be inverted.
	It("grants the exemption to an ordinary writer that knows the parent's name and UID", func() {
		set := &workloadsv1alpha2.RoleBasedGroupSet{}
		Expect(k8sClient.Get(ctxValue, types.NamespacedName{
			Name: "legacy-v2-set", Namespace: spoofNamespace,
		}, set)).To(Succeed())

		child := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "spoofed-replacement-child",
				Namespace: spoofNamespace,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: workloadsv1alpha2.GroupVersion.String(),
					Kind:       "RoleBasedGroupSet",
					Name:       set.Name,
					UID:        set.UID,
					Controller: ptr.To(true),
				}},
			},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{v2StatefulRole("worker")},
			},
		}
		Expect(k8sClient.Create(ctxValue, child)).To(Succeed())
	})

	It("rejects the same deprecated-type child without the owner reference", func() {
		// Control: proves the admission above is the exemption at work, not
		// an ungated deprecated role.
		child := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "unowned-deprecated-child",
				Namespace: spoofNamespace,
			},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{v2StatefulRole("worker")},
			},
		}
		err := k8sClient.Create(ctxValue, child)
		Expect(err).To(HaveOccurred())
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

// seedLegacyV2Set stores the hub-version form a pre-0.9.0 set has in
// production: a v1alpha2 RoleBasedGroupSet whose template carries a
// deprecated workload role. This must run before the webhook configurations
// are installed, because the compat gate rejects creating such a set.
func seedLegacyV2Set() {
	set := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-v2-set", Namespace: spoofNamespace},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(2)),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{v2StatefulRole("worker")},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctxValue, set)).To(Succeed())
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
			ObjectMeta: metav1.ObjectMeta{Name: "gate-readiness", Namespace: spoofNamespace},
			Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{v2StatefulRole("worker")}},
		}
		err := k8sClient.Create(ctxValue, child)
		return err != nil && strings.Contains(err.Error(), "new uses are not accepted")
	}, 10*time.Second, 250*time.Millisecond).Should(BeTrue(), "the compat gate did not become active")
}
