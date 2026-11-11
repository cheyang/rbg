// Package kep502doc holds the review-verification harness for PR #528
// (KEP-502 workload status model). These tests are ADDITIVE review tooling
// (verify/kep-502-status-model-codex branch); they are not part of the PR and
// change no production code or docs semantics.
//
// Polarity: every test here is a CONTRACT test. Premise (P0) and hygiene tests
// PASS on the reviewed head and guard the KEP's factual claims about main.
// Finding tests (F1-F5) FAIL on the reviewed head and PASS once the KEP text
// is corrected; a red result IS the reproduction of the finding.
package kep502doc

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

const kepRel = "keps/502-workload-status-model/README.md"

// repoRoot resolves the repository root relative to this package's directory
// (keps/502-workload-status-model/), which is the test's working directory.
func repoRoot() string { return filepath.Join("..", "..") }

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func kepDoc(t *testing.T) string { return repoFile(t, kepRel) }

// stripFences removes fenced code blocks so structural regexes never match
// content inside ``` blocks.
func stripFences(doc string) string {
	re := regexp.MustCompile("(?s)```.*?```")
	return re.ReplaceAllString(doc, "")
}

// section returns the markdown from the first heading whose text contains
// headingSubstr up to the next heading of the same or higher level.
func section(doc, headingSubstr string) string {
	lines := strings.Split(doc, "\n")
	start := -1
	level := 0
	headRe := regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	for i, l := range lines {
		m := headRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if start == -1 {
			if strings.Contains(m[2], headingSubstr) {
				start = i
				level = len(m[1])
			}
			continue
		}
		if len(m[1]) <= level {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start == -1 {
		return ""
	}
	return strings.Join(lines[start:], "\n")
}

// sectionExact returns the markdown under the heading whose text equals
// headingText exactly (unlike section, which does a substring match and would
// resolve "RoleInstance" to the earlier "RoleInstanceSet" heading).
func sectionExact(doc, headingText string) string {
	lines := strings.Split(doc, "\n")
	start := -1
	level := 0
	headRe := regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	for i, l := range lines {
		m := headRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if start == -1 {
			if strings.TrimSpace(m[2]) == headingText {
				start = i
				level = len(m[1])
			}
			continue
		}
		if len(m[1]) <= level {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start == -1 {
		return ""
	}
	return strings.Join(lines[start:], "\n")
}

func structBody(t *testing.T, file, typeName string) string {
	t.Helper()
	src := repoFile(t, file)
	re := regexp.MustCompile(`(?s)type\s+` + typeName + `\s+struct\s*\{(.*?)\n\}`)
	mm := re.FindStringSubmatch(src)
	if mm == nil {
		t.Fatalf("struct %s not found in %s", typeName, file)
	}
	return mm[1]
}

func backtickTokens(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// candidateReasonsTable extracts the RBG "| Condition / Status | Candidate Reasons |"
// table rows as rowName -> reasons.
func candidateReasonsTable(t *testing.T) map[string][]string {
	t.Helper()
	sec := section(kepDoc(t), "RoleBasedGroup")
	if sec == "" {
		t.Fatal("RoleBasedGroup section not found")
	}
	rows := map[string][]string{}
	for _, l := range strings.Split(sec, "\n") {
		if !strings.HasPrefix(l, "|") {
			continue
		}
		cells := strings.Split(l, "|")
		if len(cells) < 3 {
			continue
		}
		name := strings.TrimSpace(cells[1])
		if name == "Condition / Status" || strings.HasPrefix(name, "---") || name == "" {
			continue
		}
		rows[name] = backtickTokens(cells[2])
	}
	if len(rows) == 0 {
		t.Fatal("candidate reasons table not found in RoleBasedGroup section")
	}
	return rows
}

// ---------------------------------------------------------------------------
// P0 premise: the KEP's factual claims about main-branch code must hold.
// These run against the checked-out ref; the PR diff touches only keps/, so
// head == base for every file read here. PASS = premise claim holds.
// ---------------------------------------------------------------------------

func TestPremiseNoPhaseFieldInStatus(t *testing.T) {
	cases := map[string]string{
		"api/workloads/v1alpha2/rolebasedgroup_types.go":  "RoleBasedGroupStatus",
		"api/workloads/v1alpha2/roleinstanceset_types.go": "RoleInstanceSetStatus",
		"api/workloads/v1alpha2/roleinstance_types.go":    "RoleInstanceStatus",
	}
	fieldRe := regexp.MustCompile(`(?m)^\s*Phase\s+\w+`)
	for file, typeName := range cases {
		body := structBody(t, file, typeName)
		if fieldRe.MatchString(body) {
			t.Errorf("KEP premise broken: %s %s gained a Phase field (KEP claims none exists)", file, typeName)
		}
	}
}

func TestPremiseProgressingRollingUpdateDeclaredButUnwritten(t *testing.T) {
	types := repoFile(t, "api/workloads/v1alpha2/rolebasedgroup_types.go")
	for _, c := range []string{"RoleBasedGroupProgressing", "RoleBasedGroupRollingUpdateInProgress"} {
		if !strings.Contains(types, c+` RoleBasedGroupConditionType = "`) {
			t.Errorf("premise broken: %s no longer declared in v1alpha2 rolebasedgroup_types.go", c)
		}
	}
	// No built-in controller may write them: scan internal/ and pkg/ (non-test).
	for _, root := range []string{"internal", "pkg"} {
		err := filepath.Walk(filepath.Join(repoRoot(), root), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			for _, c := range []string{"RoleBasedGroupProgressing", "RoleBasedGroupRollingUpdateInProgress"} {
				if strings.Contains(string(b), c) {
					t.Errorf("premise broken: %s is now referenced by %s (KEP claims controllers never write it)", c, p)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}

func TestPremiseFailedScaleFailedUpdatePersisted(t *testing.T) {
	ris := repoFile(t, "pkg/reconciler/roleinstanceset/statelessmode/reconciler.go")
	for _, c := range []string{"RoleInstanceSetConditionFailedScale", "RoleInstanceSetConditionFailedUpdate"} {
		if !strings.Contains(ris, c) {
			t.Errorf("premise broken: RIS reconciler no longer writes %s", c)
		}
	}
	ri := repoFile(t, "pkg/reconciler/roleinstance/instance_reconciler.go")
	for _, c := range []string{"RoleInstanceFailedScale", "RoleInstanceFailedUpdate"} {
		if !strings.Contains(ri, c) {
			t.Errorf("premise broken: RI reconciler no longer writes %s", c)
		}
	}
}

func TestPremiseRISRIConditionsLackObservedGeneration(t *testing.T) {
	og := regexp.MustCompile(`(?m)^\s*ObservedGeneration\s+\w+`)
	if body := structBody(t, "api/workloads/v1alpha2/roleinstanceset_types.go", "RoleInstanceSetCondition"); og.MatchString(body) {
		t.Error("premise stale: RoleInstanceSetCondition now has observedGeneration (KEP says absent)")
	}
	if body := structBody(t, "api/workloads/v1alpha2/roleinstance_types.go", "RoleInstanceCondition"); og.MatchString(body) {
		t.Error("premise stale: RoleInstanceCondition now has observedGeneration (KEP says absent)")
	}
}

func TestPremiseRBGReadyReplicaCountBasedAndUnchangedDuringDeletion(t *testing.T) {
	c := repoFile(t, "internal/controller/workloads/rolebasedgroup_controller.go")
	for _, r := range []string{`"AllRolesReady"`, `"RoleNotReady"`} {
		if !strings.Contains(c, r) {
			t.Errorf("premise broken: RBG controller no longer computes Ready with reason %s", r)
		}
	}
	if !strings.Contains(c, "if !rbg.DeletionTimestamp.IsZero()") {
		t.Error("premise broken: RBG controller no longer early-returns on deletionTimestamp " +
			"(KEP's Ready=False-on-Terminating comparison baseline changed)")
	}
}

// ---------------------------------------------------------------------------
// F1: the KEP defines its activity / waiting reason vocabulary in the "Reason
// Taxonomy and Aggregation" section (reason-family table + activity table).
// Every Progressing=True candidate reason in the RBG candidate-reasons table
// must be anchored there (or be a phase value). On the reviewed head,
// `Preparing` is listed for Progressing=True but appears nowhere else in the
// document: not in the reason-family table, not in the activity table, not as
// a phase, and not in any prose or example.
// Contract: FAILS now; PASSES when Preparing is defined or removed.
// ---------------------------------------------------------------------------

func TestRBGCandidateReasonsDefined(t *testing.T) {
	doc := kepDoc(t)
	rows := candidateReasonsTable(t)
	taxonomy := section(doc, "Reason Taxonomy and Aggregation")
	if taxonomy == "" {
		t.Fatal("Reason Taxonomy and Aggregation section not found")
	}
	var undefined []string
	for _, rowName := range []string{"Progressing=True", "Progressing=True, waiting"} {
		for _, r := range rows[rowName] {
			if !strings.Contains(taxonomy, "`"+r+"`") {
				undefined = append(undefined, r+" (row "+rowName+")")
			}
		}
	}
	if len(undefined) > 0 {
		t.Errorf("F1: RBG Progressing=True candidate reasons not anchored in the "+
			"Reason Taxonomy and Aggregation section (family/activity tables): %s",
			strings.Join(undefined, ", "))
	}
}

// ---------------------------------------------------------------------------
// F2: the reporting-priority rule vs the RBG example. The priority fence lists
// reason families in reporting order. The RBG example reports
// reason=ScalingOrUpdating (an activity reason) while a child-readiness fact
// holds (Ready=False, "decode has 2/3 replicas ready"). The example is only
// derivable when ChildReadiness cannot outrank activity reasons: either it is
// absent from the fence, ranked below Activity, or the taxonomy prose states
// that child readiness is subsumed by an active workflow.
// Contract: FAILS when ChildReadiness outranks activity reasons while the
// example reports an activity reason with Ready=False; PASSES otherwise.
// Anchors are structural (the fence containing ChildReadiness), not the exact
// wording of the priority sentence, which changed across revisions.
// ---------------------------------------------------------------------------

func TestExampleConsistentWithPriorityRule(t *testing.T) {
	doc := kepDoc(t)

	// Find the priority fence: the ```text block that lists ChildReadiness.
	priorityHasChildReadiness := false
	activityAboveChildReadiness := false
	for _, m := range regexp.MustCompile("(?s)```text(.*?)```").FindAllStringSubmatch(doc, -1) {
		if !strings.Contains(m[1], "ChildReadiness") ||
			!(strings.Contains(m[1], "Permission") || strings.Contains(m[1], "Quota")) {
			continue
		}
		priorityHasChildReadiness = true
		actIdx := strings.Index(m[1], "Activity")
		crIdx := strings.Index(m[1], "ChildReadiness")
		if actIdx >= 0 && actIdx < crIdx {
			activityAboveChildReadiness = true
		}
	}
	if !priorityHasChildReadiness {
		t.Fatal("priority list fenced block (containing ChildReadiness) not found")
	}

	// RBG example: first yaml block inside the RoleBasedGroup section.
	rbgSec := section(doc, "RoleBasedGroup")
	yamlRe := regexp.MustCompile("(?s)```yaml\n(.*?)```")
	ym := yamlRe.FindStringSubmatch(rbgSec)
	if ym == nil {
		t.Fatal("RBG yaml example not found")
	}
	var ex struct {
		Status struct {
			Conditions []struct {
				Type   string `yaml:"type"`
				Status string `yaml:"status"`
				Reason string `yaml:"reason"`
			} `yaml:"conditions"`
		} `yaml:"status"`
	}
	if err := yaml.Unmarshal([]byte(ym[1]), &ex); err != nil {
		t.Fatalf("RBG example does not parse: %v", err)
	}
	progReason, readyStatus := "", ""
	for _, c := range ex.Status.Conditions {
		switch c.Type {
		case "Progressing":
			progReason = c.Reason
		case "Ready":
			readyStatus = c.Status
		}
	}
	activityReasons := map[string]bool{"Creating": true, "ScalingOrUpdating": true, "Recovering": true}

	taxonomy := section(doc, "Reason Taxonomy and Aggregation")
	clarified := false
	for _, l := range strings.Split(taxonomy, "\n") {
		ll := strings.ToLower(l)
		if (strings.Contains(ll, "childreadiness") || strings.Contains(ll, "child readiness")) &&
			(strings.Contains(ll, "activity") || strings.Contains(ll, "subsum") ||
				strings.Contains(ll, "outrank") || strings.Contains(ll, "precedence") ||
				strings.Contains(ll, "active workflow")) {
			clarified = true
		}
	}

	if readyStatus == "False" && activityReasons[progReason] &&
		!activityAboveChildReadiness && !clarified {
		t.Errorf("F2: priority rule contradicts the RBG example: ChildReadiness is in the blocker priority list "+
			"('a current blocker outranks an activity reason'), yet the example reports Progressing reason=%q "+
			"while a child-readiness fact holds (Ready=%s). Clarify whether child-readiness outranks activity reasons.",
			progReason, readyStatus)
	}
}

// ---------------------------------------------------------------------------
// F3: Progressing=False covers "configuration rejected" (per the RBG conditions
// table), so every configuration-rejection reason defined for Accepted=False
// must be reachable from Progressing=False. On the reviewed head the
// Progressing=False row omits TemplateInvalid and DependencyCycle.
// Contract: FAILS now; PASSES when the row or the Progressing prose covers them.
// ---------------------------------------------------------------------------

func TestProgressingFalseCoversConfigRejection(t *testing.T) {
	// Round-7 re-anchor (05807351): the Accepted condition is gone and
	// configuration-family blockers report Progressing=True. The durable
	// contract: every configuration-family reason the doc defines has a
	// defined Progressing mapping — listed in a candidate-table Progressing
	// row or tied to Progressing in prose. Red at f05e1dd (TemplateInvalid /
	// DependencyCycle had no Progressing mapping), green since.
	doc := kepDoc(t)
	rows := candidateReasonsTable(t)
	config := map[string]bool{}
	for _, r := range rows["Accepted=False"] {
		config[r] = true
	}
	rt := section(doc, "Reason Taxonomy and Aggregation")
	inFam := false
	for _, l := range strings.Split(rt, "\n") {
		if strings.HasPrefix(l, "| Reason family |") {
			inFam = true
			continue
		}
		if inFam && !strings.HasPrefix(l, "|") {
			break
		}
		if !inFam || strings.HasPrefix(l, "| ---") {
			continue
		}
		cells := strings.Split(l, "|")
		if len(cells) < 5 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(cells[1]), "Configuration") {
			for _, tok := range backtickTokens(cells[4]) {
				config[tok] = true
			}
		}
	}
	val := section(doc, "Validation Ownership")
	for _, r := range []string{"InvalidSpec", "TemplateInvalid", "DependencyCycle",
		"IncompatibleGangConfig", "SchedulerUnsupported"} {
		if strings.Contains(val, "`"+r+"`") {
			config[r] = true
		}
	}
	covered := map[string]bool{}
	for name, reasons := range rows {
		if strings.HasPrefix(name, "Progressing") {
			for _, r := range reasons {
				covered[r] = true
			}
		}
	}
	for _, l := range strings.Split(doc, "\n") {
		if !strings.Contains(l, "Progressing") {
			continue
		}
		for r := range config {
			if strings.Contains(l, "`"+r+"`") {
				covered[r] = true
			}
		}
	}
	var missing []string
	for r := range config {
		if !covered[r] {
			missing = append(missing, r)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("F3: these configuration-rejection reasons have no defined Progressing "+
			"mapping (candidate table or prose): %s", strings.Join(missing, ", "))
	}
}

// ---------------------------------------------------------------------------
// F4: the KEP marks the RBG Ready condition "[Retained]" but adds "or the
// object is terminating" as a new Ready=False trigger. Today the controller
// early-returns on deletionTimestamp and never rewrites Ready, so a previously
// Ready object keeps Ready=True until deletion — the KEP changes existing
// behavior without flagging it as a change or covering it in the
// upgrade/compatibility analysis.
// Contract: FAILS now; PASSES when Ready is marked [Changed], the terminating
// trigger is dropped, or the upgrade strategy / risks acknowledge it.
// ---------------------------------------------------------------------------

func TestReadyTerminatingChangeAcknowledged(t *testing.T) {
	doc := kepDoc(t)
	// current behavior evidence
	ctrl := repoFile(t, "internal/controller/workloads/rolebasedgroup_controller.go")
	controllerIgnoresDeletion := strings.Contains(ctrl, "if !rbg.DeletionTimestamp.IsZero()")
	if !controllerIgnoresDeletion {
		t.Skip("controller now writes status during termination; re-evaluate the finding")
	}
	// KEP adds the terminating trigger?
	rbgSec := section(doc, "RoleBasedGroup")
	addsTerminatingTrigger := false
	markedChanged := false
	for _, l := range strings.Split(rbgSec, "\n") {
		if strings.Contains(l, "`Ready`") && strings.Contains(strings.ToLower(l), "terminating") {
			addsTerminatingTrigger = true
		}
	}
	for _, l := range strings.Split(rbgSec, "\n") {
		if strings.Contains(l, "type: Ready") && strings.Contains(l, "[Changed]") {
			markedChanged = true
		}
	}
	acknowledged := markedChanged
	for _, secName := range []string{"Upgrade / Downgrade Strategy", "Risks and Mitigations"} {
		for _, l := range strings.Split(section(doc, secName), "\n") {
			if strings.Contains(strings.ToLower(l), "terminat") && strings.Contains(l, "Ready") {
				acknowledged = true
			}
		}
	}
	if addsTerminatingTrigger && !acknowledged {
		t.Error("F4: KEP adds Ready=False on terminating (a change vs the current controller, which " +
			"early-returns on deletion and never rewrites Ready) while marking Ready '[Retained]' and without " +
			"covering the new False trigger in the upgrade strategy or risks")
	}
}

// ---------------------------------------------------------------------------
// F5: the TOC must list every section. On the reviewed head the entry for
// "Reason Taxonomy and Aggregation" (a ### under Design Details) is missing.
// Contract: FAILS now; PASSES when the TOC entry is added.
// ---------------------------------------------------------------------------

func githubSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

func TestTOCComplete(t *testing.T) {
	doc := kepDoc(t)
	tocRe := regexp.MustCompile("(?s)<!-- toc -->(.*?)<!-- /toc -->")
	toc := tocRe.FindStringSubmatch(doc)
	if toc == nil {
		t.Fatal("TOC block not found")
	}
	inTOC := map[string]string{}
	for _, m := range regexp.MustCompile(`\[([^\]]+)\]\(#([^)]+)\)`).FindAllStringSubmatch(toc[1], -1) {
		inTOC[m[2]] = m[1]
	}
	body := stripFences(doc[tocRe.FindStringIndex(doc)[1]:])
	headRe := regexp.MustCompile(`(?m)^(#{2,4})\s+(.+?)\s*$`)
	var missing []string
	for _, m := range headRe.FindAllStringSubmatch(body, -1) {
		slug := githubSlug(m[2])
		if _, ok := inTOC[slug]; !ok {
			missing = append(missing, m[2])
		}
	}
	if len(missing) > 0 {
		t.Errorf("F5: headings missing from the TOC: %s", strings.Join(missing, ", "))
	}
}

// ---------------------------------------------------------------------------
// Doc hygiene guards (no finding attached; PASS on the reviewed head and act
// as regression guards for future revisions).
// ---------------------------------------------------------------------------

func TestKepYAMLWellFormed(t *testing.T) {
	var meta struct {
		Title        string   `yaml:"title"`
		KepNumber    int      `yaml:"kep-number"`
		Authors      []string `yaml:"authors"`
		Status       string   `yaml:"status"`
		CreationDate string   `yaml:"creation-date"`
		Stage        string   `yaml:"stage"`
	}
	if err := yaml.Unmarshal([]byte(repoFile(t, "keps/502-workload-status-model/kep.yaml")), &meta); err != nil {
		t.Fatalf("kep.yaml does not parse: %v", err)
	}
	if meta.KepNumber != 502 || meta.Status != "provisional" || meta.Stage != "alpha" ||
		meta.Title == "" || len(meta.Authors) == 0 || meta.CreationDate == "" {
		t.Errorf("kep.yaml metadata incomplete/wrong: %+v", meta)
	}
}

func TestYAMLExamplesParse(t *testing.T) {
	doc := kepDoc(t)
	blocks := regexp.MustCompile("(?s)```yaml\n(.*?)```").FindAllStringSubmatch(doc, -1)
	if len(blocks) != 3 {
		t.Fatalf("expected 3 yaml examples (RBG/RIS/RI), found %d", len(blocks))
	}
	want := map[string]bool{"RoleBasedGroup": false, "RoleInstanceSet": false, "RoleInstance": false}
	for i, b := range blocks {
		var obj struct {
			Kind   string `yaml:"kind"`
			Status struct {
				Phase string `yaml:"phase"`
			} `yaml:"status"`
		}
		if err := yaml.Unmarshal([]byte(b[1]), &obj); err != nil {
			t.Errorf("yaml example %d does not parse: %v", i, err)
			continue
		}
		if _, ok := want[obj.Kind]; !ok {
			t.Errorf("yaml example %d: unexpected kind %q", i, obj.Kind)
			continue
		}
		want[obj.Kind] = true
		// Round-5 re-anchor (0b7285b7): the pivot removed status.phase from RBG
		// and RIS; only RI keeps the optional phase field. The examples must
		// reflect exactly that split. (Pre-pivot heads carried phase on all
		// three; this guard is red there by construction — contract change per
		// the AC-doc-tooling changeReason, not a regression catch.)
		switch obj.Kind {
		case "RoleInstance":
			if obj.Status.Phase == "" {
				t.Errorf("yaml example %d (RoleInstance): status.phase empty — "+
					"RI is the one layer that keeps phase", i)
			}
		default:
			if obj.Status.Phase != "" {
				t.Errorf("yaml example %d (%s): status.phase present (%q) — post-pivot only "+
					"RI adds phase", i, obj.Kind, obj.Status.Phase)
			}
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("no yaml example for kind %s", k)
		}
	}
}

// ---------------------------------------------------------------------------
// F5-collision (round-2 ledger coverage): the KEP introduces `RolesNotReady`
// (ChildReadiness-family Progressing reason) alongside the retained
// `RoleNotReady` (Ready=False reason) — two machine-readable strings on the
// same object differing by one letter. The KEP must acknowledge the
// distinction explicitly so consumers do not normalize singular/plural.
// Contract: FAILS when no line names both spellings together.
// ---------------------------------------------------------------------------

func TestRolesNotReadyDistinctionAcknowledged(t *testing.T) {
	doc := kepDoc(t)
	for _, l := range strings.Split(stripFences(doc), "\n") {
		if strings.Contains(l, "`RolesNotReady`") && strings.Contains(l, "`RoleNotReady`") {
			return
		}
	}
	t.Error("F5-collision: no line acknowledges the RolesNotReady (Progressing, child-readiness) " +
		"vs RoleNotReady (Ready=False) near-collision; consumers told to match reasons " +
		"case/plural-sensitively will trip on the one-letter difference")
}

// ---------------------------------------------------------------------------
// F6-casing (round-2 ledger coverage): the KEP's "candidate reasons for
// aggregated RI readiness" presents itself as the strings the current
// controller writes for the RoleInstanceReady condition. Every enumerated
// reason must appear verbatim (case-sensitive) in
// pkg/reconciler/roleinstance/instance_status.go.
// Contract: FAILS on any enumerated reason absent from the code.
// ---------------------------------------------------------------------------

func TestRIReadinessReasonsMatchCode(t *testing.T) {
	doc := kepDoc(t)
	var enum, marker string
	for _, l := range strings.Split(doc, "\n") {
		if strings.Contains(l, "for aggregated RI readiness are") {
			enum, marker = l, "for aggregated RI readiness are"
			break
		}
		// Round-7 re-anchor (05807351): the sentence lead-in became
		// "`RoleInstanceReady` reasons are ..." with a trailing qualifier
		// clause ("when a configured gate condition is missing").
		if strings.Contains(l, "`RoleInstanceReady` reasons are") {
			enum, marker = l, "`RoleInstanceReady` reasons are"
			break
		}
	}
	if enum == "" {
		t.Fatal("RI aggregated-readiness reasons sentence not found")
	}
	// The sentence shares a paragraph line with the operation-specific
	// Progressing reasons; only tokens after the marker belong to the
	// readiness enumeration. Round-6 re-anchor (39e2ecdf): the doc dropped
	// the word "Candidate" ("Reasons for aggregated RI readiness are ...");
	// the enumeration content is unchanged, so match the stable suffix.
	parts := strings.SplitN(enum, marker, 2)
	// The enumeration is plain text: "Ready, PodsNotReady, InplaceUpdateNotReady,
	// and ReadinessGatesNotReady." — split on commas/and, trim spaces and periods.
	// Round 7: cut the trailing qualifier clause before tokenizing.
	tail := strings.SplitN(parts[1], " when ", 2)[0]
	tail = strings.TrimSuffix(strings.TrimSpace(tail), ".")
	tail = strings.Replace(tail, ", and ", ", ", 1)
	var tokens []string
	for _, w := range strings.FieldsFunc(tail, func(r rune) bool { return r == ',' || r == ' ' }) {
		if w = strings.TrimSpace(w); w != "" && w != "and" {
			tokens = append(tokens, w)
		}
	}
	if len(tokens) == 0 {
		t.Fatal("RI aggregated-readiness enumeration parsed to zero reasons")
	}
	code := repoFile(t, "pkg/reconciler/roleinstance/instance_status.go")
	var missing []string
	for _, r := range tokens {
		if !strings.Contains(code, `"`+r+`"`) {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		t.Errorf("F6-casing: RI readiness candidate reasons not written verbatim by the current "+
			"controller (pkg/reconciler/roleinstance/instance_status.go): %s", strings.Join(missing, ", "))
	}
}

// ---------------------------------------------------------------------------
// F8-event-budget (round-2 ledger coverage): the design moves individual
// scale/update failure reporting to Warning Events. The repo's controller code
// documents the client-go spam-filter hazard (an object's event budget can be
// drained, silencing other warnings). The Risks and Mitigations table must
// name the event-budget / spam-filter interaction.
// Contract: FAILS when the risks section omits it.
// ---------------------------------------------------------------------------

func TestEventBudgetRiskInRisksTable(t *testing.T) {
	doc := kepDoc(t)
	risks := section(doc, "Risks and Mitigations")
	if risks == "" {
		t.Fatal("Risks and Mitigations section not found")
	}
	low := strings.ToLower(risks)
	if !strings.Contains(low, "event budget") && !strings.Contains(low, "spam filter") {
		t.Error("F8-event-budget: Risks and Mitigations table does not mention the Warning-Event " +
			"event-budget / client-go spam-filter hazard that the controller code documents")
	}
}

// ---------------------------------------------------------------------------
// O1-row-ambiguity (round-2 ledger coverage): the candidate-reasons row for
// Ready must disambiguate which status the Terminating reason pairs with
// (terminating implies Ready=False).
// Contract: FAILS while a combined Ready=True / False row carries Terminating,
// or when Ready=False lacks Terminating.
// ---------------------------------------------------------------------------

func TestReadyTerminatingReasonRowSplit(t *testing.T) {
	// Round-5 re-anchor (0b7285b7): the design REMOVED the Ready=False
	// (reason=Terminating) trigger ("A deletionTimestamp does not by itself
	// rewrite Ready"), so the original combined-row ambiguity cannot recur via
	// Terminating. The durable contract: Ready=True and Ready=False remain
	// separate candidate rows, and Terminating never pairs with Ready=True.
	// Green at f622d642/da818779 (split rows with Terminating under False) and
	// at 0b7285b7 (split rows, Terminating gone); red at f05e1dd (combined row).
	doc := kepDoc(t)
	rows := candidateReasonsTable(t)
	for name, reasons := range rows {
		if strings.Contains(name, "Ready=True") && strings.Contains(name, "False") {
			t.Errorf("O1-row-ambiguity: combined Ready=True/False row %q reappeared; "+
				"rows must stay split so each reason pairs with one status", name)
		}
		if strings.Contains(name, "Ready=True") && !strings.Contains(name, "False") {
			for _, r := range reasons {
				if r == "Terminating" {
					t.Errorf("O1-row-ambiguity: Ready=True row carries Terminating")
				}
			}
		}
	}
	_, hasTrue := rows["Ready=True"]
	_, hasFalse := rows["Ready=False"]
	if hasTrue != hasFalse {
		t.Error("O1-row-ambiguity: only one of the Ready=True / Ready=False candidate rows exists")
	}
	if !hasTrue && !hasFalse {
		// Round-7 re-anchor (05807351): RBG Ready is deprecated and its
		// candidate rows are gone, so the row ambiguity cannot recur. The
		// durable contract is then that the RBG section documents the
		// deprecation and the compatibility window.
		rbgSec := section(doc, "RoleBasedGroup")
		if !(strings.Contains(rbgSec, "`Ready` condition is deprecated") &&
			strings.Contains(rbgSec, "compatibility window")) {
			t.Error("O1-row-ambiguity: RBG Ready candidate rows are gone, but the RBG section " +
				"does not document the Ready deprecation and its compatibility window")
		}
	}
}

// ---------------------------------------------------------------------------
// Resolution guards (round 2): regression coverage for design decisions the
// revision made in response to round-1 questions. Contract tests; they FAIL on
// the previous head (resolution absent) and PASS on the current head.
// ---------------------------------------------------------------------------

// TestRecoveryPhaseSplitRISvsRI guards the Q1 resolution: RI gains a
// `Recovering` phase while RIS deliberately has none and propagates recovery
// through conditions instead.
// TestRecoveryPhaseSplitRISvsRI guards the Q1 resolution: RI gains a
// `Recovering` phase while RIS deliberately has none and propagates recovery
// through conditions instead.
// Round-5 re-anchor (0b7285b7): the pivot removed RIS phase entirely, which
// subsumes the original resolution — RIS now reports recovery via
// `Progressing` (reason=Recovering) and counters. The guard accepts either
// form of the resolution: the pre-pivot statement ("does not define a
// `Recovering` phase" + propagates recovery through conditions) or the
// post-pivot form (RIS adds no status.phase AND RIS `Progressing` activity
// reasons include `Recovering`).
func TestRecoveryPhaseSplitRISvsRI(t *testing.T) {
	doc := kepDoc(t)
	risSec := sectionExact(doc, "RoleInstanceSet")
	riSec := sectionExact(doc, "RoleInstance")
	if risSec == "" || riSec == "" {
		t.Fatal("RIS or RI section not found")
	}
	if strings.Contains(risSec, "| `Recovering` |") {
		t.Error("Q1-guard: RIS phase table gained a Recovering phase; the resolution says RIS " +
			"must propagate recovery through conditions, not a phase")
	}
	if !strings.Contains(riSec, "| `Recovering` |") {
		t.Error("Q1-guard: RI phase table lacks the Recovering phase the resolution introduces")
	}
	prePivot := strings.Contains(risSec, "does not define a `Recovering` phase")
	postPivot := strings.Contains(risSec, "does not add `status.phase`") &&
		strings.Contains(risSec, "`Recovering`")
	if !prePivot && !postPivot {
		t.Error("Q1-guard: RIS section states neither that RIS has no Recovering phase " +
			"(pre-pivot form) nor that RIS adds no status.phase while reporting recovery via " +
			"Progressing (post-pivot form)")
	}
	// The reason family table must still map recovery at every layer.
	if !regexp.MustCompile(`\|\s*Recovery\s*\|\s*` + "`Recovering`").MatchString(doc) {
		t.Error("Q1-guard: reason-family table no longer maps the Recovery family to `Recovering`")
	}
}

// TestNoFailedPhaseStated guards the Q2 resolution: the KEP states explicitly
// that there is no Failed phase and that Accepted=False never transitions
// phase, so implementers do not invent one later.
func TestNoFailedPhaseStated(t *testing.T) {
	doc := kepDoc(t)
	if !strings.Contains(doc, "no `Failed` phase") {
		t.Error("Q2-guard: KEP no longer states explicitly that there is no Failed phase")
	}
	for _, l := range strings.Split(stripFences(doc), "\n") {
		if strings.HasPrefix(l, "| `Failed`") {
			t.Errorf("Q2-guard: a phase table row defines a Failed phase: %s", strings.TrimSpace(l))
		}
	}
}

// TestRISOverlapPrecedenceStated guards the Q3 / C4431017493 resolution: when
// RIS update and scaling overlap, phase display priority is independent of the
// Progressing reason selection (blockers and pause precedence preserved), and
// the message-only representation of the concurrent workflow is an explicitly
// accepted trade-off.
// TestRISOverlapPrecedenceStated guards the Q3 / C4431017493 resolution: when
// RIS update and scaling overlap, reason/display selection is deterministic
// (update outranks scaling), blocker/pause precedence is preserved, and the
// message-only representation of the concurrent workflow is an explicitly
// accepted trade-off.
// Round-5 re-anchor (0b7285b7): the pivot removed the RIS phase, so the old
// "phase display priority is independent of condition reason selection"
// formulation is gone; the same guarantees now live in the RIS Activity
// precedence sentence ("an active update outranks scaling") plus the
// "Progressing follows RBG rules for progress, pauses, blockers" inheritance.
// Green when either the pre-pivot or post-pivot anchors hold.
func TestRISOverlapPrecedenceStated(t *testing.T) {
	risSec := section(kepDoc(t), "RoleInstanceSet")
	updateOverScaling := strings.Contains(risSec, "independent of condition reason selection") ||
		strings.Contains(risSec, "outranks scaling")
	if !updateOverScaling {
		t.Error("Q3-guard: RIS overlap text no longer states update-outranks-scaling " +
			"determinism (pre-pivot: display priority independent of reason selection; " +
			"post-pivot: an active update outranks scaling)")
	}
	blockerPause := strings.Contains(risSec, "independent of condition reason selection") ||
		(strings.Contains(risSec, "follows RBG rules") && strings.Contains(risSec, "blockers"))
	if !blockerPause {
		t.Error("Q3-guard: RIS overlap text no longer preserves blocker/pause precedence " +
			"over activity reasons (directly or via inheritance of the RBG rules)")
	}
	if !strings.Contains(risSec, "accepted v1alpha2 trade-off") {
		t.Error("Q3-guard: RIS overlap text no longer records the message-only concurrent-scaling " +
			"representation as an accepted trade-off")
	}
}

// ---------------------------------------------------------------------------
// F9 (round-2 revision regression, fixed in round 3): every statement that
// describes the inputs to RBG phase transitions must use ONE consistent
// vocabulary. The 6335eac revision rewired recovery derivation to lower-level
// conditions (:463, :502) while the Goals item and the RoleBasedGroup intro
// still said "lower-level phases" only — a reader of the RBG section would
// look for child phase values as the recovery trigger the authoritative
// statements disclaim. Contract: across all transition-input statements, the
// boolean "names lower-level conditions as an input" is uniform. This passes
// at f05e1dd (uniform "phases"), fails at 6335eac (mixed), and passes again
// once the stragglers name conditions too.
// ---------------------------------------------------------------------------

func TestTransitionInputVocabularyConsistent(t *testing.T) {
	doc := stripFences(kepDoc(t))
	// Transition/aggregation-input statements: lines that say what RBG (or the
	// RBG/RIS pair) derives progress, recovery, or transitions from, at the
	// lower level. Round-5 re-anchor (0b7285b7): the pivot removed RBG/RIS
	// phases, so the phrasing shifted from "transitions based on lower-level
	// phases/conditions" to "aggregate lower-level conditions and recovery
	// facts". The guard keeps its original semantics — uniformity, not a
	// specific vocabulary: every input-enumerating statement must agree on
	// whether lower-level conditions are an input (green at f05e1dd: uniform
	// "phases"; red at 6335eac: mixed; green at f622d642+: uniform
	// "conditions").
	// A site must put the input-relationship word next to "lower-level" — a
	// bare "transition" elsewhere in the line (e.g. "would need its own
	// transition, initialization, and rollback rules", :283) is meta-discussion
	// about the removed phase, not an input statement.
	siteRe := regexp.MustCompile(`(?i)(lower-level.{0,60}(trigger|based on|aggregat|deriv))|` +
		`((trigger|based on|aggregat|deriv)[^.]{0,40}lower-level)|` +
		`(transitions? use lower-level)`)
	lowerCondRe := regexp.MustCompile(`(?i)lower-level (phases, )?conditions`)
	var withConditions, withoutConditions []string
	for i, l := range strings.Split(doc, "\n") {
		if !siteRe.MatchString(l) {
			continue
		}
		entry := fmt.Sprintf("line %d: %s", i+1, strings.TrimSpace(l))
		if len(entry) > 160 {
			entry = entry[:160] + "..."
		}
		// "names lower-level conditions as an input" — the word must appear in
		// the lower-level input enumeration, not in a trailing clause such as
		// "and uses Accepted / Progressing / Ready conditions".
		if lowerCondRe.MatchString(l) {
			withConditions = append(withConditions, entry)
		} else {
			withoutConditions = append(withoutConditions, entry)
		}
	}
	if len(withConditions)+len(withoutConditions) < 3 {
		t.Fatalf("F9-guard anchor drift: found only %d RBG transition-input statements (want >= 3)",
			len(withConditions)+len(withoutConditions))
	}
	if len(withConditions) > 0 && len(withoutConditions) > 0 {
		t.Errorf("F9: inconsistent RBG transition-input vocabulary — %d statement(s) name "+
			"lower-level conditions, %d do not:\n  with conditions:\n    %s\n  without:\n    %s",
			len(withConditions), len(withoutConditions),
			strings.Join(withConditions, "\n    "), strings.Join(withoutConditions, "\n    "))
	}
}

// ---------------------------------------------------------------------------
// F10 (round-2 late discovery on unchanged text, fixed in round 3): the RIS
// Progressing prose must enumerate the complete RIS activity-reason
// vocabulary, not a subset. The source of truth is the "Activity and
// child-readiness reasons" family table: the RIS column of the Initial
// creation / Update / Instance-count scaling / Recovery rows (child readiness
// is named separately in the same sentence). The prose must list exactly that
// set — red at f05e1dd and 6335eac ("may include ScalingUp, ScalingDown,
// RollingUpdate" — missing Creating, Recovering), green once enumerated.
// ---------------------------------------------------------------------------

func TestRISActivityReasonsEnumerated(t *testing.T) {
	doc := kepDoc(t)

	// Derive the expected RIS activity reasons from the family table.
	activityFacts := map[string]bool{
		"Initial creation":       true,
		"Update":                 true,
		"Instance-count scaling": true,
		"Recovery":               true,
	}
	expected := map[string]bool{}
	inTable := false
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, "| Fact |") {
			inTable = true
			continue
		}
		if inTable && !strings.HasPrefix(l, "|") {
			break
		}
		if !inTable || strings.HasPrefix(l, "| ---") {
			continue
		}
		cells := strings.Split(l, "|")
		if len(cells) < 5 {
			continue
		}
		fact := strings.TrimSpace(cells[1])
		if !activityFacts[fact] {
			continue
		}
		for _, tok := range backtickTokens(cells[3]) {
			expected[tok] = true
		}
	}
	if len(expected) == 0 {
		t.Fatal("F10-guard anchor drift: activity/child-readiness family table not found or empty")
	}

	// Find the RIS "Activity reasons ..." sentence and extract the enumeration
	// clause (everything before the first ';').
	risSec := sectionExact(doc, "RoleInstanceSet")
	if risSec == "" {
		t.Fatal("RoleInstanceSet section not found")
	}
	enumerated := map[string]bool{}
	foundSentence := false
	for _, l := range strings.Split(risSec, "\n") {
		idx := strings.Index(l, "Activity reasons")
		if idx == -1 {
			continue
		}
		foundSentence = true
		clause := l[idx:]
		if semi := strings.Index(clause, ";"); semi != -1 {
			clause = clause[:semi]
		}
		for _, tok := range backtickTokens(clause) {
			enumerated[tok] = true
		}
	}
	if !foundSentence {
		t.Fatal("F10-guard anchor drift: RIS 'Activity reasons' sentence not found")
	}

	var missing, extra []string
	for tok := range expected {
		if !enumerated[tok] {
			missing = append(missing, tok)
		}
	}
	for tok := range enumerated {
		if !expected[tok] {
			extra = append(extra, tok)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("F10: RIS activity-reason prose does not match the family table — "+
			"missing from prose: %v; enumerated but not an RIS activity reason: %v", missing, extra)
	}
}

// ---------------------------------------------------------------------------
// C4457744212 (round-3 Copilot advisory, fixed in round 4 at da818779): the
// reporting-priority list resolved ties BETWEEN reason families but not WITHIN
// the Activity family across heterogeneous role facts (one role Recovering
// while another updates). The fix must declare a deterministic within-Activity
// precedence for RBG covering exactly the family table's RBG activity reasons
// (Creating, Recovering, ScalingOrUpdating), in the reviewed order
// Creating > Recovering > ScalingOrUpdating, and the Test Plan must cover the
// concurrent-activity scenarios. Red at f622d642, green at da818779.
// ---------------------------------------------------------------------------

// withinActivityOrder parses the RBG within-Activity precedence fence (the
// declaration introduced for C4457744212): the phrase "Within the Activity
// family" followed by a fenced, one-reason-per-line list. Returns nil when no
// such declaration exists.
func withinActivityOrder(doc string) []string {
	lines := strings.Split(doc, "\n")
	declIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "Within the Activity family") {
			declIdx = i
			break
		}
	}
	if declIdx == -1 {
		return nil
	}
	var listed []string
	open := false
	for _, l := range lines[declIdx+1:] {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			if open {
				break
			}
			open = true
			continue
		}
		if open {
			tok := strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(l), "`", ""))
			if tok != "" {
				listed = append(listed, tok)
			}
		}
	}
	return listed
}

// rbgActivityReasons derives the RBG activity reasons from the "Activity and
// child-readiness reasons" family table (RBG column), excluding the
// child-readiness row. Returns nil when the table is absent.
func rbgActivityReasons(doc string) []string {
	activityFacts := map[string]bool{
		"Initial creation":       true,
		"Update":                 true,
		"Instance-count scaling": true,
		"Recovery":               true,
	}
	seen := map[string]bool{}
	var out []string
	inTable := false
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, "| Fact |") {
			inTable = true
			continue
		}
		if inTable && !strings.HasPrefix(l, "|") {
			break
		}
		if !inTable || strings.HasPrefix(l, "| ---") {
			continue
		}
		cells := strings.Split(l, "|")
		if len(cells) < 6 {
			continue
		}
		if !activityFacts[strings.TrimSpace(cells[1])] {
			continue
		}
		for _, tok := range backtickTokens(cells[4]) {
			if !seen[tok] {
				seen[tok] = true
				out = append(out, tok)
			}
		}
	}
	return out
}

func TestRBGActivityPrecedenceDefined(t *testing.T) {
	doc := kepDoc(t)

	// Derive the RBG activity reasons from the "Activity and child-readiness
	// reasons" family table (RBG column), excluding the child-readiness row.
	activityFacts := map[string]bool{
		"Initial creation":       true,
		"Update":                 true,
		"Instance-count scaling": true,
		"Recovery":               true,
	}
	expected := map[string]bool{}
	inTable := false
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, "| Fact |") {
			inTable = true
			continue
		}
		if inTable && !strings.HasPrefix(l, "|") {
			break
		}
		if !inTable || strings.HasPrefix(l, "| ---") {
			continue
		}
		cells := strings.Split(l, "|")
		if len(cells) < 6 {
			continue
		}
		if !activityFacts[strings.TrimSpace(cells[1])] {
			continue
		}
		for _, tok := range backtickTokens(cells[4]) {
			expected[tok] = true
		}
	}
	if len(expected) == 0 {
		t.Fatal("C4457744212-guard anchor drift: activity family table RBG column not found or empty")
	}

	// Locate the within-Activity precedence declaration: the phrase
	// "Within the Activity family" followed by a fenced precedence list.
	lines := strings.Split(doc, "\n")
	declIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "Within the Activity family") {
			declIdx = i
			break
		}
	}
	if declIdx == -1 {
		t.Errorf("C4457744212: no within-Activity precedence declaration found — " +
			"the reporting-priority list still resolves ties between families only, " +
			"leaving concurrent heterogeneous activities (e.g. one role Recovering, " +
			"another updating) without a deterministic RBG reason")
		return
	}
	var listed []string
	open := false
	for _, l := range lines[declIdx+1:] {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			if open {
				break
			}
			open = true
			continue
		}
		if open {
			// Precedence fences list one bare reason per line (no backticks),
			// matching the cross-family list's style.
			tok := strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(l), "`", ""))
			if tok != "" {
				listed = append(listed, tok)
			}
		}
	}
	if len(listed) == 0 {
		t.Errorf("C4457744212: within-Activity precedence declaration has no fenced reason list")
		return
	}
	// Exhaustive and duplicate-free: exactly the family table's RBG activity reasons.
	seen := map[string]int{}
	for _, tok := range listed {
		seen[tok]++
	}
	var missing, extra, dup []string
	for tok := range expected {
		if seen[tok] == 0 {
			missing = append(missing, tok)
		}
	}
	for tok, n := range seen {
		if !expected[tok] {
			extra = append(extra, tok)
		}
		if n > 1 {
			dup = append(dup, tok)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(dup)
	if len(missing) > 0 || len(extra) > 0 || len(dup) > 0 {
		t.Errorf("C4457744212: within-Activity precedence list does not match the family table — "+
			"missing: %v; not an RBG activity reason: %v; listed twice: %v", missing, extra, dup)
	}
	// Reviewed order semantics (round-7 re-anchor, 05807351): Creating ranks
	// first and Recovering outranks the update/scaling aggregate reason. The
	// aggregate was renamed (ScalingOrUpdating -> RoleInstanceSetUpdated), so
	// the check is positional instead of name-hardcoded; set equality with the
	// family table was already enforced above.
	if listed[0] != "Creating" {
		t.Errorf("C4457744212: within-Activity precedence must rank Creating first, got %v", listed)
	}
	recIdx := -1
	for i, tok := range listed {
		if tok == "Recovering" {
			recIdx = i
			break
		}
	}
	if recIdx == -1 {
		t.Errorf("C4457744212: within-Activity precedence omits Recovering: %v", listed)
	}
	for i, tok := range listed {
		if tok != "Creating" && tok != "Recovering" && recIdx >= 0 && i < recIdx {
			t.Errorf("C4457744212: %s outranks Recovering in the within-Activity precedence %v; "+
				"reviewed semantics rank Recovering above the update/scaling aggregate", tok, listed)
		}
	}
}

func TestRBGActivityPrecedenceCoverage(t *testing.T) {
	doc := kepDoc(t)
	// The Test Plan must cover each branch of the precedence with a
	// concurrent-activity scenario. Round-5 re-anchor (0b7285b7): the delta
	// rewrote the bullets and DROPPED the Creating branch (previously "when one
	// role is Creating and another is updating or recovering, RBG uses
	// Creating") while the precedence rule still ranks Creating first — the
	// per-branch check below keeps every branch of the declared order covered.
	tp := section(doc, "Test Plan")
	if tp == "" {
		t.Fatal("C4457744212-guard anchor drift: Test Plan section not found")
	}
	// Round-7 re-anchor (05807351): derive the branches from the declared
	// within-Activity precedence fence when present (robust to the
	// ScalingOrUpdating -> RoleInstanceSetUpdated rename); fall back to the
	// family table's RBG activity reasons on heads without the fence.
	branches := withinActivityOrder(doc)
	if len(branches) == 0 {
		branches = rbgActivityReasons(doc)
	}
	if len(branches) == 0 {
		t.Fatal("C4457744212-guard anchor drift: cannot derive the RBG activity branches")
	}
	branchCovered := map[string]bool{}
	for _, b := range branches {
		branchCovered[b] = false
	}
	for _, l := range strings.Split(tp, "\n") {
		// A concurrency bullet names the winning reason together with another
		// concurrent activity ("another", "overlap", "concurrent", "while",
		// "different" as in "when different RISs are updating and scaling").
		concurrency := strings.Contains(l, "another") || strings.Contains(l, "overlap") ||
			strings.Contains(l, "concurrent") || strings.Contains(l, "while") ||
			strings.Contains(l, "different")
		if !concurrency {
			continue
		}
		for br := range branchCovered {
			if strings.Contains(l, "`"+br+"`") {
				branchCovered[br] = true
			}
		}
	}
	var uncovered []string
	for br, ok := range branchCovered {
		if !ok {
			uncovered = append(uncovered, br)
		}
	}
	sort.Strings(uncovered)
	if len(uncovered) > 0 {
		t.Errorf("F13: Test Plan has no concurrent-activity scenario for precedence "+
			"branch(es) %v — every branch of the declared order %v needs a scenario, "+
			"e.g. one role Creating while another updates",
			uncovered, branches)
	}
}

// ---------------------------------------------------------------------------
// F11 (round-4 discovery, nit): the RIS Progressing reason selection has no
// tie-break when recovery overlaps an active update or scaling workflow. The
// RBG layer gained a within-Activity precedence at da818779, and :349 resolves
// RIS update-vs-scaling, but nothing states whether a RIS reports Recovering
// or RollingUpdate/ScalingUp/ScalingDown when both facts are present (the
// "RIS Progressing follows RBG rules for progress" sentence does not name the
// Activity precedence). Green once the KEP states the RIS within-Activity tie
// handling (explicit precedence, a yield rule, or explicit inheritance of the
// RBG Activity precedence). Red at da818779 = the open gap.
// ---------------------------------------------------------------------------

func TestRISActivityTieBreakStated(t *testing.T) {
	doc := kepDoc(t)
	ris := sectionExact(doc, "RoleInstanceSet")
	if ris == "" {
		t.Fatal("F11-guard anchor drift: RoleInstanceSet section not found")
	}
	if !strings.Contains(ris, "`Recovering`") {
		t.Fatal("F11-guard anchor drift: RIS section no longer mentions Recovering activity")
	}
	stated := false
	precRe := regexp.MustCompile(`(?i)precedence|outranks|higher-priority|takes priority|yields to`)
	for _, l := range strings.Split(ris, "\n") {
		if strings.Contains(l, "`Recovering`") && precRe.MatchString(l) {
			t.Logf("F11: RIS tie-break statement found: %s", strings.TrimSpace(l))
			stated = true
			break
		}
		// Explicit inheritance of the Activity precedence also closes the gap.
		if strings.Contains(l, "Activity") && precRe.MatchString(l) &&
			(strings.Contains(l, "RIS") || strings.Contains(l, "follows")) {
			t.Logf("F11: RIS inherits Activity precedence: %s", strings.TrimSpace(l))
			stated = true
			break
		}
	}
	if !stated {
		t.Errorf("F11: RIS Progressing reason selection is underdetermined when recovery " +
			"overlaps an active update or scaling workflow — no statement in the RoleInstanceSet " +
			"section ties `Recovering` against update / scaling reasons")
	}
	// Round-5 strengthening: the stated tie-break must also be exercised by the
	// Test Plan (the round-5 fix added exactly such a bullet).
	tp := section(doc, "Test Plan")
	covered := false
	for _, l := range strings.Split(tp, "\n") {
		if strings.Contains(l, "`Recovering`") && strings.Contains(l, "RIS") &&
			(strings.Contains(l, "overlap") || strings.Contains(l, "update or scaling") ||
				strings.Contains(l, "update/scaling")) {
			covered = true
			break
		}
	}
	if !covered {
		t.Errorf("F11: Test Plan has no RIS recovery-overlaps-workflow scenario bullet")
	}
}

// ---------------------------------------------------------------------------
// F12 (round-4 discovery on the new precedence text, minor; Copilot
// C4471100373 independently verified): the within-Activity precedence operates
// on "Activity facts", and the KEP's aggregation model (:169) derives parent
// reasons from lower-level facts — so a replacement RIS reporting `Creating`
// (new UID, :213) during an RBG recovery presents a Creating fact that
// outranks Recovering, and the new Test Plan bullet (:495) would then codify
// reporting `Creating` for a group whose own semantics (:213) call the same
// situation recovery. Green once the KEP states how replacement-child creation
// maps at the parent level (e.g. reported under `Recovering` during RBG
// recovery) or scopes RBG `Creating` to the group's initial creation.
// Red at da818779 = the mapping is unstated.
// ---------------------------------------------------------------------------

func TestRBGReplacementCreationMapsToRecovery(t *testing.T) {
	doc := stripFences(kepDoc(t))
	// Pattern A: an explicit mapping statement — a line that ties a replacement
	// / new-UID / recreated child to the parent reporting the `Recovering`
	// reason. (The :199 definitional sentence "Recovering is used when an
	// established child is being repaired or replaced" is NOT such a mapping:
	// it defines when RBG uses Recovering, but does not say how a replacement
	// child's own Creating report maps at the parent, and the family table's
	// Initial creation -> Creating row (:169) remains unconditional.)
	recMap := regexp.MustCompile("`Recovering`")
	repl := regexp.MustCompile(`(?i)new UID|replacement|recreat`)
	rbg := regexp.MustCompile(`(?i)RBG|parent`)
	// Pattern B: RBG `Creating` is explicitly scoped to the group's own initial
	// creation (so it cannot be asserted during recovery). Any order of the
	// scoping keyword and the scope phrase is accepted.
	for _, l := range strings.Split(doc, "\n") {
		if recMap.MatchString(l) && repl.MatchString(l) && rbg.MatchString(l) {
			t.Logf("F12: replacement->recovery mapping found: %s", strings.TrimSpace(l))
			return
		}
		hasCreating := strings.Contains(l, "`Creating`")
		hasScopePhrase := strings.Contains(l, "first steady state") || strings.Contains(l, "initial creation")
		hasScopeKw := strings.Contains(l, "only") || strings.Contains(l, "unless") ||
			strings.Contains(l, "reserved") || strings.Contains(l, "scope")
		if hasCreating && hasScopePhrase && hasScopeKw {
			t.Logf("F12: Creating scoped to initial creation: %s", strings.TrimSpace(l))
			return
		}
	}
	// Round-6 strengthening (head 39e2ecdf): the fix must also cover the
	// scale-up extension (Copilot C4495569498) — an established set scaling
	// out (a required RI that does not yet exist because the target grew)
	// must be classified `ScalingUp`, not `Creating`.
	scaleOK := false
	for _, l := range strings.Split(doc, "\n") {
		if strings.Contains(l, "`ScalingUp`") &&
			(strings.Contains(l, "scale-up") || strings.Contains(l, "scaling")) &&
			(strings.Contains(l, "new RI") || strings.Contains(l, "adds instances") ||
				strings.Contains(l, "not yet created")) {
			scaleOK = true
			break
		}
	}
	if !scaleOK {
		t.Errorf("F12(scale-up): no statement classifies a scale-up that creates a new RI as " +
			"`ScalingUp` (rather than `Creating`) — the established-set scale-up case must not " +
			"fall under the Creating reason")
		return
	}
	t.Errorf("F12: the within-Activity precedence (:191-197) can still report `Creating` for an " +
		"RBG that is mid-recovery. A replacement RIS reports Creating (RIS-level definition :347; " +
		"family table Initial creation -> Creating :169 is unconditional) and parents aggregate " +
		"child facts through the reason-family mapping (:175-179), while Creating outranks " +
		"Recovering (:191-197). The :199 definitions scope RBG `Creating` to 'a required child " +
		"object does not yet exist' and `Recovering` to 'an established child is being repaired " +
		"or replaced', but nothing states whether the family mapping or the situational " +
		"definition governs a replacement child. State the replacement-child -> parent-reason " +
		"mapping (replacement child reported under `Recovering`), or scope RBG `Creating` to " +
		"the group's initial creation")
}

// ---------------------------------------------------------------------------
// Round-5 additions (head 0b7285b7): the pivot dropped RBG/RIS status.phase,
// added the Consuming-the-Status decision table, the blocker-classification
// paragraph, the RIS activity precedence, the RI phase edge table, and new
// factual claims about current code (stateless/stateful readiness difference,
// RI condition timestamp rebuilds, lifecycle label). Premise guards PASS on
// the reviewed head and pin the KEP's claims about main; F14/F15 are CONTRACT
// findings tests that FAIL on the reviewed head (a red result IS the
// reproduction).
// ---------------------------------------------------------------------------

// funcBody returns the source of the function whose declaration contains
// declSubstr, from declSubstr up to the next top-level "func " (or EOF).
func funcBody(src, declSubstr string) string {
	i := strings.Index(src, declSubstr)
	if i == -1 {
		return ""
	}
	rest := src[i:]
	if j := strings.Index(rest[1:], "\nfunc "); j != -1 {
		return rest[:j+1]
	}
	return rest
}

// P0 (round-5 extension): the KEP claims the two RIS modes count RI readiness
// differently — stateless requires RoleInstanceReady plus every
// spec.readinessGates condition, stateful checks only RoleInstanceReady.
// PASS = claim holds in code.
func TestPremiseRISReadinessModeDifference(t *testing.T) {
	stateless := repoFile(t, "pkg/reconciler/roleinstanceset/statelessmode/utils/utils.go")
	sl := funcBody(stateless, "func IsRunningAndReady(")
	if sl == "" {
		t.Fatal("stateless IsRunningAndReady not found")
	}
	if !strings.Contains(sl, "RoleInstanceReady") || !strings.Contains(sl, "Spec.ReadinessGates") {
		t.Errorf("P0-mode-diff: stateless IsRunningAndReady no longer checks RoleInstanceReady " +
			"plus every spec.readinessGates condition")
	}
	stateful := repoFile(t, "pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_utils.go")
	sf := funcBody(stateful, "func isHealthy(")
	if sf == "" {
		t.Fatal("stateful isHealthy not found")
	}
	if !strings.Contains(sf, "RoleInstanceReady") {
		t.Errorf("P0-mode-diff: stateful isHealthy no longer checks RoleInstanceReady")
	}
	if strings.Contains(sf, "ReadinessGates") {
		t.Errorf("P0-mode-diff: stateful isHealthy now also checks readiness gates — " +
			"the KEP's documented stateless/stateful difference is stale")
	}
}

// P0 (round-5 extension): the KEP claims the RI controller rebuilds
// RoleInstanceReady / RoleInstanceAllPodsReady with the current time and
// replaces the condition list, resetting lastTransitionTime on every write.
// PASS = claim holds in code.
func TestPremiseRIConditionTimestampsRebuilt(t *testing.T) {
	src := repoFile(t, "pkg/reconciler/roleinstance/instance_status.go")
	for _, fn := range []string{"func getInstanceReadyCondition(", "func (r *realStatusUpdater) getAllReadyCondition("} {
		body := funcBody(src, fn)
		if body == "" {
			t.Fatalf("%s not found", fn)
		}
		if !strings.Contains(body, "LastTransitionTime: metav1.NewTime(time.Now())") {
			t.Errorf("P0-timestamps: %s no longer stamps LastTransitionTime with the current time", fn)
		}
	}
	set := funcBody(src, "func (r *realStatusUpdater) setInstanceConditions(")
	if set == "" {
		t.Fatal("setInstanceConditions not found")
	}
	if !strings.Contains(set, "newStatus.Conditions = conditions") {
		t.Errorf("P0-timestamps: setInstanceConditions no longer replaces the condition list")
	}
	// The rebuilt readiness conditions must not carry the persisted timestamp
	// forward: neither constructor references the previously persisted value.
	for _, fn := range []string{"func getInstanceReadyCondition(", "func (r *realStatusUpdater) getAllReadyCondition("} {
		body := funcBody(src, fn)
		if strings.Contains(body, "instance.Status.Conditions") {
			t.Errorf("P0-timestamps: %s now preserves the persisted LastTransitionTime — "+
				"the KEP's migration-row claim about timestamp resets is stale", fn)
		}
	}
}

// P0 (round-5 extension; code premise for F15): BOTH RIS modes derive
// minReadySeconds availability from RoleInstanceReady.lastTransitionTime.
// PASS = both modes read the timestamp.
func TestPremiseMinReadySecondsTimestampBothModes(t *testing.T) {
	stateless := repoFile(t, "pkg/reconciler/roleinstanceset/statelessmode/utils/utils.go")
	sl := funcBody(stateless, "func IsRunningAndAvailable(")
	if sl == "" || !strings.Contains(sl, "RoleInstanceReady") || !strings.Contains(sl, "LastTransitionTime") {
		t.Errorf("P0-minready: stateless IsRunningAndAvailable no longer reads " +
			"RoleInstanceReady.lastTransitionTime")
	}
	stateful := repoFile(t, "pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_helper.go")
	sf := funcBody(stateful, "func isInstanceAvailable(")
	if sf == "" || !strings.Contains(sf, "RoleInstanceReady") || !strings.Contains(sf, "LastTransitionTime") {
		t.Errorf("P0-minready: stateful isInstanceAvailable no longer reads " +
			"RoleInstanceReady.lastTransitionTime — re-evaluate F15 (the KEP attributes this to " +
			"stateless only; if stateful stops using it, the KEP sentence becomes accurate)")
	}
}

// P0 (round-5 extension): the KEP names the reserved lifecycle-state label
// rbg.workloads.x-k8s.io/role-instance-lifecycle-state with values Normal,
// PreparingUpdate, Updating, Updated, PreparingDelete. PASS = all exist.
func TestPremiseLifecycleLabelContract(t *testing.T) {
	ann := repoFile(t, "api/workloads/constants/annotation.go")
	if !strings.Contains(ann, `"role-instance-lifecycle-state"`) {
		t.Errorf("P0-lifecycle-label: role-instance-lifecycle-state key not found")
	}
	c := repoFile(t, "api/workloads/constants/constants.go")
	if !strings.Contains(c, `RBGPrefix      = "rbg.workloads.x-k8s.io/"`) &&
		!strings.Contains(c, `RBGPrefix = "rbg.workloads.x-k8s.io/"`) {
		t.Errorf("P0-lifecycle-label: RBGPrefix is no longer rbg.workloads.x-k8s.io/ — " +
			"the KEP's spelled-out label is stale")
	}
	for _, v := range []string{`"Normal"`, `"PreparingUpdate"`, `"Updating"`, `"Updated"`, `"PreparingDelete"`} {
		if !strings.Contains(c, v) {
			t.Errorf("P0-lifecycle-label: lifecycle value %s no longer defined", v)
		}
	}
}

// P0 (round-5 extension): the KEP claims RBG Ready "retains current behavior
// and is never set to Unknown". PASS = the RBG controller writes Ready only as
// True/False (no ConditionUnknown path).
func TestPremiseRBGReadyNeverUnknown(t *testing.T) {
	ctrl := repoFile(t, "internal/controller/workloads/rolebasedgroup_controller.go")
	if !strings.Contains(ctrl, "RoleBasedGroupReady") {
		t.Fatal("RBG Ready condition write not found — re-evaluate the claim")
	}
	if strings.Contains(ctrl, "ConditionUnknown") {
		t.Errorf("P0-ready-unknown: the RBG controller now has a ConditionUnknown path — " +
			"the KEP's 'retains current behavior, never Unknown' claim is stale")
	}
}

// ---------------------------------------------------------------------------
// F14 (round 5, nit): the new "Consuming the Status" decision table tells
// consumers that "needs attention" = Accepted=False OR Progressing=True with a
// reason in the blocker OR "configuration-compatibility" families. But the
// candidate-reasons table maps every configuration-incompatibility reason
// (InvalidSpec, TemplateInvalid, DependencyCycle, IncompatibleGangConfig,
// SchedulerUnsupported) to Progressing=False, and :234 confirms
// "When Accepted=False, Progressing=False uses the matching incompatibility
// reason" — so the Progressing=True disjunct can never fire for those
// families. A consumer codifying this table (the doc points Flux CEL / Argo
// Lua at it) gets a dead branch and may conclude Progressing=False reasons
// never need attention. Contract: FAILS while the row lists
// configuration-compatibility under Progressing=True and no Progressing=True
// candidate row carries a config-incompatibility reason.
// ---------------------------------------------------------------------------

func TestConsumingTableAttentionRowConsistency(t *testing.T) {
	doc := kepDoc(t)
	cs := section(doc, "Consuming the Status")
	if cs == "" {
		t.Fatal("F14-guard anchor drift: Consuming the Status section not found")
	}
	var attention string
	for _, l := range strings.Split(cs, "\n") {
		if strings.Contains(l, "need attention") {
			attention = l
			break
		}
	}
	if attention == "" {
		t.Fatal("F14-guard anchor drift: 'need attention' row not found")
	}
	// Round-7 re-anchor (05807351): the redesign made configuration-family
	// blockers report Progressing=True, so the original defect (a dead
	// config-compatibility disjunct under Progressing=True) is resolved. The
	// durable contract, generalized: EVERY reason family the attention row
	// names must have at least one RBG reason that can actually occur with the
	// condition status the row predicates on — no dead disjuncts.
	var statusTok string
	switch {
	case strings.Contains(attention, "`Progressing=True`"):
		statusTok = "Progressing=True"
	case strings.Contains(attention, "`Progressing=False`"):
		statusTok = "Progressing=False"
	default:
		t.Fatal("F14-guard anchor drift: attention row names no Progressing status")
	}
	rows := candidateReasonsTable(t)
	avail := map[string]bool{}
	for name, reasons := range rows {
		if strings.HasPrefix(name, statusTok) {
			for _, r := range reasons {
				avail[r] = true
			}
		}
	}
	// Map family names (as the row names them) to RBG reasons: the reason-
	// family table's RBG column, plus the Accepted=False candidate row for
	// configuration on pre-redesign heads.
	famReasons := map[string][]string{}
	rt := section(doc, "Reason Taxonomy and Aggregation")
	inFam := false
	for _, l := range strings.Split(rt, "\n") {
		if strings.HasPrefix(l, "| Reason family |") {
			inFam = true
			continue
		}
		if inFam && !strings.HasPrefix(l, "|") {
			break
		}
		if !inFam || strings.HasPrefix(l, "| ---") {
			continue
		}
		cells := strings.Split(l, "|")
		if len(cells) < 5 {
			continue
		}
		famReasons[strings.ToLower(strings.TrimSpace(cells[1]))] = backtickTokens(cells[4])
	}
	famReasons["configuration"] = append(famReasons["configuration"], rows["Accepted=False"]...)
	alias := map[string]string{
		"configuration-compatibility": "configuration",
		"configuration":               "configuration",
		"permission":                  "permission",
		"admission":                   "admission",
		"quota":                       "quota",
		"resources":                   "resources",
		"gang":                        "gang scheduling",
		"gang scheduling":             "gang scheduling",
		"dependencies":                "dependencies",
		"unclassified":                "unclassified",
	}
	var dead []string
	for name := range alias {
		if !strings.Contains(attention, name) {
			continue
		}
		live := false
		for _, r := range famReasons[alias[name]] {
			if avail[r] {
				live = true
				break
			}
		}
		if !live {
			dead = append(dead, name)
		}
	}
	sort.Strings(dead)
	if len(dead) > 0 {
		t.Errorf("F14: the 'need attention' row predicates on %s but these families have no "+
			"RBG reason that can occur with that status (candidate table): %s — dead disjunct(s) "+
			"point consumers at a row that can never fire", statusTok, strings.Join(dead, ", "))
	}
}

// ---------------------------------------------------------------------------
// F15 (round 5, nit): the new "RI transition timestamps" migration row says
// "stateless RIS uses these timestamps for minReadySeconds" — but the stateful
// mode's isInstanceAvailable ALSO derives availability from
// RoleInstanceReady.lastTransitionTime (stateful_instance_set_helper.go),
// feeding status.availableReplicas. The row under-scopes the behavior change;
// migration regression tests must cover minReadySeconds in BOTH modes.
// Contract: FAILS while the row names stateless without stateful (and code
// still shows both modes reading the timestamp).
// ---------------------------------------------------------------------------

func TestReadyTimestampMigrationScope(t *testing.T) {
	// Code premise first: both modes must still read the timestamp.
	stateful := repoFile(t, "pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_helper.go")
	sf := funcBody(stateful, "func isInstanceAvailable(")
	statefulUses := sf != "" && strings.Contains(sf, "RoleInstanceReady") &&
		strings.Contains(sf, "LastTransitionTime")
	if !statefulUses {
		t.Skip("stateful mode no longer reads RoleInstanceReady.lastTransitionTime — " +
			"the KEP's stateless-only scoping is accurate; finding moot")
	}
	doc := kepDoc(t)
	var row string
	for _, l := range strings.Split(doc, "\n") {
		if strings.Contains(l, "RI transition timestamps") ||
			strings.Contains(l, "every status write resets `lastTransitionTime`") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatal("F15-guard anchor drift: RI transition-timestamps migration row not found")
	}
	low := strings.ToLower(row)
	if strings.Contains(low, "stateless") && !strings.Contains(low, "stateful") {
		t.Errorf("F15: the migration row attributes the minReadySeconds dependency on " +
			"RoleInstanceReady.lastTransitionTime to stateless mode only, but stateful " +
			"isInstanceAvailable reads the same timestamp — scope the behavior change and the " +
			"regression tests to both modes")
	}
}

// ---------------------------------------------------------------------------
// F15-precheck (round-5 ledger finding; FIXED at 39e2ecdf): the common-rule
// dispatch bullet prescribed "write status.observedGeneration=N together with
// Progressing=True ... Preflight or compatibility failures must not skip this
// status write" while the Accepted section requires Accepted=False ->
// Progressing=False with the matching incompatibility reason. For exactly the
// preCheck-failure case the bullet names, the unconditional True contradicted
// the mapping. Contract: FAILS while the dispatch-write rule prescribes
// Progressing=True without deriving the value from the validation / pause /
// progress rules and naming the compatibility-failure -> Progressing=False
// case. Red at 0b7285b7, green at 39e2ecdf.
// ---------------------------------------------------------------------------

func TestPreCheckProgressingWriteDerived(t *testing.T) {
	doc := stripFences(kepDoc(t))
	var rule, testplan string
	for _, l := range strings.Split(doc, "\n") {
		if strings.Contains(l, "observedGeneration=N") && strings.Contains(l, "dispatch") {
			if rule == "" {
				rule = l
			}
		}
		if strings.Contains(l, "Before or during dispatch of a new generation") {
			testplan = l
		}
	}
	if rule == "" || testplan == "" {
		t.Fatal("F15-guard anchor drift: dispatch-write rule or its Test Plan bullet not found")
	}
	for name, l := range map[string]string{"common rule": rule, "test plan": testplan} {
		unconditionalTrue := strings.Contains(l, "together with `Progressing=True`")
		derived := strings.Contains(l, "derived from") && strings.Contains(l, "validation") &&
			strings.Contains(l, "pause") && strings.Contains(l, "progress rules")
		if unconditionalTrue || !derived {
			t.Errorf("F15: %s still prescribes the dispatch write as unconditionally "+
				"Progressing=True (or no longer derives the value from the validation/pause/"+
				"progress rules) — a preCheck compatibility failure must write Progressing=False "+
				"with the matching incompatibility reason (Accepted=False mapping): %s",
				name, strings.TrimSpace(l))
		}
	}
	// The confirmed configuration-failure case must be named explicitly, and
	// the value the rule prescribes must agree with the doc's own mapping
	// (round-7 re-anchor, 05807351): pre-redesign heads map Accepted=False ->
	// Progressing=False with the matching incompatibility reason; the
	// redesigned model maps a confirmed configuration-family blocker ->
	// Progressing=True with the configuration-family reason.
	valSec := section(doc, "Validation Ownership")
	if valSec == "" {
		t.Fatal("F15-guard anchor drift: Validation Ownership section not found")
	}
	configMapsFalse := strings.Contains(valSec, "Accepted=False") &&
		strings.Contains(valSec, "`Progressing=False`")
	if !(strings.Contains(rule, "compatib") || strings.Contains(rule, "configuration")) {
		t.Errorf("F15: the common dispatch-write rule does not name the confirmed " +
			"compatibility / configuration-failure case")
		return
	}
	if configMapsFalse {
		if !strings.Contains(rule, "`Progressing=False`") {
			t.Errorf("F15: the validation section maps confirmed incompatibility to " +
				"Progressing=False, but the dispatch-write rule does not prescribe that value")
		}
		return
	}
	// Redesigned model: config blockers are Progressing=True.
	ruleHasTrue := strings.Contains(rule, "configuration-family blocker writes `Progressing=True`") ||
		(strings.Contains(rule, "`Progressing=True`") && strings.Contains(rule, "configuration-family"))
	if !ruleHasTrue {
		t.Errorf("F15: the validation section reports confirmed configuration-family blockers as " +
			"Progressing=True, but the dispatch-write rule does not prescribe that value")
	}
}

// ---------------------------------------------------------------------------
// F18 (round 6, nit): the RI section now defers the hook protocol to
// "KEP-527" ("Hook execution, retries, and release use the hook-state labels
// and finalizer handshake defined by KEP-527"; "consistent with the KEP-527
// hook rules"; "This matches KEP-527"). KEP-527 is PR #546, still OPEN — no
// keps/527-* directory exists in this tree, so if this KEP merges first the
// normative pointer dangles (and the round-5 code-verified concrete label
// sentence it replaced is gone). Contract: FAILS while any "KEP-<n>"
// reference has no keps/<n>-* directory in-repo and the referencing line
// does not mark the target as pending / provisional / under review.
// ---------------------------------------------------------------------------

func TestReferencedKEPsResolvable(t *testing.T) {
	doc := stripFences(kepDoc(t))
	entries, err := os.ReadDir(filepath.Join(repoRoot(), "keps"))
	if err != nil {
		t.Fatalf("read keps/: %v", err)
	}
	have := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			if n := strings.SplitN(e.Name(), "-", 2); len(n) == 2 {
				have[n[0]] = true
			}
		}
	}
	kepRe := regexp.MustCompile(`KEP-(\d+)`)
	qualRe := regexp.MustCompile(`(?i)pending|provisional|under review|unmerg|PR #|upcoming`)
	var dangling []string
	seen := map[string]bool{}
	for _, l := range strings.Split(doc, "\n") {
		for _, m := range kepRe.FindAllStringSubmatch(l, -1) {
			if m[1] == "502" || seen[m[1]] {
				continue // self-reference
			}
			seen[m[1]] = true
			if have[m[1]] {
				continue
			}
			if qualRe.MatchString(l) {
				t.Logf("F18: KEP-%s not in tree but marked pending/provisional: %s",
					m[1], strings.TrimSpace(l))
				continue
			}
			dangling = append(dangling, "KEP-"+m[1])
		}
	}
	if len(dangling) > 0 {
		t.Errorf("F18: normative references to KEPs not present in this tree (keps/ has no "+
			"matching directory) and not marked pending: %s — keep the concrete hook-protocol "+
			"sentence alongside the pointer, or mark the referenced KEP as a pending proposal",
			strings.Join(dangling, ", "))
	}
}

