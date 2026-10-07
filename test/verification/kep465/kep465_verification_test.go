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

// Package kep465verify is the reviewer harness for PR #483 (KEP-465,
// docs/verification/kep-465-continuous-warmup). It is ADDITIVE ONLY: it does
// not modify production code. Because PR #483 is documentation-only, the
// "code under review" is the KEP text itself, and the strongest falsifiable
// claims are the statements the KEP makes about the existing codebase plus
// the KEP's own internal consistency.
//
// Polarity:
//   - TestKEP465_* codebase-claim checks are CONTRACT tests: they assert the
//     KEP matches the repository as it exists. A failure means the KEP (or
//     the code) drifted.
//   - TestKEP465_F* checks encode review findings as CONTRACT tests: they
//     assert the intended (fixed) doc state, so they FAIL on the current PR
//     head. Each failing F-test is the reproduction of that finding; when the
//     author revises the KEP the test flips to PASS and the finding is fixed.
package kep465verify

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// test/verification/kep465 -> repo root is three levels up.
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

func readFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func fileExists(rel string) bool {
	_, err := os.Stat(rel)
	return err == nil
}

const (
	kepReadme     = "keps/465-continuous-rbg-warmup/README.md"
	kepYAML       = "keps/465-continuous-rbg-warmup/kep.yaml"
	apiTypes      = "api/workloads/v1alpha2/rolebasedgroupwarmup_types.go"
	controller    = "internal/controller/workloads/rolebasedgroupwarmup_controller.go"
	mainGo        = "cmd/rbgs/main.go"
	kep129        = "keps/129-rbg-warmup/README.md"
	warmupCRDYAML = "config/crd/bases/workloads.x-k8s.io_rolebasedgroupwarmups.yaml"
)

// ---- KEP claims about the existing codebase (must hold on base & head) ----

// KEP: "[KEP-129] introduced RoleBasedGroupWarmup as a one-shot job" with
// phases None/Running/Paused/Completed/Failed; the KEP "gains two values".
func TestKEP465_ExistingPhaseEnumMatchesKEP(t *testing.T) {
	types := readFile(t, apiTypes)
	for _, phase := range []string{
		`WarmupJobPhaseNone      WarmupJobPhase = ""`,
		`WarmupJobPhaseRunning   WarmupJobPhase = "Running"`,
		`WarmupJobPhasePaused    WarmupJobPhase = "Paused"`,
		`WarmupJobPhaseCompleted WarmupJobPhase = "Completed"`,
		`WarmupJobPhaseFailed    WarmupJobPhase = "Failed"`,
	} {
		if !strings.Contains(types, phase) {
			t.Errorf("api types missing existing phase constant %q", phase)
		}
	}
}

// KEP: "The existing warmup name, Warmup UID, and node name labels remain."
func TestKEP465_ExistingPodIdentityLabelsMatchKEP(t *testing.T) {
	src := readFile(t, controller)
	for _, l := range []string{
		`LabelWarmupName = "workloads.x-k8s.io/warmup-name"`,
		`LabelWarmupUID  = "workloads.x-k8s.io/warmup-uid"`,
		`LabelNodeName   = "workloads.x-k8s.io/node-name"`,
	} {
		if !strings.Contains(src, l) {
			t.Errorf("controller missing label constant %q", l)
		}
	}
}

// KEP: "Once retains its existing generateName behavior."
func TestKEP465_OnceUsesGenerateName(t *testing.T) {
	src := readFile(t, controller)
	if !strings.Contains(src, "GenerateName: fmt.Sprintf(\"%s-\", warmup.Name)") {
		t.Errorf("controller no longer creates warmup Pods with generateName")
	}
}

// KEP: "the API server currently accepts updates to targets and actions" —
// i.e. there is no validating webhook for RoleBasedGroupWarmup today.
func TestKEP465_NoWarmupValidatingWebhookToday(t *testing.T) {
	root := repoRoot(t)
	if fileExists(filepath.Join(root, "api/workloads/v1alpha2/rolebasedgroupwarmup_webhook.go")) {
		t.Errorf("rolebasedgroupwarmup_webhook.go already exists; KEP claim stale")
	}
	mainSrc := readFile(t, mainGo)
	if strings.Contains(mainSrc, "RoleBasedGroupWarmup{}).SetupWebhookWithManager") {
		t.Errorf("warmup webhook already registered in cmd/rbgs/main.go; KEP claim stale")
	}
}

// Same claim, at the CRD level: no CEL immutability (oldSelf) rules on the
// warmup spec today.
func TestKEP465_NoCELImmutabilityOnWarmupSpec(t *testing.T) {
	crd := readFile(t, warmupCRDYAML)
	if strings.Contains(crd, "oldSelf") {
		t.Errorf("warmup CRD already has CEL oldSelf immutability rules; KEP motivation stale")
	}
}

// KEP: "the controller tracks completion only by node name".
func TestKEP465_CompletionTrackedByNodeName(t *testing.T) {
	src := readFile(t, controller)
	if !strings.Contains(src, "computePermanentlyFailedNodes") ||
		!strings.Contains(src, "p.Labels[LabelNodeName]") {
		t.Errorf("controller failure/completion accounting no longer keyed by node-name label")
	}
}

