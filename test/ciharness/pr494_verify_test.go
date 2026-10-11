// Verification harness for PR #494 (test(ci): split full E2E suite into parallel slices).
//
// This file is an ADDITIVE review artifact (stage 2 of the review pipeline). It does not
// modify any production or test code under review. Each test encodes one review claim as a
// CONTRACT test: it FAILS on the PR head while the finding is present and PASSES once the
// author fixes it. Tests read the workflow / action / Makefile under review and evaluate
// the e2e suite's real ginkgo label selection via `go test -ginkgo.dry-run`, so no cluster
// is needed (a fake kubeconfig is enough; the framework does not dial out at registration).
//
// Run: go test ./test/ciharness/ -run TestPR494 -v
package ciharness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"k8s.io/client-go/util/jsonpath"
)

// ---------- shared helpers ----------

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Makefile")); err != nil {
		t.Fatalf("repo root %q has no Makefile: %v", root, err)
	}
	return root
}

func readFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

var (
	fakeKCOnce sync.Once
	fakeKCPath string
)

// fakeKubeconfig returns a kubeconfig pointing at a dead server. The e2e framework calls
// config.GetConfigOrDie() at registration time, so the file must EXIST, but no client
// constructed during registration ever dials it (ginkgo --dry-run executes nothing).
func fakeKubeconfig(t *testing.T) string {
	t.Helper()
	fakeKCOnce.Do(func() {
		dir, err := os.MkdirTemp("", "pr494-fakekc")
		if err != nil {
			t.Fatalf("mktemp: %v", err)
		}
		fakeKCPath = filepath.Join(dir, "config")
		kc := `apiVersion: v1
kind: Config
clusters: [{cluster: {server: "https://127.0.0.1:1"}, name: fake}]
contexts: [{context: {cluster: fake, user: fake}, name: fake}]
current-context: fake
users: [{name: fake, user: {token: fake}}]
`
		if err := os.WriteFile(fakeKCPath, []byte(kc), 0o600); err != nil {
			t.Fatalf("write fake kubeconfig: %v", err)
		}
	})
	return fakeKCPath
}

type spec struct {
	id   string // file:line of the It node — stable identity across runs
	text string // full spec text (for readable reports)
}

var (
	dryRunMu    sync.Mutex
	dryRunCache = map[string][]spec{}
)

// dryRunSpecs returns the set of specs that WOULD run under the given ginkgo label filter,
// using the real test tree and the real ginkgo label-selection logic.
func dryRunSpecs(t *testing.T, filter string) []spec {
	t.Helper()
	dryRunMu.Lock()
	if cached, ok := dryRunCache[filter]; ok {
		dryRunMu.Unlock()
		return cached
	}
	dryRunMu.Unlock()

	cmd := exec.Command("go", "test", "./test/e2e/", "-count=1", "-v",
		"-ginkgo.dry-run", "-ginkgo.v", "--ginkgo.label-filter="+filter, "-timeout", "3m")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+fakeKubeconfig(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ginkgo dry-run failed for filter %q: %v\n--- output ---\n%s", filter, err, out)
	}

	lines := strings.Split(string(out), "\n")
	prev := []string{""}
	var specs []spec
	for _, raw := range lines {
		line := ansiRE.ReplaceAllString(raw, "")
		line = strings.TrimRight(line, " \t")
		if line == "" {
			continue
		}
		if line == "\u2022 [0.000 seconds]" { // "• [0.000 seconds]" marks a leaf spec that ran (dry-run)
			// one line up: <path>:<line>; two lines up: full spec text
			if len(prev) >= 2 {
				specs = append(specs, spec{id: prev[len(prev)-1], text: prev[len(prev)-2]})
			}
		}
		prev = append(prev, line)
	}
	if len(specs) == 0 {
		t.Fatalf("dry-run for filter %q selected 0 specs — filter broken or parser drifted.\n--- output ---\n%s", filter, out)
	}
	dryRunMu.Lock()
	dryRunCache[filter] = specs
	dryRunMu.Unlock()
	return specs
}

func specSet(specs []spec) map[string]spec {
	m := make(map[string]spec, len(specs))
	for _, s := range specs {
		m[s.id] = s
	}
	return m
}

