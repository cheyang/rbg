/*
Copyright 2025 The RBG Authors.

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

// Verification harness for review findings on PR sgl-project/rbg#413.
// Reviewer artifact; not intended to be merged as-is. See
// docs/verification/pr413-legacy-workloads/README.md.
package main

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func vfyByObjectKinds(t *testing.T, enableLegacy bool) map[string]bool {
	t.Helper()
	opts := cacheOptions(enableLegacy)
	seen := map[string]bool{}
	for obj := range opts.ByObject {
		switch obj.(type) {
		case *appsv1.StatefulSet:
			seen["StatefulSet"] = true
		case *appsv1.Deployment:
			seen["Deployment"] = true
		case *corev1.Service:
			seen["Service"] = true
		default:
			seen["other"] = true
		}
	}
	return seen
}

// ---------------------------------------------------------------------------
// F5 [CANARY] -- dropping the ByObject entry drops the label selector, it does
// not prevent the informer.
//
// cacheOptions(false) removes the Deployment/StatefulSet ByObject entries
// (cmd/rbgs/main.go:637-648). Those entries were what bounded the informers to
// objects carrying the rbg group-name label. With no entry and no
// DefaultLabelSelector, any code path that reads a Deployment/StatefulSet
// through the cached client would lazily start an UNFILTERED, cluster-wide
// informer -- which needs exactly the deployments/statefulsets list+watch RBAC
// that the chart removes when the flag is off.
//
// On head 59e384d5 the reject in getOrCreateWorkloadReconciler means no such
// read path is currently reachable, so this is LATENT rather than live. Keeping
// the selector regardless of the flag costs nothing and removes the footgun.
//
// This test PASSES today (documenting the dropped selector). It FLIPS TO RED if
// a bounded selector or an explicit informer block is added -- then invert it.
// ---------------------------------------------------------------------------
func TestVerifyPR413_F5_CacheSelectorDroppedWhenLegacyDisabled_Canary(t *testing.T) {
	enabled := vfyByObjectKinds(t, true)
	for _, kind := range []string{"StatefulSet", "Deployment", "Service"} {
		if !enabled[kind] {
			t.Fatalf("HARNESS DOES NOT BITE: cacheOptions(true) is missing a bounded %s entry", kind)
		}
	}

	disabled := vfyByObjectKinds(t, false)
	if !disabled["Service"] {
		t.Fatal("HARNESS DOES NOT BITE: cacheOptions(false) unexpectedly dropped the Service entry too")
	}
	if disabled["Deployment"] || disabled["StatefulSet"] {
		t.Fatal("CANARY FLIPPED: cacheOptions(false) now keeps a bounded entry for " +
			"Deployment/StatefulSet. The unfiltered-informer footgun is gone -- invert this test.")
	}

	// No global fallback selector either, so an informer started for these types
	// would be cluster-wide.
	if opts := cacheOptions(false); opts.DefaultLabelSelector != nil {
		t.Fatal("CANARY FLIPPED: a DefaultLabelSelector now bounds all informers -- invert this test.")
	}

	t.Log("observed: cacheOptions(false) drops the group-name selector for Deployment/StatefulSet " +
		"and sets no DefaultLabelSelector; any cached read of those types would start a " +
		"cluster-wide informer (latent on this head -- no reachable read path)")
}

var _ client.Object = (*appsv1.Deployment)(nil)
