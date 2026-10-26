// Verification harness for PR #526 review (Reviewer B / Codex). Additive only —
// does not modify production code. Finding E1 (minor): a v1alpha1 Coordination
// entry with an empty Name still cannot be applied after the fix, because
// WithName("") serializes as "name":"" and the v1alpha2 CRD enforces
// spec.policies[].name minLength=1 (config/crd/bases/
// workloads.x-k8s.io_coordinatedpolicies.yaml). Base behavior for the same input
// fails differently (SSA "omits key field"), so this is a residual edge-case gap,
// not a regression introduced by the PR.
package workloads

import (
	"encoding/json"
	"strings"
	"testing"

	applyv1alpha2 "sigs.k8s.io/rbgs/client-go/applyconfiguration/workloads/v1alpha2"
)

// Polarity: canary — documents the CURRENT (post-fix) behavior for a degenerate
// annotation entry. Flips if the controller later skips/defaults empty names.
func TestVerifyPR526_EmptyCoordinationName_StillSerializedEmpty(t *testing.T) {
	rule := applyv1alpha2.CoordinatedPolicyRule().WithName("").WithRoles("prefill")
	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The apply payload carries "name":"" — the apiserver then rejects it with a
	// minLength=1 schema violation, so the RBG stays stuck (loudly) for this input.
	if !strings.Contains(string(data), `"name":""`) {
		t.Fatalf("expected empty name to be serialized explicitly, got %s", data)
	}
	t.Logf("apply payload for empty-name coordination entry: %s", data)
}
