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

// R2-F4 (canary) — medianRange reports the upper-middle value for even-sized
// groups (sorted[len/2]), not the average of the two middle values. The
// published KEP-284 numbers use three rounds (odd), so they are true medians;
// the behavior only surfaces when a campaign directory holds an even number
// of runs per group (custom ROUNDS, or summarizing a partially complete
// campaign). Canary polarity: PASSES while the current behavior is present;
// if the author changes medianRange to average even groups (or to document
// the convention), this flips red and must be inverted.
func TestMedianRangeUpperMiddleForEvenGroups(t *testing.T) {
	odd, low, high := medianRange([]float64{3, 1, 2})
	if odd != 2 || low != 1 || high != 3 {
		t.Fatalf("odd group: got (%v,%v,%v), want median 2, range 1..3", odd, low, high)
	}
	even, _, _ := medianRange([]float64{1, 2})
	if math.Abs(even-2) > 1e-9 {
		t.Fatalf(
			"even group no longer reports the upper-middle value: got %v; if medianRange now "+
				"averages the two middle values (the R2-F4 fix), invert this canary into a contract "+
				"test for the averaged median",
			even,
		)
	}
}
