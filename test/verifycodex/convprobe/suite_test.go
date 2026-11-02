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

// Package convprobe is a reviewer-B (Codex) round-3 verification probe for
// PR #524. It reuses the PR's own v1alpha1 contract-suite setup
// (testutil.SetupV1alpha1ContractTestEnv) and asserts that the environment
// really stores v1alpha1 writes through the production v1alpha1 -> v1alpha2
// conversion — the fix for review advisory C4431130427 (schemes were
// registered after TestEnv.Start, so envtest stripped CRD conversion).
//
// The probe is additive: it imports the PR's testutil but changes nothing.
// It must compile against both the round-2 head (unexported contractNamespace,
// late scheme registration) and the round-3 head, so the namespace is a
// literal here on purpose.
package convprobe

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

func TestConversionProbe(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "v1alpha1 conversion engagement probe")
}

var _ = BeforeSuite(func() {
	testutil.SetupV1alpha1ContractTestEnv()
})

var _ = AfterSuite(func() {
	testutil.TeardownTestEnv()
})
