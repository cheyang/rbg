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

// Package deprecationv1a1conversion is a reviewer-built verification harness
// for PR #524 round-3 follow-up (Copilot advisory C4431130427, fixed in
// c875fb31): the v1alpha1 contract suite must register both workload API
// versions in the client-go scheme BEFORE TestEnv.Start(), because envtest
// decides at startup — from that scheme — which multi-version CRDs get their
// conversion webhook enabled, and strips conversion for GroupKinds that are
// not registered yet (vendor/sigs.k8s.io/controller-runtime/pkg/envtest/crd.go,
// modifyConversionWebhooks). Registering afterwards leaves the suite on
// version-only conversion instead of the production v1alpha1 -> v1alpha2
// translation the manager serves.
//
// Polarity: CONTRACT. Both specs are green only when the production
// conversion path is actually wired into the suite's environment.
package deprecationv1a1conversion

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
)

func TestConversionContract(t *testing.T) {
	GinkgoT()
	RegisterFailHandler(Fail)
	RunSpecs(t, "v1alpha1 contract suite conversion wiring suite")
}

var crdClient client.Client

var _ = BeforeSuite(func() {
	testutil.SetupV1alpha1ContractTestEnv()

	crdScheme := runtime.NewScheme()
	Expect(apiextensionsv1.AddToScheme(crdScheme)).To(Succeed())
	Expect(clientgoscheme.AddToScheme(crdScheme)).To(Succeed())
	var err error
	crdClient, err = client.New(testutil.Cfg, client.Options{Scheme: crdScheme})
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	testutil.TeardownTestEnv()
})

var _ = Describe("v1alpha1 contract suite conversion wiring", Ordered, func() {
	It("enables webhook conversion for the multi-version workload CRDs", func() {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		err := crdClient.Get(
			testutil.Ctx,
			client.ObjectKey{Name: "rolebasedgroups.workloads.x-k8s.io"},
			crd,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(crd.Spec.Conversion).NotTo(BeNil(),
			"envtest must wire a conversion webhook for the multi-version rolebasedgroups CRD; "+
				"nil conversion means the suite runs version-only conversion (advisory C4431130427)")
		Expect(crd.Spec.Conversion.Strategy).To(Equal(apiextensionsv1.WebhookConverter))
	})

	It("stores a v1alpha1 create through the production v1alpha2 conversion", func() {
		// BuildBasicRole defaults to a RoleInstanceSet role, so the create is
		// admitted under the compatibility gate.
		rbg := wrappersv1.BuildBasicRoleBasedGroup(
			"conversion-roundtrip", testutil.ContractNamespace,
		).WithRoles([]workloadsv1alpha1.RoleSpec{wrappersv1.BuildBasicRole("worker").Obj()}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())

		// The storage version is v1alpha2. Reading the stored object as
		// v1alpha2 (the hub) requires the create to have been converted by
		// the manager's production /convert endpoint.
		stored := &workloadsv1alpha2.RoleBasedGroup{}
		err := testutil.K8sClient.Get(
			testutil.Ctx,
			types.NamespacedName{Name: rbg.Name, Namespace: testutil.ContractNamespace},
			stored,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Spec.Roles).To(HaveLen(1))
		Expect(stored.Spec.Roles[0].Name).To(Equal("worker"))
	})
})