// KEP: "[the one-shot Warmup] stops after reaching Completed or Failed" and
// "its terminal phases only perform optional TTL cleanup".
func TestKEP465_TerminalPhasesOnlyDoTTL(t *testing.T) {
	src := readFile(t, controller)
	if !strings.Contains(src, "func (r *RoleBasedGroupWarmupReconciler) reconcileFinished") {
		t.Fatalf("reconcileFinished not found")
	}
	if !strings.Contains(src, "case workloadsv1alpha2.WarmupJobPhaseFailed, workloadsv1alpha2.WarmupJobPhaseCompleted:\n\t\treturn r.reconcileFinished(ctx, warmup)") {
		t.Errorf("terminal phases no longer route only to the TTL handler")
	}
	if !strings.Contains(src, "TTLSecondsAfterFinished") {
		t.Errorf("reconcileFinished no longer implements TTLSecondsAfterFinished")
	}
}

// KEP: "The controller already requires list/watch access to Nodes, Pods,
// and RBGs".
func TestKEP465_RBACMarkersMatchKEP(t *testing.T) {
	src := readFile(t, controller)
	for _, m := range []string{
		`+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch`,
		`+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete`,
		`+kubebuilder:rbac:groups=workloads.x-k8s.io,resources=rolebasedgroups,verbs=get;list;watch`,
	} {
		if !strings.Contains(src, m) {
			t.Errorf("controller missing RBAC marker %q", m)
		}
	}
}

// KEP: "Once continues to count retained failed Pods as specified by KEP-129."
func TestKEP465_KEP129FailedPodsAreRetryCounter(t *testing.T) {
	k := readFile(t, kep129)
	if !strings.Contains(k, "IS the attempt count") {
		t.Errorf("KEP-129 no longer states failed Pods are the per-node attempt counter")
	}
}

// KEP: "The current first-wins volume-conflict behavior" + "continues to emit
// the existing warning event and condition".
func TestKEP465_VolumeConflictFirstWinsWithEventAndCondition(t *testing.T) {
	src := readFile(t, controller)
	for _, s := range []string{
		`Type:               "VolumeConflict"`,
		`Reason:             "ConflictingVolumeDefinitions"`,
		`"Volume %q has conflicting definitions across roles on node %s, using first definition"`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("controller missing volume-conflict behavior %q", s)
		}
	}
}

// KEP: "The current first-wins volume-conflict behavior must also use sorted
// role order" — today the merged action order depends on Go map iteration
// over roleToNodes, so the winning volume is nondeterministic.
func TestKEP465_RoleMergeOrderIsMapIterationToday(t *testing.T) {
	src := readFile(t, controller)
	if !strings.Contains(src, "for roleName, nodes := range roleToNodes") {
		t.Errorf("role merge no longer iterates the roleToNodes map; KEP's determinism claim needs re-checking")
	}
}

// KEP: container names are controller-assigned ("clearing controller-assigned
// names" when hashing).
func TestKEP465_ControllerAssignedContainerNames(t *testing.T) {
	src := readFile(t, controller)
	if !strings.Contains(src, `fmt.Sprintf("image-preload-%d", len(containers))`) ||
		!strings.Contains(src, `fmt.Sprintf("custom-%d", len(containers))`) {
		t.Errorf("controller no longer assigns sequential container names")
	}
}

// ---- KEP document hygiene ----

// kep.yaml metadata: number matches the directory and issue, title matches
// the README H1, see-also target exists, creation-date matches the
// implementation history.
func TestKEP465_KepYAMLMetadata(t *testing.T) {
	y := readFile(t, kepYAML)
	readme := readFile(t, kepReadme)
	if !strings.Contains(y, "kep-number: 465") {
		t.Errorf("kep.yaml kep-number != 465")
	}
	if !strings.Contains(readme, "# KEP-465: Continuous Node-Pool Warmup for RoleBasedGroup") {
		t.Errorf("README H1 missing or mismatched")
	}
	if !strings.Contains(y, "title: Continuous Node-Pool Warmup for RoleBasedGroup") {
		t.Errorf("kep.yaml title mismatches README H1")
	}
	if !strings.Contains(y, `"/keps/129-rbg-warmup"`) {
		t.Errorf("kep.yaml see-also does not reference KEP-129")
	}
	if !fileExists(filepath.Join(repoRoot(t), "keps/129-rbg-warmup/README.md")) {
		t.Errorf("see-also target keps/129-rbg-warmup does not exist")
	}
	if !strings.Contains(y, "creation-date: 2026-09-23") ||
		!strings.Contains(readme, "2026-09-23: Initial KEP drafted") {
		t.Errorf("creation-date and implementation history disagree")
	}
}

