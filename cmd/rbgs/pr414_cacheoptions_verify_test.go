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

// Reviewer verification harness for PR sgl-project/rbg#414, head 66a2500a.
// See docs/verification/pr414-v1alpha1-compat-flag/README.md.
package main

import (
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// p414Lookup reports whether byObject has an entry for the same concrete type as
// want, and whether that entry carries a label selector.
func p414Lookup(byObject map[client.Object]cache.ByObject, want client.Object) (present, labelled bool) {
	wantType := reflect.TypeOf(want)
	for obj, cfg := range byObject {
		if reflect.TypeOf(obj) == wantType {
			return true, cfg.Label != nil
		}
	}
	return false, false
}

// ---------------------------------------------------------------------------
// F9 [CANARY] -- cacheOptions(false) does not narrow the Deployment/StatefulSet
// informers, it removes their ByObject entries entirely.
//
// In controller-runtime, ByObject is per-type *configuration*, not an allowlist
// of what may be cached. Dropping the entry does not stop a cached informer
// from being created for that type -- it stops it being LABEL-FILTERED. If any
// code path ever does a cached Get/List of a Deployment or StatefulSet with
// compat off, controller-runtime lazily starts an UNFILTERED, cluster-wide
// informer for it. And because this PR also removes list/watch on those types
// from the ClusterRole, that informer cannot even establish its watch: it 403s
// and the caller blocks on a cache that will never sync.
//
// So the two halves of the feature disagree about what "disabled" means: the
// RBAC half says "these types are unreachable", the cache half says "these
// types are reachable, just unbounded". Keeping the existing label selector
// registered in BOTH modes costs nothing and removes the trap -- that selector
// was the only thing bounding the informer.
//
// LATENT on 66a2500a: with compat off, Owns() is not registered, Reconcile
// stops at handleLegacyWorkloads, and svc_reconciler's StatefulSet Get is only
// reached for a StatefulSet-typed role -- so there is no reachable cached read
// today. This test asserts the missing selector, not a live failure. It FLIPS
// TO RED as soon as a selector (or a DefaultLabelSelector) is registered
// regardless of the flag -- invert it then.
// ---------------------------------------------------------------------------
func TestVerifyPR414_F9_CacheSelectorDroppedWhenCompatDisabled_Canary(t *testing.T) {
	on := cacheOptions(true)
	off := cacheOptions(false)

	// Control: Service is not gated by the flag and must stay label-selected in
	// both modes. If this fails, the test is not reading what it thinks it is.
	svcOn, svcLabelOn := p414Lookup(on.ByObject, &corev1.Service{})
	svcOff, svcLabelOff := p414Lookup(off.ByObject, &corev1.Service{})
	if !(svcOn && svcLabelOn && svcOff && svcLabelOff) {
		t.Fatalf("HARNESS DOES NOT BITE: Service must be label-selected in both modes "+
			"(compat=true present=%v labelled=%v; compat=false present=%v labelled=%v)",
			svcOn, svcLabelOn, svcOff, svcLabelOff)
	}

	for _, tc := range []struct {
		name string
		obj  client.Object
	}{
		{"Deployment", &appsv1.Deployment{}},
		{"StatefulSet", &appsv1.StatefulSet{}},
	} {
		presentOn, labelledOn := p414Lookup(on.ByObject, tc.obj)
		if !presentOn || !labelledOn {
			t.Fatalf("HARNESS DOES NOT BITE: %s must be label-selected with compat enabled "+
				"(present=%v labelled=%v)", tc.name, presentOn, labelledOn)
		}

		presentOff, labelledOff := p414Lookup(off.ByObject, tc.obj)
		if presentOff && labelledOff {
			t.Fatalf("CANARY FLIPPED for %s: a label selector is registered with compat "+
				"disabled. The unbounded-informer trap is closed -- invert this test.", tc.name)
		}
		t.Logf("observed: with compat=false, %s has no ByObject entry (present=%v, labelled=%v). "+
			"A cached read would start an unfiltered cluster-wide informer, which then 403s "+
			"because this PR also drops list/watch on %s from the ClusterRole.",
			tc.name, presentOff, labelledOff, tc.name)
	}

	// Pin the shape of the change: exactly the two gated entries are lost.
	if got := len(on.ByObject) - len(off.ByObject); got != 2 {
		t.Errorf("expected exactly 2 ByObject entries to be dropped when compat is disabled, "+
			"got %d (compat=true:%d, compat=false:%d)", got, len(on.ByObject), len(off.ByObject))
	}
}
