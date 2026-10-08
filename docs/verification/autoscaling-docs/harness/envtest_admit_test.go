// Package harness is the L2 (integration) layer of the PR #389 verification.
//
// It starts a real API server (controller-runtime envtest), installs the CRDs the
// docs rely on (this repo's workloads.x-k8s.io CRDs plus the rbg-planner AutoScaler
// CRD and the KEDA ScaledObject CRD, snapshotted in harness/crds/), extracts the
// *exact* YAML payloads the documentation tells users to `kubectl apply`, and
// submits them with the real kubectl binary.
//
// What it proves:
//   - every manifest the docs instruct users to apply is admitted by the real CRD
//     schemas (field names / structure correct) -> contract for Operations 1-4
//   - the RBGSA /scale subresource really supports `kubectl scale rbgsa`
//     (Operation 1 Step 3) and round-trips spec.replicas
//   - a user following the concept doc's inline comment
//     `metricSource: sglang | vllm | dynamo` gets their AutoScaler REJECTED
//     (canary for finding B8: the real enum is sglang|vllm|patio)
//
// Harness-bites negative controls (an apply that must FAIL; proves schema
// validation is actually enforced, so a green doc-manifest run means something):
//   - RBG with an unknown field under spec.roles[0]
//   - AutoScaler with an unknown field under DynamoPlanner
//
// Requires: KUBEBUILDER_ASSETS pointing at envtest binaries (1.36.x), kubectl on PATH.
// Run: KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64 \
//      go test ./docs/verification/autoscaling-docs/harness/ -v -timeout 10m
package harness

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	kubeconfigPath string
	kubectlOut     = &bytes.Buffer{}
)

func TestMain(m *testing.M) {
	root, err := filepath.Abs(filepath.Join(".", "..", "..", "..", ".."))
	if err != nil {
		fmt.Println("FATAL: cannot resolve repo root:", err)
		os.Exit(2)
	}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		assets := os.ExpandEnv("$HOME/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64")
		if _, err := os.Stat(assets); err == nil {
			_ = os.Setenv("KUBEBUILDER_ASSETS", assets)
		}
	}
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(root, "config", "crd", "bases"),
			filepath.Join(root, "docs", "verification", "autoscaling-docs", "harness", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Println("FATAL: envtest start:", err)
		os.Exit(2)
	}
	defer func() { _ = testEnv.Stop() }()

	kcDir, err := os.MkdirTemp("", "pr389-kc")
	if err != nil {
		fmt.Println("FATAL:", err)
		os.Exit(2)
	}
	kubeconfigPath = filepath.Join(kcDir, "kubeconfig")
	kc := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: envtest
  cluster:
    server: %s
    certificate-authority-data: %s
users:
- name: envtest
  user:
    client-certificate-data: %s
    client-key-data: %s
contexts:
- name: envtest
  context:
    cluster: envtest
    user: envtest
current-context: envtest
`, cfg.Host,
		base64.StdEncoding.EncodeToString(cfg.CAData),
		base64.StdEncoding.EncodeToString(cfg.CertData),
		base64.StdEncoding.EncodeToString(cfg.KeyData))
	if err := os.WriteFile(kubeconfigPath, []byte(kc), 0o600); err != nil {
		fmt.Println("FATAL: write kubeconfig:", err)
		os.Exit(2)
	}
	code := m.Run()
	// persist the raw kubectl transcript as evidence
	transcript := filepath.Join(root, "docs", "verification", "autoscaling-docs", "results", "l2-kubectl-transcript.txt")
	_ = os.WriteFile(transcript, kubectlOut.Bytes(), 0o644)
	os.Exit(code)
}

func kubectl(t *testing.T, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"--kubeconfig", kubeconfigPath}, args...)
	cmd := exec.Command("kubectl", full...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	kubectlOut.WriteString(fmt.Sprintf("$ kubectl %s\n  rc=%v\n  out: %s\n  err: %s\n",
		strings.Join(args, " "), err, strings.TrimSpace(out.String()), strings.TrimSpace(errb.String())))
	return strings.TrimSpace(out.String()), err
}

// apply submits a manifest exactly the way the docs instruct (kubectl apply).
func apply(t *testing.T, name, manifest string, extra ...string) error {
	t.Helper()
	f, err := os.CreateTemp("", "pr389-manifest-*.yaml")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	_ = f.Close()
	args := append([]string{"apply", "--validate=strict", "-f", f.Name()}, extra...)
	_, err = kubectl(t, args...)
	return err
}

// extractHeredocs pulls every `cat <<'EOF' | kubectl apply -f -` payload out of a
// markdown guide — i.e. exactly what a reader of the doc would paste.
func extractHeredocs(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var blocks []string
	lines := strings.Split(string(raw), "\n")
	in := false
	var cur []string
	for _, l := range lines {
		if !in && strings.Contains(l, "cat <<'EOF' | kubectl apply -f -") {
			in = true
			cur = nil
			continue
		}
		if in && l == "EOF" {
			blocks = append(blocks, strings.Join(cur, "\n"))
			in = false
			continue
		}
		if in {
			cur = append(cur, l)
		}
	}
	if in {
		t.Fatalf("unterminated heredoc in %s", path)
	}
	return blocks
}

var yamlFence = regexp.MustCompile("(?s)```yaml\n(.*?)```")

// extractYamlBlocks pulls every fenced ```yaml block out of a concept document.
func extractYamlBlocks(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var blocks []string
	for _, m := range yamlFence.FindAllStringSubmatch(string(raw), -1) {
		if strings.Contains(m[1], "kind:") {
			blocks = append(blocks, m[1])
		}
	}
	return blocks
}