// ---------------------------------------------------------------------------
// F19 (round 6, minor): the F12 fix scoped `Creating` to initial
// establishment at both layers, but the two boundaries read asymmetrically.
// RBG: "before the target's required roles have been established"
// (collective — Creating lasts until ALL required roles are established).
// RIS: "before the RIS has an established revision or established RIs for
// its target" (existential — the FIRST established revision or RI ends it).
// Read existentially, an initial 0 -> 3 bootstrap stops being `Creating`
// once RI-0 establishes, and the still-missing initial RIs fall under
// "a required RI that does not yet exist" -> `ScalingUp` — the exact
// misclassification the Test Plan bullet tells implementers to distinguish
// ("Distinguish RIS initial creation from scale-up"). Contract: FAILS while
// the RIS definition lacks a collective boundary marker (the RIs / instances
// required by the target) or explicit mid-bootstrap handling.
// ---------------------------------------------------------------------------

func TestRISEstablishmentBoundaryCollective(t *testing.T) {
	doc := kepDoc(t)
	ris := sectionExact(doc, "RoleInstanceSet")
	if ris == "" {
		t.Fatal("F19-guard anchor drift: RoleInstanceSet section not found")
	}
	// Anchor on the boundary SENTENCE (a paragraph is one markdown line):
	// later sentences ("a required RI that does not yet exist ...") are the
	// classification rule, not the establishment boundary, and an earlier
	// sentence on the same line may be the precedence declaration.
	var def string
	for _, l := range strings.Split(ris, "\n") {
		for _, s := range strings.Split(l, ". ") {
			if strings.Contains(s, "`Creating` is used") {
				def = s
				break
			}
		}
		if def != "" {
			break
		}
	}
	if def == "" {
		t.Fatal("F19-guard anchor drift: RIS Creating definition not found")
	}
	collective := strings.Contains(def, "required") || strings.Contains(def, "all ") ||
		strings.Contains(def, "mid-bootstrap")
	if !collective {
		t.Errorf("F19: the RIS Creating boundary is existential ('an established revision or "+
			"established RIs') while the RBG analog is collective ('required roles have been "+
			"established') — read as written, the tail of an initial bootstrap reports ScalingUp. "+
			"Bind the boundary to the RIs / revision required by the target, or state how the "+
			"mid-bootstrap tail is classified. RIS definition: %s", strings.TrimSpace(def))
	}
}

