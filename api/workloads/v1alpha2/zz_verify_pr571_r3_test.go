/*
Round-3 additions to the PR #571 verification harness (reviewer: codex).
Additive test file — no production code changed.

Contract tests pinning the round-3 fixes (head c87a2e1e, previous head
f3d4849a); each was RED on f3d4849a (bites evidence:
results/round3/bites-previous-head-f3d4849a.log) and must be GREEN on
c87a2e1e.

R3-F3 (contract): F-UPDATE-WEDGE fix — UPDATE exempts roles whose
engine-runtime surface is unchanged, so an object written during a
webhook-absent window no longer wedges on label-only updates, while newly
introduced violations and surface changes are still rejected. Covers the
RoleBasedGroupSet twin.

R3-F4 (contract): F-SCOPE-ERROR-ORDER fix — the D6 scope-ownership aggregate
reports in a deterministic, ascending order across runs.
*/

package v1alpha2

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func zzR3WedgedRBG() *RoleBasedGroup {
	return &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "ns"},
		Spec: RoleBasedGroupSpec{Roles: []RoleSpec{{
			Name:    "lg",
			Pattern: Pattern{LeaderWorkerPattern: &LeaderWorkerPattern{}},
			EngineRuntimes: []EngineRuntime{{
				ProfileName:      "vllm-runtime",
				TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}},
			}},
		}}},
	}
}

func TestVerifyPR571R3_UpdateWedgeExemptionRBG(t *testing.T) {
	v := &RoleBasedGroupValidator{Client: newFakeValidatorClient(t), EnableDeprecatedWorkloadTypes: true}

	t.Run("label-only update on a wedged object passes", func(t *testing.T) {
		newObj := zzR3WedgedRBG()
		newObj.Labels = map[string]string{"app": "x"}
		if _, err := v.ValidateUpdate(context.Background(), zzR3WedgedRBG(), newObj); err != nil {
			t.Fatalf("label-only update on a wedged object must pass, got: %v", err)
		}
	})

	t.Run("newly introduced violation is still rejected", func(t *testing.T) {
		newObj := zzR3WedgedRBG()
		newObj.Spec.Roles[0].EngineRuntimes = append(newObj.Spec.Roles[0].EngineRuntimes,
			EngineRuntime{ProfileName: "p2", TargetContainers: []EngineRuntimeTargetContainer{{Name: "c"}}})
		_, err := v.ValidateUpdate(context.Background(), zzR3WedgedRBG(), newObj)
		if err == nil || !strings.Contains(err.Error(), `"p2"`) {
			t.Fatalf("newly introduced violation must be rejected naming p2, got: %v", err)
		}
	})

	t.Run("surface change on the wedged role is re-validated", func(t *testing.T) {
		newObj := zzR3WedgedRBG()
		newObj.Spec.Roles[0].EngineRuntimes[0].ProfileName = "renamed"
		_, err := v.ValidateUpdate(context.Background(), zzR3WedgedRBG(), newObj)
		if err == nil || !strings.Contains(err.Error(), `"renamed"`) {
			t.Fatalf("surface change must re-validate and reject, got: %v", err)
		}
	})

	t.Run("clean role added alongside the wedged one passes", func(t *testing.T) {
		newObj := zzR3WedgedRBG()
		newObj.Spec.Roles = append(newObj.Spec.Roles, RoleSpec{
			Name:           "aux",
			Pattern:        Pattern{LeaderWorkerPattern: &LeaderWorkerPattern{}},
			EngineRuntimes: []EngineRuntime{{ProfileName: "other", Component: "worker"}},
		})
		if _, err := v.ValidateUpdate(context.Background(), zzR3WedgedRBG(), newObj); err != nil {
			t.Fatalf("adding a clean role alongside the wedged one must pass, got: %v", err)
		}
	})
}

func TestVerifyPR571R3_UpdateWedgeExemptionRBGS(t *testing.T) {
	wedgedRBGS := func() *RoleBasedGroupSet {
		return &RoleBasedGroupSet{
			ObjectMeta: metav1.ObjectMeta{Name: "rbgs", Namespace: "ns"},
			Spec: RoleBasedGroupSetSpec{GroupTemplate: RoleBasedGroupTemplateSpec{
				Spec: RoleBasedGroupSpec{Roles: []RoleSpec{{
					Name:    "lg",
					Pattern: Pattern{LeaderWorkerPattern: &LeaderWorkerPattern{}},
					EngineRuntimes: []EngineRuntime{{
						ProfileName:      "vllm-runtime",
						TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}},
					}},
				}}},
			}},
		}
	}
	v := &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: true}

	newObj := wedgedRBGS()
	newObj.Labels = map[string]string{"app": "x"}
	if _, err := v.ValidateUpdate(context.Background(), wedgedRBGS(), newObj); err != nil {
		t.Fatalf("label-only update on a wedged RBGS must pass, got: %v", err)
	}

	newObj = wedgedRBGS()
	newObj.Spec.GroupTemplate.Spec.Roles[0].EngineRuntimes = append(
		newObj.Spec.GroupTemplate.Spec.Roles[0].EngineRuntimes,
		EngineRuntime{ProfileName: "p2", TargetContainers: []EngineRuntimeTargetContainer{{Name: "c"}}})
	_, err := v.ValidateUpdate(context.Background(), wedgedRBGS(), newObj)
	if err == nil || !strings.Contains(err.Error(), `"p2"`) {
		t.Fatalf("newly introduced violation must be rejected, got: %v", err)
	}
}

func TestVerifyPR571R3_ScopeOwnershipErrorOrder(t *testing.T) {
	role := &RoleSpec{Name: "r", Pattern: Pattern{LeaderWorkerPattern: &LeaderWorkerPattern{}}}
	runtimes := make([]EngineRuntime, 0, 24)
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("zz-p-%d", i)
		// Duplicate (profile, component) pairs: a D6 overlap each.
		runtimes = append(runtimes,
			EngineRuntime{ProfileName: name, Component: "leader", TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}}},
			EngineRuntime{ProfileName: name, Component: "leader", TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}}},
			EngineRuntime{ProfileName: name, Component: "worker", TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}}},
			EngineRuntime{ProfileName: name, Component: "worker", TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}}},
		)
	}
	role.EngineRuntimes = runtimes

	var first []string
	for run := 0; run < 20; run++ {
		err := ValidateEngineRuntimes(role)
		if err == nil {
			t.Fatalf("run %d: expected scope-ownership errors", run)
		}
		agg, ok := err.(utilerrors.Aggregate)
		if !ok {
			t.Fatalf("run %d: expected an aggregate, got %T", run, err)
		}
		msgs := make([]string, len(agg.Errors()))
		for i, e := range agg.Errors() {
			msgs[i] = e.Error()
		}
		sorted := append([]string(nil), msgs...)
		sort.Strings(sorted)
		if strings.Join(sorted, "|") != strings.Join(msgs, "|") {
			t.Fatalf("run %d: scope-ownership errors must be reported in ascending order:\n%s", run, strings.Join(msgs, "\n"))
		}
		if first == nil {
			first = msgs
			continue
		}
		if strings.Join(first, "|") != strings.Join(msgs, "|") {
			t.Fatalf("run %d: error order must be stable across runs", run)
		}
	}
}
