/*
Verification harness for review findings on PR #415 (sgl-project/rbg).
DO NOT treat as product code: this file only asserts facts the review depends on.

Topic: pr415-en-inplace-doc      Finding: F1
See docs/verification/pr415-en-inplace-doc/README.md

F1 (major): the English doc invents a technical claim that the Chinese source does
not make. en/04-configuring-inplace-update-and-scheduling-policies.md:76 says:

	"The `InPlaceOnly` strategy is not recommended. When changes exceed image
	 scope, this strategy does not fall back to Pod recreation and the update
	 gets stuck."

zh:76 only says InPlaceOnly is Deprecated. The en causal claim is falsifiable, and
these tests falsify it: the stateful update path guards in-place update with
`set.Spec.UpdateStrategy.Type != RecreatePodUpdateStrategyType` (a single
exclusion, NOT an allowlist), so InPlaceOnly enters exactly the same branch as
InPlaceIfPossible, and when in-place is not possible (`!inplacing`) both fall
through to deleteInstance() -- i.e. both recreate the Pod. Nothing gets stuck.
*/
package statefulmode

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// runApplyTargetUpdateWithStrategy drives applyTargetUpdate once for the given
// UpdateStrategy.Type against a fakeInplaceControl whose CanUpdateInPlace()
// returns false -- i.e. "the change exceeds image scope", precisely the scenario
// the en doc claims makes InPlaceOnly get stuck.
func runApplyTargetUpdateWithStrategy(
	t *testing.T,
	strategy workloadsv1alpha2.UpdateStrategyType,
) (transitioned bool, inplace *fakeInplaceControl, deleted []string) {
	t.Helper()
	set := buildSet("s", 1, nil, nil)
	set.Spec.UpdateStrategy.Type = strategy
	target := buildInst("s", 0, testOldRev, true, true)
	objectManager := &fakeInstanceObjectManager{}
	inplaceControl := &fakeInplaceControl{}
	control := &defaultStatefulInstanceSetControl{
		instanceControl: NewStatefulInstanceControlFromManager(objectManager, record.NewFakeRecorder(10)),
		inplaceControl:  inplaceControl,
	}

	got, err := control.applyTargetUpdate(
		set,
		target,
		&apps.ControllerRevision{ObjectMeta: metav1.ObjectMeta{Name: testOldRev}},
		&apps.ControllerRevision{ObjectMeta: metav1.ObjectMeta{Name: testUpdateRev}},
		[]*apps.ControllerRevision{{ObjectMeta: metav1.ObjectMeta{Name: testOldRev}}},
		false,
		false,
	)
	if err != nil {
		t.Fatalf("applyTargetUpdate(%s) error = %v", strategy, err)
	}
	return got, inplaceControl, objectManager.deleted
}

// TestVerifyPR415_F1_InPlaceOnlyFallsBackToRecreate is a CONTRACT test.
//
// POLARITY: contract
//
// It asserts the fact that makes the en-only sentence wrong: with InPlaceOnly,
// when in-place update is not possible, applyTargetUpdate still deletes the
// instance (Pod recreation). The update does NOT get stuck.
//
// Expected on the reviewed code (285761e9): PASS.
// If someone ever really implements "InPlaceOnly never recreates", this goes RED
// and the doc sentence becomes correct -- at which point the doc and this test
// must be revisited together.
func TestVerifyPR415_F1_InPlaceOnlyFallsBackToRecreate(t *testing.T) {
	transitioned, inplace, deleted := runApplyTargetUpdateWithStrategy(
		t, workloadsv1alpha2.InPlaceOnlyUpdateStrategyType)

	if inplace.canUpdateCalls == 0 {
		t.Errorf("InPlaceOnly: expected the in-place branch to be entered "+
			"(CanUpdateInPlace consulted), got canUpdateCalls=%d", inplace.canUpdateCalls)
	}
	if len(deleted) != 1 || deleted[0] != "s-0" {
		t.Errorf("InPlaceOnly: en doc:76 claims the update gets stuck with no Pod "+
			"recreation, but applyTargetUpdate deleted %v (want [s-0]) -- it DOES "+
			"fall back to recreation", deleted)
	}
	if !transitioned {
		t.Errorf("InPlaceOnly: expected transitioned=true via recreate fallback, got false")
	}
}

