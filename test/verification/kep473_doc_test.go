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

// Package verification is the additive review-verification harness for
// sgl-project/rbg PR #473 (KEP-473 topology-aware scheduling, docs-only).
//
// It contains two kinds of tests:
//
//   - Contract tests (TestBaseFact_*, TestKEP473_TOCAnchorsResolve,
//     TestKEP473_GroupNameBudget, TestKEP473_F1..F6_Contract*): assert facts
//     the KEP relies on about the base tree and invariants of the KEP text
//     itself. They PASS on the PR under review; a failure means the ground
//     truth moved (e.g. a rename) or the KEP regressed. The F1..F6 contract
//     tests are round-1 canaries that flipped when head 6894a2e fixed the
//     findings; they were inverted per polarity rules so a future regression
//     of the fix turns them red again.
//   - Bug-canaries (TestKEP473_F7..F10_*): assert the CURRENT,
//     suspected-defective text is present. They PASS while the finding is
//     unfixed and must FLIP to red once the author fixes the KEP text. A
//     green canary is NOT a clean bill of health; see
//     docs/verification/topology-aware-scheduling.
//
// The TestExternalDialect_* tests are the integration layer: they fetch the
// upstream scheduler APIs the KEP cites (Volcano, Koordinator, KAI) and check
// the exact field/annotation names, which anchors the KEP's core premise that
// three incompatible topology dialects exist and are quoted correctly.
// Set RBG_VERIFY_NO_NETWORK=1 to skip them explicitly.
package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

const kepRelPath = "keps/473-topology-aware-scheduling/README.md"

func repoRoot(t *testing.T) string {
	t.Helper()
	if root := os.Getenv("RBG_ROOT"); root != "" {
		return root
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	// <root>/test/verification/kep473_doc_test.go -> <root>
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

func readRel(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func kepText(t *testing.T) string { return readRel(t, kepRelPath) }

func prBodyFixture(t *testing.T) string {
	return readRel(t, "docs/verification/topology-aware-scheduling/fixtures/pr-body-473.md")
}

// section returns the markdown body between the heading containing
// headingSubstr and the next heading of the same or higher level.
func section(doc, headingSubstr string) string {
	lines := strings.Split(doc, "\n")
	start, level := -1, 0
	for i, l := range lines {
		if strings.HasPrefix(l, "#") && strings.Contains(l, headingSubstr) {
			start = i + 1
			level = len(l) - len(strings.TrimLeft(l, "#"))
			break
		}
	}
	if start < 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range lines[start:] {
		if strings.HasPrefix(l, "#") {
			lvl := len(l) - len(strings.TrimLeft(l, "#"))
			if lvl <= level {
				break
			}
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}

func assertContains(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected %s to contain %q", what, needle)
	}
}

func assertNotContains(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("expected %s NOT to contain %q", what, needle)
	}
}

// ---------------------------------------------------------------------------
// Contract tests: base-tree facts the KEP references (verified against the
// merge-base state of origin/main; the PR itself only adds the KEP).
// ---------------------------------------------------------------------------

func TestBaseFact_KEP430GangSymbols(t *testing.T) {
	assertContains(t, readRel(t, "pkg/scheduler/common/gang_strategy.go"),
		"func ResolveGangStrategy(", "pkg/scheduler/common/gang_strategy.go")
	assertContains(t, readRel(t, "pkg/scheduler/common/gang_error.go"),
		"IncompatibleGangConfig", "pkg/scheduler/common/gang_error.go")
	assertContains(t, readRel(t, "api/workloads/v1alpha2/rolebasedgroup_types.go"),
		`RoleBasedGroupGangConfigured RoleBasedGroupConditionType = "GangConfigured"`,
		"api/workloads/v1alpha2/rolebasedgroup_types.go")
	if _, err := os.Stat(filepath.Join(repoRoot(t), "keps/430-gang-scheduling/README.md")); err != nil {
		t.Errorf("KEP-430 document referenced by KEP-473 is missing: %v", err)
	}
}

func TestBaseFact_CoordinatedPolicySchedulingStrategy(t *testing.T) {
	// KEP-473 proposes TopologyConstraint as a sibling of Gang under
	// spec.policies[].strategy.scheduling; the container must exist.
	body := readRel(t, "api/workloads/v1alpha2/coordinatedpolicy_types.go")
	assertContains(t, body, "Scheduling *SchedulingCoordinationStrategy `json:\"scheduling,omitempty\"`",
		"CoordinatedPolicyStrategy")
	assertContains(t, body, "Gang *GangSchedulingStrategy `json:\"gang,omitempty\"`",
		"SchedulingCoordinationStrategy")
}

func TestBaseFact_VolcanoVendoredDialectFields(t *testing.T) {
	// The KEP's Volcano translation matrix rests on these vendored fields.
	body := readRel(t, "vendor/volcano.sh/apis/pkg/apis/scheduling/types.go")
	for _, needle := range []string{
		"NetworkTopology *NetworkTopologySpec `json:\"networkTopology,omitempty\"", // PodGroup + SubGroupPolicy
		"MatchLabelKeys []string `json:\"matchLabelKeys,omitempty\"",               // per-instance partitioning
		"Mode NetworkTopologyMode `json:\"mode,omitempty\"",
		"HighestTierAllowed *int `json:\"highestTierAllowed,omitempty\"",
		"HighestTierName string `json:\"highestTierName,omitempty\"",
	} {
		assertContains(t, body, needle, "vendored volcano scheduling types")
	}
}

func TestBaseFact_CurrentGangPodGroupNamedAfterRBG(t *testing.T) {
	// KEP-473: "Legacy gang-only PodGroups keep the KEP-430 name rbg.Name".
	assertContains(t, readRel(t, "pkg/scheduler/volcano/scheduler.go"),
		"Name:      rbg.Name", "pkg/scheduler/volcano/scheduler.go PodGroup metadata")
}

func TestBaseFact_PodMembershipLabelKeys(t *testing.T) {
	body := readRel(t, "api/workloads/constants/label.go")
	for _, needle := range []string{
		`GroupNameLabelKey = RBGPrefix + "group-name"`,
		`RoleNameLabelKey = RBGPrefix + "role-name"`,
		`RoleInstanceNameLabelKey = RBGPrefix + "role-instance-name"`,
	} {
		assertContains(t, body, needle, "api/workloads/constants/label.go")
	}
}

func TestBaseFact_CoordinatedPolicyBoundByName(t *testing.T) {
	// The KEP's finalizer design (F1) is evaluated against this binding.
	assertContains(t, readRel(t, "internal/controller/workloads/rolebasedgroup_controller.go"),
		"same name/namespace", "RBG controller CoordinatedPolicy binding")
}

// TestBaseFact_RBGSetAdmissionDoesNoChildLookup documents the current
// admission shape relevant to finding F3: the RBGSet validator today performs
// no cross-resource reads at all, so the KEP's "one cross-resource admission
// exception ... lists children from the informer cache" describes something
// that neither exists nor matches the KEP's own Mutability section.
func TestBaseFact_RBGSetAdmissionDoesNoChildLookup(t *testing.T) {
	body := readRel(t, "api/workloads/v1alpha2/rolebasedgroupset_admission.go")
	assertNotContains(t, body, "client.Reader", "RoleBasedGroupSetValidator")
	assertNotContains(t, body, ".List(", "RoleBasedGroupSetValidator")
}

// TestBaseFact_VolcanoSubGroupLabelSelector pins the vendored Volcano field
// the round-2 F2 fix relies on: a subGroupPolicy entry can select the child
// roles with a labelSelector (and optionally refine per instance with
// matchLabelKeys) while carrying its own networkTopology.
func TestBaseFact_VolcanoSubGroupLabelSelector(t *testing.T) {
	body := readRel(t, "vendor/volcano.sh/apis/pkg/apis/scheduling/types.go")
	assertContains(t, body, "LabelSelector *metav1.LabelSelector `json:\"labelSelector,omitempty\"",
		"vendored volcano SubGroupPolicySpec")
}

// TestBaseFact_PolicyFetchAndWatch pins the mechanism behind finding F7:
// the RBG controller resolves the CoordinatedPolicy through the
// cache-backed client (r.client.Get) and the policy watch uses
// GenerationChangedPredicate (create events DO enqueue; resync does not).
func TestBaseFact_PolicyFetchAndWatch(t *testing.T) {
	body := readRel(t, "internal/controller/workloads/rolebasedgroup_controller.go")
	assertContains(t, body,
		"err := r.client.Get(ctx, types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, coordinatedPolicy)",
		"RBG controller CoordinatedPolicy fetch (cache-backed client)")
	assertContains(t, body,
		"Watches(&workloadsv1alpha2.CoordinatedPolicy{}",
		"RBG controller CoordinatedPolicy watch")
	assertContains(t, body, "builder.WithPredicates(predicate.GenerationChangedPredicate{}))",
		"RBG controller CoordinatedPolicy watch predicate")
}

// TestBaseFact_RBGPredicateFiltersSpecEqualUpdates pins the mechanism behind
// finding F8: the RBG update predicate enqueues only when the spec changes,
// so an informer resync (old == new) never enqueues an RBG reconcile.
func TestBaseFact_RBGPredicateFiltersSpecEqualUpdates(t *testing.T) {
	body := readRel(t, "internal/controller/workloads/rolebasedgroup_controller.go")
	assertContains(t, body, "if !reflect.DeepEqual(oldRbg.Spec, newRbg.Spec) {",
		"RBGPredicate.UpdateFunc (spec-equality filter)")
}

// TestBaseFact_GetSelectorUsesSelectorFromSet pins that the tree under
// review carries the RoleInstance revision-selector fix: the pre-fix code
// discarded labels.Selector.Add's return value, so GetSelector returned an
// empty selector that matched every ControllerRevision in the namespace.
func TestBaseFact_GetSelectorUsesSelectorFromSet(t *testing.T) {
	body := readRel(t, "pkg/reconciler/roleinstance/utils/instance_utils.go")
	assertContains(t, body, "return labels.SelectorFromSet(GetSelectorMatchLabels(instance.Name))",
		"pkg/reconciler/roleinstance/utils/instance_utils.go GetSelector")
}

// ---------------------------------------------------------------------------
// Contract tests on the KEP document itself.
// ---------------------------------------------------------------------------

var tocLinkRE = regexp.MustCompile(`(?m)^\s*- \[([^\]]+)\]\((#[^)]+)\)`)
var headingRE = regexp.MustCompile(`(?m)^(#{1,4}) (.+)$`)

// githubSlug approximates GitHub's heading slugger: lowercase, drop anything
// that is not a letter/digit/space/hyphen/underscore (en-dashes included),
// spaces become hyphens.
func githubSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		case r > 127 && !strings.ContainsRune("–—/", r):
			b.WriteRune(r) // CJK etc. are kept by GitHub
		}
	}
	return b.String()
}

