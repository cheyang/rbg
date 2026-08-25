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

// Reviewer verification harness for sgl-project/rbg#434 (Implement kep-430).
//
// These tests are ADDITIVE and touch no production code. Each test states its
// polarity explicitly:
//
//   CONTRACT — asserts the intended correct behavior. RED on the PR head,
//              GREEN once fixed.
//   CANARY   — asserts the current (suspected-wrong) behavior. GREEN on the PR
//              head, flips RED once fixed; invert it then.
//
// Run: go test ./pkg/scheduler/volcano/ -run 'TestVerify' -v
package volcano

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreapplyv1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// newPodTemplateApplyConfig returns a fresh, empty pod template apply
// configuration for the injection tests.
func newPodTemplateApplyConfig() *coreapplyv1.PodTemplateSpecApplyConfiguration {
	return &coreapplyv1.PodTemplateSpecApplyConfiguration{}
}

// buildRBG constructs a minimal RBG with the given roles.
func buildRBG(name string, roles ...workloadsv1alpha2.RoleSpec) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: roles,
		},
	}
}

// standaloneRole builds a StandalonePattern role, optionally pinned to a
// non-default workload type via the role-workload-type annotation.
func standaloneRole(name string, replicas int32, workloadType string) workloadsv1alpha2.RoleSpec {
	r := workloadsv1alpha2.RoleSpec{
		Name:     name,
		Replicas: ptr.To(replicas),
		Pattern: workloadsv1alpha2.Pattern{
			StandalonePattern: &workloadsv1alpha2.StandalonePattern{
				TemplateSource: workloadsv1alpha2.TemplateSource{
					Template: &corev1.PodTemplateSpec{},
				},
			},
		},
	}
	if workloadType != "" {
		r.Annotations = map[string]string{
			constants.RoleWorkloadTypeAnnotationKey: workloadType,
		}
	}
	return r
}

// ---------------------------------------------------------------------------
// F1 (major) — subGroup partitioning relies on a label that only the
// RoleInstanceSet workload path writes.
//
// buildSubGroupPolicy unconditionally sets
//   MatchLabelKeys: [rbg.workloads.x-k8s.io/role-instance-name]
// The code's own comment argues this key is safe because it "is written by the
// shared RoleInstance-to-Pod label path (so it exists in both stateful and
// stateless modes)" and warns that without it "every pod of the role collapses
// into a single subGroup, which contradicts subGroupSize and leaves the pods
// permanently unschedulable".
//
// But that label is written only in pkg/reconciler/roleinstance/utils
// (the RoleInstanceSet path). A role pinned to apps/v1/StatefulSet,
// apps/v1/Deployment or LeaderWorkerSet produces pods without it, while
// buildSubGroupPolicy still emits the same MatchLabelKeys.
//
// POLARITY: CANARY. It asserts today's behavior (the key is emitted regardless
// of workload type). When the PR gates MatchLabelKeys on the workload type — or
// rejects non-RoleInstanceSet roles from per-role gang — this flips to RED.
// ---------------------------------------------------------------------------
func TestVerifyF1_MatchLabelKeysEmittedForWorkloadTypesThatNeverSetTheLabel(t *testing.T) {
	for _, workloadType := range []string{
		constants.StatefulSetWorkloadType,
		constants.DeploymentWorkloadType,
		constants.LeaderWorkerSetWorkloadType,
	} {
		t.Run(workloadType, func(t *testing.T) {
			rbg := buildRBG("rbg-a", standaloneRole("prefill", 3, workloadType))
			strategy := &workloadsv1alpha2.GangSchedulingStrategy{
				MinReplicas: map[string]int32{"prefill": 2},
			}

			policies := buildSubGroupPolicy(rbg, strategy)
			if len(policies) != 1 {
				t.Fatalf("expected 1 subGroupPolicy entry, got %d", len(policies))
			}

			got := policies[0].MatchLabelKeys
			if len(got) != 1 || got[0] != constants.RoleInstanceNameLabelKey {
				t.Fatalf("MatchLabelKeys = %v, want [%s]", got, constants.RoleInstanceNameLabelKey)
			}

			// The label is a RoleInstanceSet-only artifact. Record that the role
			// under test is NOT that workload type, so the partitioning key can
			// never match any of its pods.
			if rbg.Spec.Roles[0].GetWorkloadType() == constants.RoleInstanceSetWorkloadType {
				t.Fatalf("test setup error: role should not be RoleInstanceSet")
			}

			t.Logf("CANARY GREEN: role %q (workloadType=%s) gets MatchLabelKeys=%v, "+
				"but pods of this workload type never carry %s",
				rbg.Spec.Roles[0].Name, workloadType, got, constants.RoleInstanceNameLabelKey)
		})
	}
}

