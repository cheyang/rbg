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

package workloads

// KEP-576 (PR #578) review harness — Reviewer A (Claude), downgrade-matrix facts.
//
// Canaries for the KEP's downgrade claims: an OLD controller (this code, the
// shipped v0.7/v0.8 behavior) that meets an unknown `discovery-config-mode`
// value (`refine-compact`) re-pins it to `legacy` when the RBG has status
// (post-migration / new-but-reconciled RBGs) and to `refine` when it has no
// status and no per-role ConfigMap. Green = the KEP's downgrade-matrix
// mechanics description is faithful to the shipped code.

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

func TestKEP576Claude_OldControllerRepinsUnknownMode(t *testing.T) {
	cases := []struct {
		name       string
		hasStatus  bool
		wantRepine string
	}{
		{
			// Downgrade matrix: "to `legacy` when the RBG has status (the common
			// case)" — post-migration and already-reconciled new RBGs.
			name:       "unknown mode with status repins to legacy",
			hasStatus:  true,
			wantRepine: "legacy",
		},
		{
			// Downgrade matrix: "to `refine` when the RBG has no status yet" —
			// the old controller then (re)creates the `<rbg>` enumeration.
			name:       "unknown mode without status repins to refine",
			hasStatus:  false,
			wantRepine: "refine",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = workloadsv1alpha2.AddToScheme(scheme)
			_ = appsv1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			rbg := wrappersv2.BuildBasicRoleBasedGroup("g", "default").Obj()
			rbg.SetDiscoveryConfigMode(constants.DiscoveryConfigMode("refine-compact"))
			if tc.hasStatus {
				rbg.Status.ObservedGeneration = 1
			}

			client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(rbg).Build()
			reconciler := &RoleBasedGroupReconciler{client: client, scheme: scheme}

			_, err := reconciler.ensureDiscoveryConfigMode(context.Background(), rbg)
			if err != nil {
				t.Fatalf("ensureDiscoveryConfigMode() error = %v", err)
			}
			if got := string(rbg.GetDiscoveryConfigMode()); got != tc.wantRepine {
				t.Fatalf("re-pinned mode = %q, want %q", got, tc.wantRepine)
			}
		})
	}
}