func TestKEP473_TOCAnchorsResolve(t *testing.T) {
	doc := kepText(t)
	headings := map[string]bool{}
	for _, m := range headingRE.FindAllStringSubmatch(doc, -1) {
		headings[githubSlug(m[2])] = true
	}
	links := tocLinkRE.FindAllStringSubmatch(doc, -1)
	if len(links) < 30 {
		t.Fatalf("TOC extraction looks broken: only %d TOC links found (KEP-473 has ~38)", len(links))
	}
	if len(headingRE.FindAllStringSubmatch(doc, -1)) < 30 {
		t.Fatal("heading extraction looks broken: fewer than 30 headings found")
	}
	broken := []string{}
	for _, m := range links {
		anchor := strings.TrimPrefix(m[2], "#")
		if !headings[anchor] {
			broken = append(broken, m[1]+" -> "+m[2])
		}
	}
	if len(broken) > 0 {
		t.Errorf("TOC anchors without a matching heading: %v", broken)
	}
}

func TestKEP473_GroupNameBudget(t *testing.T) {
	// "<truncated-rbg-name>-<placement-name>-<rbg-uid-hash>":
	// 26 + 1 + 22 + 1 + 12 = 62 chars, below the 63-char DNS label limit.
	sec := section(kepText(t), "Generated Group Names")
	if sec == "" {
		t.Fatal("Generated Group Names section not found")
	}
	assertContains(t, sec, "at most 26 characters", "naming section (rbg prefix)")
	assertContains(t, sec, "at most 22 characters", "naming section (placement name)")
	assertContains(t, sec, "twelve hexadecimal characters", "naming section (uid hash)")
	assertContains(t, sec, "at most 62 characters", "naming section (total)")
	if total := 26 + 1 + 22 + 1 + 12; total != 62 || total >= 63 {
		t.Errorf("stated budget inconsistent: 26+1+22+1+12=%d (want 62, <63)", total)
	}
}

// ---------------------------------------------------------------------------
// Bug canaries. PASS = finding still present in the text; must FLIP to red
// when the author fixes the document (or the PR body fixture is refreshed).
// ---------------------------------------------------------------------------

