/*
Verification harness for the review of PR #491 (KEP-455: rbgset rolling update).
Reviewer B (Codex). Additive only — no production code is touched.

The PR under review is docs-only (keps/455-rbgs-rolling-update/), so the claims
under test are the KEP's factual statements about the current codebase plus the
documentation gaps raised as findings F1/F2.

Polarity:
  - TestVerify_C*  : contract tests asserting the KEP's premises about the code.
    They PASS on the PR head (719851a) and flip red if the code
    drifts from what the KEP claims (=> KEP needs updating).
  - TestVerify_F1/F2: contract tests for the review findings. They FAIL on the
    PR head (the KEP text lacks the required statements) and
    must PASS once the KEP is amended. A red result here IS the
    reproduction of the finding.
*/
package pr491kep

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot resolves the repository root from this test file's location
// (test/verification/pr491kep -> ../../..).
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("cannot locate repo root from %s: %v", wd, err)
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

// extractFuncBody returns the source of the first func whose declaration contains name.
func extractFuncBody(t *testing.T, src, name string) string {
	t.Helper()
	idx := strings.Index(src, "func "+name)
	if idx < 0 {
		// method: "func (r *T) name"
		re := regexp.MustCompile(`func \([^)]*\) ` + name + `\(`)
		loc := re.FindStringIndex(src)
		if loc == nil {
			t.Fatalf("function %s not found", name)
		}
		idx = loc[0]
	}
	rest := src[idx:]
	brace := strings.Index(rest, "{")
	if brace < 0 {
		t.Fatalf("function %s: no body", name)
	}
	depth := 0
	for i := brace; i < len(rest); i++ {
		switch rest[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[:i+1]
			}
		}
	}
	t.Fatalf("function %s: unbalanced braces", name)
	return ""
}

// C1: KEP "Child Template Propagation" claims children currently receive only
// spec.roles (roleTemplates are NOT propagated). Verify against the set controller.
func TestVerify_C1_ChildrenReceiveOnlyRolesToday(t *testing.T) {
	src := readFile(t, "internal/controller/workloads/rolebasedgroupset_controller.go")

	create := extractFuncBody(t, src, "newRBGForSet")
	if !strings.Contains(create, "Roles: normalizedGroupTemplateRoles(rbgset)") {
		t.Errorf("newRBGForSet no longer builds children from normalizedGroupTemplateRoles")
	}
	if strings.Contains(create, "RoleTemplates") {
		t.Errorf("newRBGForSet now propagates RoleTemplates; KEP claim C1 is stale")
	}

	update := extractFuncBody(t, src, "updateExistingRBGs")
	if !strings.Contains(update, "latestRBG.Spec.Roles = normalizedGroupTemplateRoles(rbgset)") {
		t.Errorf("updateExistingRBGs no longer updates only Spec.Roles")
	}
	if strings.Contains(update, "RoleTemplates") {
		t.Errorf("updateExistingRBGs now propagates RoleTemplates; KEP claim C1 is stale")
	}
}

// C2: KEP "Blast radius is bounded" claims RoleBasedGroupSpec holds only
// `roles` and `roleTemplates`. Verify the v1alpha2 struct has exactly those two
// JSON fields.
func TestVerify_C2_RoleBasedGroupSpecHasOnlyTwoFields(t *testing.T) {
	src := readFile(t, "api/workloads/v1alpha2/rolebasedgroup_types.go")
	idx := strings.Index(src, "type RoleBasedGroupSpec struct {")
	if idx < 0 {
		t.Fatalf("RoleBasedGroupSpec not found")
	}
	rest := src[idx:]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		t.Fatalf("RoleBasedGroupSpec: no closing brace")
	}
	body := rest[:end]
	fields := regexp.MustCompile("`json:\"([a-zA-Z]+)").FindAllStringSubmatch(body, -1)
	got := map[string]bool{}
	for _, f := range fields {
		got[f[1]] = true
	}
	if len(got) != 2 || !got["roles"] || !got["roleTemplates"] {
		t.Errorf("RoleBasedGroupSpec fields = %v, want exactly {roles, roleTemplates}; KEP blast-radius claim is stale", got)
	}
}

