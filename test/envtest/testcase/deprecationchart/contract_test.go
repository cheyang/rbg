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

package deprecationchart

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
)

// Round-3 specs: the chart-rendered webhook configurations must drive the
// documented 0.9.0 gate in the post-#556 composition — v1alpha1 writes flow
// through conversion into the v1alpha2 validators (matchPolicy: Equivalent),
// and the CRD deprecationWarning is the sole warning layer.
//
// chartGrandfathered, chartSwitch, and chartSetGrandfathered are seeded
// BEFORE the chart-rendered webhooks are installed, so they carry persisted
// deprecated roles the way a pre-0.9.0 installation would.
var (
	chartGrandfathered    *workloadsv1alpha1.RoleBasedGroup
	chartSwitch           *workloadsv1alpha1.RoleBasedGroup
	chartSetGrandfathered *workloadsv1alpha1.RoleBasedGroupSet
)

func seedGrandfatheredObjects() {
	deprecated := func() workloadsv1alpha1.WorkloadSpec {
		return workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		}
	}

	chartGrandfathered = wrappersv1.BuildBasicRoleBasedGroup("chart-grandfathered", testutil.ChartContractNamespace).
		WithRoles([]workloadsv1alpha1.RoleSpec{
			func() workloadsv1alpha1.RoleSpec {
				role := wrappersv1.BuildBasicRole("worker").Obj()
				role.Workload = deprecated()
				return role
			}(),
		}).Obj()
	Expect(testutil.K8sClient.Create(testutil.Ctx, chartGrandfathered)).To(Succeed())

	chartSwitch = wrappersv1.BuildBasicRoleBasedGroup("chart-switch", testutil.ChartContractNamespace).
		WithRoles([]workloadsv1alpha1.RoleSpec{
			func() workloadsv1alpha1.RoleSpec {
				role := wrappersv1.BuildBasicRole("switcher").Obj()
				role.Workload = deprecated()
				return role
			}(),
		}).Obj()
	Expect(testutil.K8sClient.Create(testutil.Ctx, chartSwitch)).To(Succeed())

	chartSetGrandfathered = wrappersv1.BuildBasicRoleBasedGroupSet("chart-set-grandfathered", testutil.ChartContractNamespace).Obj()
	member := wrappersv1.BuildBasicRole("member").Obj()
	member.Workload = deprecated()
	chartSetGrandfathered.Spec.Template.Roles = []workloadsv1alpha1.RoleSpec{member}
	Expect(testutil.K8sClient.Create(testutil.Ctx, chartSetGrandfathered)).To(Succeed())
}

var _ = Describe("chart-rendered deprecation gate (post-#556 composition)", Ordered, func() {
	BeforeEach(func() {
		testutil.ContractWarnings.Reset()
	})

	It("rejects a new deprecated workload use submitted as v1alpha1 (conversion -> v1alpha2 gate)", func() {
		rbg := buildRBG("chart-reject-new-use")
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		}
		err := testutil.K8sClient.Create(testutil.Ctx, rbg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("rejects a v1alpha1 create whose workload key is genuinely absent (schema default -> StatefulSet -> new use)", func() {
		// kubectl-style YAML omits the workload key entirely; the v1alpha1 CRD
		// schema then defaults it to apps/v1 StatefulSet, which the gate
		// (reached through conversion) rejects as a new deprecated use. This
		// is the path the chart README's "v1alpha1 defaults to a deprecated
		// workload type" warning documents, and it is not covered in-repo.
		doc := fmt.Sprintf(`{
  "apiVersion": "workloads.x-k8s.io/v1alpha1",
  "kind": "RoleBasedGroup",
  "metadata": {"name": "chart-reject-absent", "namespace": %q},
  "spec": {
    "roles": [{
      "name": "worker",
      "replicas": 1,
      "template": {"spec": {"containers": [{"name": "nginx", "image": "nginx:latest"}]}}
    }]
  }
}`, testutil.ChartContractNamespace)
		u := &unstructured.Unstructured{}
		Expect(json.Unmarshal([]byte(doc), &u.Object)).To(Succeed())
		err := testutil.K8sClient.Create(testutil.Ctx, u)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"),
			"an omitted workload key must default to apps/v1 StatefulSet and be rejected as a new use")
	})

	It("accepts a v1alpha1 create with explicit RoleInstanceSet and returns exactly one CRD deprecation warning", func() {
		rbg := buildRBG("chart-warn")
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())
		Expect(testutil.ContractWarnings.Containing("is deprecated")).To(HaveLen(1),
			"exactly one CRD-level deprecation warning expected, got %v", testutil.ContractWarnings.All())
		Expect(testutil.ContractWarnings.Containing("removed in 0.10.0")).To(HaveLen(1))
	})

	It("returns no deprecation warning on a v1alpha2 write", func() {
		rbg := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "chart-v1a2-nowarn", Namespace: testutil.ChartContractNamespace},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{{
					Name:     "worker",
					Replicas: ptr.To(int32(1)),
					Pattern: workloadsv1alpha2.Pattern{
						StandalonePattern: &workloadsv1alpha2.StandalonePattern{
							TemplateSource: workloadsv1alpha2.TemplateSource{},
						},
					},
				}},
			},
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())
		Expect(testutil.ContractWarnings.All()).To(BeEmpty(),
			"a v1alpha2 write must not carry a v1alpha1 deprecation warning")
	})

	It("keeps an existing deprecated use writable (type-exact exemption)", func() {
		rbg := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(
			testutil.Ctx, objectKey(chartGrandfathered.Name), rbg,
		)).To(Succeed())

		rbg.Spec.Roles[0].Replicas = ptr.To(int32(3))
		Expect(testutil.K8sClient.Update(testutil.Ctx, rbg)).To(Succeed(),
			"a grandfathered deprecated use must stay updatable in the compatibility window")
	})

	It("rejects switching between deprecated workload types", func() {
		rbg := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(
			testutil.Ctx, objectKey(chartSwitch.Name), rbg,
		)).To(Succeed())

		// StatefulSet -> Deployment: both deprecated, but the switch provisions
		// a new deprecated workload and counts as a new use.
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		}
		err := testutil.K8sClient.Update(testutil.Ctx, rbg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("rejects a deprecated type in a new set template role", func() {
		set := wrappersv1.BuildBasicRoleBasedGroupSet("chart-set-new", testutil.ChartContractNamespace).Obj()
		role := wrappersv1.BuildBasicRole("member").Obj()
		role.Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		}
		set.Spec.Template.Roles = []workloadsv1alpha1.RoleSpec{role}
		err := testutil.K8sClient.Create(testutil.Ctx, set)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})
})