// ---------------------------------------------------------------------------
// F22 (round 7, nit — new code-factual claim introduced by the delta): the RIS
// section states "Today's RIS code reads only `RoleInstanceReady`". Read in
// context the sentence means the RIS never reads the RI `Progressing`
// condition (true — pkg/reconciler/roleinstanceset has no Progressing reads),
// but as written it is absolute and false for stateless mode, whose
// IsRunningAndReady also reads every spec.readinessGates condition — the doc's
// own mode-difference sentence says so two paragraphs later. Contract: FAILS
// while the absolute claim stands and stateless code still iterates
// readinessGates; PASSES if the sentence is scoped (e.g. "reads no RI
// `Progressing` condition") or removed. Green at 39e2ecdf (sentence absent).
// ---------------------------------------------------------------------------

func TestRISBlockerPropagationClaimAccurate(t *testing.T) {
	doc := kepDoc(t)
	var claim string
	for _, l := range strings.Split(doc, "\n") {
		if strings.Contains(l, "reads only `RoleInstanceReady`") {
			claim = l
			break
		}
	}
	if claim == "" {
		return // sentence absent on pre-round-7 heads; nothing to falsify
	}
	code := repoFile(t, "pkg/reconciler/roleinstanceset/statelessmode/utils/utils.go")
	body := funcBody(code, "func IsRunningAndReady(")
	if body != "" && strings.Contains(body, "ReadinessGates") {
		t.Errorf("F22: the doc claims today's RIS code reads only `RoleInstanceReady`, but " +
			"stateless IsRunningAndReady also reads every spec.readinessGates condition " +
			"(pkg/reconciler/roleinstanceset/statelessmode/utils/utils.go) — the doc itself " +
			"documents that mode difference. Scope the sentence to blocker propagation " +
			"(e.g. 'reads no RI `Progressing` condition').")
	}
}

