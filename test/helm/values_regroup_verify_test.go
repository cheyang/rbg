// Package helm holds verification tests for the rbgs Helm chart.
//
// These tests are a REVIEW HARNESS for PR #406
// (https://github.com/sgl-project/rbg/pull/406), which regroups chart values
// under global/controller/crdUpgrade. They are additive — they do not touch
// production code — and are driven by the review-finding-verifier skill's
// re-verify.sh (gotest layer).
//
// Test polarity (see docs/verification/pr406-helm-values-regroup/README.md):
//   - All tests here are CONTRACT tests: they assert the INTENDED behavior, so
//     they FAIL on the buggy PR head and PASS once the finding is fixed.
//
// Findings under test:
//   F1  test/stress/scripts/deploy-controller.sh still passes pre-refactor flat
//       --set paths (image.tag, replicaCount, controllerTuning.*) that the chart
//       no longer reads, so the overrides are silently dropped. These tests read
//       the paths the script actually uses and assert the override takes effect.
//   F2  templates reference .Values.global.imagePullSecrets without safe
//       navigation, so `helm template --set global=null` fails with a nil-pointer.
package helm

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot walks up from the test's working directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate repo root (go.mod) from working dir")
		}
		dir = parent
	}
}

func chartDir(t *testing.T) string { return filepath.Join(repoRoot(t), "deploy", "helm", "rbgs") }

func stressScript(t *testing.T) string {
	return filepath.Join(repoRoot(t), "test", "stress", "scripts", "deploy-controller.sh")
}

// requireHelm skips the test when the helm binary is unavailable.
func requireHelm(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm binary not found in PATH; skipping chart render verification")
	}
	return path
}

// helmTemplate renders the chart with the given --set flags and returns stdout,
// combined output, and any error.
func helmTemplate(t *testing.T, sets ...string) (string, string, error) {
	t.Helper()
	helm := requireHelm(t)
	args := []string{"template", "rbgs", chartDir(t)}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	cmd := exec.Command(helm, args...)
	out, err := cmd.Output()
	so := string(out)
	if ee, ok := err.(*exec.ExitError); ok {
		// combined = stdout + stderr, so callers can assert on either stream.
		return so, so + string(ee.Stderr), err
	}
	return so, so, err
}

// setPathForVar extracts the LHS of the `--set <path>="${VAR}"` line in the
// stress deploy script that binds the given shell variable. This ties the test
// to the ACTUAL path the consumer script uses, so it flips correctly when the
// script is updated to the new controller.* paths.
func setPathForVar(t *testing.T, shellVar string) string {
	t.Helper()
	data, err := os.ReadFile(stressScript(t))
	if err != nil {
		t.Fatalf("read stress script: %v", err)
	}
	// matches:  --set some.path="${VAR}"
	re := regexp.MustCompile(`--set\s+([A-Za-z0-9_.\[\]]+)="\$\{` + regexp.QuoteMeta(shellVar) + `\}"`)
	m := re.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("could not find a --set line binding ${%s} in %s", shellVar, stressScript(t))
	}
	return m[1]
}

// F1: the image tag override the stress script passes must actually reach the
// rendered controller image. On the buggy PR head the script uses `image.tag`,
// which the chart no longer reads, so the sentinel never appears -> FAIL.
func TestStressDeployScriptImageTagOverrideHonored(t *testing.T) {
	path := setPathForVar(t, "IMAGE_TAG")
	const sentinel = "stress-sentinel-tag"
	out, combined, err := helmTemplate(t, path+"="+sentinel)
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, combined)
	}
	if !strings.Contains(out, ":"+sentinel) {
		t.Errorf("stress script sets image tag via %q, but sentinel %q did not reach the rendered image.\n"+
			"The override is silently dropped; the stress deploy uses the chart-default tag instead.", path, sentinel)
	}
}

// F1: the concurrent-reconciles tuning override the stress script passes must
// reach the manager args. Buggy head uses `controllerTuning.*` -> dropped -> FAIL.
func TestStressDeployScriptTuningOverrideHonored(t *testing.T) {
	path := setPathForVar(t, "MAX_RECONCILES")
	const sentinel = "77"
	out, combined, err := helmTemplate(t, path+"="+sentinel)
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, combined)
	}
	if !strings.Contains(out, "--max-concurrent-reconciles="+sentinel) {
		t.Errorf("stress script sets max-concurrent-reconciles via %q, but %q did not reach the manager args.\n"+
			"The tuning override is silently dropped; the controller uses the default (10).", path, sentinel)
	}
}

// F1: the replica-count override the stress script passes must reach the
// Deployment. Buggy head uses top-level `replicaCount` -> dropped -> FAIL.
func TestStressDeployScriptReplicaOverrideHonored(t *testing.T) {
	path := setPathForVar(t, "REPLICAS")
	const sentinel = "7"
	out, combined, err := helmTemplate(t, path+"="+sentinel)
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, combined)
	}
	if !strings.Contains(out, "replicas: "+sentinel) {
		t.Errorf("stress script sets replica count via %q, but replicas: %s did not reach the Deployment.\n"+
			"The replica override is silently dropped; the controller uses the default (2).", path, sentinel)
	}
}

// F2: rendering must not nil-pointer when global is null. Helm's `global` is a
// reserved subchart key with null-coalescing semantics, so a parent chart or an
// override file can make .Values.global null. The templates dereference
// .Values.global.imagePullSecrets without safe navigation -> render error.
func TestRenderSucceedsWithNullGlobal(t *testing.T) {
	_, combined, err := helmTemplate(t, "global=null")
	if err != nil {
		t.Errorf("helm template failed when global is null (expected safe navigation):\n%s", combined)
	}
	if strings.Contains(combined, "nil pointer evaluating") {
		t.Errorf("render hit a nil-pointer on null global:\n%s", combined)
	}
}
