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

package deprecationv1a1helm

// Verification harness, PR #552 round 2 (reviewer: codex). The chart README's
// "Choosing a value" section tells operators to decide the
// deprecated-workload-types toggle from the output of an exact command:
//
//	kubectl get rbg,rbgs -A -o json | jq -r '...'
//
// "empty output means false is safe". These specs run that documented pipeline
// end-to-end against envtest with one seeded deprecated RoleBasedGroup, one
// seeded deprecated RoleBasedGroupSet, and one modern control, so a wrong
// field path or a wrong version assumption cannot ship silently.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"

	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

// inventoryJQ is the exact jq program from deploy/helm/rbgs/README.md
// ("Choosing a value"). Keep byte-identical with the doc; if the doc changes,
// update both.
const inventoryJQ = `.items[] | . as $o
  | (.spec.roles // .spec.groupTemplate.spec.roles // [])[]
  | (.annotations["rbg.workloads.x-k8s.io/role-workload-type"] // "") as $wt
  | select($wt | test("/(Deployment|StatefulSet|LeaderWorkerSet)$"))
  | "\($o.kind) \($o.metadata.namespace)/\($o.metadata.name) role=\(.name) \($wt)"`

// writeEnvtestKubeconfig renders a client-certificate kubeconfig for the
// envtest API server so the documented kubectl command runs verbatim.
func writeEnvtestKubeconfig() string {
	cfg := testutil.Cfg
	kc := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: envtest
  cluster:
    server: %s
    certificate-authority-data: %s
contexts:
- name: envtest
  context:
    cluster: envtest
    user: envtest
current-context: envtest
users:
- name: envtest
  user:
    client-certificate-data: %s
    client-key-data: %s
`, cfg.Host,
		base64.StdEncoding.EncodeToString(cfg.TLSClientConfig.CAData),
		base64.StdEncoding.EncodeToString(cfg.TLSClientConfig.CertData),
		base64.StdEncoding.EncodeToString(cfg.TLSClientConfig.KeyData))
	path := filepath.Join(GinkgoT().TempDir(), "envtest.kubeconfig")
	Expect(os.WriteFile(path, []byte(kc), 0o600)).To(Succeed())
	return path
}

var _ = Describe("chart README deprecated-role inventory command", Ordered, func() {
	BeforeEach(func() {
		if suiteMode() == "premise" {
			Skip("docs verification runs in contract mode")
		}
	})

	It("serves workloads.x-k8s.io with v1alpha2 as the discovery-preferred version", func() {
		// The README's jq paths (.spec.roles / .spec.groupTemplate.spec.roles)
		// are the v1alpha2 shapes; they are correct for `kubectl get` output
		// only if discovery negotiates the storage version v1alpha2.
		disco, err := discovery.NewDiscoveryClientForConfig(testutil.Cfg)
		Expect(err).NotTo(HaveOccurred())
		data, err := disco.RESTClient().Get().
			AbsPath("/apis/workloads.x-k8s.io").DoRaw(testutil.Ctx)
		Expect(err).NotTo(HaveOccurred())
		var group metav1.APIGroup
		Expect(json.Unmarshal(data, &group)).To(Succeed())
		GinkgoWriter.Printf("discovery preferredVersion: %s (versions: %v)\n",
			group.PreferredVersion.GroupVersion, group.Versions)
		Expect(group.PreferredVersion.GroupVersion).To(Equal("workloads.x-k8s.io/v1alpha2"),
			"the README inventory command's field paths assume kubectl negotiates v1alpha2")
	})

	It("lists every seeded deprecated role and not the modern control", func() {
		kubeconfig := writeEnvtestKubeconfig()
		get := exec.Command("kubectl", "--kubeconfig", kubeconfig, "get", "rbg,rbgs", "-A", "-o", "json")
		listOut, err := get.Output()
		Expect(err).NotTo(HaveOccurred(), "the documented kubectl get must succeed against the live API server")

		jq := exec.Command("jq", "-r", inventoryJQ)
		jq.Stdin = strings.NewReader(string(listOut))
		inventory, err := jq.Output()
		Expect(err).NotTo(HaveOccurred(), "the documented jq pipeline must parse the kubectl output")
		lines := strings.TrimSpace(string(inventory))
		GinkgoWriter.Printf("inventory output:\n%s\n", lines)

		Expect(lines).To(ContainSubstring("RoleBasedGroup %s/%s", helmContractNamespace, seededRBGName),
			"a deprecated RoleBasedGroup role must appear in the inventory")
		Expect(lines).To(ContainSubstring("RoleBasedGroupSet %s/%s", helmContractNamespace, seededRBGSetName),
			"a deprecated RoleBasedGroupSet template role must appear in the inventory")
		Expect(lines).NotTo(ContainSubstring(seededModernName),
			"a role already on RoleInstanceSet must not be listed")
	})
})