// C3: finding F1's code fact — the RBG condition `RollingUpdateInProgress` is
// declared in the API but has NO producer in any controller/package today.
// The KEP's InPlaceUpdate availability gating relies on it as a barrier.
func TestVerify_C3_RollingUpdateInProgressNeverPublished(t *testing.T) {
	root := repoRoot(t)
	producers := []string{}
	for _, dir := range []string{"internal", "pkg", "cmd"} {
		_ = filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			if strings.Contains(string(b), "RollingUpdateInProgress") {
				rel, _ := filepath.Rel(root, path)
				producers = append(producers, rel)
			}
			return nil
		})
	}
	if len(producers) > 0 {
		t.Errorf("RollingUpdateInProgress now has producers %v; re-evaluate F1 (the KEP may no longer need to state this is new RBG-controller work)", producers)
	}
	// sanity: the condition type is at least declared in the API
	decl := readFile(t, "api/workloads/v1alpha2/rolebasedgroup_types.go")
	if !strings.Contains(decl, `RoleBasedGroupRollingUpdateInProgress RoleBasedGroupConditionType = "RollingUpdateInProgress"`) {
		t.Errorf("RollingUpdateInProgress condition declaration missing from v1alpha2 API")
	}
}

// C4: KEP conversion claim — "The rendered CRD manifests configure this webhook".
func TestVerify_C4_ConversionWebhookConfiguredForRBGSet(t *testing.T) {
	src := readFile(t, "config/crd/patches/webhook_in_rolebasedgroupsets.yaml")
	if !strings.Contains(src, "conversion:") || !strings.Contains(src, "strategy: Webhook") {
		t.Errorf("rolebasedgroupsets CRD conversion webhook patch missing; KEP conversion claim is stale")
	}
	// and the stash-annotation prefix the KEP references exists today
	conv := readFile(t, "api/workloads/v1alpha1/rolebasedgroup_conversion.go")
	if !strings.Contains(conv, "conversion.workloads.x-k8s.io/") {
		t.Errorf("conversion.workloads.x-k8s.io/ annotation prefix not found; KEP conversion claim is stale")
	}
}

// C5: KEP scaling-adapter conflict claim — the RBG validating webhook rejects
// replica changes with "cannot be changed to <N> while scalingAdapter.enable is true".
func TestVerify_C5_ScalingAdapterRejectionMessageMatchesKEP(t *testing.T) {
	src := readFile(t, "api/workloads/v1alpha2/rolebasedgroup_validation.go")
	if !strings.Contains(src, "while scalingAdapter.enable is true") {
		t.Errorf("scalingAdapter rejection message not found; KEP conflict description is stale")
	}
}

// C6: KEP label naming — proposed `rbg.workloads.x-k8s.io/groupset-revision`
// must match the v1alpha2 label prefix convention.
func TestVerify_C6_V1Alpha2LabelPrefixMatchesKEP(t *testing.T) {
	src := readFile(t, "api/workloads/constants/constants.go")
	if !strings.Contains(src, `RBGPrefix      = "rbg.workloads.x-k8s.io/"`) {
		t.Errorf("v1alpha2 RBGPrefix changed; KEP label names (rbg.workloads.x-k8s.io/groupset-*) are stale")
	}
	lbl := readFile(t, "api/workloads/constants/label.go")
	if !strings.Contains(lbl, "GroupSetNameLabelKey") || !strings.Contains(lbl, "GroupSetIndexLabelKey") {
		t.Errorf("groupset label keys moved; KEP label claims are stale")
	}
}

// C7: kep.yaml latest-milestone matches the tree VERSION.
func TestVerify_C7_KEPMilestoneMatchesTreeVersion(t *testing.T) {
	mk := readFile(t, "Makefile")
	m := regexp.MustCompile(`(?m)^VERSION \?= (v[0-9.]+)$`).FindStringSubmatch(mk)
	if m == nil {
		t.Fatalf("Makefile VERSION not found")
	}
	kep := readFile(t, "keps/455-rbgs-rolling-update/kep.yaml")
	if !strings.Contains(kep, `latest-milestone: "`+m[1]+`"`) {
		t.Errorf("kep.yaml latest-milestone does not match Makefile VERSION %s", m[1])
	}
}

