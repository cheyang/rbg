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

package verifypr509

import (
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// legacyAnnotatedRole is a v1alpha2 role as stored for a pre-0.9.0 object that
// used a deprecated workload type (or was defaulted to one via v1alpha1).
func legacyAnnotatedRole(name string) workloadsv1alpha2.RoleSpec {
	return workloadsv1alpha2.RoleSpec{
		Name:     name,
		Replicas: ptr.To(int32(1)),
		Annotations: map[string]string{
			constants.RoleWorkloadTypeAnnotationKey: constants.StatefulSetWorkloadType,
		},
	}
}

// ContinueOnFailure: the contract specs (E1 scale-up, E2 delete-on-outage) are
// expected to be RED on the reviewed head; the later evidence specs must still run.
var _ = Describe("PR #509 compat-window behavior against a real API server", Ordered, ContinueOnFailure, func() {
	ns := "default"

	var legacySet *workloadsv1alpha2.RoleBasedGroupSet

	It("seeds the pre-upgrade state (webhooks not installed yet)", func() {
		// A RoleBasedGroupSet as it exists on pre-0.9.0 clusters: its template
		// still uses a deprecated workload type.
		legacySet = &workloadsv1alpha2.RoleBasedGroupSet{
			ObjectMeta: metav1.ObjectMeta{Name: "legacy-set", Namespace: ns},
			Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
				Replicas: ptr.To(int32(1)),
				GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
					Spec: workloadsv1alpha2.RoleBasedGroupSpec{
						Roles: []workloadsv1alpha2.RoleSpec{legacyAnnotatedRole("worker")},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, legacySet)).To(Succeed())

		// The child the set's controller already created before the upgrade
		// (child 0). newRBGForSet deep-copies the template roles, annotations
		// included, so this is the exact shape scaleUp would create.
		child0 := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "legacy-set-0", Namespace: ns},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{*legacySet.Spec.GroupTemplate.Spec.Roles[0].DeepCopy()},
			},
		}
		Expect(k8sClient.Create(ctx, child0)).To(Succeed())

		// A v1alpha1 RoleBasedGroup relying on the schema-defaulted workload
		// (apps/v1 StatefulSet), as plenty of pre-0.9.0 manifests do. Written as
		// unstructured YAML-shaped data because that is how real clients submit
		// it: the workload field is omitted entirely and the API server applies
		// the schema default. (The typed Go struct would serialize the zero
		// WorkloadSpec as explicit empty strings, which the schema pattern
		// rejects — a client-encoding artifact, not user behavior.)
		Expect(k8sClient.Create(ctx, pr509V1alpha1Manifest("legacy-defaulted", ns, false))).To(Succeed())
	})

	It("installs the 0.9.0 validating webhook set (compat mode)", func() {
		installShippedValidatingWebhooks()
	})

	It("E1: still admits updates to an existing legacy child RBG (documented exemption)", func() {
		child := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "legacy-set-0", Namespace: ns}, child)).To(Succeed())
		child.Labels = map[string]string{"verify.pr509": "existing-child-update"}
		Expect(k8sClient.Update(ctx, child)).To(Succeed(),
			"an existing role must keep reconciling during the compat window")
	})

	It("E1: CONTRACT - the set's controller can still scale up / recreate children (scaleUp create is admitted)", func() {
		// This is exactly what RoleBasedGroupSetReconciler.scaleUp does for
		// index 1: build the child from the template and Create it through the
		// API server.
		child1 := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "legacy-set-1", Namespace: ns},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{*legacySet.Spec.GroupTemplate.Spec.Roles[0].DeepCopy()},
			},
		}
		err := k8sClient.Create(ctx, child1)
		Expect(err).NotTo(HaveOccurred(),
			"F1: scale-up/self-heal of an existing legacy RoleBasedGroupSet must work during the compat window; "+
				"got: %v", err)
	})

	It("E3: updating the defaulted v1alpha1 object is admitted (existing-role exemption end-to-end)", func() {
		fetched := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "legacy-defaulted", Namespace: ns}, fetched)).To(Succeed())
		// Sanity: the stored object really did pick up the schema default.
		Expect(fetched.Spec.Roles[0].Workload.Kind).To(Equal("StatefulSet"))
		Expect(fetched.Spec.Roles[0].Workload.APIVersion).To(Equal("apps/v1"))
		if fetched.Labels == nil {
			fetched.Labels = map[string]string{}
		}
		fetched.Labels["verify.pr509"] = "still-writable"
		Expect(k8sClient.Update(ctx, fetched)).To(Succeed(),
			"a v1alpha1 object whose workload came from the schema default must stay writable in compat mode")
	})

	It("E3: the admission oldObj carries the schema-defaulted workload", func() {
		_, oldRaw, calls := probe.snapshot()
		Expect(calls).To(BeNumerically(">", 0), "probe webhook was never called")
		Expect(string(oldRaw)).To(ContainSubstring(`"kind":"StatefulSet"`),
			"oldObj sent to validating webhooks carries schema defaults, contradicting the "+
				"deprecation_webhook.go comment 'oldObj ... may not carry v1alpha1 schema defaults'")
	})

	It("E4: a v1alpha1 write returns an admission warning and new deprecated uses are rejected", func() {
		warnings := &warningCapture{}
		warnClient, err := client.New(registerWarningHandler(cfg, warnings), client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())

		ok := pr509V1alpha1Manifest("explicit-ris", ns, true)
		Expect(warnClient.Create(ctx, ok)).To(Succeed())
		Expect(warnings.contains("v1alpha1 is deprecated")).To(BeTrue(),
			"every v1alpha1 write must return the deprecation admission warning")

		err = warnClient.Create(ctx, pr509V1alpha1Manifest("new-defaulted", ns, false))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("deprecated"),
			"a new v1alpha1 object relying on the deprecated default workload must be rejected")
	})

	It("E2: CONTRACT - deleting a v1alpha1 object is not blocked when the warning webhook is unreachable", func() {
		fail := admissionregistrationv1.Fail
		exact := admissionregistrationv1.Exact
		none := admissionregistrationv1.SideEffectClassNone
		dead := &admissionregistrationv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "verify-pr509-dead"},
			Webhooks: []admissionregistrationv1.ValidatingWebhook{{
				Name:                    "vrolebasedgroup-v1alpha1-deprecation-dead.kb.io",
				AdmissionReviewVersions: []string{"v1"},
				SideEffects:             &none,
				FailurePolicy:           &fail,
				MatchPolicy:             &exact,
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					// Nothing listens here: the webhook server is down.
					URL:      ptrTo("https://127.0.0.1:1/validate-workloads-x-k8s-io-v1alpha1-rolebasedgroup"),
					CABundle: caData,
				},
				Rules: []admissionregistrationv1.RuleWithOperations{{
					Operations: []admissionregistrationv1.OperationType{
						admissionregistrationv1.Create,
						admissionregistrationv1.Update,
						admissionregistrationv1.Delete,
					},
					Rule: admissionregistrationv1.Rule{
						APIGroups:   []string{"workloads.x-k8s.io"},
						APIVersions: []string{"v1alpha1"},
						Resources:   []string{"rolebasedgroups"},
					},
				}},
			}},
		}
		Expect(k8sClient.Create(ctx, dead)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, dead)).To(Succeed())
		})

		victim := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "explicit-ris", Namespace: ns}, victim)).To(Succeed())
		err := k8sClient.Delete(ctx, victim)
		Expect(err).NotTo(HaveOccurred(),
			"F2: a warning-only webhook (verbs=create;update;delete, failurePolicy=Fail) must not block "+
				"v1alpha1 deletes while the webhook server is down; got: %v", err)
	})
})

