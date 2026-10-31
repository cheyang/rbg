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

package main

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// P0 — KEP-284 premise check (contract test, runs against the PR base code).
//
// Issue sgl-project/rbg#284 claims the operator's informer cache retains the
// spec and status of every Pod in the cluster because cacheOptions() attaches
// a group-name label selector to Service, StatefulSet and Deployment, but not
// to Pods. A red result here IS the reproduction: the Pod informer is started
// unfiltered and its retained memory scales with total cluster Pods, not with
// RBG-managed Pods.
//
// Polarity: contract. RED on the base branch == premise CONFIRMED.
// It turns green only when an implementation (KEP-284 or any other) actually
// constrains the Pod cache.
func TestCacheOptionsConstrainsPodCache(t *testing.T) {
	opts := cacheOptions()

	for obj, byObject := range opts.ByObject {
		if _, ok := obj.(*corev1.Pod); !ok {
			continue
		}
		if byObject.Label == nil || byObject.Label.Empty() {
			t.Errorf("Pod cache entry exists but carries no label selector: %+v", byObject)
			return
		}
		t.Logf("Pod cache is filtered by %q — premise refuted", byObject.Label.String())
		return
	}

	t.Errorf(
		"P0 premise CONFIRMED: cacheOptions() configures %d ByObject entries but none for *corev1.Pod; "+
			"the operator's Pod informer retains every Pod in the cluster",
		len(opts.ByObject),
	)
}
