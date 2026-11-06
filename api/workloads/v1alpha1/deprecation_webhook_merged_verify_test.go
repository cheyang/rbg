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

package v1alpha1

// Verification harness for PR #552 round 2 (reviewer: codex). Additive only;
// asserts the merged warning-only handler's contract. On-the-wire coverage
// (path registration + admission chain) lives in
// test/envtest/testcase/deprecationv1a1helm.

import (
	"context"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Contract: the merged warning-only endpoint must allow every request and
// attach the v1alpha1 deprecation warning, without decoding the object (the
// response is identical for all four covered kinds).
func TestV1alpha1WarningHandlerAllowsWithWarning(t *testing.T) {
	resp := v1alpha1WarningHandler{}.Handle(context.Background(), admission.Request{})
	if !resp.Allowed {
		t.Fatalf("merged warning handler must allow every request, got allowed=%v", resp.Allowed)
	}
	found := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, "v1alpha1 is deprecated") {
			found = true
		}
	}
	if !found {
		t.Fatalf("merged warning handler returned no deprecation warning: warnings=%v", resp.Warnings)
	}
}

// Contract: the warning text is the same constant the shipped admission chain
// surfaced through the four per-kind validators before the merge (users and
// docs quote this string).
func TestV1alpha1WarningHandlerTextMatchesPerKindPredecessors(t *testing.T) {
	resp := v1alpha1WarningHandler{}.Handle(context.Background(), admission.Request{})
	if len(resp.Warnings) != 1 || resp.Warnings[0] != v1alpha1DeprecationWarning {
		t.Fatalf("warnings = %v, want exactly the shared v1alpha1DeprecationWarning", resp.Warnings)
	}
	if !strings.Contains(v1alpha1DeprecationWarning, "removed in 0.10.0") {
		t.Fatalf("deprecation warning no longer names the removal release: %q", v1alpha1DeprecationWarning)
	}
}
