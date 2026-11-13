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

package statefulmode

// Verification harness for the review of KEP-543 (PR #569). Additive only; no
// production code is changed by this file.
//
// Claim under test (finding F1): the KEP's "Policy Overrides and RIS Execution
// Windows" section requires that a Segment rule whose group-level budget
// resolves to B = 0 makes each member RIS execute with maxUnavailable = 0
// ("Ignore the role value and apply `0`; only actually available surge
// increases the execution budget"), while the KEP simultaneously promises that
// Segment does not change the RIS availability-budgeting algorithm.
//
// These tests are BUG-CANARIES: they characterize the current Stateful RIS
// resolution and PASS on the code under review, proving that the existing
// RIS layer contradicts the KEP requirement. If a later implementation
// deliberately changes the floor for the Segment override path, the first
// test flips to red and must be inverted or replaced by a contract test for
// the new semantics.

import (
	"testing"

	intstrutil "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// An explicit maxUnavailable of 0 with no surge is floored to 1 by
// computeMaxUnavailable, matching the k8s Deployment rule that a rollout must
// always be able to make progress. A Segment override that writes literal 0 to
// RIS therefore does NOT yield a zero execution window today: each member RIS
// could still make one RI unavailable, defeating the group-level guarantee the
// KEP builds on (no two Segments unavailable at once when B = 0).
func TestVerifyKEP543_ExplicitZeroMaxUnavailableFloorsToOneWithoutSurge(t *testing.T) {
	set := buildSet("seg-member", 4, ptr.To(intstrutil.FromInt32(0)), ptr.To(intstrutil.FromInt32(0)))

	got, err := computeMaxUnavailable(set, 0)
	if err != nil {
		t.Fatalf("computeMaxUnavailable() error = %v", err)
	}
	if got != 1 {
		t.Fatalf("computeMaxUnavailable(explicit 0, maxSurge 0) = %d, want 1 (floor); "+
			"if this flips to 0 the KEP-543 B=0 override became implementable and this canary must be re-inverted", got)
	}
	t.Logf("observed: explicit maxUnavailable=0 with maxSurge=0 resolves to %d, not 0", got)
}

// Control: the floor is specifically tied to maxSurge == 0. With surge
// configured, an explicit 0 is preserved, so the KEP's B=0 rule IS
// implementable when Policy maxSurge is also set. This pins down the exact
// gap: B = 0 combined with omitted/zero maxSurge.
func TestVerifyKEP543_ExplicitZeroMaxUnavailablePreservedWithSurge(t *testing.T) {
	set := buildSet("seg-member", 4, ptr.To(intstrutil.FromInt32(2)), ptr.To(intstrutil.FromInt32(0)))

	got, err := computeMaxUnavailable(set, 2)
	if err != nil {
		t.Fatalf("computeMaxUnavailable() error = %v", err)
	}
	if got != 0 {
		t.Fatalf("computeMaxUnavailable(explicit 0, maxSurge 2) = %d, want 0 (no floor when surge configured)", got)
	}
}
