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

package kep465verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Integration layer for the KEP-465 verification harness: checks that need
// the real repository tooling (git diff scope, whitespace integrity, YAML
// validity of kep.yaml and the embedded example) rather than pure content
// assertions. All checks are contract polarity.

func git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// The PR must remain documentation-only: only the two KEP-465 files may
// change relative to the merge-base with the base branch.
func TestKEP465Integration_PRDiffScopeIsDocsOnly(t *testing.T) {
	base := os.Getenv("KEP465_BASE_REF")
	if base == "" {
		base = "origin/main"
	}
	mergeBase := strings.TrimSpace(git(t, "merge-base", base, "HEAD"))
	files := git(t, "diff", "--name-only", mergeBase+"...HEAD")
	allowed := map[string]bool{
		"keps/465-continuous-rbg-warmup/README.md": true,
		"keps/465-continuous-rbg-warmup/kep.yaml":  true,
	}
	for _, f := range strings.Split(files, "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !allowed[f] {
			t.Errorf("unexpected non-KEP file in PR diff: %s", f)
		}
	}
}

// The PR body instructs reviewers to run `git diff --check`; it must be clean.
func TestKEP465Integration_GitDiffCheckClean(t *testing.T) {
	base := os.Getenv("KEP465_BASE_REF")
	if base == "" {
		base = "origin/main"
	}
	mergeBase := strings.TrimSpace(git(t, "merge-base", base, "HEAD"))
	cmd := exec.Command("git", "diff", "--check", mergeBase+"...HEAD")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git diff --check reported problems:\n%s", out)
	}
}

// kep.yaml must be valid YAML with a number matching the directory, and the
// README's embedded status example must be valid YAML too.
func TestKEP465Integration_YAMLParses(t *testing.T) {
	root := repoRoot(t)
	script := `
import sys, yaml, re
ky = yaml.safe_load(open(sys.argv[1]))
assert ky["kep-number"] == 465, ky["kep-number"]
assert ky["status"] == "provisional", ky["status"]
assert ky["stage"] == "alpha", ky["stage"]
readme = open(sys.argv[2]).read()
blocks = re.findall(r"` + "```" + `yaml\n(.*?)` + "```" + `", readme, re.S)
assert blocks, "no yaml example block in README"
for b in blocks:
    doc = yaml.safe_load(b)
    assert doc["kind"] == "RoleBasedGroupWarmup", doc.get("kind")
    assert doc["apiVersion"] == "workloads.x-k8s.io/v1alpha2", doc.get("apiVersion")
    assert doc["spec"]["mode"] == "Continuous", doc["spec"].get("mode")
print("yaml-ok")
`
	cmd := exec.Command("python3", "-c", script,
		filepath.Join(root, "keps/465-continuous-rbg-warmup/kep.yaml"),
		filepath.Join(root, "keps/465-continuous-rbg-warmup/README.md"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("yaml validation failed: %v\n%s", err, out)
	}
}
