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

// Verification harness for the review of PR #474 (RoleBasedGroupSet rolling update).
// Additive only — no production code or existing test behavior is touched.
//
// The suite's shared setup installs only the mutating webhooks, so each spec
// below installs its own RoleBasedGroupSet ValidatingWebhookConfiguration (and,
// for the conversion case, a conversion stanza on the CRD) pointing at the
// local webhook server that serves the PR head code, and removes it again via
// DeferCleanup. (ginkgo allows a single BeforeSuite per suite, which is why the
// setup lives inside the specs instead.)
//
//   - F1 (contract): spec.rolloutStrategy must survive a v1alpha1
//     read-modify-write round trip through the real API server. RED today.
//   - F2 (contract): an update that newly enables scalingAdapter must be
//     rejected through the real admission chain. RED today. Control: create
//     with an enabled adapter is already rejected (GREEN).
//   - F5 (contract): negative rollout budgets must be rejected. RED today.
//     Control: valid budgets accepted (GREEN).

package webhook

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

const (
	verifyPR474ValidatingConfig = "verify-pr474-rbgs-validating"
	verifyPR474Namespace        = "verify-pr474"
	verifyPR474CRD              = "rolebasedgroupsets.workloads.x-k8s.io"
	verifyPR474ValidatePath     = "/validate-workloads-x-k8s-io-v1alpha2-rolebasedgroupset"
)

// verifyPR474WebhookClientConfig points the API server at the webhook server
// envtest reserved for this suite (the PR head code), with the generated CA.
func verifyPR474WebhookClientConfig(path string) admissionv1.WebhookClientConfig {
	opts := &testutil.TestEnv.WebhookInstallOptions
	host, port := opts.LocalServingHost, opts.LocalServingPort
	url := fmt.Sprintf("https://%s:%d%s", host, port, path)
	// The CA envtest generated for the local webhook server, the same bundle it
	// injects into the webhook configurations it installs.
	return admissionv1.WebhookClientConfig{URL: &url, CABundle: opts.LocalServingCAData}
}

// installVerifyPR474Admission installs the RoleBasedGroupSet validating webhook
// for the duration of one spec.
func installVerifyPR474Admission() {
	fail := admissionv1.Fail
	none := admissionv1.SideEffectClassNone
	cfg := &admissionv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: verifyPR474ValidatingConfig},
		Webhooks: []admissionv1.ValidatingWebhook{{
			Name:                    "vrolebasedgroupset.verify.kb.io",
			AdmissionReviewVersions: []string{"v1"},
			SideEffects:             &none,
			FailurePolicy:           &fail,
			ClientConfig:            verifyPR474WebhookClientConfig(verifyPR474ValidatePath),
			Rules: []admissionv1.RuleWithOperations{{
				Operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update},
				Rule: admissionv1.Rule{
					APIGroups:   []string{"workloads.x-k8s.io"},
					APIVersions: []string{"v1alpha2"},
					Resources:   []string{"rolebasedgroupsets"},
				},
			}},
		}},
	}
	err := testutil.K8sClient.Create(testutil.Ctx, cfg)
	if apierrors.IsAlreadyExists(err) {
		err = nil
	}
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		_ = testutil.K8sClient.Delete(testutil.Ctx, cfg)
	})
}

// installVerifyPR474Conversion turns on the conversion webhook for the
// RoleBasedGroupSet CRD for the duration of one spec, served by the same local
// webhook server (the PR head registers /convert for the hub type).
func installVerifyPR474Conversion() {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	Expect(testutil.K8sClient.Get(
		testutil.Ctx, client.ObjectKey{Name: verifyPR474CRD}, crd,
	)).To(Succeed())
	orig := crd.Spec.Conversion.DeepCopy()

	cc := verifyPR474WebhookClientConfig("/convert")
	crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
		Strategy: apiextensionsv1.WebhookConverter,
		Webhook: &apiextensionsv1.WebhookConversion{
			ClientConfig: &apiextensionsv1.WebhookClientConfig{
				URL:      cc.URL,
				CABundle: cc.CABundle,
			},
			ConversionReviewVersions: []string{"v1"},
		},
	}
	Expect(testutil.K8sClient.Update(testutil.Ctx, crd)).To(Succeed())
	DeferCleanup(func() {
		current := &apiextensionsv1.CustomResourceDefinition{}
		if err := testutil.K8sClient.Get(
			testutil.Ctx, client.ObjectKey{Name: verifyPR474CRD}, current,
		); err == nil {
			current.Spec.Conversion = orig
			_ = testutil.K8sClient.Update(testutil.Ctx, current)
		}
	})

	// The API server picks the conversion stanza up asynchronously; wait until a
	// v1alpha1 read actually goes through the webhook instead of racing it.
	probe := verifyPR474BasicSet("f1-conversion-probe")
	Expect(testutil.K8sClient.Create(testutil.Ctx, probe)).To(Succeed())
	Eventually(func() error {
		spoke := &workloadsv1alpha1.RoleBasedGroupSet{}
		return testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(probe), spoke)
	}, 15*time.Second, 200*time.Millisecond).Should(Succeed())
	_ = testutil.K8sClient.Delete(testutil.Ctx, probe)
}