// F1 FIXED at 6894a2e (was a round-1 canary; inverted to a contract test).
// The finalizer release is now keyed to the matching RBG's UID, a terminating
// policy matching that UID stays authoritative, a stale terminating policy is
// ignored and released once its recorded UID is gone, and a manual escape
// hatch is documented.
func TestKEP473_F1_ContractFinalizerReleaseKeyedByUID(t *testing.T) {
	sec := section(kepText(t), "Mutability and Update Semantics")
	if sec == "" {
		t.Fatal("Mutability and Update Semantics section not found")
	}
	assertNotContains(t, sec,
		"releases it only after the RBG has been deleted",
		"mutability section (name-scoped release condition must be gone)")
	// Round-3 wording (head 99b991c5):
	assertContains(t, sec, "keyed to the RBG **UID**, not merely its name",
		"mutability section (UID-keyed release)")
	assertContains(t, sec, "remains the authoritative contract",
		"mutability section (terminating policy handling)")
	assertContains(t, sec, "may be released when the recorded RBG UID no longer exists",
		"mutability section (stale-owner release)")
	assertContains(t, sec, "PlacementDrained=True",
		"mutability section (drain-marker release path)")
	assertContains(t, sec, "Escape hatch and rollback",
		"mutability section (documented escape hatch)")
}

// F2 FIXED at 6894a2e (inverted to a contract test). The Volcano containment
// bullet now specifies the gang-parent + cross-role topology-child rendering
// (a subGroupPolicy entry whose labelSelector selects the child roles and
// carries the child networkTopology); the vendored Volcano
// SubGroupPolicySpec.LabelSelector field this relies on is pinned by
// TestBaseFact_VolcanoSubGroupLabelSelector.
func TestKEP473_F2_ContractVolcanoGangParentTopologyChildSpecified(t *testing.T) {
	sec := section(kepText(t), "Translation Channels")
	if sec == "" {
		t.Fatal("Translation Channels section not found")
	}
	// Round-3 resolution: explicitly rejected with safeguards, not silently
	// dropped and not approximated (the round-2 labelSelector rendering was
	// found unworkable against Volcano v1.14.4 — see the integration layer).
	assertContains(t, sec, "with a cross-role topology child over `{prefill, decode}` is **unsupported in Phase 1**",
		"Volcano containment bullet (gang-parent case explicitly rejected)")
	assertContains(t, sec, "must not flatten away per-role minima, widen the topology scope to router, or emit overlapping subgroup policies",
		"Volcano containment bullet (no-silent-degradation safeguards)")
}

// F3 FIXED at 6894a2e (inverted to a contract test): the informer-cache
// admission sentence is gone and the validation preamble now states all
// immutability validation is self-contained (no child lookup, no cache
// dependency), matching the base tree's RBGSet validator
// (TestBaseFact_RBGSetAdmissionDoesNoChildLookup).
func TestKEP473_F3_ContractRBGSetAdmissionSelfContained(t *testing.T) {
	doc := kepText(t)
	assertNotContains(t, doc, "it lists children from the informer cache",
		"validation preamble (stale sentence must be gone)")
	// Round-3 locus (Mutability and Update Semantics):
	assertContains(t, doc, "Admission compares the old and new topology declarations directly",
		"Mutability and Update Semantics section (self-contained admission)")
	assertContains(t, doc, "requires no cross-resource child lookup",
		"Mutability and Update Semantics section")
}

// F4a FIXED at 6894a2e + PR-body edit (inverted to a contract test): the PR
// body claims "preferred level not broader than required" and the KEP
// validation table now specifies that ordering check (InvalidLevelOrder)
// instead of "Not validated by RBG".
func TestKEP473_F4a_ContractValidationClaimAligned(t *testing.T) {
	// Harness-update (round 4): the PR body was rewritten for the
	// ClusterNetworkTopology redesign and no longer carries the exact round-1
	// phrase "preferred level not broader than required"; body-vs-KEP alignment
	// is now covered by TestKEP473_F20_ContractPRBodyMatchesRedesign. The
	// KEP-side assertions below are unchanged.
	assertNotContains(t, kepText(t), "| Not validated by RBG |",
		"KEP validation text (relative-order punt must be gone)")
	// Round-3 locus: reconcile-flow step 6 + the TopologyTranslated reason list.
	assertContains(t, kepText(t), "preferred/required ordering",
		"KEP reconcile flow (ordering check specified)")
	assertContains(t, kepText(t), "InvalidLevelOrder",
		"KEP condition reasons (ordering rejection surfaced)")
}

// F4b FIXED by PR-body edit (inverted to a contract test): the phantom
// conditions PlacementPlanReady / TopologyConstraintActive are gone from the
// PR body; the KEP defines only GangConfigured and TopologyTranslated.
func TestKEP473_F4b_ContractNoPhantomConditions(t *testing.T) {
	body := prBodyFixture(t)
	assertNotContains(t, body, "PlacementPlanReady", "PR body fixture")
	assertNotContains(t, body, "TopologyConstraintActive", "PR body fixture")
	doc := kepText(t)
	assertNotContains(t, doc, "PlacementPlanReady", "KEP text")
	assertNotContains(t, doc, "TopologyConstraintActive", "KEP text")
}

// F4c FIXED by PR-body edit (inverted to a contract test): the claim "The
// Chinese translation is kept in-tree with the English KEP" was removed (no
// Chinese KEP file exists under keps/).
func TestKEP473_F4c_ContractChineseTranslationClaimGone(t *testing.T) {
	assertNotContains(t, prBodyFixture(t), "Chinese translation is kept in-tree", "PR body fixture")
	kepsDir := filepath.Join(repoRoot(t), "keps")
	found := []string{}
	_ = filepath.Walk(kepsDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(strings.ToLower(info.Name()), "zh") {
			found = append(found, path)
		}
		return nil
	})
	if len(found) > 0 {
		t.Errorf("Chinese KEP files now exist (%v); re-check the claim and invert this canary", found)
	}
}

// F5 FIXED at 6894a2e (inverted to a contract test): the condition surface
// is now consistently the RBG, with the event/message naming the declaring
// CoordinatedPolicy rule or RoleSpec.
func TestKEP473_F5_ContractConditionSurfaceIsRBG(t *testing.T) {
	doc := kepText(t)
	assertNotContains(t, doc,
		"on the declaring object (CoordinatedPolicy for rules",
		"Risks and Mitigations table (old surface must be gone)")
	// Round-3 wording: the Risks row surfaces the condition and names the
	// declaring object in the message; Observability keeps it under RBG
	// conditions.
	assertContains(t, doc, "failures surface `TopologyTranslated=False` and name the declaring CoordinatedPolicy rule or RoleSpec",
		"Risks and Mitigations table")
	obs := section(doc, "Observability")
	assertContains(t, obs, "**RBG conditions**", "Observability section")
	assertContains(t, obs, "`TopologyTranslated`", "Observability section (RBG conditions)")
	assertContains(t, doc, "status and edge-triggered events on RBG",
		"Graduation Criteria")
}