// ---------------------------------------------------------------------------
// Round-7 resolution pins (regression coverage for findings the delta fixed).
// Contract tests: green at 05807351, red at 39e2ecdf where the defect lived.
// ---------------------------------------------------------------------------

// Pin for Copilot advisory C4515733997 (resolved at 05807351): RI phase
// completion must not wait on externally owned readiness gates, or the
// lifecycle-owned gate held until Running creates a circular wait.
func TestPhaseCompletionIndependentOfExternalGates(t *testing.T) {
	doc := stripFences(kepDoc(t))
	ris := section(doc, "RI Phase Transition Principles") + "\n" + sectionExact(doc, "RoleInstance")
	stated := strings.Contains(ris, "do not gate phase transitions") ||
		strings.Contains(ris, "do not block the phase transition") ||
		strings.Contains(ris, "do not gate this phase") ||
		strings.Contains(ris, "controller-observed workload readiness")
	if !stated {
		t.Errorf("C4515733997: the doc no longer states that RI phase completion excludes " +
			"externally owned readiness gates — the lifecycle-gate circular wait can recur")
	}
}

// Pin for Copilot advisory C4515734101 (resolved at 05807351): an
// unclassifiable error must not displace a blocker family already confirmed
// for the current target.
func TestConfirmedBlockerSurvivesUnclassifiedError(t *testing.T) {
	doc := stripFences(kepDoc(t))
	stated := strings.Contains(doc, "does not replace it") &&
		strings.Contains(doc, "confirmed blocker remains") &&
		strings.Contains(doc, "fresh successful observation")
	if !stated {
		t.Errorf("C4515734101: the doc no longer states that an unclassifiable error does not " +
			"replace a confirmed blocker family (which remains until a fresh successful " +
			"observation) — the unconditional Activity fallback can displace a live blocker")
	}
}