func verifyPR474BasicSet(name string) *workloadsv1alpha2.RoleBasedGroupSet {
	return &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: verifyPR474Namespace},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(2)),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}},
				},
			},
		},
	}
}

var _ = Describe("PR474 verification: admission and conversion through the real API server", func() {

	BeforeEach(func() {
		ns := &corev1.Namespace{}
		err := testutil.K8sClient.Get(testutil.Ctx, client.ObjectKey{Name: verifyPR474Namespace}, ns)
		if apierrors.IsNotFound(err) {
			testutil.CreateNamespace(verifyPR474Namespace)
			return
		}
		Expect(err).NotTo(HaveOccurred())
	})

	It("F1: spec.rolloutStrategy survives a v1alpha1 read-modify-write round trip", func() {
		installVerifyPR474Conversion()

		set := verifyPR474BasicSet("f1-roundtrip")
		set.Spec.RolloutStrategy = &workloadsv1alpha2.GroupSetRolloutStrategy{
			Type:           workloadsv1alpha2.RecreateStrategyType,
			MaxUnavailable: ptr.To(intstr.FromInt32(1)),
			MaxSurge:       ptr.To(intstr.FromInt32(1)),
			Partition:      ptr.To(intstr.FromInt32(0)),
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, set)).To(Succeed())

		// Read the object as v1alpha1 (what the deprecated served version returns).
		spoke := &workloadsv1alpha1.RoleBasedGroupSet{}
		Expect(testutil.K8sClient.Get(
			testutil.Ctx, client.ObjectKeyFromObject(set), spoke,
		)).To(Succeed())

		// Write it back through v1alpha1 with an unrelated change.
		if spoke.Labels == nil {
			spoke.Labels = map[string]string{}
		}
		spoke.Labels["verify-pr474"] = "touched"
		Expect(testutil.K8sClient.Update(testutil.Ctx, spoke)).To(Succeed())

		// The strategy must still be on the stored object.
		after := &workloadsv1alpha2.RoleBasedGroupSet{}
		Expect(testutil.K8sClient.Get(
			testutil.Ctx, client.ObjectKeyFromObject(set), after,
		)).To(Succeed())
		Expect(after.Labels).To(HaveKeyWithValue("verify-pr474", "touched"))
		Expect(after.Spec.RolloutStrategy).NotTo(BeNil(),
			"spec.rolloutStrategy was silently dropped by the v1alpha1 round trip")
	})

	It("F2 control: creating a set with an enabled scalingAdapter is rejected", func() {
		installVerifyPR474Admission()
		set := verifyPR474BasicSet("f2-create-control")
		set.Spec.GroupTemplate.Spec.Roles[0].ScalingAdapter = &workloadsv1alpha2.ScalingAdapter{Enable: true}
		err := testutil.K8sClient.Create(testutil.Ctx, set)
		Expect(err).To(HaveOccurred())
		Expect(strings.ToLower(err.Error())).To(ContainSubstring("scalingadapter"))
	})

	It("F2: an update that newly enables scalingAdapter is rejected", func() {
		installVerifyPR474Admission()
		set := verifyPR474BasicSet("f2-update-enable")
		Expect(testutil.K8sClient.Create(testutil.Ctx, set)).To(Succeed())

		latest := &workloadsv1alpha2.RoleBasedGroupSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(set), latest)).To(Succeed())
		latest.Spec.GroupTemplate.Spec.Roles[0].ScalingAdapter = &workloadsv1alpha2.ScalingAdapter{Enable: true}
		err := testutil.K8sClient.Update(testutil.Ctx, latest)
		Expect(err).To(HaveOccurred(),
			"an update that newly enables scalingAdapter must be rejected like a create")
		Expect(strings.ToLower(err.Error())).To(ContainSubstring("scalingadapter"))
	})

	It("F5: negative rollout budgets are rejected", func() {
		installVerifyPR474Admission()
		set := verifyPR474BasicSet("f5-negative")
		set.Spec.RolloutStrategy = &workloadsv1alpha2.GroupSetRolloutStrategy{
			MaxUnavailable: ptr.To(intstr.FromInt32(-1)),
			MaxSurge:       ptr.To(intstr.FromInt32(1)),
		}
		err := testutil.K8sClient.Create(testutil.Ctx, set)
		Expect(err).To(HaveOccurred(),
			"maxUnavailable=-1 must be rejected; today it is accepted and stalls the rollout")
	})

	It("F5 control: valid budgets are accepted", func() {
		installVerifyPR474Admission()
		set := verifyPR474BasicSet("f5-valid-control")
		set.Spec.RolloutStrategy = &workloadsv1alpha2.GroupSetRolloutStrategy{
			MaxUnavailable: ptr.To(intstr.FromInt32(1)),
			MaxSurge:       ptr.To(intstr.FromInt32(1)),
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, set)).To(Succeed())
	})
})