// ---------------------------------------------------------------------------
// F2 (major) — a role name typo in minReplicas silently disables gang
// scheduling instead of being rejected.
//
// calculateGangMinimum only sums roles that exist in BOTH rbg.Spec.Roles and
// minReplicas. buildSubGroupPolicy likewise skips unknown keys. So a
// CoordinatedPolicy carrying minReplicas={"prefil": 2} (typo) yields
// minMember=0 and an EMPTY subGroupPolicy — a PodGroup that gang-protects
// nothing. KEP-430 specified a webhook rejecting invalid keys
// ("Webhook Validation: Self-contained validations (key validity, value > 0,
// scheduler capability)"), but this PR ships no such validation, so nothing
// upstream catches the typo either.
//
// POLARITY: CANARY. Asserts today's silent-zero behavior. Flips RED when
// unknown keys are rejected (webhook) or surfaced as a reconcile error.
// ---------------------------------------------------------------------------
func TestVerifyF2_UnknownRoleNameInMinReplicasSilentlyYieldsZeroMinMember(t *testing.T) {
	rbg := buildRBG("rbg-b",
		standaloneRole("prefill", 3, ""),
		standaloneRole("decode", 3, ""),
	)
	// "prefil" / "decodee" are typos: neither matches a real role name.
	strategy := &workloadsv1alpha2.GangSchedulingStrategy{
		MinReplicas: map[string]int32{"prefil": 2, "decodee": 1},
	}

	minMember := calculateGangMinimum(rbg, strategy)
	policies := buildSubGroupPolicy(rbg, strategy)

	if minMember != 0 {
		t.Fatalf("calculateGangMinimum() = %d, expected 0 to demonstrate the silent-disable path", minMember)
	}
	if len(policies) != 0 {
		t.Fatalf("buildSubGroupPolicy() returned %d entries, expected 0", len(policies))
	}

	t.Logf("CANARY GREEN: minReplicas with only unknown role names produced "+
		"minMember=%d and subGroupPolicy=%d entries — the PodGroup would be "+
		"created gang-protecting nothing, with no error and no webhook rejection",
		minMember, len(policies))
}

// ---------------------------------------------------------------------------
// F2b (major, contract framing) — the same defect stated as intended behavior.
//
// Whatever the chosen remedy (reject at admission, or error out in the
// scheduler), an all-unknown minReplicas map must not produce a "successful"
// gang with minMember=0. This test asserts the intended contract directly, so
// it is RED on the PR head and GREEN once any real remedy lands.
//
// POLARITY: CONTRACT.
// ---------------------------------------------------------------------------
func TestVerifyF2b_MinMemberMustNeverCollapseToZeroWhenGangRequested(t *testing.T) {
	rbg := buildRBG("rbg-b2",
		standaloneRole("prefill", 3, ""),
		standaloneRole("decode", 3, ""),
	)
	strategy := &workloadsv1alpha2.GangSchedulingStrategy{
		MinReplicas: map[string]int32{"prefil": 2},
	}

	minMember := calculateGangMinimum(rbg, strategy)

	// Intended: a non-empty minReplicas that requests a gang must never
	// degrade into minMember=0 (gang disabled) without an error.
	if minMember == 0 {
		t.Errorf("CONTRACT RED: gang requested via minReplicas=%v but "+
			"calculateGangMinimum() = 0; a gang that protects zero pods should be "+
			"rejected (webhook per KEP-430) or surfaced as a reconcile error, "+
			"not silently accepted", strategy.MinReplicas)
	}
}