// Every TOC anchor must resolve to a heading, and relative links must exist.
func TestKEP465_TOCAnchorsAndLinksResolve(t *testing.T) {
	readme := readFile(t, kepReadme)
	heads := map[string]bool{}
	for _, line := range strings.Split(readme, "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		h := strings.TrimSpace(strings.TrimLeft(line, "#"))
		h = strings.ToLower(h)
		h = regexp.MustCompile(`[^\w\s-]`).ReplaceAllString(h, "")
		h = regexp.MustCompile(`\s+`).ReplaceAllString(h, "-")
		heads[h] = true
	}
	anchorRe := regexp.MustCompile(`\]\(#([^)]+)\)`)
	for _, m := range anchorRe.FindAllStringSubmatch(readme, -1) {
		if !heads[m[1]] {
			t.Errorf("TOC anchor #%s has no matching heading", m[1])
		}
	}
	for _, link := range []string{"keps/129-rbg-warmup/README.md"} {
		if !fileExists(filepath.Join(repoRoot(t), link)) {
			t.Errorf("relative link target %s missing", link)
		}
	}
}

// The KEP must keep the repo template's required sections.
func TestKEP465_RequiredSectionsPresent(t *testing.T) {
	readme := readFile(t, kepReadme)
	for _, s := range []string{
		"## Summary", "## Motivation", "### Goals", "### Non-Goals",
		"## Proposal", "### User Stories", "### Risks and Mitigations",
		"## Design Details", "### Test Plan", "### Graduation Criteria",
		"## Implementation History", "## Drawbacks", "## Alternatives",
	} {
		if !strings.Contains(readme, s) {
			t.Errorf("KEP missing required section %q", s)
		}
	}
}

// ---- Review findings encoded as contract tests (RED on current head) ----

// F2: the safety-requeue interval is specified with two contradictory bounds.
// Controller Flow step 8 says "no later than five minutes in the future"
// (interval <= 5m, i.e. at least one pass per 5m), while Scalability says
// "capped at one enqueue per Continuous resource per five minutes" (at most
// one pass per 5m). This test asserts the intended state: exactly one bound.
// It FAILS while both contradictory bounds are present.
func TestKEP465_F2_RequeueIntervalBoundsContradictory(t *testing.T) {
	readme := readFile(t, kepReadme)
	atLeast := strings.Contains(readme, "no later than five minutes in the future")
	atMost := regexp.MustCompile(`capped at one enqueue per\s+Continuous resource per five minutes`).MatchString(readme)
	if atLeast && atMost {
		t.Fatalf("F2: requeue interval specified as both '<= 5m' (line ~389) and '>= 5m' (line ~621); pick one bound")
	}
}

// F1: user story 1 has operators gate workload eligibility on Ready, and a
// Continuous resource with an available target but zero matched nodes reports
// Ready (reason NoNodesMatched). The Observability section never tells the
// operator to distinguish an empty-set Ready from a fully-warmed Ready (via
// the NoNodesMatched reason or desired==0). This test asserts the intended
// state: the Observability section mentions NoNodesMatched. Fails today.
func TestKEP465_F1_ObservabilityOmitsNoNodesMatched(t *testing.T) {
	readme := readFile(t, kepReadme)
	idx := strings.Index(readme, "### Observability and Troubleshooting")
	if idx < 0 {
		t.Fatalf("observability section missing")
	}
	section := readme[idx:]
	if end := strings.Index(section, "\n### "); end > 0 {
		section = section[:end]
	}
	if !strings.Contains(section, "NoNodesMatched") {
		t.Fatalf("F1: Observability section does not explain Ready/NoNodesMatched (empty target set reports Ready; see line ~281)")
	}
}

// F3: the state-machine diagram shows Paused reachable only from Running, but
// rule 1 ("Paused when spec.paused is true") and the note "Paused takes phase
// precedence" apply from Ready/Degraded as well. This test asserts the
// intended state: the diagram or its section explicitly covers entering
// Paused from a non-Running phase. Fails today.
func TestKEP465_F3_DiagramOmitsPausedFromReadyOrDegraded(t *testing.T) {
	readme := readFile(t, kepReadme)
	start := strings.Index(readme, "### Continuous State Machine")
	end := strings.Index(readme, "### Revision and Node Identity")
	if start < 0 || end < 0 {
		t.Fatalf("state machine section missing")
	}
	section := readme[start:end]
	// Intended: an explicit statement or diagram transition covering
	// Paused-from-Ready/Degraded (any phase).
	covered := strings.Contains(section, "any phase") ||
		regexp.MustCompile(`(Ready|Degraded)\S*[^\n]*(->|─+|--+)>\s*Paused`).MatchString(section)
	if !covered {
		t.Fatalf("F3: state machine diagram shows Paused only adjacent to Running; Ready/Degraded -> Paused (rule 1) is not represented")
	}
}

// F5: kep.yaml lists no approvers, so the governance path for moving the KEP
// to implementable ("must be approved by each of the KEP approvers") is
// undefined. Asserts the intended state: at least one approver. Fails today.
func TestKEP465_F5_KepYAMLHasNoApprovers(t *testing.T) {
	y := readFile(t, kepYAML)
	re := regexp.MustCompile(`(?m)^approvers:\s*\[\]\s*$`)
	if re.MatchString(y) {
		t.Fatalf("F5: kep.yaml approvers is empty; who approves the move to implementable?")
	}
}