// TestVerifyPR415_F1_InPlaceOnlyAndIfPossibleAreIndistinguishable is a CONTRACT test.
//
// POLARITY: contract
//
// The core claim: InPlaceOnly and InPlaceIfPossible produce identical observable
// behavior in the stateful update path, because the guard excludes only
// RecreatePod. So the en doc cannot legitimately warn about one and recommend the
// other on behavioral grounds.
//
// Expected on the reviewed code (285761e9): PASS.
func TestVerifyPR415_F1_InPlaceOnlyAndIfPossibleAreIndistinguishable(t *testing.T) {
	onlyTransitioned, onlyInplace, onlyDeleted := runApplyTargetUpdateWithStrategy(
		t, workloadsv1alpha2.InPlaceOnlyUpdateStrategyType)
	ifPossTransitioned, ifPossInplace, ifPossDeleted := runApplyTargetUpdateWithStrategy(
		t, workloadsv1alpha2.InPlaceIfPossibleUpdateStrategyType)

	if onlyInplace.canUpdateCalls != ifPossInplace.canUpdateCalls {
		t.Errorf("canUpdateCalls differ: InPlaceOnly=%d InPlaceIfPossible=%d",
			onlyInplace.canUpdateCalls, ifPossInplace.canUpdateCalls)
	}
	if onlyInplace.updateCalls != ifPossInplace.updateCalls {
		t.Errorf("updateCalls differ: InPlaceOnly=%d InPlaceIfPossible=%d",
			onlyInplace.updateCalls, ifPossInplace.updateCalls)
	}
	if strings.Join(onlyDeleted, ",") != strings.Join(ifPossDeleted, ",") {
		t.Errorf("deletion behavior differs: InPlaceOnly=%v InPlaceIfPossible=%v "+
			"(en doc:76 implies InPlaceOnly does NOT recreate)", onlyDeleted, ifPossDeleted)
	}
	if onlyTransitioned != ifPossTransitioned {
		t.Errorf("transitioned differs: InPlaceOnly=%v InPlaceIfPossible=%v",
			onlyTransitioned, ifPossTransitioned)
	}
}

// TestVerifyPR415_F1_RecreatePodIsTheOnlyExcludedStrategy is a CONTRACT test.
//
// POLARITY: contract
//
// Guards the *mechanism* rather than one sample: an arbitrary unknown strategy
// string takes the same in-place-then-recreate path, proving the guard is a
// single "!= RecreatePod" exclusion and not an allowlist that could ever single
// out InPlaceOnly. (This overlaps F3: unknown strings are silently accepted.)
//
// Expected on the reviewed code (285761e9): PASS.
func TestVerifyPR415_F1_RecreatePodIsTheOnlyExcludedStrategy(t *testing.T) {
	for _, strategy := range []workloadsv1alpha2.UpdateStrategyType{
		workloadsv1alpha2.InPlaceIfPossibleUpdateStrategyType,
		workloadsv1alpha2.InPlaceOnlyUpdateStrategyType,
		"",
		"TotallyBogusValue",
	} {
		_, inplace, deleted := runApplyTargetUpdateWithStrategy(t, strategy)
		if inplace.canUpdateCalls == 0 {
			t.Errorf("strategy %q: expected in-place branch entered, canUpdateCalls=0", strategy)
		}
		if len(deleted) != 1 {
			t.Errorf("strategy %q: expected recreate fallback (1 deletion), got %v", strategy, deleted)
		}
	}

	// And the sole exclusion really is RecreatePod.
	_, inplace, deleted := runApplyTargetUpdateWithStrategy(
		t, workloadsv1alpha2.RecreatePodUpdateStrategyType)
	if inplace.canUpdateCalls != 0 {
		t.Errorf("RecreatePod: expected in-place branch skipped, canUpdateCalls=%d", inplace.canUpdateCalls)
	}
	if len(deleted) != 1 {
		t.Errorf("RecreatePod: expected 1 deletion, got %v", deleted)
	}
}

// verifyPR415RepoRoot walks up from the test's cwd to the module root (go.mod).
func verifyPR415RepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate module root (go.mod) from cwd")
	return ""
}

// TestVerifyPR415_F1_InPlaceOnlyHasNoImplementationReferences is a BUG-CANARY.
//
// POLARITY: canary
//
// Documents the current state: the InPlaceOnly constant is referenced NOWHERE in
// internal/ or pkg/ except its own declaration in api/ -- there is no code that
// treats it specially, which is *why* the en doc:76 claim is unfounded.
//
// Expected on the reviewed code (285761e9): PASS (zero references).
// AFTER A FIX that genuinely implements a distinct InPlaceOnly behavior, this
// canary FLIPS TO RED. That is the intended signal: the doc sentence would then
// need to be rewritten, and this canary must be inverted (or promoted to a
// contract test asserting the new behavior). Do not silence it by loosening it.
func TestVerifyPR415_F1_InPlaceOnlyHasNoImplementationReferences(t *testing.T) {
	root := verifyPR415RepoRoot(t)
	const selfName = "zz_verify_pr415_inplaceonly_test.go"

	var refs []string
	for _, top := range []string{"pkg", "internal"} {
		base := filepath.Join(root, top)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			if filepath.Base(path) == selfName {
				return nil // this harness file itself
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil // unparseable file: not our concern
			}
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name == "InPlaceOnlyUpdateStrategyType" {
					rel, _ := filepath.Rel(root, path)
					pos := fset.Position(sel.Pos())
					refs = append(refs, rel+":"+strconv.Itoa(pos.Line))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}

	if len(refs) != 0 {
		t.Errorf("CANARY FLIPPED: InPlaceOnlyUpdateStrategyType is now referenced "+
			"in implementation code (%d site(s)): %v\n"+
			"The review of PR #415 rested on this being zero -- InPlaceOnly having no "+
			"distinct code path is why en doc:76's \"update gets stuck\" claim was wrong.\n"+
			"Re-read doc/best-practice/{en,zh}/04-configuring-inplace-update-and-"+
			"scheduling-policies.md:76 and update it to match the new behavior.",
			len(refs), refs)
	}
}
