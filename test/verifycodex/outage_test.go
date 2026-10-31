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

package verifycodex

import (
	"strings"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
)

// deadWebhookURL points at a port nothing listens on: the simulated webhook
// outage (same failure mode as the webhook pod being down during a rollout).
func deadWebhookURL(path string) string {
	return "https://127.0.0.1:1" + path
}

func instanceSetV1alpha1(name string) *workloadsv1alpha1.InstanceSet {
	return &workloadsv1alpha1.InstanceSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gateNamespace},
		Spec: workloadsv1alpha1.InstanceSetSpec{
			Replicas: ptr.To(int32(1)),
			InstanceTemplate: workloadsv1alpha1.InstanceTemplate{
				InstanceSpec: workloadsv1alpha1.InstanceSpec{
					Components: []workloadsv1alpha1.InstanceComponent{{
						Name:     "c",
						Template: *podTemplate(),
					}},
				},
			},
		},
	}
}

func installInstanceSetWebhook(t *testing.T, name string, policy admissionregistrationv1.FailurePolicyType, ops []admissionregistrationv1.OperationType) {
	t.Helper()
	config := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{{
			Name:                    "vinstanceset-v1alpha1-deprecation.kb.io",
			AdmissionReviewVersions: []string{"v1"},
			SideEffects:             &noneSideEffect,
			FailurePolicy:           &policy,
			MatchPolicy:             &exactMatch,
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				URL:      ptrTo(deadWebhookURL("/validate-workloads-x-k8s-io-v1alpha1-instanceset")),
				CABundle: testEnv.WebhookInstallOptions.LocalServingCAData,
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: ops,
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{"workloads.x-k8s.io"},
					APIVersions: []string{"v1alpha1"},
					Resources:   []string{"instancesets"},
				},
			}},
		}},
	}
	if err := k8sClient.Create(testCtx, config); err != nil {
		t.Fatalf("install %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(testCtx, config)
	})

	// Wait until the API server dispatcher observes the config: probe with a
	// dry-run create until the outcome matches the policy's blocking behavior.
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := k8sClient.Create(testCtx, instanceSetV1alpha1("policy-probe"), &client.DryRunAll)
		active := (err != nil) == (policy == admissionregistrationv1.Fail)
		if active {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("webhook config %s never became active; last err=%v", name, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestWarningOnlyWebhookOutage demonstrates the outage mechanism behind
// ledger F2: with failurePolicy=Fail a dead warning-only webhook rejects every
// v1alpha1 write to that kind — with zero enforcement value, because the
// handler can only ever return a warning — while failurePolicy=Ignore admits
// the same write. The shipped bytes themselves are pinned by
// TestShippedWarningOnlyPolicy (warning-only = Ignore since 22acc570); this
// test stays as the self-contained mechanism proof and regression demo for why
// the policy choice matters. The same PR deliberately does NOT register DELETE
// for exactly this reason; TestDeleteNotInterceptedDuringOutage covers that.
//
// Polarity: contract — both subtests assert the intended mechanism behavior.
func TestWarningOnlyWebhookOutage(t *testing.T) {
	ensureNamespace(t)
	ops := []admissionregistrationv1.OperationType{
		admissionregistrationv1.Create, admissionregistrationv1.Update,
	}

	t.Run("Fail policy blocks a warning-only v1alpha1 write during outage", func(t *testing.T) {
		installInstanceSetWebhook(t, "outage-fail", admissionregistrationv1.Fail, ops)
		err := k8sClient.Create(testCtx, instanceSetV1alpha1("blocked-write"))
		if err == nil {
			t.Fatalf("expected the dead Fail webhook to block the create")
		}
		if !strings.Contains(err.Error(), "failed calling webhook") {
			t.Fatalf("unexpected error shape: %v", err)
		}
		t.Logf("observed (Fail policy): %v", err)
	})

	t.Run("Ignore policy does not block the write during outage", func(t *testing.T) {
		installInstanceSetWebhook(t, "outage-ignore", admissionregistrationv1.Ignore, ops)
		if err := k8sClient.Create(testCtx, instanceSetV1alpha1("allowed-write")); err != nil {
			t.Fatalf("Ignore policy must admit during outage, got: %v", err)
		}
	})
}

// TestDeleteNotInterceptedDuringOutage (contract, F2 carried from the PR #509
// review): the shipped webhooks register CREATE;UPDATE only, so a v1alpha1
// delete during a webhook outage is not blocked. Seed the object first, then
// install the dead Fail webhook, then delete.
func TestDeleteNotInterceptedDuringOutage(t *testing.T) {
	ensureNamespace(t)
	victim := instanceSetV1alpha1("delete-me")
	if err := k8sClient.Create(testCtx, victim); err != nil {
		t.Fatalf("seed: %v", err)
	}
	installInstanceSetWebhook(t, "outage-fail-delete-only-create-update", admissionregistrationv1.Fail,
		[]admissionregistrationv1.OperationType{admissionregistrationv1.Create, admissionregistrationv1.Update})
	if err := k8sClient.Delete(testCtx, victim); err != nil {
		t.Fatalf("delete must not be intercepted (no DELETE verb registered), got: %v", err)
	}
}