// Pin for Copilot advisory C4515734270 (resolved at 05807351): the RI example
// must not show an externally owned gate determining RoleInstanceReady, and the
// doc must state that external gates do not change RoleInstanceReady.
func TestCustomReadyDoesNotDetermineInstanceReady(t *testing.T) {
	doc := kepDoc(t)
	ri := sectionExact(doc, "RoleInstance")
	if ri == "" {
		t.Fatal("anchor drift: RoleInstance section not found")
	}
	stated := strings.Contains(ri, "do not change `RoleInstanceReady`") ||
		strings.Contains(ri, "external gates are not folded into it")
	if !stated {
		t.Errorf("C4515734270: the doc no longer states that externally owned readiness gates " +
			"do not change `RoleInstanceReady`")
	}
	// The example must not pair RoleInstanceReady=False with a custom-gate cause.
	lines := strings.Split(ri, "\n")
	for i, l := range lines {
		if strings.Contains(l, "type: RoleInstanceReady") {
			window := strings.Join(lines[i:min(i+6, len(lines))], "\n")
			if strings.Contains(window, "ReadinessGatesNotReady") &&
				strings.Contains(window, "RoleInstanceCustomReady") {
				t.Errorf("C4515734270: the RI example again shows RoleInstanceReady=False " +
					"(ReadinessGatesNotReady) caused by RoleInstanceCustomReady, unlike the " +
					"code's aggregation (instance_status.go folds only the in-place gate)")
			}
			break
		}
	}
}

