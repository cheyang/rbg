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
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

// TestChartDeprecationContract runs against envtest with the production
// manager webhook registrations and the *chart-rendered* webhook
// configurations (helm template output) installed — the exact admission
// wiring a Helm install of PR #552 produces with a post-#524 image.
//
// Round 3: the chart renders only the v1alpha2 webhook entries; v1alpha1
// writes are gated through conversion (matchPolicy: Equivalent) and warned
// by the CRD deprecationWarning. It pins the chart README's documented gate
// behavior end-to-end: the new-use rejection (explicit and schema-defaulted),
// the type-exact compatibility exemption, the deprecated-type switch
// rejection, the sole CRD-level warning, and the deprecated-role inventory.
func TestChartDeprecationContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Chart Deprecation Contract Suite")
}

var _ = BeforeSuite(func() {
	testutil.SetupChartContractTestEnv(seedGrandfatheredObjects)
})

var _ = AfterSuite(func() {
	testutil.TeardownTestEnv()
})