// F6 FIXED at 6894a2e (inverted to a contract test): the Generated Group
// Names section now has per-dialect naming rules for Volcano, Koordinator and
// KAI (kind-prefixed, tree-unique KAI subGroup names).
func TestKEP473_F6_ContractNamingSchemeCoversAllDialects(t *testing.T) {
	sec := section(kepText(t), "Generated Group Names")
	if sec == "" {
		t.Fatal("Generated Group Names section not found")
	}
	assertContains(t, sec, "Volcano", "naming section")
	assertContains(t, sec, "KAI", "naming section")
	assertContains(t, sec, "Koordinator", "naming section")
	assertContains(t, sec, "unique across the entire subGroup tree",
		"naming section (KAI tree-uniqueness rule)")
}

// ---------------------------------------------------------------------------
// Round-2 canaries that FLIPPED at head 99b991c5 (round 3): inverted to
// contract tests per polarity rules. A future regression of the fixed text
// turns these red again.
// ---------------------------------------------------------------------------

// Ledger F1 (round-2 blocker): the contract freeze now reads the policy
// through the uncached apiReader before first acceptance, non-NotFound read
// errors block acceptance, and spec.topologyPolicyRequired provides a
// deterministic wait for GitOps ordering. The probabilistically-unsafe
// "policy before RBG is a normal launch ordering" sentence is gone.
func TestKEP473_F7_ContractFreezeReadsUncached(t *testing.T) {
	sec := section(kepText(t), "Mutability and Update Semantics")
	if sec == "" {
		t.Fatal("Mutability and Update Semantics section not found")
	}
	assertContains(t, sec, "uncached `apiReader`",
		"contract freeze (uncached policy read at acceptance)")
	assertContains(t, sec, "any read error other than `NotFound` blocks acceptance",
		"contract freeze (read-error gating)")
	assertContains(t, sec, "topologyPolicyRequired",
		"contract freeze (explicit policy dependency)")
	assertNotContains(t, sec,
		"A CoordinatedPolicy may be created before the RBG; that is a normal launch ordering",
		"contract freeze (unguarded blessed-ordering sentence must be gone)")
}

// Ledger F2: "controller resync" is no longer listed as a drift-discovery
// trigger anywhere (it could not pass the stated predicates, see
// TestBaseFact_RBGPredicateFiltersSpecEqualUpdates); drift is now discovered
// through topology/native-resource watches plus workload events.
func TestKEP473_F8_ContractNoResyncTrigger(t *testing.T) {
	doc := kepText(t)
	assertNotContains(t, doc, "controller resync",
		"drift-discovery triggers (resync cannot pass the stated predicates)")
	assertContains(t, doc, "Topology and native-resource changes enqueue referencing RBGs",
		"drift discovery (watch-based triggers)")
}

// Ledger F3: the Koordinator worked example is now explicitly a gated
// Phase-2 illustration (unsupported on the inspected baseline) and renders
// the genuinely cross-role-only member set (2 role-level members), not the
// contradictory 3-member per-instance partition.
func TestKEP473_F9_ContractKoordinatorExampleGatedAndConsistent(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc, "future Phase 2 dialect illustration, not runnable supported output",
		"Koordinator example (gated-illustration disclaimer)")
	assertContains(t, doc, "2 total: prefill + decode",
		"Koordinator example (role-level member set)")
	assertNotContains(t, doc, "3 total: prefill-0/1 + decode",
		"Koordinator example (contradictory per-instance member set must be gone)")
	assertNotContains(t, doc, "prefill is partitioned per instance, but decode has `PartitionBy=none`",
		"Koordinator example (self-contradictory justification must be gone)")
}

// Ledger F4: the webhooks-disabled sentence no longer prescribes rendering
// the latest desired topology over an accepted record; the controller
// rejects mismatches and retains previous bindings even when admission is
// bypassed.
func TestKEP473_F10_ContractWebhookBypassStillGated(t *testing.T) {
	doc := kepText(t)
	assertNotContains(t, doc, "the controller then renders the latest desired topology",
		"webhooks-disabled sentence (contradictory render-latest outcome must be gone)")
	assertContains(t, doc, "neither authorizes overwriting an existing accepted record",
		"webhooks-disabled sentence (accepted record still enforced)")
}

// Ledger F6: KAI subGroup names are now kind-prefixed, length-bounded and
// hash-suffixed, compatible with the 63-character label-value limit of the
// kai.scheduler/subgroup-name binding label
// (TestExternalDialect_KAISubGroupLabelKey).
func TestKEP473_F11_ContractKAISubGroupNamesBounded(t *testing.T) {
	sec := section(kepText(t), "Generated Group Names")
	if sec == "" {
		t.Fatal("Generated Group Names section not found")
	}
	assertContains(t, sec, "policy-<source[0:20]>-<hash12>", "KAI naming rule (policy form)")
	assertContains(t, sec, "instance-<role[0:20]>-<instance-hash12>", "KAI naming rule (instance form)")
	assertContains(t, sec, "role-<source[0:20]>-<hash12>", "KAI naming rule (role form)")
	assertContains(t, sec, "names stay below 63 characters", "KAI naming rule (budget stated)")
	// Arithmetic: worst case is the instance form, 9+20+1+12 = 42 < 63.
	if worst := len("instance-") + 20 + 1 + 12; worst >= 63 {
		t.Errorf("instance form budget inconsistent: %d >= 63", worst)
	}
	if worst := len("policy-") + 20 + 1 + 12; worst >= 63 {
		t.Errorf("policy form budget inconsistent: %d >= 63", worst)
	}
}

// Ledger F5 (fixed at 99b991c5): the false justification "RBG admission makes
// the Role declaration immutable from create onward" is gone; ordinary role
// changes are now correctly scoped to layout-preserving updates.
func TestKEP473_F14_ContractNoFalseRoleImmutabilityJustification(t *testing.T) {
	doc := kepText(t)
	assertNotContains(t, doc, "Role declaration immutable from create onward",
		"contract freeze (false whole-Role immutability justification must be gone)")
	assertContains(t, doc, "Ordinary changes remain allowed only when they preserve the accepted physical membership layout",
		"mutability section (ordinary changes scoped to layout-preserving updates)")
}