// ---------------------------------------------------------------------------
// F20 (round 6, nit — revision regression, still open at 05807351): the RI
// summary prose ("Identifying an update target enters Updating") must be
// scoped consistently with the Create-convergence fold edge (a Creating RI
// folds an identified update target instead of transitioning). Green at
// 0b7285b7 (fold edge absent, prose matched the then-current table); red
// since 39e2ecdf.
// ---------------------------------------------------------------------------

func TestRISummaryProseScopedToFoldRule(t *testing.T) {
	doc := kepDoc(t)
	ri := sectionExact(doc, "RoleInstance")
	if ri == "" {
		t.Fatal("F20-guard anchor drift: RoleInstance section not found")
	}
	fold := strings.Contains(ri, "fold the new target into Create convergence") ||
		strings.Contains(ri, "folded into Create convergence")
	if !fold {
		return // fold rule absent; prose cannot contradict it
	}
	for _, l := range strings.Split(ri, "\n") {
		for _, s := range strings.Split(l, ". ") {
			if strings.Contains(s, "Identifying an update target enters Updating") ||
				strings.Contains(s, "recovery of required Pods outside an update enters Recovering") {
				scoped := strings.Contains(s, "From `Running`") || strings.Contains(s, "established") ||
					strings.Contains(s, "outside initial creation") || strings.Contains(s, "after initial creation")
				if !scoped {
					t.Errorf("F20: the RI summary prose %q is unscoped: under the fold rule an "+
						"update target identified during initial creation must NOT enter Updating — "+
						"scope the sentence (e.g. 'From `Running` or `Recovering`, ...')", strings.TrimSpace(s))
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// F21 (round 6, nit — late discovery, still open at 05807351): "the display
// priority defined in the RoleInstance section" must resolve. Green on
// pre-pivot heads (the reference phrasing did not exist); red since the
// round-5 pivot.
// ---------------------------------------------------------------------------

func TestDisplayPriorityReferenceResolves(t *testing.T) {
	doc := kepDoc(t)
	ref := strings.Contains(doc, "display priority defined in the RoleInstance section")
	if !ref {
		return // reference absent (pre-pivot or fixed by rewording)
	}
	ri := sectionExact(doc, "RoleInstance")
	defined := false
	for _, l := range strings.Split(ri, "\n") {
		if strings.Contains(l, "display priority") &&
			!strings.Contains(l, "defined in the RoleInstance section") {
			defined = true
		}
	}
	if !defined {
		t.Errorf("F21: 'select the RI phase using the display priority defined in the " +
			"RoleInstance section' does not resolve — the RoleInstance section defines no " +
			"display priority (define it there or point at the edge / situation tables)")
	}
}

// ---------------------------------------------------------------------------
// Q5 (round 5, nit — kind-scope half still open at 05807351): the
// Consuming-the-Status decision table must state which kinds it applies to,
// or define `Converged` for RI (RI completion is phase=Running; the RI reason
// list has no Converged). Green on heads without the table.
// ---------------------------------------------------------------------------

func TestDecisionTableStatesKindScope(t *testing.T) {
	doc := kepDoc(t)
	cs := section(doc, "Consuming the Status")
	if cs == "" || !strings.Contains(cs, "Is the change done?") {
		return // decision table absent on pre-round-5 heads
	}
	scoped := false
	for _, l := range strings.Split(cs, "\n") {
		low := strings.ToLower(l)
		// A scoping sentence names the table (or its questions) AND restricts
		// the kinds it covers, e.g. "this table applies to RBG and RIS; RI
		// completion is phase=Running". Merely mentioning kinds inside one
		// answer row (the serving row) does not scope the done row.
		namesTable := strings.Contains(low, "table") || strings.Contains(low, "appl") ||
			strings.Contains(low, "converged") || strings.Contains(low, "change is done") ||
			strings.Contains(low, "completion")
		restrictsKinds := (strings.Contains(low, "rbg") || strings.Contains(low, "ris")) &&
			strings.Contains(low, "ri")
		if namesTable && restrictsKinds {
			scoped = true
		}
	}
	// Alternatively the gap is closed if RI gets a Converged completion reason.
	ri := sectionExact(doc, "RoleInstance")
	riConverged := strings.Contains(ri, "`Converged`")
	if !scoped && !riConverged {
		t.Errorf("Q5: the decision table states no kind scope and `Converged` is undefined for " +
			"RI — a consumer cannot answer 'Is the change done?' for a RoleInstance " +
			"(scope the table to RBG / RIS or define RI completion)")
	}
}

// ---------------------------------------------------------------------------
// Q6 (round 5, nit — still open at 05807351): the doc must state what
// Progressing reports while an RBG / RIS is being deleted (or explicitly
// defer it). Lines that only say deletion is indicated by
// metadata.deletionTimestamp do not answer the Progressing question.
// ---------------------------------------------------------------------------

func TestProgressingDuringDeletionSpecified(t *testing.T) {
	doc := kepDoc(t)
	for _, l := range strings.Split(doc, "\n") {
		// Segment into table cells / sentences so a row label ("Ready /
		// Progressing") cannot lend "Progressing" to a clause that only
		// describes Ready.
		for _, seg := range strings.FieldsFunc(l, func(r rune) bool { return r == '|' }) {
			for _, s := range strings.Split(seg, ". ") {
				low := strings.ToLower(s)
				if !strings.Contains(s, "Progressing") {
					continue
				}
				if !strings.Contains(low, "deletion") && !strings.Contains(low, "terminating") {
					continue
				}
				behavior := strings.Contains(low, "retains") || strings.Contains(low, "stale") ||
					strings.Contains(low, "stops") || strings.Contains(low, "not updated") ||
					strings.Contains(low, "frozen") || strings.Contains(low, "unchanged") ||
					strings.Contains(low, "out of scope") || strings.Contains(low, "unspecified")
				if behavior {
					return
				}
			}
		}
	}
	t.Errorf("Q6: no statement of what `Progressing` reports while an RBG / RIS object is " +
		"terminating — the controller early-returns on deletionTimestamp, so the condition goes " +
		"stale; say so (e.g. '`Progressing` retains its last value during deletion; consumers must " +
		"check deletionTimestamp first')")
}

// ---------------------------------------------------------------------------
// F22-own-crop (round 7, minor; FIXED at 9862499f): the reason a controller
// reports for its OWN CR-operation errors was prescribed two ways — the
// provenance table routed classifiable own-operation errors (a quota-rejected
// RIS create) to the family reasons while the taxonomy paragraph said
// categorically "directly as `UnclassifiedBlocker`" and the RIS section hedged
// "(normally `UnclassifiedBlocker`)". Contract (corroborated by Copilot
// C4528984348): one prescription — classify own CR-operation errors with the
// same rules, `UnclassifiedBlocker` reserved for errors whose family cannot be
// determined — stated consistently in the taxonomy paragraph and the RIS
// section. Red at 05807351, green at 9862499f.
// ---------------------------------------------------------------------------

func TestOwnCROperationErrorsClassified(t *testing.T) {
	doc := stripFences(kepDoc(t))
	if !strings.Contains(doc, "own CR-operation errors") {
		return // taxonomy redesigned away; guard retired by harness-update
	}
	if strings.Contains(doc, "reports its own CR-operation errors directly as `UnclassifiedBlocker`") {
		t.Errorf("F22-own-crop: the taxonomy paragraph again prescribes own CR-operation " +
			"errors categorically as `UnclassifiedBlocker`, contradicting the provenance table, " +
			"which routes classifiable own-operation errors to the family reasons")
	}
	if strings.Contains(doc, "directly as blockers (normally `UnclassifiedBlocker`)") {
		t.Errorf("F22-own-crop: the RIS section again hedges own-operation errors as " +
			"'normally `UnclassifiedBlocker`' instead of the same classification rules")
	}
	taxonomy := strings.Contains(doc, "classifies its own CR-operation errors with the same rules") &&
		strings.Contains(doc, "`UnclassifiedBlocker` is reserved for errors whose family cannot be determined")
	ris := strings.Contains(doc, "through the same classification rules") &&
		strings.Contains(doc, "whose family cannot be determined use `UnclassifiedBlocker`")
	if !taxonomy || !ris {
		t.Errorf("F22-own-crop: own CR-operation error prescription incomplete — want one rule "+
			"('classifies its own CR-operation errors with the same rules' + 'UnclassifiedBlocker "+
			"reserved for errors whose family cannot be determined') in the taxonomy paragraph AND "+
			"the RIS section (taxonomy-ok=%v ris-ok=%v)", taxonomy, ris)
	}
}

// ---------------------------------------------------------------------------
// C4528984285 (round-7 Copilot advisory, carried open; FIXED at 9862499f):
// `GangConfigured` reports configuration compatibility only — it is True when
// gang configuration reconciliation succeeds even while Pods remain
// unscheduled — so it cannot distinguish a waiting gang from a released one
// and must not be the observation source for `WaitingForGang`. Contract: the
// waiting observation is sourced from runtime gang-scheduling state (PodGroup
// phase / member scheduling status) and the doc states GangConfigured never
// implies waiting. Red at 05807351 ("today the `GangConfigured` condition"),
// green at 9862499f.
// ---------------------------------------------------------------------------

func TestWaitingForGangSourcedFromRuntimeState(t *testing.T) {
	doc := stripFences(kepDoc(t))
	if !strings.Contains(doc, "`WaitingForGang`") {
		return // gang-waiting reason absent (pre-round-5 heads)
	}
	if strings.Contains(doc, "gang state owned at RBG scope (today the `GangConfigured` condition)") {
		t.Errorf("C4528984285: `WaitingForGang` is again observed from the `GangConfigured` " +
			"condition, which reports only configuration compatibility and cannot distinguish " +
			"a waiting gang from a released one")
	}
	runtime := strings.Contains(doc, "runtime gang-scheduling state") ||
		strings.Contains(doc, "runtime gang-scheduling facts")
	disclaim := strings.Contains(doc, "never used as the waiting observation") ||
		strings.Contains(doc, "never implies whether the gang is waiting") ||
		strings.Contains(doc, "never implies waiting")
	if !runtime || !disclaim {
		t.Errorf("C4528984285: `WaitingForGang` observation must come from runtime "+
			"gang-scheduling state (PodGroup phase / member scheduling status) and the doc must "+
			"state `GangConfigured` never implies waiting (runtime-ok=%v disclaimer-ok=%v)",
			runtime, disclaim)
	}
}

// ---------------------------------------------------------------------------
// C4528984415 (round-7 Copilot advisory, carried open; FIXED at 9862499f):
// after a controller restart, retained fields could not distinguish a
// never-established child from a replaced one, so `Creating` vs `Recovering`
// was underdetermined (e.g. blocked creation of a never-established
// zero-replica RIS vs blocked replacement of a deleted, established one).
// Contract: the doc resolves the ambiguity — `Recovering` requires durable
// evidence of prior establishment; without it the child is `Creating` and
// message notes replacement is unconfirmed — and the restart test bullet
// covers it. Red at 05807351, green at 9862499f.
// ---------------------------------------------------------------------------

func TestRestartAmbiguityResolved(t *testing.T) {
	doc := stripFences(kepDoc(t))
	if !strings.Contains(doc, "controller restart") {
		return // restart rule absent entirely (early heads)
	}
	prose := strings.Contains(doc, "no durable evidence distinguishes") &&
		strings.Contains(doc, "durable evidence of prior establishment") &&
		strings.Contains(doc, "replacement is unconfirmed")
	plan := strings.Contains(doc, "zero-replica ambiguity")
	if !prose || !plan {
		t.Errorf("C4528984415: post-restart establishment ambiguity unresolved — "+
			"'Recovering' must require durable evidence of prior establishment, the "+
			"evidence-less case must fall back to 'Creating' with a message note that "+
			"replacement is unconfirmed, and the restart test bullet must cover it "+
			"(prose-ok=%v test-plan-ok=%v)", prose, plan)
	}
}

// ---------------------------------------------------------------------------
// C4528984469 (round-7 Copilot advisory, carried open; FIXED at 9862499f):
// the test-plan bullet "Individual RIS / RI scaling or update failures produce
// Warning Events ... and do not directly change the aggregated `Progressing`
// reason" conflated a single failed operation with a confirmed current
// blocker — a confirmed blocker DOES change the reason via the classification
// and propagation rules. Contract: the bullet scopes the no-change clause to a
// single failure and states that a confirmed current blocker changes the
// reason. Red at 05807351, green at 9862499f.
// ---------------------------------------------------------------------------

func TestSingleFailureVsConfirmedBlocker(t *testing.T) {
	doc := stripFences(kepDoc(t))
	if !strings.Contains(doc, "Warning Event") {
		return // events discussion absent (early heads)
	}
	if strings.Contains(doc, "Individual RIS / RI scaling or update failures produce Warning Events") {
		t.Errorf("C4528984469: the events bullet again describes repeated/individual failures " +
			"as never changing the aggregated `Progressing` reason, conflating a single failed " +
			"operation with a confirmed current blocker")
	}
	scoped := strings.Contains(doc, "does not by itself")
	confirmed := strings.Contains(doc, "A confirmed current blocker changes the reason")
	if !scoped || !confirmed {
		t.Errorf("C4528984469: the events bullet must scope the no-reason-change clause to a "+
			"single failure ('does not by itself') and state that a confirmed current blocker "+
			"changes the reason through the classification and propagation rules "+
			"(scoped-ok=%v confirmed-ok=%v)", scoped, confirmed)
	}
}

// ---------------------------------------------------------------------------
// F23 (round 7, nit; still open at 9862499f): the leftover sentence "Readiness
// gates only `Creating → Running` and completion decisions" assigns readiness
// gates — unqualified, so including externally owned ones — a phase-transition
// gating role, contradicting the separation rule ("externally owned readiness
// gates do not gate phase transitions", :413) that resolved the KEP-527
// markPodNotReady circular wait. Contract: once the separation rule exists, no
// sentence may give (external) readiness gates a phase-gating role; the
// sentence must be scoped to the controller-observed inputs (readyPolicy plus
// the configured in-place update gate). Red at 05807351 and at 9862499f.
// ---------------------------------------------------------------------------

func TestPhaseGateTerminologyConsistent(t *testing.T) {
	doc := stripFences(kepDoc(t))
	ri := section(doc, "RI Phase Transition Principles") + "\n" + sectionExact(doc, "RoleInstance")
	if !strings.Contains(ri, "do not gate phase transitions") {
		return // separation rule absent (pre-round-7 heads); nothing to contradict
	}
	if strings.Contains(ri, "Readiness gates only `Creating → Running`") {
		t.Errorf("F23: 'Readiness gates only `Creating → Running`' assigns (external) readiness " +
			"gates a phase-transition gating role, contradicting the separation rule that " +
			"externally owned readiness gates do not gate phase transitions — scope the sentence " +
			"to the controller-observed inputs (readyPolicy + the configured in-place gate)")
	}
}
