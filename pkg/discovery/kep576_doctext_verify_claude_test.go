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

package discovery

// KEP-576 (PR #578) doc-contract tests — Reviewer A (Claude).
//
// Polarity: CONTRACT tests on the KEP document itself. RED on the PR head is
// the reproduction of the doc finding; they flip GREEN when the KEP text is
// corrected. On the merge-base the KEP file does not exist, so the tests pass
// vacuously (nothing to check) — that is the base-pass/head-fail differential.
//
// If the KEP is reworded so a substring check no longer matches, that is a
// Harness-update, not a pass: re-read the doc and re-anchor the assertion on
// the corrected wording.

import (
	"os"
	"strings"
	"testing"
)

const kep576Path = "../../keps/576-compact-discovery-configmap/README.md"

func kep576Text(t *testing.T) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(kep576Path)
	if os.IsNotExist(err) {
		return "", false // merge-base: KEP not introduced yet -> vacuous pass
	}
	if err != nil {
		t.Fatalf("read %s: %v", kep576Path, err)
	}
	return string(data), true
}

// F1: the Shipped Baseline must not claim the serialized `roles` map is in
// spec declaration order. Today's builder emits `roles` as a Go map, so
// sigs.k8s.io/yaml serializes it with alphabetically sorted keys (only
// `group.roles` is spec order) — see
// TestKEP576Claude_ShippedRolesMapOrderIsSortedNotSpecOrder. A renderer that
// "preserves the shipped enumeration order" by emitting roles in spec order
// fails the KEP's own order-sensitive equality gate for any RBG whose role
// names are not already alphabetical.
func TestKEP576Claude_DocMustNotClaimRolesMapIsSpecOrder(t *testing.T) {
	text, ok := kep576Text(t)
	if !ok {
		t.Skip("KEP-576 not present on this revision (base)")
	}
	if strings.Contains(text, "and roles in spec order") {
		t.Fatal("F1: Shipped Baseline claims \"and roles in spec order\" — the shipped " +
			"`roles` map is serialized with sorted keys, not spec order " +
			"(TestKEP576Claude_ShippedRolesMapOrderIsSortedNotSpecOrder). Reword the " +
			"baseline and pin what the renderer must emit for the roles mapping.")
	}
}

// F3: the Committed Membership source-of-truth list must not present the
// pre-child dense fallback as the only pre-child behavior. With exclusion
// input present and no child yet, the shipped builder PROJECTS the user
// annotation (ordinals.Select), not dense membership — see
// TestKEP576Claude_PreChildExclusionWindowProjectsAnnotation. "does not
// project the user-facing excluded-ordinals annotation" therefore diverges
// from today's output in that window.
func TestKEP576Claude_DocMustCoverPreChildExclusionWindow(t *testing.T) {
	text, ok := kep576Text(t)
	if !ok {
		t.Skip("KEP-576 not present on this revision (base)")
	}
	if strings.Contains(text, "dense `[0, replicas)` when no exclusion input is present") {
		t.Fatal("F3: Committed Membership lists the pre-child source of truth as dense " +
			"\"when no exclusion input is present\" but is silent about the window where " +
			"exclusion input IS present and the child does not exist yet; the shipped " +
			"builder projects the annotation there. Specify what the compact source " +
			"publishes in that window.")
	}
}

// F4: the Summary's size arithmetic must be internally consistent. "Measured
// at roughly 43 bytes per pod" does not cross the 1 MiB limit at 4000
// replicas x 5 pods (20000 x 43 B = 860 KB). The measured per-entry size for
// the KEP's own example (with ports) is ~129 B (=> ~2.6 MB), which does. Use
// one consistent number.
func TestKEP576Claude_DocSummarySizeArithmeticMustBeConsistent(t *testing.T) {
	text, ok := kep576Text(t)
	if !ok {
		t.Skip("KEP-576 not present on this revision (base)")
	}
	if strings.Contains(text, "Measured at roughly 43 bytes per pod") &&
		strings.Contains(text, "crosses the 1 MiB ConfigMap limit") {
		t.Fatal("F4: Summary states ~43 bytes/pod and claims the 1 MiB limit is crossed " +
			"at 4000 replicas x 5 pods; 20000 x 43 B = 860 KB, under the limit. The " +
			"Motivation's ~118 B/entry analysis (~2.4 MB) is the correct one; make the " +
			"Summary consistent with it.")
	}
}
