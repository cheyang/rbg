/*
Copyright 2025 The RBG Authors.

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

// Verification harness for PR #410. See docs/verification/pr410-deploy-skill/README.md.
//
// PR #410 adds CLAUDE.md and .claude/skills/rbg-inference-deploy/ — prose that an agent
// executes verbatim. These tests hold that prose to the same standard as code: every
// command, Makefile target, API field and enum value it names must actually exist.
//
// Polarity of every test: CONTRACT. Each one FAILS on the code under review and PASSES
// once the docs are corrected.
package pr410

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot walks up from this test file until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate repo root (no go.mod found walking up from %s)", dir)
	return ""
}

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// F2: CLAUDE.md:13-15 tells the agent to run `make build-cli` / `make build-cli-all`.
func TestF2_MakeTargetsDocumentedInClaudeMdExist(t *testing.T) {
	claude := read(t, "CLAUDE.md")
	makefile := read(t, "Makefile")

	// Collect every `make <target>` the doc instructs the reader to run.
	re := regexp.MustCompile(`(?m)^make\s+([a-zA-Z0-9_.-]+)`)
	var missing []string
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(claude, -1) {
		target := m[1]
		if seen[target] {
			continue
		}
		seen[target] = true
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`).MatchString(makefile) {
			missing = append(missing, target)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("CLAUDE.md documents make target(s) that do not exist in the Makefile: %v", missing)
	}
	if len(seen) == 0 {
		t.Fatal("harness bug: found no `make <target>` lines in CLAUDE.md")
	}
}

// F3: CLAUDE.md:130-135 documents five `kubectl rbg llm ...` commands, but the llm
// command tree is never attached to the CLI root, so none of them are reachable.
func TestF3_LLMCommandTreeIsReachable(t *testing.T) {
	claude := read(t, "CLAUDE.md")
	if !strings.Contains(claude, "kubectl rbg llm") {
		t.Skip("CLAUDE.md no longer documents `kubectl rbg llm`; nothing to check")
	}
	root := read(t, "cmd/cli/cmd/root.go")
	if !strings.Contains(root, "NewLLMCmd") {
		t.Fatalf("CLAUDE.md documents `kubectl rbg llm ...` commands, but llm.NewLLMCmd is " +
			"never registered in cmd/cli/cmd/root.go, so `kubectl rbg llm` does not exist")
	}
}

// F4: CLAUDE.md:120-123 lists RecreateRBGOnPodRestart as a restartPolicy value. The
// kubebuilder enum on the v1alpha2 API does not accept it, so the API server rejects it
// (see docs/verification/pr410-deploy-skill/results/L3-api-validation.txt).
func TestF4_DocumentedRestartPolicyValuesAreAccepted(t *testing.T) {
	claude := read(t, "CLAUDE.md")
	types := read(t, "api/workloads/v1alpha2/rolebasedgroup_types.go")

	enumRe := regexp.MustCompile(`\+kubebuilder:validation:Enum=\{(None[^}]*)\}`)
	m := enumRe.FindStringSubmatch(types)
	if m == nil {
		t.Fatal("harness-update: could not find the restartPolicy kubebuilder Enum marker")
	}
	allowed := map[string]bool{}
	for _, v := range strings.Split(m[1], ",") {
		allowed[strings.TrimSpace(v)] = true
	}

	// Restart Policy section of CLAUDE.md lists values as `- \x60Value\x60: description`.
	section := claude
	if i := strings.Index(claude, "### Restart Policy"); i >= 0 {
		section = claude[i:]
		if j := strings.Index(section[1:], "\n### "); j >= 0 {
			section = section[:j+1]
		}
	}
	valRe := regexp.MustCompile("(?m)^- `([A-Za-z]+)`:")
	var bad []string
	for _, v := range valRe.FindAllStringSubmatch(section, -1) {
		if !allowed[v[1]] {
			bad = append(bad, v[1])
		}
	}
	if len(bad) > 0 {
		t.Fatalf("CLAUDE.md documents restartPolicy value(s) %v that the v1alpha2 enum rejects "+
			"(allowed: %v); the API server refuses them", bad, m[1])
	}
}

// F4c: CLAUDE.md presents restartPolicy as a top-level role concept without saying it
// lives under the pattern. Guard the structural fact so the doc stays honest.
func TestF4c_RestartPolicyLivesUnderThePatternNotTheRole(t *testing.T) {
	types := read(t, "api/workloads/v1alpha2/rolebasedgroup_types.go")
	// RoleSpec must NOT carry a restartPolicy field; the patterns must.
	roleSpec := between(types, "type RoleSpec struct {", "\n}")
	if strings.Contains(roleSpec, "RestartPolicy ") {
		t.Skip("RoleSpec now has its own RestartPolicy; CLAUDE.md's role-level framing became correct")
	}
	lwp := between(types, "type LeaderWorkerPattern struct {", "\n}")
	if !strings.Contains(lwp, "RestartPolicy ") {
		t.Fatal("harness-update: LeaderWorkerPattern no longer carries RestartPolicy")
	}
	t.Log("confirmed: restartPolicy is a pattern-level field (leaderWorkerPattern / " +
		"customComponentsPattern), not roles[].restartPolicy as CLAUDE.md implies")
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return rest
	}
	return rest[:j]
}

// F5: SKILL.md has an odd number of ``` fences, so from the stray fence onward the
// document renders inverted — three Phase 4 headings end up inside a code block.
func TestF5_SkillMarkdownFencesAreBalanced(t *testing.T) {
	for _, f := range []string{
		".claude/skills/rbg-inference-deploy/SKILL.md",
		".claude/skills/rbg-inference-deploy/yaml-rules.md",
		".claude/skills/rbg-inference-deploy/prerequisites.md",
		".claude/skills/rbg-inference-deploy/deployment-analysis.md",
		".claude/skills/rbg-inference-deploy/benchmark.md",
	} {
		body := read(t, f)
		var fences []int
		for i, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				fences = append(fences, i+1)
			}
		}
		if len(fences)%2 != 0 {
			t.Errorf("%s: %d code fences (odd) — unbalanced; fence lines %v. "+
				"Everything after the stray fence renders with inverted parity.",
				f, len(fences), fences)
			continue
		}
		// Sections swallowed by a fence never render as headings.
		lines := strings.Split(body, "\n")
		inside := false
		var open int
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inside = !inside
				if inside {
					open = i + 1
				}
				continue
			}
			if inside && (strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ")) {
				t.Errorf("%s:%d heading %q is inside the code fence opened at line %d, "+
					"so it never renders as a section", f, i+1, strings.TrimSpace(line), open)
			}
		}
	}
}

// F1 (doc side): the runtime claim itself. The four TestF1_* tests in
// api/workloads/v1alpha2 prove the behavior is fine; this one fails as long as the
// skill still tells the agent otherwise, so re-verify.sh flips it to green only when
// the wrong guidance is actually removed.
// CONTRACT.
func TestF1_DocsDoNotClaimTolerationsAreDropped(t *testing.T) {
	type claim struct{ file, marker string }
	// Phrases that only appear when the doc asserts tolerations cannot go through
	// templateRef.patch.
	claims := []claim{
		{".claude/skills/rbg-inference-deploy/yaml-rules.md", "tolerations"},
		{".claude/skills/rbg-inference-deploy/SKILL.md", "silently dropped"},
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`silently dropped`),
		regexp.MustCompile("tolerations[^\n]*无法[^\n]*templateRef"),
		regexp.MustCompile("不会被 patch 传递"),
		regexp.MustCompile("无效，会被丢弃"),
	}
	var hits []string
	for _, c := range claims {
		body := read(t, c.file)
		for _, line := range strings.Split(body, "\n") {
			for _, p := range patterns {
				if p.MatchString(line) {
					hits = append(hits, c.file+": "+strings.TrimSpace(line))
					break
				}
			}
		}
	}
	if len(hits) > 0 {
		t.Fatalf("the skill still states that tolerations cannot travel through "+
			"templateRef.patch, which the TestF1_* tests in api/workloads/v1alpha2 and the "+
			"live run in docs/verification/pr410-deploy-skill/results/L3-f1-tolerations.txt "+
			"both disprove:\n  %s", strings.Join(hits, "\n  "))
	}
}