// pr509V1alpha1Manifest builds a v1alpha1 RoleBasedGroup the way a user
// manifest looks: an optional explicit workload, a pod template (required by
// the CRD CEL rule for non-InstanceSet workloads), one replica.
func pr509V1alpha1Manifest(name, ns string, explicitRIS bool) *unstructured.Unstructured {
	role := map[string]interface{}{
		"name":     "worker",
		"replicas": int64(1),
		"template": map[string]interface{}{
			"spec": map[string]interface{}{
				"containers": []interface{}{
					map[string]interface{}{
						"name":  "c",
						"image": "registry.k8s.io/pause:3.9",
					},
				},
			},
		},
	}
	if explicitRIS {
		role["workload"] = map[string]interface{}{
			"apiVersion": "workloads.x-k8s.io/v1alpha2",
			"kind":       "RoleInstanceSet",
		}
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "workloads.x-k8s.io/v1alpha1",
		"kind":       "RoleBasedGroup",
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"spec":       map[string]interface{}{"roles": []interface{}{role}},
	}}
}

// warningCapture records HTTP warning headers the API server returns.
type warningCapture struct {
	mu   sync.Mutex
	seen []string
}

func (w *warningCapture) HandleWarningHeader(_ int, _ string, message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = append(w.seen, message)
}

func (w *warningCapture) contains(sub string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range w.seen {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func registerWarningHandler(cfg *rest.Config, w *warningCapture) *rest.Config {
	out := rest.CopyConfig(cfg)
	out.WarningHandler = w
	return out
}
