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

// Package verifycodex is the reviewer-B verification harness for PR #524
// (0.9.0 compatibility admission gate). It is additive: no production code is
// touched. The suite boots ONE envtest environment with the real conversion
// webhook wired (CRD conversion patched to the in-process webhook server), so
// the v1alpha1 admission path under test is the production one:
//
//	v1alpha1 request -> mutating/validating admission (Exact match, v1alpha1
//	deprecation webhooks) + (Equivalent match, v1alpha2 validators on the
//	converted object) -> conversion webhook -> storage as v1alpha2.
package verifycodex

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

var (
	testEnv   *envtest.Environment
	k8sClient client.Client
	// warnClient records every admission warning the API server sends back.
	warnClient client.Client
	warnings   = &warningRecorder{}

	testCtx    context.Context
	testCancel context.CancelFunc

	gateNamespace = "verify-gate"
)

type warningRecorder struct {
	mu   sync.Mutex
	text []string
}

func (w *warningRecorder) HandleWarningHeader(_ int, _ string, text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.text = append(w.text, text)
}

func (w *warningRecorder) reset() { w.mu.Lock(); w.text = nil; w.mu.Unlock() }

func (w *warningRecorder) contains(sub string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, t := range w.text {
		if strings.Contains(t, sub) {
			return true
		}
	}
	return false
}

func TestMain(m *testing.M) {
	testCtx, testCancel = context.WithCancel(context.Background())

	_, thisFile, _, _ := goruntime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(repoRoot, "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		// Webhook configurations are installed by the tests themselves, at the
		// moment the scenario needs the gate to switch on.
	}

	restCfg, err := testEnv.Start()
	if err != nil {
		fmt.Println("envtest start:", err)
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(admissionregistrationv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(workloadsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(workloadsv1alpha2.AddToScheme(scheme))

	k8sClient, err = client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Println("client:", err)
		os.Exit(1)
	}

	warnCfg := rest.CopyConfig(restCfg)
	warnCfg.WarningHandler = warnings
	warnClient, err = client.New(warnCfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Println("warn client:", err)
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: server.Options{BindAddress: "0"},
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    testEnv.WebhookInstallOptions.LocalServingHost,
			Port:    testEnv.WebhookInstallOptions.LocalServingPort,
			CertDir: testEnv.WebhookInstallOptions.LocalServingCertDir,
		}),
	})
	if err != nil {
		fmt.Println("manager:", err)
		os.Exit(1)
	}

	// Register exactly the webhooks PR #524 wires in cmd/rbgs/main.go with the
	// compatibility gate enabled (the 0.9.0 default).
	if err := (&workloadsv1alpha2.RoleBasedGroup{}).SetupWebhookWithManager(mgr, true); err != nil {
		fmt.Println("rbg webhook:", err)
		os.Exit(1)
	}
	if err := (&workloadsv1alpha2.RoleBasedGroupSet{}).SetupWebhookWithManager(mgr, true); err != nil {
		fmt.Println("rbgs webhook:", err)
		os.Exit(1)
	}
	if err := (&workloadsv1alpha1.RoleBasedGroup{}).SetupDeprecationWebhookWithManager(mgr, true); err != nil {
		fmt.Println("v1alpha1 rbg webhook:", err)
		os.Exit(1)
	}
	if err := (&workloadsv1alpha1.RoleBasedGroupSet{}).SetupDeprecationWebhookWithManager(mgr, true); err != nil {
		fmt.Println("v1alpha1 rbgs webhook:", err)
		os.Exit(1)
	}
	if err := (&workloadsv1alpha1.InstanceSet{}).SetupDeprecationWebhookWithManager(mgr); err != nil {
		fmt.Println("v1alpha1 instanceset webhook:", err)
		os.Exit(1)
	}

	go func() {
		if err := mgr.Start(testCtx); err != nil {
			fmt.Println("manager start:", err)
		}
	}()

	host := testEnv.WebhookInstallOptions.LocalServingHost
	port := testEnv.WebhookInstallOptions.LocalServingPort
	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, derr := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 250*time.Millisecond)
		if derr == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			fmt.Println("webhook server never came up:", derr)
			os.Exit(1)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Wire the CRD conversion webhooks to the in-process server, mirroring what
	// kustomize + the cert controller do in production.
	conversionURL := fmt.Sprintf("https://%s", net.JoinHostPort(host, fmt.Sprint(port))) + "/convert"
	for _, crdName := range []string{
		"rolebasedgroups.workloads.x-k8s.io",
		"rolebasedgroupsets.workloads.x-k8s.io",
	} {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := k8sClient.Get(testCtx, client.ObjectKey{Name: crdName}, crd); err != nil {
			fmt.Println("get crd:", err)
			os.Exit(1)
		}
		crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
			Strategy: apiextensionsv1.WebhookConverter,
			Webhook: &apiextensionsv1.WebhookConversion{
				ClientConfig: &apiextensionsv1.WebhookClientConfig{
					URL:      &conversionURL,
					CABundle: testEnv.WebhookInstallOptions.LocalServingCAData,
				},
				ConversionReviewVersions: []string{"v1"},
			},
		}
		if err := k8sClient.Update(testCtx, crd); err != nil {
			fmt.Println("patch crd conversion:", err)
			os.Exit(1)
		}
	}

	code := m.Run()
	testCancel()
	_ = testEnv.Stop()
	os.Exit(code)
}

