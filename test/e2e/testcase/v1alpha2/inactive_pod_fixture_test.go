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

package v1alpha2

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestFailingPodTemplateFixture pins the shell contract of the terminal-failure
// fixture introduced by PR #514. The e2e specs rely on three behaviors that only
// a real kubelet can prove end to end; this test proves the script-level half
// locally, so a regression in the command string fails fast without a cluster:
//
//   - healthy until the marker exists (the loop must not exit on its own),
//   - exit code exactly 1 after the marker (failPod's watch requires
//     Terminated.ExitCode == 1 to accept the PodFailed transition),
//   - exit code 0 on TERM/INT (the trap), so an unexpected mid-test deletion
//     yields a Succeeded pod rather than a spurious failure trigger.
//
// Polarity: contract test. It FAILS on a broken fixture and PASSES on a fixed one.
func TestFailingPodTemplateFixture(t *testing.T) {
	template := buildFailingPodTemplate()
	require.Equal(t, "Never", string(template.Spec.RestartPolicy),
		"the fixture must use restartPolicy=Never so the terminal phase is kubelet-owned")
	require.NotEmpty(t, template.Spec.Containers)
	require.Equal(t, "sh", template.Spec.Containers[0].Command[0],
		"the fixture must run a shell command")

	// Re-point the marker at a per-subtest path so runs cannot collide and the
	// first subtest's marker cannot leak into the TERM subtest.
	fixtureScript := func(t *testing.T) (string, string) {
		marker := filepath.Join(t.TempDir(), "rbg-e2e-fail")
		script := strings.Replace(template.Spec.Containers[0].Command[2], podFailureMarker, marker, 1)
		return script, marker
	}

	t.Run("stays healthy until the marker exists, then exits 1", func(t *testing.T) {
		script, marker := fixtureScript(t)
		cmd := exec.Command("sh", "-c", script)
		require.NoError(t, cmd.Start())
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		// No marker yet: the loop must still be alive after well over one iteration.
		time.Sleep(1500 * time.Millisecond)
		select {
		case err := <-done:
			t.Fatalf("fixture exited before the marker was planted: %v", err)
		default:
		}

		require.NoError(t, os.WriteFile(marker, nil, 0o644))
		select {
		case err := <-done:
			require.Error(t, err, "container must exit non-zero to be observed as failed")
			exitErr, ok := err.(*exec.ExitError)
			require.True(t, ok, "expected an exit-status error, got %v", err)
			require.Equal(t, 1, exitErr.ExitCode(),
				"exit code must be exactly 1 — failPod's watch only accepts ExitCode == 1")
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			t.Fatal("fixture did not exit after the marker was planted")
		}
	})

	t.Run("exits 0 on TERM before the marker exists", func(t *testing.T) {
		script, _ := fixtureScript(t)
		cmd := exec.Command("sh", "-c", script)
		require.NoError(t, cmd.Start())
		time.Sleep(200 * time.Millisecond)
		require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
		select {
		case err := <-func() <-chan error {
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			return done
		}():
			code := 0
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			} else {
				require.NoError(t, err, "unexpected wait error")
			}
			require.Equal(t, 0, code, "the trap must turn SIGTERM into a graceful exit 0")
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			t.Fatal("fixture did not exit on SIGTERM")
		}
	})
}

// TestBackoffSpecsRetainTerminalFailureCoverage is the repro for finding F1 of
// the PR #514 review: both backoff specs (runRestartBackoffDelayTest and
// runRestartBackoffSpecChangeTest) inject the second failure as a container
// restart, which leaves the Pod Running. checkRestartBackoff's slow path only
// applies backoff when a PodFailed pod exists (pkg/reconciler/roleinstance/
// sync/instance_scale.go:417-426), so with a Running pod the generation/revision
// bypass (instance_scale.go:413-416) can be removed without these specs
// noticing — the e2e suite loses its guard for the "backoff freezes rolling
// updates" regression.
//
// This test FAILS until at least one backoff spec also injects a terminal
// Failed Pod via this PR's own buildFailingPodTemplate + failPod fixture.
// Polarity: contract test for the coverage gap (currently red, green once the
// terminal-failure variant is added).
func TestBackoffSpecsRetainTerminalFailureCoverage(t *testing.T) {
	src, err := os.ReadFile("restart_policy_stability.go")
	require.NoError(t, err, "run from the package directory")

	combined := ""
	for _, fn := range []string{
		"runRestartBackoffDelayTest",
		"runRestartBackoffSpecChangeTest",
	} {
		body := extractGoFuncBody(string(src), fn)
		require.NotEmpty(t, body, "function %s not found in restart_policy_stability.go", fn)
		combined += body
	}
	terminalInjection := strings.Contains(combined, "failPod(") ||
		strings.Contains(combined, "buildFailingPodTemplate(")
	require.True(t, terminalInjection,
		"no backoff spec injects a terminal Failed Pod (failPod/buildFailingPodTemplate); "+
			"the container-restart injection alone cannot detect removal of the "+
			"generation/revision bypass in checkRestartBackoff (see finding F1)")
}

// extractGoFuncBody returns the source of the named top-level func, without a
// full parse: from "func <name>(" to the next top-level declaration.
func extractGoFuncBody(src, name string) string {
	lines := strings.Split(src, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "func "+name+"(") {
			start = i
			break
		}
	}
	if start == -1 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "func ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}