func kindOf(manifest string) string {
	for _, l := range strings.Split(manifest, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "kind:") {
			return strings.TrimSpace(strings.TrimPrefix(l, "kind:"))
		}
	}
	return "?"
}

// TestDocManifestsAdmitted — contract: every apply-able manifest in the PR docs is
// admitted by the real CRDs.
func TestDocManifestsAdmitted(t *testing.T) {
	if err := ensureDefaultNamespace(t); err != nil {
		t.Fatalf("default namespace: %v", err)
	}
	// test CWD is docs/verification/autoscaling-docs/harness/
	guide := filepath.Join("..", "..", "..", "..", "doc", "best-practice", "en", "08-configuring-autoscaling-guide.md")
	concept := filepath.Join("..", "..", "..", "..", "doc", "best-practice", "en", "08-configuring-autoscaling.md")

	total := 0
	for i, b := range extractHeredocs(t, guide) {
		name := fmt.Sprintf("guide-heredoc-%02d(%s)", i+1, kindOf(b))
		if err := apply(t, name, b); err != nil {
			t.Errorf("%s: REJECTED by real CRDs: %v", name, err)
		} else {
			t.Logf("%s: admitted", name)
		}
		total++
	}
	for i, b := range extractYamlBlocks(t, concept) {
		name := fmt.Sprintf("concept-yaml-%02d(%s)", i+1, kindOf(b))
		if err := apply(t, name, b); err != nil {
			t.Errorf("%s: REJECTED by real CRDs: %v", name, err)
		} else {
			t.Logf("%s: admitted", name)
		}
		total++
	}
	if total == 0 {
		t.Fatal("no manifests extracted from the docs — extraction is broken")
	}
	t.Logf("applied %d doc manifests against real CRDs", total)
}

func ensureDefaultNamespace(t *testing.T) error {
	if _, err := kubectl(t, "get", "ns", "default"); err == nil {
		return nil
	}
	_, err := kubectl(t, "create", "ns", "default")
	return err
}

// TestRBGSAScaleSubresource — contract: the doc's Operation 1 Step 3 command
// (`kubectl scale rbgsa <name> --replicas=N`) works against the real CRD scale
// subresource and round-trips spec.replicas.
func TestRBGSAScaleSubresource(t *testing.T) {
	if err := ensureDefaultNamespace(t); err != nil {
		t.Fatalf("default namespace: %v", err)
	}
	rbgsa := `apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroupScalingAdapter
metadata:
  name: scaling-demo-backend
spec:
  scaleTargetRef:
    name: scaling-demo
    role: backend
`
	if err := apply(t, "rbgsa-manual", rbgsa); err != nil {
		t.Fatalf("create RBGSA: %v", err)
	}
	if _, err := kubectl(t, "scale", "rbgsa", "scaling-demo-backend", "--replicas=4"); err != nil {
		t.Fatalf("kubectl scale rbgsa (doc Operation 1 Step 3) failed: %v", err)
	}
	out, err := kubectl(t, "get", "rbgsa", "scaling-demo-backend", "-o", "jsonpath={.spec.replicas}")
	if err != nil {
		t.Fatalf("get rbgsa: %v", err)
	}
	if out != "4" {
		t.Fatalf("spec.replicas after scale: got %q, want 4", out)
	}
	// the raw /scale endpoint HPA and KEDA drive
	if _, err := kubectl(t, "get", "--raw",
		"/apis/workloads.x-k8s.io/v1alpha2/namespaces/default/rolebasedgroupscalingadapters/scaling-demo-backend/scale"); err != nil {
		t.Fatalf("raw /scale subresource GET failed: %v", err)
	}
	t.Logf("kubectl scale rbgsa --replicas=4 -> spec.replicas=4, raw /scale reachable (doc Op1 Step3 mechanics OK)")
}