// C8: the feature does not exist yet — no rolloutStrategy on the set spec in
// either served version (so the KEP is a proposal, not documentation of
// shipped behavior).
func TestVerify_C8_NoSetRolloutStrategyYet(t *testing.T) {
	v2 := readFile(t, "api/workloads/v1alpha2/rolebasedgroupset_types.go")
	if strings.Contains(v2, "rolloutStrategy") || strings.Contains(v2, "RolloutStrategy") {
		t.Errorf("v1alpha2 RoleBasedGroupSet already has a rolloutStrategy; re-check KEP against the implementation")
	}
	v1 := readFile(t, "api/workloads/v1alpha1/rolebasedgroupset_types.go")
	if strings.Contains(v1, "rolloutStrategy") || strings.Contains(v1, "RolloutStrategy") {
		t.Errorf("v1alpha1 RoleBasedGroupSet already has a rolloutStrategy")
	}
}

func kepReadme(t *testing.T) string {
	t.Helper()
	return readFile(t, "keps/455-rbgs-rolling-update/README.md")
}

// F1 [finding, round-1]: the KEP gated InPlaceUpdate availability on the RBG
// condition RollingUpdateInProgress (C3: no controller publishes it today)
// without designing it. ROUND-2 CONTRACT UPDATE (harness-update, recorded in
// README): the round-2 head (b03a7e6) added a "Child Rollout Barrier" design
// section (producer on the RBG controller, ordering invariant, consumer gate),
// which satisfies the round-1 ask's first option. The contract is updated
// accordingly: the KEP must carry the barrier design section. FAILS on the
// round-1 head (no design), PASSES on the round-2 head. The residual defect in
// the new consumer rule is tracked separately as R2-N1 in kep_round2_test.go.
// Polarity: contract.
func TestVerify_F1_KEPStatesRollingUpdateInProgressIsNewWork(t *testing.T) {
	readme := kepReadme(t)
	if !strings.Contains(readme, "RollingUpdateInProgress") {
		t.Fatalf("KEP no longer references RollingUpdateInProgress at all; harness-update needed")
	}
	i := strings.Index(readme, "### Child Rollout Barrier")
	if i < 0 {
		t.Errorf("F1 reproduced: KEP relies on RBG RollingUpdateInProgress as the InPlaceUpdate " +
			"availability barrier but carries no design for it (no Child Rollout Barrier section)")
		return
	}
	j := strings.Index(readme[i:], "### Status")
	if j < 0 {
		t.Fatalf("KEP structure changed: no Status section after Child Rollout Barrier")
	}
	sec := readme[i : i+j]
	for _, want := range []string{"**Producer.**", "**Consumer.**", "ObservedGeneration", "groupSetChildReady"} {
		if !strings.Contains(sec, want) {
			t.Errorf("F1 barrier design incomplete: Child Rollout Barrier section lacks %q", want)
		}
	}
	if !regexp.MustCompile(`(?i)RBG controller checks every role`).MatchString(sec) {
		t.Errorf("F1 barrier design incomplete: producer (RBG controller status construction) not specified")
	}
}

// F2 [finding]: the KEP requires a `groupset-observed-replicas` checkpoint that
// "controller restarts must not lose", but never says where it is persisted
// (it is absent from the Status table). Fixed state: the KEP names a concrete
// storage location (status field or annotation) for the checkpoint.
// Polarity: contract. FAILS on the PR head = finding reproduced.
func TestVerify_F2_KEPStatesCheckpointPersistence(t *testing.T) {
	readme := kepReadme(t)
	if !strings.Contains(readme, "groupset-observed-replicas") {
		t.Fatalf("KEP no longer references groupset-observed-replicas; harness-update needed")
	}
	re := regexp.MustCompile(`(?i)groupset-observed-replicas[^\n]{0,300}(annotation|status|persisted (in|on)|stored (in|on))` +
		`|(?i)(annotation|status|persisted (in|on)|stored (in|on))[^\n]{0,300}groupset-observed-replicas`)
	if !re.MatchString(readme) {
		t.Errorf("F2 reproduced: KEP requires a restart-safe groupset-observed-replicas checkpoint but never specifies where it is persisted (status field vs annotation)")
	}
}
