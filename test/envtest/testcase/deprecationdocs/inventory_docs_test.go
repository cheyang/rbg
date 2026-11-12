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

package deprecationdocs

// The chart README's "Choosing a value" section documents an exact inventory
// pipeline:
//
//	kubectl get rbg,rbgs -A -o json | jq -r '<program>'
//
// and states that empty output means no role still selects a deprecated
// workload type. These specs run that pipeline verbatim against the envtest
// API server, over the objects the suite seeded (a deprecated v1alpha1
// RoleBasedGroup, a deprecated v1alpha1 RoleBasedGroupSet, and a modern
// control), so a wrong field path or a wrong served-version assumption cannot
// ship silently. The jq program is extracted from the README itself.

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/discovery"
)

func writeEnvtestKubeconfig() string {
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
	path := filepath.Join(GinkgoT().TempDir(), "kubeconfig")
	ExpectWithOffset(1, os.WriteFile(path, []byte(kc), 0o600)).To(Succeed())
	return path
}

// extractInventoryJQ mirrors the extractor in test/chart/inventory_command_test.go.
func extractInventoryJQ() string {
	_, thisFile, _, _ := runtimeCaller()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "deploy", "helm", "rbgs", "README.md"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	const marker = "kubectl get rbg,rbgs -A -o json | jq -r '"
	text := string(data)
	i := strings.Index(text, marker)
	ExpectWithOffset(1, i).To(BeNumerically(">=", 0), "chart README no longer documents the inventory command")
	rest := text[i+len(marker):]
	end := strings.Index(rest, "'\n")
	ExpectWithOffset(1, end).To(BeNumerically(">", 0), "inventory jq program is not closed by a single quote")
	return rest[:end]
}

func runtimeCaller() (uintptr, string, int, bool) { return runtime.Caller(0) }

var _ = Describe("The documented deprecated-role inventory command", func() {
	It("runs verbatim against the API server and lists exactly the deprecated roles", func() {
		if _, err := exec.LookPath("kubectl"); err != nil {
			Skip("kubectl not installed")
		}
		if _, err := exec.LookPath("jq"); err != nil {
			Skip("jq not installed")
		}

		By("confirming discovery prefers the storage version the documented jq paths assume")
		disco, err := discovery.NewDiscoveryClientForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		rl, err := disco.ServerResourcesForGroupVersion("workloads.x-k8s.io/v1alpha2")
		Expect(err).NotTo(HaveOccurred(), "v1alpha2 must remain served")
		Expect(rl.APIResources).NotTo(BeEmpty())

		By("running the documented pipeline verbatim")
		kc := writeEnvtestKubeconfig()
		pipeline := fmt.Sprintf("kubectl get rbg,rbgs -A -o json | jq -r '%s'", extractInventoryJQ())
		cmd := exec.Command("bash", "-c", pipeline)
		cmd.Env = append(os.Environ(), "KUBECONFIG="+kc)
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "documented pipeline failed: %s", out)

		var lines []string
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l != "" {
				lines = append(lines, l)
			}
		}
		sort.Strings(lines)
		joined := strings.Join(lines, "\n")

		Expect(joined).To(ContainSubstring("RoleBasedGroup %s/%s role=test-role %s", docsNamespace, seededRBGName, stsWorkload),
			"the deprecated v1alpha1 RoleBasedGroup seed must be listed; got:\n%s", joined)
		Expect(joined).To(ContainSubstring("RoleBasedGroupSet %s/%s role=test-role %s", docsNamespace, seededRBGSetName, stsWorkload),
			"the deprecated v1alpha1 RoleBasedGroupSet template role must be listed; got:\n%s", joined)
		Expect(joined).NotTo(ContainSubstring(seededModernName),
			"the modern RoleInstanceSet control must not be listed; got:\n%s", joined)
	})
})