// Ledger F10 (fixed at 99b991c5): the "minMember/minSubGroup render only
// where gang is configured" sentence that contradicted the mandated
// zero-value rendering is replaced by an explicit rule (zero values always
// rendered; nonzero thresholds only from gang).
func TestKEP473_F15_ContractZeroValueRenderingClarified(t *testing.T) {
	doc := kepText(t)
	assertNotContains(t, doc, "render only where gang is configured",
		"KAI rendering rule (contradictory gang-only sentence must be gone)")
	assertContains(t, doc, "KAI explicitly renders `minMember: 0` on topology-only leaf subgroups",
		"KAI rendering rule (zero values mandated)")
	assertContains(t, doc, "Nonzero thresholds come only from gang configuration",
		"KAI rendering rule (nonzero thresholds scoped to gang)")
}

// ---------------------------------------------------------------------------
// Bug canaries (round 3, head 99b991c5). PASS = finding still present in the
// text; must FLIP to red when the author fixes the document.
// ---------------------------------------------------------------------------

// Ledger F7 FIXED at 9f682b08 (canary flipped -> inverted to a contract
// test): the naming-safety rule is now scoped to topology compiler sources
// ("Role names used as sources by a topology-bearing compiled plan"), with an
// explicit non-tightening guarantee for topology-free RBGs and gang-only
// policies. The old unscoped sentence must stay gone.
func TestKEP473_F12_ContractRoleNameRuleScoped(t *testing.T) {
	doc := kepText(t)
	assertNotContains(t, doc,
		"Role names and topology-bearing policy-rule names are validated at admission as DNS-1123 labels",
		"naming-safety rule (old unscoped sentence must stay gone)")
	assertContains(t, doc,
		"Role names used as sources by a topology-bearing compiled plan must be DNS-1123 labels",
		"naming-safety rule (scoped to topology compiler sources)")
	assertContains(t, doc,
		"adds no stricter naming admission to topology-free RBGs or gang-only policies",
		"naming-safety rule (explicit non-tightening guarantee for KEP-430 objects)")
}

// Ledger F9 FIXED at 9f682b08 (canary flipped -> inverted to a contract
// test): the Dependencies section now carries an explicit controller
// service-account RBAC matrix covering the topology CR, scheduler PodGroups
// per dialect, Managed vs Imported native permissions, Node/CRD reads, and
// events, plus cross-namespace retention visibility and the
// disablement-never-removes-cleanup rule.
func TestKEP473_F13_ContractRBACMatrixEnumerated(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc, "The required RBAC surface is listed below",
		"Dependencies (RBAC matrix preamble)")
	assertContains(t, doc, "`topology.x-k8s.io/clusternetworktopologies` (cluster)",
		"RBAC matrix (topology CR row)")
	assertContains(t, doc, "`scheduling.volcano.sh`, `scheduling.sigs.k8s.io` (gated Koordinator), `scheduling.run.ai` (KAI)",
		"RBAC matrix (scheduler PodGroups row)")
	assertContains(t, doc, "Managed `kai.scheduler/topologies` or `topology.volcano.sh/hypernodes` (cluster)",
		"RBAC matrix (Managed native row)")
	assertContains(t, doc, "Imported `kai.scheduler/topologies` or `scheduling.koordinator.sh/clusternetworktopologies` (cluster)",
		"RBAC matrix (Imported read-only row)")
	assertContains(t, doc, "feature disablement never removes cleanup permissions",
		"RBAC matrix (cleanup-permission retention rule)")
	assertContains(t, doc, "RBAC: verify the documented resource/verb matrix",
		"integration test plan (RBAC matrix verification)")
}

// ---------------------------------------------------------------------------
// Round-4 contract tests (head 9f682b08): the round-4 KEP rewrite resolved
// ledger findings F7/F8/F9/F26/F27/F28/F29/F30. These contracts pin the fixed
// text so a regression turns red. F22 is the round-4 bug-canary for the one
// newly found gap (ledger F31).
// ---------------------------------------------------------------------------

// Ledger F29 FIXED at 9f682b08: the Volcano HyperNode readable prefix is now
// sanitized (dots -> hyphens) BEFORE the 16-char truncation, and the identity
// hash covers the full (untruncated, unsanitized) topology UID, level name,
// and ordered domain path. Doc pin plus a behavioral simulation of the
// specified algorithm at the exact boundary case from the finding.
func TestKEP473_F16_ContractHyperNodeNameSanitizesDots(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc, "replacing every `.` in the DNS-subdomain topology name with `-`, then taking the first 16 characters",
		"Volcano HyperNode naming rule (sanitize before truncate)")
	assertContains(t, doc, "do not truncate before sanitizing",
		"Volcano HyperNode naming rule (ordering)")
	assertContains(t, doc, "abcdefghijklmno.example.com",
		"Volcano HyperNode naming rule (boundary example retained)")
	assertContains(t, doc, "sanitization/truncation never changes those hash inputs",
		"Volcano HyperNode naming rule (hash inputs are full identity)")

	// Behavioral simulation of the algorithm exactly as the KEP specifies it.
	dns1123Label := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	name := func(topoUID, topoName, level string, domainPath []string) string {
		prefix := strings.ReplaceAll(topoName, ".", "-")
		if len(prefix) > 16 {
			prefix = prefix[:16]
		}
		lvl := level
		if len(lvl) > 16 {
			lvl = lvl[:16]
		}
		h := sha256.Sum256([]byte(topoUID + "\x00" + level + "\x00" + strings.Join(domainPath, "\x00")))
		return "r-" + prefix + "-" + lvl + "-" + hex.EncodeToString(h[:6])
	}
	cases := []struct {
		uid, topo, level string
		path             []string
	}{
		// The round-3 finding: dot exactly at the 16-char truncation boundary.
		{"uid-1", "abcdefghijklmno.example.com", "rack", []string{"example.com", "rack-1"}},
		// Maximal inputs: 253-char subdomain topology name + 32-char level.
		{"uid-2", strings.Repeat("a", 250) + ".bc", strings.Repeat("l", 32), []string{"zone", "rack"}},
		// Level name whose 16-char truncation ends on a hyphen.
		{"uid-3", "topo", "0123456789abcde-fghijklmno", []string{"zone"}},
		// Repeated rack names under different zones (full-domain hash must split them).
		{"uid-4", "topo", "rack", []string{"zone-a", "rack"}},
		{"uid-4", "topo", "rack", []string{"zone-b", "rack"}},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		n := name(c.uid, c.topo, c.level, c.path)
		if len(n) > 48 {
			t.Errorf("name %q exceeds the documented 48-char bound (%d)", n, len(n))
		}
		if !dns1123Label.MatchString(n) {
			t.Errorf("name %q is not a valid DNS-1123 label", n)
		}
		if seen[n] {
			t.Errorf("name %q collides with an earlier case", n)
		}
		seen[n] = true
	}
	// Prefixes that sanitize identically but belong to different topology UIDs
	// must still produce distinct names (hash covers the full identity).
	a := name("uid-A", "a.b", "rack", []string{"x"})
	b := name("uid-B", "a-b", "rack", []string{"x"})
	if a == b {
		t.Errorf("sanitize-identical prefixes collapsed: %q == %q", a, b)
	}
}

