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
//     TestKEP473_GroupNameBudget): assert facts the KEP relies on about the
//     base tree and invariants of the KEP text itself. They PASS on the PR
//     under review; a failure means the ground truth moved (e.g. a rename)
//     or the KEP regressed.
//   - Bug-canaries (TestKEP473_F*): assert the CURRENT, suspected-defective
//     text is present. They PASS while the finding is unfixed and must FLIP
//     to red once the author fixes the KEP/PR-body text. A green canary is
//     NOT a clean bill of health; see docs/verification/topology-aware-scheduling.
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

// F1: the immutability-enforcing finalizer releases "only after the RBG has
// been deleted", but the policy binds to the RBG by name only. A delete +
// immediate same-name recreate (standard GitOps flow) leaves the old policy
// terminating forever, blocking any new policy of that name and leaving the
// stale topology constraints attached to the new RBG. The Mutability section
// as written does not key the release on the RBG identity (UID) nor define
// how terminating policies are treated by the planner.
func TestKEP473_F1_FinalizerWedgeUnaddressed(t *testing.T) {
	sec := section(kepText(t), "Mutability and Update Semantics")
	if sec == "" {
		t.Fatal("Mutability and Update Semantics section not found")
	}
	assertContains(t, sec,
		"releases it only after the RBG has been deleted",
		"mutability section (name-scoped release condition)")
	assertNotContains(t, sec, "UID",
		"mutability section (release condition is still not keyed on RBG identity)")
	assertNotContains(t, sec, "terminating",
		"mutability section (treatment of terminating policies still unspecified)")
}

// F2: the KEP's motivating scenario (PD co-location) composes with gang
// scheduling as gang-parent {all roles} containing a cross-role topology
// child {prefill,decode}. The KEP only commits Volcano to "parent topology +
// per-instance child topology" and punts everything else to
// SchedulerUnsupported, leaving unspecified whether the Phase-1 backend can
// render the KEP's own Story 2 next to gang.
func TestKEP473_F2_VolcanoGangParentTopologyChildUnspecified(t *testing.T) {
	sec := section(kepText(t), "Translation Channels")
	if sec == "" {
		t.Fatal("Translation Channels section not found")
	}
	assertContains(t, sec, "not arbitrary cross-role containment",
		"Volcano containment limitation sentence")
	lower := strings.ToLower(sec)
	for _, clarification := range []string{"gang parent", "gang-parent", "parent gang"} {
		if strings.Contains(lower, clarification) {
			t.Errorf("KEP now addresses gang-parent/topology-child (%q); invert this canary", clarification)
		}
	}
}

// F3: the Level Identifiers section says the RBGSet immutability guard "lists
// children from the informer cache" (cross-resource admission read), while the
// Mutability section says RBGSet template validation "requires no
// cross-resource child lookup". Both cannot be true, and the base tree's
// RBGSet validator does no cross-resource reads at all.
func TestKEP473_F3_RBGSetAdmissionContradiction(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc, "it lists children from the informer cache",
		"Level Identifiers validation preamble")
	assertContains(t, doc, "requires no cross-resource child lookup",
		"Mutability and Update Semantics section")
}

// F4a: the PR body claims reconcile validates "preferred level not broader
// than required"; the KEP's validation table says the required/preferred
// relative order is "Not validated by RBG".
func TestKEP473_F4a_PRBodyValidationClaimDrift(t *testing.T) {
	assertContains(t, prBodyFixture(t), "preferred level not broader than required",
		"PR body fixture")
	assertContains(t, kepText(t), "Required/preferred relative order | Not validated by RBG",
		"KEP validation table")
}

// F4b: the PR body lists conditions PlacementPlanReady and
// TopologyConstraintActive that appear nowhere in the KEP (which defines only
// GangConfigured and TopologyTranslated).
func TestKEP473_F4b_PRBodyPhantomConditions(t *testing.T) {
	body := prBodyFixture(t)
	assertContains(t, body, "PlacementPlanReady", "PR body fixture")
	assertContains(t, body, "TopologyConstraintActive", "PR body fixture")
	doc := kepText(t)
	assertNotContains(t, doc, "PlacementPlanReady", "KEP text")
	assertNotContains(t, doc, "TopologyConstraintActive", "KEP text")
}

// F4c: the PR body states "The Chinese translation is kept in-tree with the
// English KEP", but the PR adds only an English README.md and no Chinese KEP
// file exists anywhere under keps/.
func TestKEP473_F4c_PRBodyChineseTranslationClaim(t *testing.T) {
	assertContains(t, prBodyFixture(t), "Chinese translation is kept in-tree", "PR body fixture")
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

// F5: the Risks and Mitigations table says TopologyTranslated=False surfaces
// "on the declaring object (CoordinatedPolicy for rules...)", while the
// Observability section and Graduation Criteria put the condition on the RBG
// (which is also the KEP-430 precedent for gang failures).
func TestKEP473_F5_ConditionSurfaceInconsistent(t *testing.T) {
	doc := kepText(t)
	assertContains(t, doc,
		"on the declaring object (CoordinatedPolicy for rules",
		"Risks and Mitigations table")
	assertContains(t, section(doc, "Observability"), "**RBG conditions**",
		"Observability section")
	assertContains(t, doc, "status and edge-triggered events on RBG",
		"Graduation Criteria")
}

// F6: the Generated Group Names section defines a naming scheme only for
// Volcano; the KAI example reuses the legacy rbg.Name and the Koordinator
// example introduces per-instance member PodGroup names, neither of which is
// covered by the stated scheme.
func TestKEP473_F6_NamingSchemeDialectsUnspecified(t *testing.T) {
	sec := section(kepText(t), "Generated Group Names")
	if sec == "" {
		t.Fatal("Generated Group Names section not found")
	}
	assertContains(t, sec, "Volcano", "naming section")
	assertNotContains(t, sec, "KAI", "naming section (KAI still unspecified)")
	assertNotContains(t, sec, "Koordinator", "naming section (Koordinator still unspecified)")
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
	body := fetch(t, "https://raw.githubusercontent.com/NVIDIA/KAI-scheduler/main/pkg/apis/scheduling/v2alpha2/podgroup_types.go")
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
