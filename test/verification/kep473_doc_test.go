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
	"io"
	"net/http"
	"os"
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

// ---------------------------------------------------------------------------
// Contract tests on the KEP document itself.
// ---------------------------------------------------------------------------

var tocLinkRE = regexp.MustCompile(`(?m)^\s*- \[([^\]]+)\]\((#[^)]+)\)`)
var headingRE = regexp.MustCompile(`(?m)^(#{2,4}) (.+)$`)

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
	assertContains(t, sec, "Release is keyed to the matching RBG's **UID**",
		"mutability section (UID-keyed release)")
	assertContains(t, sec, "remains authoritative",
		"mutability section (terminating policy handling)")
	assertContains(t, sec, "releases the finalizer once that recorded UID is deleted",
		"mutability section (stale-owner release)")
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
	assertContains(t, sec, "a gang parent with a cross-role topology child",
		"Volcano containment bullet (gang-parent case specified)")
	assertContains(t, sec, "entry whose `labelSelector` selects the child roles",
		"Volcano containment bullet (rendering mechanism)")
	assertContains(t, sec, "does not support arbitrary multi-level cross-role containment",
		"Volcano containment bullet (remaining limitation stated)")
}

// F3 FIXED at 6894a2e (inverted to a contract test): the informer-cache
// admission sentence is gone and the validation preamble now states all
// immutability validation is self-contained (no child lookup, no cache
// dependency), matching the base tree's RBGSet validator
// (TestBaseFact_RBGSetAdmissionDoesNoChildLookup).
func TestKEP473_F3_ContractRBGSetAdmissionSelfContained(t *testing.T) {
	doc := kepText(t)
	assertNotContains(t, doc, "it lists children from the informer cache",
		"Level Identifiers validation preamble (stale sentence must be gone)")
	assertContains(t, doc, "performs no child lookup and no informer-cache dependency",
		"Level Identifiers validation preamble (self-contained statement)")
	assertContains(t, doc, "requires no cross-resource child lookup",
		"Mutability and Update Semantics section")
}

// F4a FIXED at 6894a2e + PR-body edit (inverted to a contract test): the PR
// body claims "preferred level not broader than required" and the KEP
// validation table now specifies that ordering check (InvalidLevelOrder)
// instead of "Not validated by RBG".
func TestKEP473_F4a_ContractValidationClaimAligned(t *testing.T) {
	assertContains(t, prBodyFixture(t), "preferred level not broader than required",
		"PR body fixture")
	assertNotContains(t, kepText(t), "| Not validated by RBG |",
		"KEP validation table (relative-order punt must be gone)")
	assertContains(t, kepText(t), "rejected as `InvalidLevelOrder`",
		"KEP validation table (ordering check specified)")
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
	assertContains(t, doc, "on the RBG, with the event/message naming the declaring",
		"Risks and Mitigations table")
	assertContains(t, section(doc, "Observability"), "**RBG conditions**",
		"Observability section")
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
// Bug canaries (round 2, head 6894a2e). PASS = finding still present in the
// text; must FLIP to red when the author fixes the document.
// ---------------------------------------------------------------------------

// F7: the new contract-freeze mechanism records the accepted topology
// contract (including "none") at the RBG's first placement acceptance, and
// states "A CoordinatedPolicy may be created before the RBG; that is a normal
// launch ordering and the recorded contract is its topology." The controller
// resolves the policy through the cache-backed client
// (TestBaseFact_PolicyFetchAndWatch), so a policy that exists in etcd but is
// not yet visible in the informer cache at freeze time freezes the contract
// as "none"; when the policy event later arrives the RBG reports
// TopologyPolicyLate and gates role updates, and recovery requires deleting
// and recreating the RBG. The KEP specifies no uncached read, settling
// window, or other guard at freeze time, so the blessed "normal launch
// ordering" is only probabilistically safe.
func TestKEP473_F7_ContractFreezeCacheRaceUnaddressed(t *testing.T) {
	sec := section(kepText(t), "Mutability and Update Semantics")
	if sec == "" {
		t.Fatal("Mutability and Update Semantics section not found")
	}
	assertContains(t, sec,
		"A CoordinatedPolicy may be created before the RBG; that is a normal launch ordering",
		"mutability section (blessed create ordering)")
	for _, guard := range []string{"uncached", "APIReader", "direct read", "settling", "cache"} {
		if strings.Contains(sec, guard) {
			t.Errorf("mutability section now mentions %q at freeze time; re-check F7 and invert this canary", guard)
		}
	}
}

// F8: the drift-discovery text lists "controller resync" as a trigger, but
// the RBG update predicate enqueues only on spec changes
// (TestBaseFact_RBGPredicateFiltersSpecEqualUpdates) and the CoordinatedPolicy
// watch uses GenerationChangedPredicate, so informer resync events (old ==
// new, same generation) never enqueue a reconcile. On a quiescent workload,
// topology-object drift is discovered later than the KEP claims.
func TestKEP473_F8_ResyncListedAsDriftTrigger(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc, "controller resync",
		"drift-discovery trigger list (resync cannot pass the stated predicates)")
}

// F9: the Koordinator worked example says "the full example input is not
// renderable on Koordinator ... no PodGroup is created or updated" and then
// shows a 3-member GangGroup (prefill-0/1 + decode) for "the cross-role-only
// portion", justifying it with "prefill is partitioned per instance, but
// decode has PartitionBy=none". In a cross-role-only input prefill has no
// instance constraint, so by the KEP's own naming rule (per-role members when
// PartitionBy=none) prefill should also contribute one role-level member —
// the example contradicts the stated partition rule under either reading.
func TestKEP473_F9_KoordinatorExampleMemberPartition(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc, "prefill is partitioned per instance, but decode has `PartitionBy=none`",
		"Koordinator example justification")
	assertContains(t, doc, "3 total: prefill-0/1 + decode",
		"Koordinator example member count")
}

// F10: the contract-freeze paragraph gates any drift between the recorded
// contract and the currently resolved declarations with TopologyPolicyLate
// ("Removing all topology after a non-empty contract is treated the same
// way"), while the next paragraph still says that with validating webhooks
// disabled "the controller then renders the latest desired topology". For an
// in-place topology change with webhooks disabled the two sentences give
// opposite outcomes.
func TestKEP473_F10_WebhookDisabledContradiction(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc,
		"Removing all topology after a non-empty contract is treated the same way",
		"contract-freeze paragraph")
	assertContains(t, doc,
		"the controller then renders the latest desired topology",
		"webhooks-disabled sentence")
}

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
