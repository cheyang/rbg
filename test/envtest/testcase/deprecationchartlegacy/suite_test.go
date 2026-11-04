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

package deprecationchartlegacy

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

// TestChartLegacyBinaryContract runs the chart-rendered webhook
// configurations (PR #552's exact admission wiring) against a manager that
// registers ONLY the pre-#524 handler set — the set the chart's pinned
// default image v0.9.0-d88b3186 serves, since that image predates #524.
//
// This is the reproduction harness for finding F1: with the default image
// rolled out (or during the rollout window of a helm upgrade), the two
// failurePolicy=Fail v1alpha1 deprecation entries point at endpoints the
// serving binary does not register. A 404 from the webhook server is an
// admission *error*, and fail-closed turns it into a hard rejection.
func TestChartLegacyBinaryContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Chart Deprecation Legacy Binary Suite")
}

var _ = BeforeSuite(func() {
	testutil.SetupChartContractTestEnv(true, nil)
})

var _ = AfterSuite(func() {
	testutil.TeardownTestEnv()
})
