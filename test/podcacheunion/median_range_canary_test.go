//go:build podcacheunion

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

package podcacheunion

import (
	"math"
	"testing"
)

// R2-F4 (inverted canary -> contract) — round 2 booked medianRange reporting
// the upper-middle value for even-sized groups (sorted[len/2]) as a bug-canary.
// PR #530 commit d1c331ae (round 3) fixed it by averaging the two middle
// values, so the canary flipped red exactly as designed (observed in
// results/round3/reverify). Per the polarity rule the assertion is now
// inverted: this test PINS the averaged median and must stay green. A flip to
// red means even-sized groups again report the upper-middle value (or a new
// convention), which would change campaign summaries for custom ROUNDS or
// partially complete campaign directories.
func TestMedianRangeUpperMiddleForEvenGroups(t *testing.T) {
	odd, low, high := medianRange([]float64{3, 1, 2})
	if odd != 2 || low != 1 || high != 3 {
		t.Fatalf("odd group: got (%v,%v,%v), want median 2, range 1..3", odd, low, high)
	}
	even, _, _ := medianRange([]float64{1, 2})
	if math.Abs(even-1.5) > 1e-9 {
		t.Fatalf(
			"even group no longer averages the two middle values: got %v; R2-F4 was fixed in "+
				"d1c331ae (medianRange averages even groups) and this contract pins that behavior",
			even,
		)
	}
	single, _, _ := medianRange([]float64{42})
	if single != 42 {
		t.Fatalf("single-sample group: got %v, want 42", single)
	}
}