// ---------------------------------------------------------------------------
// F3 (moderate) — roles excluded from minReplicas are still joined to the
// PodGroup.
//
// The API doc for MinReplicas states: "Roles absent from this map are excluded
// from gang constraints and scheduled normally." But InjectPodSchedulingFields
// ignores its `role` argument entirely and stamps the PodGroup annotation onto
// every role's pod template. Those pods therefore become PodGroup members in
// Volcano while contributing nothing to minMember — which is not "scheduled
// normally".
//
// POLARITY: CANARY (documents that `role` is unused / annotation is
// unconditional). Flips RED when injection becomes role-aware.
// ---------------------------------------------------------------------------
func TestVerifyF3_ExcludedRoleStillGetsPodGroupAnnotation(t *testing.T) {
	rbg := buildRBG("rbg-c",
		standaloneRole("prefill", 2, ""),
		standaloneRole("sidecar-only", 2, ""), // deliberately absent from minReplicas
	)
	strategy := &workloadsv1alpha2.GangSchedulingStrategy{
		MinReplicas: map[string]int32{"prefill": 2},
	}

	// The excluded role contributes nothing to the gang minimum...
	minMember := calculateGangMinimum(rbg, strategy)
	if minMember != 2 {
		t.Fatalf("calculateGangMinimum() = %d, want 2 (only prefill counts)", minMember)
	}
	// ...and has no subGroupPolicy entry.
	policies := buildSubGroupPolicy(rbg, strategy)
	for _, p := range policies {
		if p.Name == "sidecar-only" {
			t.Fatalf("unexpected subGroupPolicy entry for excluded role")
		}
	}

	// ...yet its pod template is still annotated into the PodGroup.
	m := New(nil)
	pts := newPodTemplateApplyConfig()
	excluded := &rbg.Spec.Roles[1]
	m.InjectPodSchedulingFields(rbg, excluded, strategy, pts)

	if pts.Annotations[AnnotationKey] != rbg.Name {
		t.Fatalf("expected annotation %s=%s on excluded role, got %v",
			AnnotationKey, rbg.Name, pts.Annotations)
	}
	if pts.Spec == nil || pts.Spec.SchedulerName == nil || *pts.Spec.SchedulerName != SchedulerName {
		t.Fatalf("expected schedulerName=%s on excluded role", SchedulerName)
	}

	t.Logf("CANARY GREEN: role %q is excluded from minReplicas (minMember=%d "+
		"counts only prefill, no subGroup entry) yet its pods are annotated "+
		"%s=%s and pinned to schedulerName=%s — they join the gang they are "+
		"documented to be excluded from",
		excluded.Name, minMember, AnnotationKey, rbg.Name, SchedulerName)
}

// ---------------------------------------------------------------------------
// F3b — the `role` parameter added to InjectPodSchedulingFields is dead.
//
// Passing two different roles (one gang member, one excluded) produces byte
// identical injection, proving the new parameter changes nothing in the Volcano
// implementation.
//
// POLARITY: CANARY.
// ---------------------------------------------------------------------------
func TestVerifyF3b_RoleParameterDoesNotAffectInjection(t *testing.T) {
	rbg := buildRBG("rbg-d",
		standaloneRole("prefill", 2, ""),
		standaloneRole("decode", 2, ""),
	)
	strategy := &workloadsv1alpha2.GangSchedulingStrategy{
		MinReplicas: map[string]int32{"prefill": 2},
	}
	m := New(nil)

	included := newPodTemplateApplyConfig()
	m.InjectPodSchedulingFields(rbg, &rbg.Spec.Roles[0], strategy, included)

	excluded := newPodTemplateApplyConfig()
	m.InjectPodSchedulingFields(rbg, &rbg.Spec.Roles[1], strategy, excluded)

	if included.Annotations[AnnotationKey] != excluded.Annotations[AnnotationKey] {
		t.Fatalf("annotations differ between roles: %v vs %v",
			included.Annotations, excluded.Annotations)
	}
	if *included.Spec.SchedulerName != *excluded.Spec.SchedulerName {
		t.Fatalf("schedulerName differs between roles")
	}

	// Also: a nil role must not panic, since nothing reads it.
	nilRole := newPodTemplateApplyConfig()
	m.InjectPodSchedulingFields(rbg, nil, strategy, nilRole)
	if nilRole.Annotations[AnnotationKey] != rbg.Name {
		t.Fatalf("nil role produced different injection")
	}

	t.Logf("CANARY GREEN: InjectPodSchedulingFields ignores its `role` argument " +
		"entirely — included, excluded and nil roles all inject identically")
}

