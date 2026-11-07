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

// Reviewer verification harness for PR #556 (additive; not part of the PR).
//
// The PR removes the v1alpha1 admission webhooks on the claim that two
// mechanisms already cover them:
//
//	(a) the CRD deprecationWarning, returned by the API server on every
//	    v1alpha1 request with no webhook dependency, and
//	(b) the v1alpha2 validating webhooks, whose default matchPolicy=Equivalent
//	    routes v1alpha1 writes through the conversion webhook into the
//	    v1alpha2 deprecated-workload-type gate.
//
// This suite runs the post-PR production shape — shipped CRDs + shipped
// webhook manifests + production webhook registrations, no v1alpha1 admission
// webhooks — and asserts both claims end to end through a real API server.
package v1a1gate

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
)

func TestV1alpha1Gate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "v1alpha1 Through-Conversion Gate Suite (PR556 verify)")
}

// legacyRBG is the pre-gate object the grandfathering specs update: it already
// uses a deprecated workload type before the shipped gate is installed, the
// same position an upgraded 0.8.x cluster is in.
var legacyRBG *workloadsv1alpha1.RoleBasedGroup

var _ = BeforeSuite(func() {
	testutil.SetupV1alpha1GateTestEnv()

	// Seed the legacy object while no admission gate is installed, mirroring
	// the production upgrade order (objects predate the gate).
	legacyRBG = wrappersv1.BuildBasicRoleBasedGroup("legacy-rbg", testutil.V1alpha1GateNamespace).
		WithRoles([]workloadsv1alpha1.RoleSpec{legacyStatefulV1alpha1Role("worker")}).Obj()
	Expect(testutil.K8sClient.Create(testutil.Ctx, legacyRBG)).To(Succeed())

	By("installing the shipped admission webhook configurations")
	testutil.InstallShippedAdmissionWebhooks()
	testutil.WaitForV1alpha2Gate()
})

var _ = AfterSuite(func() {
	testutil.TeardownTestEnv()
})

func legacyStatefulV1alpha1Role(name string) workloadsv1alpha1.RoleSpec {
	role := wrappersv1.BuildBasicRole(name).Obj()
	role.Workload = workloadsv1alpha1.WorkloadSpec{
		APIVersion: "apps/v1",
		Kind:       "StatefulSet",
	}
	return role
}

func roleInstanceSetV1alpha1Role(name string) workloadsv1alpha1.RoleSpec {
	role := wrappersv1.BuildBasicRole(name).Obj()
	role.Replicas = ptr.To(int32(1))
	return role
}