// Ledger F30 FIXED at 9f682b08: the upgrade-bootstrap gap (Copilot flag
// C4314247675) is closed by the creation-provenance design — a reserved
// create-only contract-version annotation, conservative legacy `none`
// bootstrap, TopologyPolicyLate for pre-first-reconcile policies, epoch
// rotation on reupgrade, and TopologyBootstrapBlocked for unknown provenance.
func TestKEP473_F17_ContractUpgradeBootstrap(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc, "`topology.x-k8s.io/contract-version: v1:<handover-uuid>`",
		"upgrade bootstrap (provenance marker)")
	assertContains(t, doc, "persist a legacy `none` record for that UID, regardless of current policy",
		"upgrade bootstrap (conservative legacy none)")
	assertContains(t, doc, "A topology-bearing policy already present before the new controller's first reconcile is late",
		"upgrade bootstrap (F30 acceptance window closed)")
	assertContains(t, doc, "`TopologyBootstrapBlocked`",
		"upgrade bootstrap (blocked reason)")
	assertContains(t, doc, "reupgrade after an older controller rotates the epoch",
		"upgrade bootstrap (epoch rotation)")
	assertContains(t, doc, "33. Upgrade bootstrap:",
		"unit test plan (bootstrap coverage)")
	assertContains(t, doc, "24. Legacy upgrade:",
		"integration test plan (legacy upgrade coverage)")
	assertContains(t, doc, "19. Upgrade handover:",
		"e2e test plan (handover coverage)")
}

// Ledger F8 ANSWERED at 9f682b08 (was an open question): a never-selected
// rejected late policy acquires no policy finalizer or consumer reservation,
// and deleting it resumes the existing none/role-only contract; an
// interrupted protected first acceptance stays on the retention protocol.
func TestKEP473_F18_ContractLatePolicyRecovery(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc,
		"A rejected late policy that was never selected for acceptance acquires no `topology.x-k8s.io/policy` finalizer or topology consumer reservation",
		"late-policy recovery (no protection for unselected policies)")
	assertContains(t, doc,
		"Removing that policy restores the previously accepted role-only or `none` declarations and allows reconciliation to resume",
		"late-policy recovery (delete-to-resume path)")
	assertContains(t, doc,
		"a policy already protected during an interrupted first acceptance remains subject to the retention protocol",
		"late-policy recovery (interrupted acceptance excluded from the shortcut)")
	assertContains(t, doc, "34. Late-policy recovery:",
		"unit test plan (late-policy recovery coverage)")
}

// Ledger F28 ANSWERED at 9f682b08 (was an open question): topology CR
// creation is rejected when the active scheduler is unknown or unsupported
// (including a recognized native kube-scheduler), without widening the enum;
// workloads referencing an existing topology under an unsupported scheduler
// report SchedulerUnsupported.
func TestKEP473_F19_ContractUnsupportedSchedulerRejected(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc,
		"reject creation if the active scheduler is unknown or is not one of `KAI`, `Koordinator`, and `Volcano`, including a recognized native kube-scheduler configuration",
		"topology CR admission (unsupported scheduler rejected)")
	assertContains(t, doc,
		"do not add an enum value merely to persist a failed topology CR",
		"topology CR admission (enum not widened)")
	assertContains(t, doc,
		"A workload referencing an existing topology under an unsupported scheduler still reports `SchedulerUnsupported`",
		"topology CR admission (workload failure path)")
}

// Ledger F26 FIXED at 9f682b08: the PR body was rewritten for the
// ClusterNetworkTopology redesign. The fixture is the live body archived at
// review time (results/pr-body.md); all four stale legs must stay gone.
func TestKEP473_F20_ContractPRBodyMatchesRedesign(t *testing.T) {
	body := prBodyFixture(t)
	assertContains(t, body, "ClusterNetworkTopology",
		"PR body (the CR the KEP centers on must be named)")
	assertNotContains(t, body, "TopologyResourceUnresolved",
		"PR body (phantom condition reason must stay gone)")
	assertNotContains(t, body, "selects the KAI Topology CR",
		"PR body (stale KAI-reference claim must stay gone)")
	assertNotContains(t, body, "controller default can be overridden",
		"PR body (stale controller-default claim must stay gone)")
	assertContains(t, body, "only the two KEP files",
		"PR body (KEP-only scope statement)")
}

// Ledger F27 FIXED at 9f682b08: the rebase dropped the byte-identical copy of
// already-merged #495. Live git check: the merge-base..HEAD diff must touch
// only the KEP directory (plus this harness's own paths when run on the
// verify branch). On the round-3 head (99b991c5, which carried the #495
// duplicate under pkg/) this test fails; grafted onto 9f682b08 it passes.
func TestKEP473_F21_ContractKEPOnlyDiff(t *testing.T) {
	root := repoRoot(t)
	gitOut := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}
	base, err := exec.Command("git", "-C", root, "merge-base", "origin/main", "HEAD").Output()
	if err != nil {
		base, err = exec.Command("git", "-C", root, "merge-base", "main", "HEAD").Output()
		if err != nil {
			t.Skipf("no main ref available for merge-base: %v", err)
		}
	}
	diff := gitOut("diff", "--name-only", strings.TrimSpace(string(base)), "HEAD")
	allowed := func(path string) bool {
		return strings.HasPrefix(path, "keps/473-topology-aware-scheduling/") ||
			strings.HasPrefix(path, "docs/verification/") ||
			strings.HasPrefix(path, "test/verification/")
	}
	for _, f := range strings.Split(diff, "\n") {
		if f != "" && !allowed(f) {
			t.Errorf("non-KEP path in PR diff: %s (F27 class: runtime files in a docs-only PR)", f)
		}
	}
}