// ---------------------------------------------------------------------------
// F4 (moderate) — subGroupSize is derived from the role pattern, but
// minSubGroups is taken verbatim from minReplicas with no upper bound against
// role.Replicas.
//
// A CoordinatedPolicy may ask for minReplicas larger than the role's replicas
// (e.g. minReplicas=5 on a 2-replica role). calculateGangMinimum then produces
// a minMember the RBG can never reach, so the gang can never be satisfied and
// every pod stays Pending forever. KEP-430's webhook was meant to validate
// "value > 0"; nothing validates value <= replicas.
//
// POLARITY: CONTRACT — asserts the intended clamp/rejection. RED on PR head.
// ---------------------------------------------------------------------------
func TestVerifyF4_MinReplicasExceedingRoleReplicasIsUnsatisfiable(t *testing.T) {
	rbg := buildRBG("rbg-e", standaloneRole("prefill", 2, ""))
	strategy := &workloadsv1alpha2.GangSchedulingStrategy{
		MinReplicas: map[string]int32{"prefill": 5}, // role only has 2 replicas
	}

	minMember := calculateGangMinimum(rbg, strategy)
	groupSize := rbg.GetGroupSize()

	if minMember > groupSize {
		t.Errorf("CONTRACT RED: minMember=%d exceeds the total pods the RBG will "+
			"ever create (GetGroupSize()=%d) because minReplicas=5 > replicas=2; "+
			"the gang is unsatisfiable by construction and all pods stay Pending. "+
			"Expected admission rejection or a clamp to replicas.",
			minMember, groupSize)
	}
}

// ---------------------------------------------------------------------------
// F5 (minor) — zero and negative minReplicas are accepted.
//
// KEP-430 required the webhook to validate "value > 0". The CRD schema adds no
// Minimum, and no webhook exists. minReplicas=0 contributes nothing (a no-op
// subGroup); a negative value propagates into MinSubGroups and lowers minMember.
//
// POLARITY: CONTRACT — asserts values are validated. RED on PR head.
// ---------------------------------------------------------------------------
func TestVerifyF5_NonPositiveMinReplicasAccepted(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		rbg := buildRBG("rbg-f", standaloneRole("prefill", 2, ""))
		strategy := &workloadsv1alpha2.GangSchedulingStrategy{
			MinReplicas: map[string]int32{"prefill": 0},
		}
		policies := buildSubGroupPolicy(rbg, strategy)
		if len(policies) == 1 && policies[0].MinSubGroups != nil && *policies[0].MinSubGroups == 0 {
			t.Errorf("CONTRACT RED: minReplicas=0 accepted, emitting a subGroupPolicy "+
				"with minSubGroups=0 and minMember=%d; KEP-430 required value > 0 "+
				"validation", calculateGangMinimum(rbg, strategy))
		}
	})

	t.Run("negative", func(t *testing.T) {
		rbg := buildRBG("rbg-g", standaloneRole("prefill", 2, ""))
		strategy := &workloadsv1alpha2.GangSchedulingStrategy{
			MinReplicas: map[string]int32{"prefill": -3},
		}
		minMember := calculateGangMinimum(rbg, strategy)
		policies := buildSubGroupPolicy(rbg, strategy)
		if minMember < 0 || (len(policies) == 1 && policies[0].MinSubGroups != nil && *policies[0].MinSubGroups < 0) {
			t.Errorf("CONTRACT RED: negative minReplicas=-3 accepted, producing "+
				"minMember=%d and minSubGroups=%d; KEP-430 required value > 0 validation",
				minMember, *policies[0].MinSubGroups)
		}
	})
}

// ---------------------------------------------------------------------------
// Baseline sanity — ComputeSubGroupSize and the happy path behave as documented.
// This is the harness-bites control: if these fail, the harness is wrong, not
// the PR.
//
// POLARITY: CONTRACT (expected GREEN on the PR head).
// ---------------------------------------------------------------------------
func TestVerifyBaseline_HappyPathSubGroupPolicy(t *testing.T) {
	rbg := buildRBG("rbg-ok",
		standaloneRole("prefill", 3, ""),
		standaloneRole("decode", 3, ""),
	)
	strategy := &workloadsv1alpha2.GangSchedulingStrategy{
		MinReplicas: map[string]int32{"prefill": 2, "decode": 1},
	}

	if got := calculateGangMinimum(rbg, strategy); got != 3 {
		t.Errorf("calculateGangMinimum() = %d, want 3 (2*1 + 1*1)", got)
	}

	policies := buildSubGroupPolicy(rbg, strategy)
	if len(policies) != 2 {
		t.Fatalf("expected 2 subGroupPolicy entries, got %d", len(policies))
	}
	byName := map[string]int32{}
	for _, p := range policies {
		byName[p.Name] = *p.MinSubGroups
		if *p.SubGroupSize != 1 {
			t.Errorf("role %s: subGroupSize = %d, want 1 for standalone", p.Name, *p.SubGroupSize)
		}
	}
	if byName["prefill"] != 2 || byName["decode"] != 1 {
		t.Errorf("minSubGroups = %v, want prefill=2 decode=1", byName)
	}
}
