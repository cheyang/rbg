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

// Reviewer-private harness: LeaderWorkerSet -> LeaderWorkerPattern migration parity (finding P1).
//
// Context: the roadmap removes Deployment / StatefulSet / LeaderWorkerSet support entirely,
// leaving RoleInstanceSet + the v1alpha2 pattern family. For the LWS case the API-level story
// is good -- RBG only ever exposed Size + two template patches, so almost nothing user-facing
// is lost. The risk is NOT in the API, it is in the POD ENVIRONMENT, which is a contract with
// the user's application rather than with the API server:
//
//	LWS-backed role:             pods get LWS_LEADER_ADDRESS / LWS_GROUP_SIZE / LWS_WORKER_INDEX,
//	                             injected by the LWS controller itself (lws_reconciler.go:270
//	                             deliberately does NOT request "lwp_env").
//	RoleInstanceSet-backed role: pods get RBG_LEADER_ADDRESS / RBG_SIZE / RBG_INDEX, injected by
//	                             RBG (roleinstanceset_reconciler.go:379 and :398 request
//	                             "lwp_env").
//
// Same semantics, different NAMES. An application that reads LWS_LEADER_ADDRESS keeps starting
// and then fails at runtime -- no admission error, no reconcile error, nothing in the RBG
// status. RBG already declares DeprecatedEnvLwsLeaderAddress / DeprecatedEnvLwsWorkerIndex /
// DeprecatedEnvLwsGroupSize in api/workloads/v1alpha1/constant.go, but they are referenced
// nowhere, so no compatibility shim exists.
package discovery

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// The three variables the LWS controller adds to every container in a LeaderWorkerSet.
// Values copied from sigs.k8s.io/lws@v0.7.0 api/leaderworkerset/v1/leaderworkerset_types.go
// (LwsLeaderAddress, LwsGroupSize, LwsWorkerIndex) -- referenced as literals so this test does
// not depend on the LWS module surviving the removal.
var lwsInjectedEnvNames = []string{
	"LWS_LEADER_ADDRESS",
	"LWS_GROUP_SIZE",
	"LWS_WORKER_INDEX",
}

// TestVerifyPR416_P1_MigrationDropsTheLWSEnvContract is a CONTRACT test.
//
// Claim: migrating a LeaderWorkerSet-backed role to RoleInstanceSet + LeaderWorkerPattern
// silently removes the LWS_* environment variables from user containers, and RBG injects no
// aliases for them, so any application reading the old names breaks at container start.
//
// Intended behaviour (asserted here): during the deprecation window the RoleInstanceSet path
// should ALSO inject the deprecated LWS_* names as aliases, so a workload that has not been
// updated keeps working while the operator migrates. RBG already has the constants for this.
//
// Expected on the PR head: RED (no aliases are injected).
func TestVerifyPR416_P1_MigrationDropsTheLWSEnvContract(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = workloadsv1alpha2.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "infer", Namespace: "default"},
	}
	// A role on the NEW path: RoleInstanceSet + LeaderWorkerPattern.
	role := &workloadsv1alpha2.RoleSpec{
		Name: "worker",
		Annotations: map[string]string{
			constants.RoleWorkloadTypeAnnotationKey: constants.RoleInstanceSetWorkloadType,
		},
	}
	podSpec := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "main", Image: "vllm"}},
		},
	}

	injector := NewDefaultInjector(scheme, fake.NewClientBuilder().WithScheme(scheme).Build())
	// Reproduce exactly what roleinstanceset_reconciler.go does for a LeaderWorkerPattern role
	// (injectors: config, sidecar, common_env, lwp_env -- the two env ones matter here).
	if err := injector.InjectEnv(context.Background(), podSpec, rbg, role); err != nil {
		t.Fatalf("InjectEnv() error = %v", err)
	}
	if err := injector.InjectLeaderWorkerSetEnv(context.Background(), podSpec, rbg, role); err != nil {
		t.Fatalf("InjectLeaderWorkerSetEnv() error = %v", err)
	}

	present := map[string]bool{}
	for _, e := range podSpec.Spec.Containers[0].Env {
		present[e.Name] = true
	}
	t.Logf("env injected on the RoleInstanceSet+LeaderWorkerPattern path: %v", keysOf(present))

	// HARNESS BITE: the replacement variables must be there, otherwise this test is measuring
	// nothing and the whole comparison is void.
	for _, replacement := range []string{
		constants.EnvRBGLeaderAddress, constants.EnvRBGSize, constants.EnvRBGIndex,
	} {
		if !present[replacement] {
			t.Fatalf("HARNESS PROBLEM: replacement variable %s was not injected on the new path;"+
				" this test cannot say anything about parity", replacement)
		}
	}

	var missing []string
	for _, name := range lwsInjectedEnvNames {
		if !present[name] {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		t.Errorf("P1 REPRODUCED: the RoleInstanceSet+LeaderWorkerPattern path injects the"+
			" replacements (%s, %s, %s) but NOT the names LWS used to provide (%v)."+
			" A container that reads the old names keeps starting and fails at runtime:"+
			" no admission error, no reconcile error, nothing on the RBG status."+
			" RBG declares DeprecatedEnvLws* constants for exactly this but references them"+
			" nowhere, so no alias shim exists.",
			constants.EnvRBGLeaderAddress, constants.EnvRBGSize, constants.EnvRBGIndex, missing)
		return
	}
	t.Logf("P1 FIXED: deprecated LWS_* aliases are injected alongside the RBG_* names")
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