// webhookURL builds the https URL for a webhook path on the test server.
func webhookURL(path string) string {
	opts := testEnv.WebhookInstallOptions
	return fmt.Sprintf("https://%s%s",
		net.JoinHostPort(opts.LocalServingHost, fmt.Sprint(opts.LocalServingPort)), path)
}

var (
	failPolicy     = admissionregistrationv1.Fail
	ignorePolicy   = admissionregistrationv1.Ignore
	noneSideEffect = admissionregistrationv1.SideEffectClassNone
	exactMatch     = admissionregistrationv1.Exact
)

// installGateWebhooks switches the 0.9.0 admission gate on for
// rolebasedgroups/rolebasedgroupsets, matching the shipped
// deploy/kubectl/manifests.yaml: the v1alpha2 validators match Equivalent
// (matchPolicy unset), the v1alpha1 deprecation validators match Exact.
func installGateWebhooks(t *testing.T) {
	t.Helper()
	opts := testEnv.WebhookInstallOptions
	vwh := func(name, path string, versions []string, resource string, matchPolicy *admissionregistrationv1.MatchPolicyType) admissionregistrationv1.ValidatingWebhook {
		return admissionregistrationv1.ValidatingWebhook{
			Name:                    name,
			AdmissionReviewVersions: []string{"v1"},
			SideEffects:             &noneSideEffect,
			FailurePolicy:           &failPolicy,
			MatchPolicy:             matchPolicy,
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				URL:      ptrTo(webhookURL(path)),
				CABundle: opts.LocalServingCAData,
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{
					admissionregistrationv1.Create, admissionregistrationv1.Update,
				},
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{"workloads.x-k8s.io"},
					APIVersions: versions,
					Resources:   []string{resource},
				},
			}},
		}
	}
	config := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-gate"},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			vwh("vrolebasedgroup.kb.io", "/validate-workloads-x-k8s-io-v1alpha2-rolebasedgroup", []string{"v1alpha2"}, "rolebasedgroups", nil),
			vwh("vrolebasedgroupset.kb.io", "/validate-workloads-x-k8s-io-v1alpha2-rolebasedgroupset", []string{"v1alpha2"}, "rolebasedgroupsets", nil),
			vwh("vrolebasedgroup-v1alpha1-deprecation.kb.io", "/validate-workloads-x-k8s-io-v1alpha1-rolebasedgroup", []string{"v1alpha1"}, "rolebasedgroups", &exactMatch),
			vwh("vrolebasedgroupset-v1alpha1-deprecation.kb.io", "/validate-workloads-x-k8s-io-v1alpha1-rolebasedgroupset", []string{"v1alpha1"}, "rolebasedgroupsets", &exactMatch),
		},
	}
	if err := k8sClient.Create(testCtx, config); err != nil {
		t.Fatalf("install gate webhooks: %v", err)
	}
}

func ptrTo[T any](v T) *T { return &v }

// ensureNamespace creates the shared test namespace if missing.
func ensureNamespace(t *testing.T) {
	t.Helper()
	err := k8sClient.Create(testCtx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: gateNamespace},
	})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
}