// Ledger F31 (round-4 canary, NEW): the epoch UUID is "generated once per
// handover from an older controller" and CREATE admission "fails closed if
// that configuration is unavailable" — but the KEP never says who provisions
// the initial epoch on a FRESH install (no older controller exists). Read
// literally, a fresh deployment has no handover, hence no configured epoch,
// hence fail-closed RBG CREATE (or TopologyBootstrapBlocked everywhere). One
// sentence ("the deployment generates an initial epoch at install time;
// upgrades rotate it") closes the gap. Canary: PASSES while the gap is
// present; must FLIP when the author documents initial-epoch provisioning.
func TestKEP473_F22_FreshInstallEpochProvisioningUnspecified(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc, "generated once per handover from an older controller",
		"bootstrap section (epoch generation tied to handover)")
	assertContains(t, doc, "fails closed if that configuration is unavailable",
		"bootstrap section (fail-closed admission)")
	assertNotContains(t, doc, "initial epoch",
		"bootstrap section (no fresh-install provisioning path)")
	assertNotContains(t, doc, "fresh install",
		"bootstrap section (no fresh-install provisioning path)")
}

// ---------------------------------------------------------------------------
// Integration layer: the upstream scheduler dialects the KEP cites.
// These anchor the premise (three incompatible dialects, quoted correctly).
// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Integration layer: the upstream scheduler dialects the KEP cites.
// These anchor the premise (three incompatible dialects, quoted correctly).
// ---------------------------------------------------------------------------

func fetch(t *testing.T, url string) string {
	t.Helper()
	if os.Getenv("RBG_VERIFY_NO_NETWORK") == "1" {
		t.Skip("RBG_VERIFY_NO_NETWORK=1 set; skipping upstream API check")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		defer resp.Body.Close()
		b, rerr := io.ReadAll(resp.Body)
		if rerr != nil {
			lastErr = rerr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = &httpError{url: url, code: resp.StatusCode, body: string(b)}
			continue
		}
		return string(b)
	}
	t.Fatalf("fetch %s: %v", url, lastErr)
	return ""
}

type httpError struct {
	url  string
	code int
	body string
}

func (e *httpError) Error() string { return e.url + " -> HTTP " + http.StatusText(e.code) }

func TestExternalDialect_VolcanoHyperNodeTierName(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/volcano-sh/apis/master/pkg/apis/topology/v1alpha1/hypernode_types.go")
	assertContains(t, body, "Tier int `json:\"tier,omitempty\"", "HyperNode spec")
	assertContains(t, body, "TierName string `json:\"tierName,omitempty\"", "HyperNode spec")
}

func TestExternalDialect_KoordinatorNetworkTopologySpec(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/koordinator-sh/koordinator/master/apis/extension/network_topology.go")
	assertContains(t, body, `AnnotationGangNetworkTopologySpec = AnnotationGangPrefix + "/network-topology-spec"`,
		"koordinator gang annotations")
	assertContains(t, body, `= "MustGather"`, "koordinator gather strategies")
	assertContains(t, body, `= "PreferGather"`, "koordinator gather strategies")
}

func TestExternalDialect_KAIPodGroupTopologyConstraint(t *testing.T) {
	// The KAI Go API types moved from NVIDIA/KAI-scheduler/pkg/apis/... to the
	// dedicated kai-scheduler/api repo between rounds (round-1 URL 404s since).
	body := fetch(t, "https://raw.githubusercontent.com/kai-scheduler/api/main/scheduling/v2alpha2/podgroup_types.go")
	// Singular minSubGroup, exactly as the KEP renders it.
	assertContains(t, body, "MinSubGroup *int32 `json:\"minSubGroup,omitempty\"", "KAI PodGroup/SubGroup")
	assertContains(t, body, "RequiredTopologyLevel string `json:\"requiredTopologyLevel,omitempty\"",
		"KAI TopologyConstraint")
	assertContains(t, body, "PreferredTopologyLevel string `json:\"preferredTopologyLevel,omitempty\"",
		"KAI TopologyConstraint")
	assertContains(t, body, "Topology string `json:\"topology,omitempty\"", "KAI TopologyConstraint")
	assertContains(t, body, "SubGroups []SubGroup `json:\"subGroups,omitempty\"", "KAI PodGroup")
	assertContains(t, body, "Parent *string `json:\"parent,omitempty\"", "KAI SubGroup")
}

// The round-2 KEP text claims a KAI level is identified by its nodeLabel OR
// its TopologyLevel.alias. Verify the alias field exists upstream.
func TestExternalDialect_KAITopologyLevelAlias(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/kai-scheduler/api/main/kai/v1alpha1/topology_types.go")
	assertContains(t, body, "NodeLabel string `json:\"nodeLabel\"", "KAI TopologyLevel")
	assertContains(t, body, "Alias string `json:\"alias,omitempty\"", "KAI TopologyLevel")
}

// The KEP pins the alias claim to "Current KAI (including v0.18.3)": the
// v0.18.3 Topology CRD must document alias as usable in place of nodeLabel in
// requiredTopologyLevel/preferredTopologyLevel.
func TestExternalDialect_KAIAliasInV0183CRD(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/NVIDIA/KAI-scheduler/v0.18.3/deployments/kai-scheduler/crds/kai.scheduler_topologies.yaml")
	assertContains(t, body, "alias:", "KAI v0.18.3 Topology CRD levels")
	assertContains(t, body, "usable in place of nodeLabel when",
		"KAI v0.18.3 Topology CRD alias description")
	assertContains(t, body, "requiredTopologyLevel", "KAI v0.18.3 Topology CRD alias description")
	assertContains(t, body, "nodeLabel:", "KAI v0.18.3 Topology CRD levels")
}

// The round-2 KEP text claims Koordinator's deployed PodGroup CRD
// (scheduling.sigs.k8s.io) permits minMember: 0 (no minimum in the schema)...
func TestExternalDialect_KoordinatorPodGroupMinMember(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/koordinator-sh/koordinator/master/config/crd/bases/scheduling.sigs.k8s.io_podgroups.yaml")
	assertContains(t, body, "group: scheduling.sigs.k8s.io", "Koordinator PodGroup CRD group")
	block := propertyBlock(t, body, "minMember")
	assertContains(t, block, "format: int32", "Koordinator PodGroup minMember")
	assertNotContains(t, block, "minimum:", "Koordinator PodGroup minMember (0 must be allowed)")
}

