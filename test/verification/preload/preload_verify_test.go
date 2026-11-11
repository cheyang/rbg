/*
Copyright 2026 The RBGS Authors.

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

// Package preload is a REVIEW HARNESS for PR #567 (verify/preload-registry-codex).
// It is additive only and is not part of the shipped code.
//
// It exercises hack/preload-e2e-images.sh with stubbed `docker`/`kind` binaries
// and checks the one invariant that actually matters for the e2e suites:
//
//	Every workload image the e2e pods reference must be present in the kind
//	cluster after the preload, under the exact name the pods reference.
//
// Stub fidelity note: real `docker image save name:tag` archives carry ONLY the
// named tag, not sibling tags of the same image ID (moby v25
// image/tarexport/save.go parseNames: explicit name:tag -> rs.Get(namedRef) ->
// addAssoc(id, namedRef); only a bare repo name fans out via ReferencesByName).
// The stub therefore records exactly the saved name as the archive content.
package preload

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// ---------- harness plumbing ----------

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// test/verification/preload/ -> repo root
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "hack", "preload-e2e-images.sh")); err != nil {
		t.Fatalf("hack/preload-e2e-images.sh not found under %s: %v", root, err)
	}
	return root
}

const dockerStub = `#!/usr/bin/env bash
set -u
echo "docker $*" >> "${STUB_LOG}"
case "${1:-}" in
  pull)
    img="$2"
    if [ -n "${STUB_PULL_FAIL_MATCH:-}" ] && echo "$img" | grep -qE "${STUB_PULL_FAIL_MATCH}"; then
      cf="${STUB_DIR}/fails.$(echo "$img" | tr '/:' '__')"
      n=0; [ -f "$cf" ] && n=$(cat "$cf")
      if [ "$n" -lt "${STUB_PULL_FAIL_TIMES:-999}" ]; then
        echo $((n+1)) > "$cf"
        echo "stub: simulated pull failure for $img" >&2
        exit 1
      fi
    fi
    exit 0
    ;;
  tag) exit 0 ;;
  image) exit 0 ;; # save recorded via the log line
  *) exit 0 ;;
esac
`

const kindStub = `#!/usr/bin/env bash
echo "kind $*" >> "${STUB_LOG}"
exit 0
`

type stubEnv struct {
	dir string
	log string
}

func makeStubs(t *testing.T) stubEnv {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"docker": dockerStub, "kind": kindStub} {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "runner-temp"), 0o755); err != nil {
		t.Fatal(err)
	}
	return stubEnv{dir: dir, log: filepath.Join(dir, "stub.log")}
}

// runScript executes hack/preload-e2e-images.sh against the stubs.
// extraEnv entries are KEY=VALUE pairs; the stubs always win PATH.
func runPreloadScript(t *testing.T, s stubEnv, extraEnv ...string) (exitCode int, logged []string) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "hack", "preload-e2e-images.sh"))
	cmd.Dir = root
	env := []string{
		"PATH=" + filepath.Join(s.dir, "bin") + ":" + os.Getenv("PATH"),
		"STUB_DIR=" + s.dir,
		"STUB_LOG=" + s.log,
		"RUNNER_TEMP=" + filepath.Join(s.dir, "runner-temp"),
		"KIND_CLUSTER_NAME=verify-cluster",
		"E2E_IMAGE_PULL_ATTEMPTS=3",
		"E2E_IMAGE_PULL_BACKOFF=0", // keep the harness fast
	}
	env = append(env, extraEnv...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		exitCode = 0
	} else if ee, ok := err.(*exec.ExitError); ok {
		exitCode = ee.ExitCode()
	} else {
		t.Fatalf("cannot run script: %v", err)
	}
	t.Logf("script exit=%d output:\n%s", exitCode, out)
	data, _ := os.ReadFile(s.log)
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if l != "" {
			logged = append(logged, l)
		}
	}
	return exitCode, logged
}

func pullsOf(logged []string) (out []string) {
	for _, l := range logged {
		if f := strings.Fields(l); len(f) == 3 && f[0] == "docker" && f[1] == "pull" {
			out = append(out, f[2])
		}
	}
	return out
}

// savedImages returns the names passed to `docker image save ... <name>` —
// i.e. exactly what `kind load image-archive` puts into the node (see stub
// fidelity note in the package doc).
func savedImages(logged []string) map[string]bool {
	out := map[string]bool{}
	for _, l := range logged {
		f := strings.Fields(l)
		if len(f) >= 3 && f[0] == "docker" && f[1] == "image" && f[2] == "save" {
			out[f[len(f)-1]] = true
		}
	}
	return out
}

func tagsOf(logged []string) [][2]string {
	var out [][2]string
	for _, l := range logged {
		if f := strings.Fields(l); len(f) == 4 && f[0] == "docker" && f[1] == "tag" {
			out = append(out, [2]string{f[2], f[3]})
		}
	}
	return out
}

// ---------- what the e2e suites actually reference (derived from test source) ----------

// e2eReferencedImages reads the test sources and returns the workload images the
// e2e pods reference:
//   - utils.DefaultImage (the dominant builder image, 26 usage sites)
//   - the patio-runtime engine image (CreatePatioRuntime)
//   - short-name literals of the known test repos (nginx/busybox/python/pause)
//
// `nonexistent-registry.example.com/fake-image:v999` is intentionally absent: the
// warmup suite uses it to prove pull failures surface, so preloading it would be
// wrong.
func e2eReferencedImages(t *testing.T) []string {
	t.Helper()
	return referencedImages(t, true)
}

// referencedImages derives the e2e-referenced workload images from the test
// sources; withPatio controls whether the patio-runtime engine image is included.
func referencedImages(t *testing.T, withPatio bool) []string {
	t.Helper()
	root := repoRoot(t)
	utilsSrc, err := os.ReadFile(filepath.Join(root, "test", "utils", "utils.go"))
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	seen := map[string]bool{}
	add := func(img string) {
		if img != "" && !seen[img] {
			seen[img] = true
			want = append(want, img)
		}
	}
	if m := regexp.MustCompile(`DefaultImage\s*=\s*"([^"]+)"`).FindStringSubmatch(string(utilsSrc)); m != nil {
		add(m[1])
	} else {
		t.Fatal("could not derive utils.DefaultImage")
	}
	if withPatio {
		if m := regexp.MustCompile(`Image:\s*"(registry-cn-hangzhou[^"]+)"`).FindStringSubmatch(string(utilsSrc)); m != nil {
			add(m[1])
		} else {
			t.Fatal("could not derive patio-runtime image from test/utils/utils.go")
		}
	}
	// Short-name literals of the known workload repos referenced by e2e pods.
	shortRe := regexp.MustCompile(`"((?:nginx|busybox|python):[^"]+|registry\.k8s\.io/pause:[^"]+)"`)
	_ = filepath.WalkDir(filepath.Join(root, "test", "e2e"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		data, _ := os.ReadFile(p)
		for _, m := range shortRe.FindAllStringSubmatch(string(data), -1) {
			add(m[1])
		}
		return nil
	})
	return want
}

// ---------- findings ----------

// F1 (blocker): every image the e2e pods reference must be loaded into kind in
// docker.io mode — the mode every current workflow selects (release-test.yml
// hard-codes it; setup-e2e-cluster defaults to it).
func TestPreloadCoversE2EReferencedImages_DockerIO(t *testing.T) {
	s := makeStubs(t)
	code, logged := runPreloadScript(t, s, "E2E_IMAGE_REGISTRY=docker.io")
	if code != 0 {
		t.Fatalf("script exited %d in docker.io mode", code)
	}
	saved := savedImages(logged)
	for _, img := range referencedImages(t, false) {
		if !saved[img] {
			t.Errorf("e2e-referenced image %q is NOT loaded into kind in docker.io mode", img)
		}
	}
}

// F2 (major): the old inline steps preloaded
// registry-cn-hangzhou.ack.aliyuncs.com/dev/patio-runtime:v0.2.0 (used by the
// engine-runtime tests via utils.CreatePatioRuntime). docker.io mode skips it
// entirely, so those pods pull the ACK registry at pod-start time — the same
// intermittent-overseas-reachability exposure this PR sets out to remove.
func TestPreloadCoversPatioRuntime_DockerIO(t *testing.T) {
	s := makeStubs(t)
	code, logged := runPreloadScript(t, s, "E2E_IMAGE_REGISTRY=docker.io")
	if code != 0 {
		t.Fatalf("script exited %d in docker.io mode", code)
	}
	utilsSrc, err := os.ReadFile(filepath.Join(repoRoot(t), "test", "utils", "utils.go"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`Image:\s*"(registry-cn-hangzhou[^"]+)"`).FindStringSubmatch(string(utilsSrc))
	if m == nil {
		t.Fatal("could not derive patio-runtime image from test/utils/utils.go")
	}
	if !savedImages(logged)[m[1]] {
		t.Errorf("patio-runtime image %q is NOT loaded into kind in docker.io mode", m[1])
	}
}

// F1 (blocker), aliyun leg: pods reference utils.DefaultImage verbatim
// (registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest). The script pulls
// that mirror but then re-tags it to nginx:latest and saves nginx:latest; docker
// save by explicit name:tag drops the sibling acs-sample tag, so the kind node
// still lacks the name the pods reference.
func TestPreloadCoversDefaultImage_Aliyun(t *testing.T) {
	s := makeStubs(t)
	code, logged := runPreloadScript(t, s, "E2E_IMAGE_REGISTRY=aliyun")
	if code != 0 {
		t.Fatalf("script exited %d in aliyun mode", code)
	}
	saved := savedImages(logged)
	utilsSrc, err := os.ReadFile(filepath.Join(repoRoot(t), "test", "utils", "utils.go"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`DefaultImage\s*=\s*"([^"]+)"`).FindStringSubmatch(string(utilsSrc))
	if m == nil {
		t.Fatal("could not derive utils.DefaultImage")
	}
	if !saved[m[1]] {
		t.Errorf("aliyun mode: pods reference %q but only these names are loaded: %v", m[1], saved)
	}
}

// F3 (minor): in aliyun mode a mirrored target must come from a mirror carrying
// the SAME tag (or be declared "-" so it is pulled upstream). Tagging
// acs-sample/nginx:latest as nginx:1.25 silently serves the wrong content;
// registry API check 2026-11-12: acs-sample/nginx has only the "latest" tag.
func TestAliyunMirrorCarriesSameTag(t *testing.T) {
	s := makeStubs(t)
	code, logged := runPreloadScript(t, s, "E2E_IMAGE_REGISTRY=aliyun")
	if code != 0 {
		t.Fatalf("script exited %d in aliyun mode", code)
	}
	for _, tg := range tagsOf(logged) {
		src, dst := tg[0], tg[1]
		srcTag := src[strings.LastIndex(src, ":")+1:]
		dstTag := dst[strings.LastIndex(dst, ":")+1:]
		if srcTag != dstTag {
			t.Errorf("aliyun retag mismatches content: %s (tag %q) tagged as %s (tag %q)", src, srcTag, dst, dstTag)
		}
	}
}

// P0 canary (premise, frozen base fixture): the base release-test.yml inline
// pre-load step had NO retry, so one transient pull failure failed the whole
// step — the flake in run 42213206716. Canary asserts the observed base
// behavior; if the fixture is ever "fixed" this flips and must be re-inverted.
func TestBaseInlinePreloadFailsOnFirstTransientPull(t *testing.T) {
	s := makeStubs(t)
	root := repoRoot(t)
	fixture := filepath.Join(root, "docs", "verification", "preload-registry-codex", "fixtures", "base-release-test-preload.sh")
	cmd := exec.Command("bash", fixture)
	cmd.Dir = root
	cmd.Env = []string{
		"PATH=" + filepath.Join(s.dir, "bin") + ":" + os.Getenv("PATH"),
		"STUB_DIR=" + s.dir,
		"STUB_LOG=" + s.log,
		"RUNNER_TEMP=" + filepath.Join(s.dir, "runner-temp"),
		"KIND_IMAGE_PLATFORM=linux/amd64",
		"STUB_PULL_FAIL_MATCH=.*", // every pull fails
		"STUB_PULL_FAIL_TIMES=99",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("base inline script unexpectedly succeeded despite failing pulls:\n%s", out)
	}
	data, _ := os.ReadFile(s.log)
	pulls := pullsOf(strings.Split(strings.TrimSpace(string(data)), "\n"))
	if len(pulls) != 1 {
		t.Fatalf("base inline script attempted %d pulls, want exactly 1 (no retry): %v", len(pulls), pulls)
	}
	t.Logf("base behavior confirmed: first transient pull failure fails the step (pulls=%v)", pulls)
}

// Contract (head): the new script must survive a transient pull failure that
// killed the base inline step — this is the resilience the PR is paying for.
func TestNewScriptSurvivesTransientPullFailure(t *testing.T) {
	s := makeStubs(t)
	code, logged := runPreloadScript(t, s,
		"E2E_IMAGE_REGISTRY=docker.io",
		"STUB_PULL_FAIL_MATCH=.*", // every image fails the first time
		"STUB_PULL_FAIL_TIMES=1",
	)
	if code != 0 {
		t.Fatalf("script exited %d despite single transient failure per image", code)
	}
	counts := map[string]int{}
	for _, p := range pullsOf(logged) {
		counts[p]++
	}
	for img, n := range counts {
		if n != 2 {
			t.Errorf("image %s pulled %d times, want 2 (1 failure + 1 retry)", img, n)
		}
	}
}

// Contract (head): persistent pull failure must still fail the job (with a
// bounded number of attempts).
func TestPullRetryExhaustionFailsJob(t *testing.T) {
	s := makeStubs(t)
	code, logged := runPreloadScript(t, s,
		"E2E_IMAGE_REGISTRY=docker.io",
		"STUB_PULL_FAIL_MATCH=.*",
		"STUB_PULL_FAIL_TIMES=99",
	)
	if code == 0 {
		t.Fatal("script succeeded despite all pulls failing")
	}
	pulls := pullsOf(logged)
	if len(pulls) != 3 { // E2E_IMAGE_PULL_ATTEMPTS=3
		t.Fatalf("attempted %d pulls of the first image, want 3 (E2E_IMAGE_PULL_ATTEMPTS)", len(pulls))
	}
}

// Contract (head): an unsupported E2E_IMAGE_REGISTRY value is rejected loudly.
func TestInvalidRegistryModeRejected(t *testing.T) {
	s := makeStubs(t)
	code, _ := runPreloadScript(t, s, "E2E_IMAGE_REGISTRY=quay.io")
	if code == 0 {
		t.Fatal("script accepted an unsupported registry mode")
	}
}

// Base control for the ledger (premise-era behavior): the base pre-load steps
// loaded the China-registry images the pods reference (acs-sample DefaultImage
// and patio-runtime) under their referenced names. This documents base=pass for
// the contract that F1/F2 show failing on the head.
func TestBaseInlinePreloadCoversChinaRegistryImages(t *testing.T) {
	s := makeStubs(t)
	root := repoRoot(t)
	fixture := filepath.Join(root, "docs", "verification", "preload-registry-codex", "fixtures", "base-release-test-preload.sh")
	cmd := exec.Command("bash", fixture)
	cmd.Dir = root
	cmd.Env = []string{
		"PATH=" + filepath.Join(s.dir, "bin") + ":" + os.Getenv("PATH"),
		"STUB_DIR=" + s.dir,
		"STUB_LOG=" + s.log,
		"RUNNER_TEMP=" + filepath.Join(s.dir, "runner-temp"),
		"KIND_IMAGE_PLATFORM=linux/amd64",
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("base fixture failed: %v\n%s", err, out)
	}
	data, _ := os.ReadFile(s.log)
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	saved := savedImages(lines)
	utilsSrc, err := os.ReadFile(filepath.Join(root, "test", "utils", "utils.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, re := range []string{`DefaultImage\s*=\s*"([^"]+)"`, `Image:\s*"(registry-cn-hangzhou[^"]+)"`} {
		m := regexp.MustCompile(re).FindStringSubmatch(string(utilsSrc))
		if m == nil {
			t.Fatalf("could not derive image from test/utils/utils.go via %q", re)
		}
		if !saved[m[1]] {
			t.Errorf("base preload did NOT load %q under its referenced name", m[1])
		}
	}
}