// chartInventoryCommand is the deprecated-role inventory documented verbatim
// in deploy/helm/rbgs/README.md ("Choosing a value").
const chartInventoryCommand = `.items[] | . as $o
  | (.spec.roles // .spec.groupTemplate.spec.roles // .spec.template.roles // [])[]
  | ((.annotations // {})["rbg.workloads.x-k8s.io/role-workload-type"]
     // (if .workload.kind then "\(.workload.apiVersion)/\(.workload.kind)" else "" end)) as $wt
  | select($wt | test("/(Deployment|StatefulSet|LeaderWorkerSet)$"))
  | "\($o.kind) \($o.metadata.namespace)/\($o.metadata.name) role=\(.name) \($wt)"`

var _ = Describe("chart README deprecated-role inventory command", Ordered, func() {
	BeforeEach(func() {
		testutil.ContractWarnings.Reset()
	})

	It("runs the documented jq verbatim against the live API server and lists exactly the deprecated roles", func() {
		// kubectl negotiates the discovery-preferred version (v1alpha2 — also
		// the storage version); emulate `kubectl get rbg,rbgs -A -o json` by
		// listing both kinds through the v1alpha2 typed client and shaping the
		// items into a single List document, TypeMeta included the way
		// kubectl serializes it.
		rbgList := &workloadsv1alpha2.RoleBasedGroupList{}
		Expect(testutil.K8sClient.List(testutil.Ctx, rbgList)).To(Succeed())
		rbgsList := &workloadsv1alpha2.RoleBasedGroupSetList{}
		Expect(testutil.K8sClient.List(testutil.Ctx, rbgsList)).To(Succeed())

		items := []map[string]interface{}{}
		for i := range rbgList.Items {
			o := rbgList.Items[i]
			o.TypeMeta = metav1.TypeMeta{Kind: "RoleBasedGroup", APIVersion: "workloads.x-k8s.io/v1alpha2"}
			raw, err := json.Marshal(&o)
			Expect(err).NotTo(HaveOccurred())
			var m map[string]interface{}
			Expect(json.Unmarshal(raw, &m)).To(Succeed())
			items = append(items, m)
		}
		for i := range rbgsList.Items {
			o := rbgsList.Items[i]
			o.TypeMeta = metav1.TypeMeta{Kind: "RoleBasedGroupSet", APIVersion: "workloads.x-k8s.io/v1alpha2"}
			raw, err := json.Marshal(&o)
			Expect(err).NotTo(HaveOccurred())
			var m map[string]interface{}
			Expect(json.Unmarshal(raw, &m)).To(Succeed())
			items = append(items, m)
		}
		doc, err := json.Marshal(map[string]interface{}{"items": items})
		Expect(err).NotTo(HaveOccurred())

		jq := exec.Command("jq", "-r", chartInventoryCommand)
		jq.Stdin = bytes.NewReader(doc)
		out, err := jq.Output()
		Expect(err).NotTo(HaveOccurred(), "jq failed on the documented inventory filter")

		lines := nonEmptyLines(string(out))
		// Expected: the two grandfathered RBGs and the grandfathered RBGSet
		// template role (all apps/v1 StatefulSet, conversion-stamped onto the
		// role-workload-type annotation); every RoleInstanceSet-shaped role
		// (chart-warn, chart-v1a2-nowarn, gate-readiness probes) is excluded.
		Expect(lines).To(ConsistOf(
			fmt.Sprintf("RoleBasedGroup %s/chart-grandfathered role=worker apps/v1/StatefulSet", testutil.ChartContractNamespace),
			fmt.Sprintf("RoleBasedGroup %s/chart-switch role=switcher apps/v1/StatefulSet", testutil.ChartContractNamespace),
			fmt.Sprintf("RoleBasedGroupSet %s/chart-set-grandfathered role=member apps/v1/StatefulSet", testutil.ChartContractNamespace),
		), "inventory output:\n%s", string(out))
	})
})

func objectKey(name string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: testutil.ChartContractNamespace}
}

func buildRBG(name string) *workloadsv1alpha1.RoleBasedGroup {
	rbg := wrappersv1.BuildBasicRoleBasedGroup(name, testutil.ChartContractNamespace).
		WithRoles([]workloadsv1alpha1.RoleSpec{
			wrappersv1.BuildBasicRole("worker").Obj(),
		}).Obj()
	// The v1alpha1 default for an unspecified workload is apps/v1
	// StatefulSet; every accepted role names RoleInstanceSet explicitly.
	for i := range rbg.Spec.Roles {
		rbg.Spec.Roles[i].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "workloads.x-k8s.io/v1alpha2",
			Kind:       "RoleInstanceSet",
		}
	}
	return rbg
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