func minus(t *testing.T, a, b []spec) []spec {
	bb := specSet(b)
	var out []spec
	for _, s := range a {
		if _, ok := bb[s.id]; !ok {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func union(specs ...[]spec) []spec {
	m := map[string]spec{}
	for _, ss := range specs {
		for _, s := range ss {
			m[s.id] = s
		}
	}
	out := make([]spec, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// workflowMatrixFilters parses the e2e-matrix slice definitions (group -> filter,
// timeout) out of the workflow under review, so the harness tracks the file.
var matrixEntryRE = regexp.MustCompile(`(?m)^\s+- group: (\S+)\s*\n\s+filter: "([^"]+)"\s*\n\s+timeout_minutes: (\d+)`)

type matrixEntry struct {
	group, filter string
	timeoutMin    string
}

func matrixEntries(t *testing.T) []matrixEntry {
	t.Helper()
	wf := readFile(t, ".github/workflows/e2e-test.yml")
	ms := matrixEntryRE.FindAllStringSubmatch(wf, -1)
	if len(ms) == 0 {
		t.Fatalf("could not parse any matrix entries from e2e-test.yml — workflow shape changed?")
	}
	out := make([]matrixEntry, 0, len(ms))
	for _, m := range ms {
		out = append(out, matrixEntry{group: m[1], filter: m[2], timeoutMin: m[3]})
	}
	return out
}

// literalJobLabelFilters extracts E2E_LABEL_FILTER='...' literals passed to `make test-e2e`
// by standalone (non-matrix) jobs in e2e-test.yml. `${{ ... }}` indirections are skipped.
var literalFilterRE = regexp.MustCompile(`E2E_LABEL_FILTER='([^']+)'`)

func literalJobLabelFilters(t *testing.T) map[string]string {
	t.Helper()
	wf := readFile(t, ".github/workflows/e2e-test.yml")
	filters := map[string]string{}
	for _, job := range []string{"e2e-manifest", "e2e-volcano-gang", "e2e-scheduler-plugins-gang"} {
		start := strings.Index(wf, "\n  "+job+":")
		if start < 0 {
			t.Fatalf("job %s not found in e2e-test.yml", job)
		}
		rest := wf[start:]
		end := strings.Index(rest[1:], "\n  e2e-")
		block := rest
		if end > 0 {
			block = rest[:end+1]
		}
		for _, m := range literalFilterRE.FindAllStringSubmatch(block, -1) {
			if !strings.Contains(m[1], "${{") {
				filters[job] = m[1]
			}
		}
	}
	return filters
}

// oldWorldFilters are the label filters the monolithic workflow used at the merge-base:
// one default-scheduler suite + one suite per gang backend.
func oldWorldFilters() map[string]string {
	return map[string]string{
		"old-default(monolith+manifest)": "!volcano && !scheduler-plugins",
		"old-volcano":                    "volcano",
		"old-scheduler-plugins":          "scheduler-plugins",
	}
}

// newWorldFilters are every label filter the new workflow runs against the ./test/e2e suite.
func newWorldFilters(t *testing.T) map[string]string {
	t.Helper()
	filters := map[string]string{}
	for _, e := range matrixEntries(t) {
		filters["matrix:"+e.group] = e.filter
	}
	for job, f := range literalJobLabelFilters(t) {
		filters["job:"+job] = f
	}
	return filters
}

// ---------- guard (not a finding): the split must not drop any spec ----------

// TestPR494_MatrixUnionCoversOldSuite is the coverage guard for the PR's central claim
// ("keeps full E2E coverage"): every spec the OLD workflow would have run must be selected
// by at least one NEW matrix slice or standalone job. Also reports specs double-run inside
// the matrix (waste, not a failure).
func TestPR494_MatrixUnionCoversOldSuite(t *testing.T) {
	oldSets := map[string][]spec{}
	var oldUnion []spec
	for name, f := range oldWorldFilters() {
		oldSets[name] = dryRunSpecs(t, f)
		oldUnion = union(oldUnion, oldSets[name])
	}

	newSets := map[string][]spec{}
	var newUnion []spec
	for name, f := range newWorldFilters(t) {
		newSets[name] = dryRunSpecs(t, f)
		newUnion = union(newUnion, newSets[name])
	}

	t.Logf("old-world total specs covered: %d; new-world total: %d", len(oldUnion), len(newUnion))
	for name, ss := range newSets {
		t.Logf("  new filter %-28s -> %d specs", name, len(ss))
	}

	// matrix-internal duplicates (same cluster config run twice = pure waste)
	count := map[string]int{}
	text := map[string]string{}
	for name, ss := range newSets {
		if !strings.HasPrefix(name, "matrix:") {
			continue
		}
		for _, s := range ss {
			count[s.id]++
			text[s.id] = s.text
		}
	}
	for id, n := range count {
		if n > 1 {
			t.Logf("  note: spec runs in %d matrix slices: %s (%s)", n, text[id], id)
		}
	}

	dropped := minus(t, oldUnion, newUnion)
	for _, s := range dropped {
		t.Errorf("DROPPED by new workflow (ran before, runs nowhere now): %s (%s)", s.text, s.id)
	}
	if len(dropped) > 0 {
		t.Fatalf("%d specs lost all CI coverage in the split", len(dropped))
	}
}

// ---------- F1: bare `make test-e2e` consumers lose the v1alpha1 suite ----------

var makefileFilterRE = regexp.MustCompile(`(?m)^E2E_LABEL_FILTER \?= *'?([^'\n]+)'?`)

// TestPR494_BareMakeTestE2EConsumersKeepFullSuite encodes: any workflow that invokes bare
// `make test-e2e` (no E2E_LABEL_FILTER override) must keep selecting the full
// default-scheduler suite it ran before this PR (old default: '!volcano &&
// !scheduler-plugins', which included the whole v1alpha1 compat tree). The PR narrows the
// Makefile default to also exclude v1alpha1 but only updates e2e-test.yml; release-test.yml
// still invokes bare `make test-e2e` (its /release-test qualification runs), so those
// v1alpha1 specs silently stop running there.
func TestPR494_BareMakeTestE2EConsumersKeepFullSuite(t *testing.T) {
	mk := readFile(t, "Makefile")
	m := makefileFilterRE.FindStringSubmatch(mk)
	if m == nil {
		t.Fatalf("could not parse E2E_LABEL_FILTER default from Makefile")
	}
	makeDefault := m[1]
	oldDefault := oldWorldFilters()["old-default(monolith+manifest)"]
	t.Logf("Makefile E2E_LABEL_FILTER default: %s (old default: %s)", makeDefault, oldDefault)

	dropped := minus(t, dryRunSpecs(t, oldDefault), dryRunSpecs(t, makeDefault))
	if len(dropped) == 0 {
		t.Logf("Makefile default still selects the full old default-scheduler suite")
	}

	// find bare `make test-e2e` invocations (no E2E_LABEL_FILTER= on the command)
	bareRE := regexp.MustCompile(`make test-e2e(?:\s|$)`)
	type consumer struct{ file, line string }
	var bare []consumer
	entries, err := os.ReadDir(filepath.Join(repoRoot(t), ".github", "workflows"))
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		rel := filepath.Join(".github", "workflows", e.Name())
		content := readFile(t, rel)
		// join `\\`-continued lines so multiline `make test-e2e \\` invocations are
		// evaluated with their flags (E2E_LABEL_FILTER=... on the next line)
		joined := regexp.MustCompile(`\\\n\s*`).ReplaceAllString(content, " ")
		for i, line := range strings.Split(joined, "\n") {
			if bareRE.MatchString(line) && !strings.Contains(line, "E2E_LABEL_FILTER") {
				bare = append(bare, consumer{rel, fmt.Sprintf("L~%d: %s", i+1, strings.TrimSpace(line))})
			}
		}
	}
	t.Logf("bare `make test-e2e` consumers: %v", bare)

	if len(dropped) > 0 && len(bare) > 0 {
		for _, s := range dropped {
			t.Logf("  no longer run by bare `make test-e2e`: %s (%s)", s.text, s.id)
		}
		t.Fatalf("Makefile default drops %d specs (incl. the v1alpha1 compat suite) while %d bare `make test-e2e` consumer(s) rely on it: %v",
			len(dropped), len(bare), bare)
	}
}

// ---------- F2: webhook caBundle gate weakened from all-webhooks to any-webhook ----------

var caBundleJSONPathRE = regexp.MustCompile(`\{[^{}]*caBundle[^{}]*\}`)

func renderJSONPath(t *testing.T, tmpl string, obj map[string]interface{}) string {
	t.Helper()
	jp := jsonpath.New("harness")
	jp.AllowMissingKeys(true) // same semantics as `kubectl get -o jsonpath`
	if err := jp.Parse(tmpl); err != nil {
		t.Fatalf("parse jsonpath %q: %v", tmpl, err)
	}
	var sb strings.Builder
	if err := jp.Execute(&sb, obj); err != nil {
		t.Fatalf("execute jsonpath: %v", err)
	}
	return sb.String()
}

func webhookConfig(bundles ...string) map[string]interface{} {
	var hooks []interface{}
	for _, b := range bundles {
		cc := map[string]interface{}{}
		if b != "" {
			cc["caBundle"] = b
		}
		hooks = append(hooks, map[string]interface{}{"clientConfig": cc})
	}
	return map[string]interface{}{"webhooks": hooks}
}

// TestPR494_WebhookCABundleGateRequiresEveryWebhook: at the merge-base the e2e gate
// required (.webhooks | length > 0) AND all(.webhooks[]; caBundle != "") for BOTH webhook
// configurations (jq). The new composite action replaces it with "the concatenated
// `{.webhooks[*].clientConfig.caBundle}` output is non-empty" per configuration — which
// passes as soon as ANY one webhook in a configuration has a bundle, even if sibling
// webhooks in the SAME configuration are still unpatched.
func TestPR494_WebhookCABundleGateRequiresEveryWebhook(t *testing.T) {
	action := readFile(t, ".github/actions/setup-e2e-cluster/action.yaml")
	// The expected fix restores an explicit per-webhook quantifier (the merge-base jq
	// `all(.webhooks[]; ...)`). Detect that shape first — a jq expression also contains
	// `{...caBundle...}` braces, so it must win over the jsonpath regex below.
	if regexp.MustCompile(`all\(\s*\.webhooks`).MatchString(action) {
		t.Logf("gate uses a per-webhook quantifier (jq all(.webhooks[]...)) — contract satisfied")
		return
	}
	m := caBundleJSONPathRE.FindString(action)
	if m == "" {
		t.Fatalf("no caBundle jsonpath or per-webhook quantifier found in action.yaml — harness needs update")
	}
	t.Logf("gate jsonpath used by the action: %s", m)

	// new gate semantics, exactly as the action implements them: ready iff output non-empty
	newGate := func(cfg map[string]interface{}) bool { return renderJSONPath(t, m, cfg) != "" }
	// old gate semantics (merge-base jq): length>0 AND every webhook has a non-empty bundle
	oldGate := func(cfg map[string]interface{}) bool {
		hooks, _ := cfg["webhooks"].([]interface{})
		if len(hooks) == 0 {
			return false
		}
		for _, h := range hooks {
			cc, _ := h.(map[string]interface{})["clientConfig"].(map[string]interface{})
			if cc["caBundle"] == nil || cc["caBundle"] == "" {
				return false
			}
		}
		return true
	}

	full := webhookConfig("QQ==", "Qg==")
	if !newGate(full) || !oldGate(full) {
		t.Fatalf("sanity: fully-injected config must pass both gates (new=%v old=%v)", newGate(full), oldGate(full))
	}
	emptyList := map[string]interface{}{"webhooks": []interface{}{}}
	if newGate(emptyList) || oldGate(emptyList) {
		t.Fatalf("sanity: empty webhook list must fail both gates")
	}
	none := webhookConfig("")
	if newGate(none) || oldGate(none) {
		t.Fatalf("sanity: config with no bundles must fail both gates")
	}

	partial := webhookConfig("QQ==", "") // one webhook patched, its sibling not
	t.Logf("partially-injected config: old gate passes=%v, new gate passes=%v", oldGate(partial), newGate(partial))
	if !oldGate(partial) && newGate(partial) {
		t.Errorf("caBundle gate regression: with 2+ webhooks per configuration, the new gate passes while a sibling webhook still has no caBundle; the merge-base gate (all webhooks non-empty) rejected this")
	}
}

// ---------- F3: volcano job diagnostics query the scheduler-plugins PodGroup group ----------

// TestPR494_VolcanoJobDiagnosticsQueryVolcanoPodGroup: the merge-base volcano job dumped
// `podgroups.scheduling.volcano.sh`; the rewritten job dumps `podgroups.scheduling.x-k8s.io`
// — the scheduler-plugins coscheduling group. In-repo authority for the Volcano group:
// pkg/scheduler/volcano/scheduler.go (CrdName = "podgroups.scheduling.volcano.sh").
func TestPR494_VolcanoJobDiagnosticsQueryVolcanoPodGroup(t *testing.T) {
	volcanoSrc := readFile(t, "pkg/scheduler/volcano/scheduler.go")
	const volcanoPodGroup = "podgroups.scheduling.volcano.sh"
	if !strings.Contains(volcanoSrc, volcanoPodGroup) {
		t.Fatalf("in-repo authority for volcano PodGroup group gone — harness needs update")
	}

	wf := readFile(t, ".github/workflows/e2e-test.yml")
	jobBlock := func(name string) string {
		start := strings.Index(wf, "\n  "+name+":")
		if start < 0 {
			t.Fatalf("job %s not found", name)
		}
		rest := wf[start:]
		end := strings.Index(rest[1:], "\n  e2e-")
		if end < 0 {
			return rest
		}
		return rest[:end+1]
	}
	volcanoJob := jobBlock("e2e-volcano-gang")
	spJob := jobBlock("e2e-scheduler-plugins-gang")

	if !strings.Contains(spJob, "podgroups.scheduling.x-k8s.io") {
		t.Fatalf("sanity: scheduler-plugins job should query podgroups.scheduling.x-k8s.io — block slicing drifted")
	}
	if !strings.Contains(volcanoJob, volcanoPodGroup) {
		t.Errorf("volcano job diagnostics never query %s (it queries podgroups.scheduling.x-k8s.io, the scheduler-plugins group) — volcano PodGroups silently absent from failure diagnostics", volcanoPodGroup)
	}
}

// ---------- F4: job timeout equals the go test deadline ----------

// TestPR494_JobTimeoutExceedsGoTestDeadline: each matrix job sets
// `timeout-minutes: ${{ matrix.timeout_minutes }}` and passes the same value as the go test
// deadline `E2E_TIMEOUT=${{ matrix.timeout_minutes }}m`. Setup (checkout, kind, LWS, helm,
// image loads) consumes the same wall clock, so the GitHub job deadline always expires
// before ginkgo's own timeout can fire — the intended "go test timeout -> panic with stack
// -> failure() diagnostics + report upload" path is unreachable and a slow slice dies as an
// opaque job-level timeout racing the always()/failure() cleanup steps.
func TestPR494_JobTimeoutExceedsGoTestDeadline(t *testing.T) {
	wf := readFile(t, ".github/workflows/e2e-test.yml")
	jobTimeout := regexp.MustCompile(`timeout-minutes: \$\{\{ matrix\.timeout_minutes \}\}`)
	testTimeout := regexp.MustCompile(`E2E_TIMEOUT=\$\{\{ matrix\.timeout_minutes \}\}m`)
	if jobTimeout.MatchString(wf) && testTimeout.MatchString(wf) {
		entries := matrixEntries(t)
		for _, e := range entries {
			t.Logf("matrix %-14s job timeout = go test timeout = %sm", e.group, e.timeoutMin)
		}
		t.Errorf("matrix job timeout and go test deadline are the SAME value; add setup headroom (e.g. timeout-minutes = timeout_minutes + 15) so ginkgo's timeout, diagnostics and report upload can actually run")
	}
}

// ---------- F5: setup readiness gate verifies only 3 of the 10 committed CRDs ----------

var crdNameRE = regexp.MustCompile(`(?m)^  name: (\S+)$`)

// TestPR494_SetupGateVerifiesAllCommittedCRDs: the merge-base main job verified every CRD
// in config/crd/bases after install; the new composite action hardcodes 3 names in the
// "Wait for controller readiness" step, so a chart/manifest regression that drops any of
// the other 7 CRDs is no longer caught at setup time.
func TestPR494_SetupGateVerifiesAllCommittedCRDs(t *testing.T) {
	base := filepath.Join(repoRoot(t), "config", "crd", "bases")
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("read config/crd/bases: %v", err)
	}
	var crds []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		content := readFile(t, filepath.Join("config", "crd", "bases", e.Name()))
		if m := crdNameRE.FindStringSubmatch(content); m != nil {
			crds = append(crds, m[1])
		}
	}
	if len(crds) == 0 {
		t.Fatalf("no CRDs parsed from config/crd/bases")
	}

	action := readFile(t, ".github/actions/setup-e2e-cluster/action.yaml")
	start := strings.Index(action, "- name: Wait for controller readiness")
	if start < 0 {
		t.Fatalf("readiness step not found in action.yaml")
	}
	rest := action[start:]
	end := strings.Index(rest[1:], "\n    - name:")
	step := rest
	if end > 0 {
		step = rest[:end+1]
	}

	// token-exact matching: "roleinstances.workloads.x-k8s.io" must not satisfy
	// "instances.workloads.x-k8s.io"
	tokens := map[string]bool{}
	for _, tok := range strings.FieldsFunc(step, func(r rune) bool {
		return !(r == '.' || r == '-' || r == '_' || r == '/' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		tokens[tok] = true
	}
	var missing []string
	if strings.Contains(step, "config/crd/bases") {
		// the step enumerates the committed CRD directory (merge-base shape): all covered
		t.Logf("readiness step enumerates config/crd/bases — all %d CRDs covered", len(crds))
	} else {
		for _, crd := range crds {
			if !tokens[crd] {
				missing = append(missing, crd)
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("setup readiness gate checks %d of %d committed CRDs; missing: %s",
			len(crds)-len(missing), len(crds), strings.Join(missing, ", "))
	}
}