// TestHarnessBitesUnknownFields — negative control: unknown fields must be
// REJECTED, proving schema validation is enforced (otherwise a green doc-manifest
// run above would prove nothing).
func TestHarnessBitesUnknownFields(t *testing.T) {
	if err := ensureDefaultNamespace(t); err != nil {
		t.Fatalf("default namespace: %v", err)
	}
	// NOTE: unknown fields *inside* spec.roles[] are NOT rejected — the CRD marks
	// `roles` with x-kubernetes-preserve-unknown-fields (deliberate, see
	// +kubebuilder:pruning:PreserveUnknownFields on Roles in
	// api/workloads/v1alpha2/rolebasedgroup_types.go). So the negative control
	// targets spec top level, which has no preserve-unknown.
	badRBG := `apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: bites-rbg
spec:
  bogusTopLevel: true
  roles:
    - name: backend
      standalonePattern:
        template:
          spec:
            containers:
              - name: engine
                image: lmsysorg/sglang:v0.5.9
                command: ["sleep", "3600"]
`
	if err := apply(t, "bites-rbg", badRBG); err == nil {
		t.Fatal("RBG with unknown top-level spec field was ADMITTED — strict validation not enforced; harness does not bite")
	} else {
		t.Logf("RBG unknown top-level spec field rejected (expected): %v", firstLine(err.Error()))
	}
	badAutoScaler := `apiVersion: inference-extension.rolebasedgroup.io/v1alpha1
kind: AutoScaler
metadata:
  name: bites-as
spec:
  scalingInterval: 180
  pattern:
    PDDisaggregated:
      prefill:
        roleName: prefill
        minReplicas: 1
        maxReplicas: 10
      decode:
        roleName: decode
        minReplicas: 1
        maxReplicas: 10
  implementation:
    DynamoPlanner:
      modelName: "Qwen/Qwen3-0.6B"
      bogusKnob: 42
`
	if err := apply(t, "bites-as", badAutoScaler); err == nil {
		t.Fatal("AutoScaler with unknown field was ADMITTED — planner CRD validation not enforced; harness does not bite")
	} else {
		t.Logf("AutoScaler unknown field rejected (expected): %v", firstLine(err.Error()))
	}
}

// TestCanaryMetricSourceDynamoRejected — canary (finding B8): the concept doc's
// inline comment advertises `metricSource: sglang | vllm | dynamo`. A user who
// follows that comment and sets metricSource: dynamo gets their AutoScaler
// REJECTED by the real CRD (enum is sglang|vllm|patio). PASS now = defect
// confirmed; flips to FAIL (manifest admitted) only if the planner CRD adds
// dynamo to the enum — then invert or drop this test.
func TestCanaryMetricSourceDynamoRejected(t *testing.T) {
	if err := ensureDefaultNamespace(t); err != nil {
		t.Fatalf("default namespace: %v", err)
	}
	docStyle := `apiVersion: inference-extension.rolebasedgroup.io/v1alpha1
kind: AutoScaler
metadata:
  name: canary-dynamo
spec:
  scalingInterval: 180
  pattern:
    PDDisaggregated:
      prefill:
        roleName: prefill
        minReplicas: 1
        maxReplicas: 10
      decode:
        roleName: decode
        minReplicas: 1
        maxReplicas: 10
  implementation:
    DynamoPlanner:
      modelName: "Qwen/Qwen3-0.6B"
      metricsEndpoint:
        metricSource: dynamo
        port: 8000
`
	err := apply(t, "canary-dynamo", docStyle)
	if err == nil {
		t.Errorf("canary B8 FLIPPED: metricSource=dynamo was admitted by the planner CRD (enum changed?) — invert/remove this canary")
	} else {
		t.Logf("canary B8 confirmed: metricSource=dynamo rejected by real CRD: %v", firstLine(err.Error()))
	}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i > 0 {
		s = s[:i]
	}
	return s
}
