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
	"os"
	"path/filepath"
	"regexp"
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
// F2: the reporting-priority rule vs the RBG example. The priority list after
// "highest reporting priority" includes ChildReadiness, and the text says a
// current blocker outranks an activity reason. The RBG example nevertheless
// reports reason=ScalingOrUpdating (activity) while a child-readiness fact
// holds (Ready=False, "decode has 2/3 replicas ready"). As written, an
// implementer following the priority list would report RolesNotReady.
// Contract: FAILS now; PASSES when the list drops ChildReadiness, the example
// changes, or the taxonomy section states how child-readiness ranks vs
// activity reasons.
// ---------------------------------------------------------------------------

func TestExampleConsistentWithPriorityRule(t *testing.T) {
	doc := kepDoc(t)
	idx := strings.Index(doc, "highest reporting priority")
	if idx < 0 {
		t.Fatal("reporting-priority paragraph not found")
	}
	rest := doc[idx:]
	fence := regexp.MustCompile("(?s)```text(.*?)```").FindStringSubmatch(rest)
	if fence == nil {
		t.Fatal("priority list fenced block not found")
	}
	priorityHasChildReadiness := strings.Contains(fence[1], "ChildReadiness")

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
		if (strings.Contains(ll, "childreadiness") || strings.Contains(ll, "child readiness") ||
			strings.Contains(ll, "child readiness")) &&
			(strings.Contains(ll, "activity") || strings.Contains(ll, "subsum") ||
				strings.Contains(ll, "outrank") || strings.Contains(ll, "precedence")) {
			clarified = true
		}
	}

	if priorityHasChildReadiness && readyStatus == "False" && activityReasons[progReason] && !clarified {
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
	doc := kepDoc(t)
	rows := candidateReasonsTable(t)
	acceptedFalse := rows["Accepted=False"]
	progressingFalse := map[string]bool{}
	for _, r := range rows["Progressing=False"] {
		progressingFalse[r] = true
	}
	// A missing reason is also acceptable if some line ties it to Progressing.
	coveredInProse := map[string]bool{}
	for _, l := range strings.Split(doc, "\n") {
		if !strings.Contains(l, "Progressing") {
			continue
		}
		for _, r := range acceptedFalse {
			if strings.Contains(l, "`"+r+"`") {
				coveredInProse[r] = true
			}
		}
	}
	var missing []string
	for _, r := range acceptedFalse {
		if !progressingFalse[r] && !coveredInProse[r] {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		t.Errorf("F3: Progressing=False covers 'configuration rejected' but these Accepted=False "+
			"rejection reasons have no defined Progressing=False mapping: %s", strings.Join(missing, ", "))
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
		if obj.Status.Phase == "" {
			t.Errorf("yaml example %d (%s): status.phase empty", i, obj.Kind)
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("no yaml example for kind %s", k)
		}
	}
}
