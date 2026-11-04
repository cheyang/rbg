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

// Package deprecationv1a1helm is the PR #552 verification harness. It proves that
// the Helm-rendered webhook configuration (deploy/helm/rbgs/templates/webhook/
// manifests.yaml) drives the same v1alpha1 deprecation admission behavior that
// #524's shipped config/webhook/manifests.yaml provides: the chart bytes are
// installed into envtest verbatim (only clientConfig is rewritten to the local
// test server, exactly as a cluster would after the controller patches caBundle),
// while the webhook handlers are the production registrations from cmd/rbgs.
//
// Modes (env RBGS_HELM_SUITE_MODE):
//
//	contract (default): assert the gate is active (PR head behavior).
//	premise:            assert the gate is ABSENT — used with RBGS_CHART_DIR
//	                    pointing at the base branch's chart to prove the problem
//	                    the PR fixes (chart installs shipped no v1alpha1 gate).
//
// RBGS_CHART_DIR overrides the chart directory (default: deploy/helm/rbgs).
package deprecationv1a1helm

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestV1alpha1DeprecationHelmContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "v1alpha1 Deprecation Helm Chart Contract Suite")
}

var _ = BeforeSuite(func() {
	setupHelmChartContractEnv()
})

var _ = AfterSuite(func() {
	teardownEnv()
})