// ...while the upstream scheduler-plugins PodGroup CRD (scheduling.x-k8s.io,
// the dialect the repo's k8s-scheduler-plugin backend renders) requires
// minMember >= 1, which is why topology-only rendering cannot assume it.
func TestExternalDialect_SchedulerPluginsPodGroupMinMemberOne(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/kubernetes-sigs/scheduler-plugins/master/manifests/coscheduling/crd.yaml")
	assertContains(t, body, "group: scheduling.x-k8s.io", "scheduler-plugins PodGroup CRD group")
	block := propertyBlock(t, body, "minMember")
	assertContains(t, block, "minimum: 1", "scheduler-plugins PodGroup minMember")
}

// KAI binds pods to subGroups through the pod label
// `kai.scheduler/subgroup-name`; label values are limited to 63 chars, which
// is why F11 asks for a length budget on generated KAI subGroup names.
func TestExternalDialect_KAISubGroupLabelKey(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/kai-scheduler/api/main/constants/constants.go")
	assertContains(t, body, "SubGroupLabelKey", "KAI constants")
	assertContains(t, body, "kai.scheduler/subgroup-name", "KAI constants")
}

// ---------------------------------------------------------------------------
// Round-3 integration checks: the new Backend Capability Gates section makes
// load-bearing claims about upstream scheduler sources. Pin each one.
// ---------------------------------------------------------------------------

// KAI v0.18.6 calcDomainId joins label values with "." without escaping, so
// paths ["a.b","c"] and ["a","b.c"] collide — the basis for the KAI
// domain-encoding gate.
func TestExternalDialect_KAIDomainIDJoinUnescaped(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/kai-scheduler/KAI-scheduler/v0.18.6/pkg/scheduler/plugins/topology/topology_structs.go")
	assertContains(t, body, "func calcDomainId(", "KAI v0.18.6 topology_structs.go")
	assertContains(t, body, `strings.Join(domainsNames, ".")`,
		"KAI v0.18.6 calcDomainId (unescaped dot join)")
}

// Volcano v1.14.4 allocate skips tasks with empty resource requests, so a
// topology-covered BestEffort pod would bypass the hard topology path — the
// basis for the effective-request gate.
func TestExternalDialect_VolcanoAllocateSkipsBestEffort(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/volcano-sh/volcano/v1.14.4/pkg/scheduler/actions/allocate/allocate.go")
	assertContains(t, body, "task.Resreq.IsEmpty()", "Volcano v1.14.4 allocate (BestEffort skip)")
	assertContains(t, body, "Skip BestEffort task in 'allocate' action",
		"Volcano v1.14.4 allocate (BestEffort skip log)")
}

// Volcano v1.14.4 getOrCreateSubJob only honors the first policy with
// non-empty match values; a labelSelector-only policy produces empty
// matchValues and never forms its subgroup — the basis for rejecting
// cross-role topology children under a wider gang parent in Phase 1.
func TestExternalDialect_VolcanoSelectorOnlyPolicyCreatesNoSubGroup(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/volcano-sh/volcano/v1.14.4/pkg/scheduler/api/job_info.go")
	assertContains(t, body, "if matchValues := getSubJobMatchValues(policy, ti.Pod); len(matchValues) > 0 {",
		"Volcano v1.14.4 getOrCreateSubJob (first non-empty match wins)")
	sub := fetch(t, "https://raw.githubusercontent.com/volcano-sh/volcano/v1.14.4/pkg/scheduler/api/sub_job_info.go")
	assertContains(t, sub, "matchValues := make([]string, 0, len(policy.MatchLabelKeys))",
		"Volcano v1.14.4 getSubJobMatchValues (empty without matchLabelKeys)")
}

// Volcano v1.14.4 default subjob inherits the parent PodGroup's
// networkTopology — the basis for the "pods in the default subjob keep the
// backend's verified parent inheritance" claim.
func TestExternalDialect_VolcanoDefaultSubJobInheritsParentTopology(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/volcano-sh/volcano/v1.14.4/pkg/scheduler/api/job_info.go")
	assertContains(t, body, "policy.NetworkTopology = cloneNetworkTopology(ji.NetworkTopology)",
		"Volcano v1.14.4 getOrCreateDefaultSubJob (parent topology inheritance)")
}

// Volcano v1.14.4 recoverAllocatedHyperNode skips subjobs without their own
// networkTopology — the basis for propagating effective parent topology into
// every explicit subgroup policy.
func TestExternalDialect_VolcanoRecoverySkipsTopologyFreeSubJobs(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/volcano-sh/volcano/v1.14.4/pkg/scheduler/framework/session.go")
	assertContains(t, body, "func (ssn *Session) recoverAllocatedHyperNode(",
		"Volcano v1.14.4 session (recovery entrypoint)")
	assertContains(t, body, `if !subJob.WithNetworkTopology() || subJob.AllocatedHyperNode != "" {`,
		"Volcano v1.14.4 session (topology-free subjobs skipped in recovery)")
}

// Koordinator at the inspected baseline 81e01347 explicitly limits the
// network-topology workflow to jobs with no Bound members and total
// membership equal to the minimum — the basis for the continuous-placement
// gate (vocabulary binding != workload placement support).
func TestExternalDialect_KoordinatorBoundMembersUnsupported(t *testing.T) {
	body := fetch(t, "https://raw.githubusercontent.com/koordinator-sh/koordinator/81e013476f441ae7f331789de8d2aa4890b2c641/pkg/scheduler/plugins/coscheduling/core/network_topology_workflow.go")
	assertContains(t, body, "there are no Bound member Pods",
		"Koordinator baseline network_topology_workflow.go (FIXME limitation)")
	assertContains(t, body, "the total number of Job member Pods is equal to the minimum number",
		"Koordinator baseline network_topology_workflow.go (FIXME limitation)")
}

// propertyBlock extracts the CRD schema block for one property name: from
// "<indent><name>:" to the next line with the same or smaller indent.
func propertyBlock(t *testing.T, yaml, name string) string {
	t.Helper()
	lines := strings.Split(yaml, "\n")
	start := -1
	indent := 0
	for i, l := range lines {
		trimmed := strings.TrimLeft(l, " ")
		if trimmed == name+":" {
			start = i
			indent = len(l) - len(trimmed)
			break
		}
	}
	if start < 0 {
		t.Fatalf("property %q not found in CRD yaml", name)
	}
	var b strings.Builder
	b.WriteString(lines[start] + "\n")
	for _, l := range lines[start+1:] {
		trimmed := strings.TrimLeft(l, " ")
		if trimmed != "" && len(l)-len(trimmed) <= indent {
			break
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}
