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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
)

// Round-3 harness for PR #416 -- hack/gen-helm-rbac.
//
// Round 2 found that a rule shaped differently from "apiGroups + resources" -- a
// nonResourceURLs-only rule from a kubebuilder urls= marker -- produces no output
// block at all, so the generator wrote a chart ClusterRole missing those
// permissions and still exited 0 (R2-F15/R2-F16).
//
// Commit 68850e6f answers that with a guard in run():
//
//	if len(blocks) < len(role.Rules) { return error }
//
// That is a COUNT comparison, and splitRules is not count-preserving: a rule whose
// resources straddle the gate emits TWO blocks (one kept, one gated). A split
// inflates the count and can absorb a drop, leaving the guard silent.
//
// These are CONTRACT tests -- they assert the intended behaviour (a dropped rule is
// always detected) and are RED at this head. They go GREEN once the generator
// tracks per-input-rule coverage instead of comparing totals, at whichever level
// that check lands.

// dropDetected mirrors run()'s acceptance logic on an in-memory rule set: the
// generator refuses the input if splitRules errors, or if its count guard trips.
// Keeping this in one place means the tests below describe the *contract* ("a
// dropped rule is detected") rather than any particular implementation of it, so
// they stay valid if the fix moves into splitRules, parseRole, or run().
func dropDetected(t *testing.T, rules []rbacv1.PolicyRule) (detected bool, blocks []ruleBlock) {
	t.Helper()
	blocks, err := splitRules(rules)
	if err != nil {
		t.Logf("detected inside splitRules: %v", err)
		return true, nil
	}
	if len(blocks) < len(rules) {
		t.Logf("detected by run()'s count guard: %d rules -> %d blocks", len(rules), len(blocks))
		return true, blocks
	}
	return false, blocks
}

// resourcelessRule is what a kubebuilder `urls=` marker produces: verbs and
// nonResourceURLs, no apiGroups/resources. splitRules partitions on .Resources, so
// this rule yields no block.
var resourcelessRule = rbacv1.PolicyRule{
	NonResourceURLs: []string{"/metrics"},
	Verbs:           []string{"get"},
}

// straddlingRule has one kept and one gated resource, so it splits into 2 blocks.
var straddlingRule = rbacv1.PolicyRule{
	APIGroups: []string{"apps"},
	Resources: []string{"controllerrevisions", "deployments"},
	Verbs:     []string{"get"},
}

// TestPR416R3_DropGuardIsMaskedByASplit is the minimal reproduction: two input
// rules, one that splits and one that is dropped. The count comes out equal, so the
// guard never fires -- yet the nonResourceURLs permission is gone from the output.
func TestPR416R3_DropGuardIsMaskedByASplit(t *testing.T) {
	rules := []rbacv1.PolicyRule{straddlingRule, resourcelessRule}

	detected, blocks := dropDetected(t, rules)
	if detected {
		return // fixed
	}

	// Confirm the drop is real before blaming the guard for missing it.
	for _, b := range blocks {
		require.Empty(t, b.rule.NonResourceURLs,
			"premise: the nonResourceURLs rule must actually have been dropped")
	}

	t.Errorf(
		"R3-F23 REPRODUCED: %d input rules produced %d blocks, so run()'s "+
			"`len(blocks) < len(role.Rules)` guard does NOT fire -- but the "+
			"nonResourceURLs rule was silently dropped from the chart ClusterRole and "+
			"the generator exits 0. The guard added in 68850e6f compares totals, and "+
			"splitRules emits 2 blocks for the straddling rule, which pays for the "+
			"missing one. Fix: reject any input rule that produced no block, instead of "+
			"comparing counts.",
		len(rules), len(blocks),
	)
}

// TestPR416R3_DropIsUndetectedInTheCommittedRoleYaml is the reachability proof. It
// takes the rule set from the config/rbac/role.yaml actually committed at this head
// and appends one urls= rule -- the exact change a future kubebuilder marker would
// make. Two of the committed rules already straddle the gate, so the count guard
// has slack to spare and the new rule vanishes from the chart ClusterRole silently.
//
// This is what makes R3-F23 reachable rather than contrived: the slack is the
// repository's current state, not a constructed input.
func TestPR416R3_DropIsUndetectedInTheCommittedRoleYaml(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", sourcePath))
	require.NoError(t, err, "read %s", sourcePath)

	role, err := parseRole(raw)
	require.NoError(t, err)

	// Context: how much slack the count guard already has on the real input.
	baseBlocks, err := splitRules(role.Rules)
	require.NoError(t, err)
	t.Logf("committed %s: %d rules -> %d blocks (count guard slack = %d)",
		sourcePath, len(role.Rules), len(baseBlocks), len(baseBlocks)-len(role.Rules))

	withURLs := append(append([]rbacv1.PolicyRule{}, role.Rules...), resourcelessRule)

	detected, _ := dropDetected(t, withURLs)
	if detected {
		return // fixed
	}

	t.Errorf(
		"R3-F23 REPRODUCED against the committed %s: adding a single urls= "+
			"kubebuilder marker (%d rules total) strips that permission from the chart "+
			"ClusterRole with exit 0, no error and no CI diff, because the two "+
			"straddling apps rules already inflate the block count past the guard. "+
			"R2-F15 is therefore only partially fixed.",
		sourcePath, len(withURLs),
	)
}

// TestPR416R3_SplitRulesIsNotCountPreserving isolates the root cause so a future
// round can see at a glance why a count comparison cannot work here. Not a defect
// on its own -- splitting is the generator's whole job -- but it is the property
// that invalidates the guard.
func TestPR416R3_SplitRulesIsNotCountPreserving(t *testing.T) {
	blocks, err := splitRules([]rbacv1.PolicyRule{straddlingRule})
	require.NoError(t, err)
	require.Len(t, blocks, 2, "a straddling rule must split into a kept and a gated block")

	assert.False(t, blocks[0].deprecated, "kept block must be outside the conditional")
	assert.True(t, blocks[1].deprecated, "gated block must be inside the conditional")
	t.Log("1 input rule -> 2 output blocks: len(blocks) cannot be compared to len(rules)")
}
