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
// Covers common.GetGangStrategy, the new shared resolution helper.
//
// ADDITIVE — no production code is modified. Polarity is stated per test.
//
// Run: go test ./pkg/scheduler/common/ -run 'TestVerify' -v
package common

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := workloadsv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add rbgs scheme: %v", err)
	}
	return s
}

func rbgNamed(name string, annotations map[string]string) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "default",
			Annotations: annotations,
		},
	}
}

func gangRule(name string, roles []string, minReplicas map[string]int32) workloadsv1alpha2.CoordinatedPolicyRule {
	return workloadsv1alpha2.CoordinatedPolicyRule{
		Name:  name,
		Roles: roles,
		Strategy: workloadsv1alpha2.CoordinatedPolicyStrategy{
			Scheduling: &workloadsv1alpha2.SchedulingCoordinationStrategy{
				Gang: &workloadsv1alpha2.GangSchedulingStrategy{MinReplicas: minReplicas},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// F6 (moderate) — GetGangStrategy discards CoordinatedPolicyRule.Roles.
//
// A CoordinatedPolicyRule is scoped to a role list. GetGangStrategy returns
// policy.Strategy.Scheduling.Gang without ever consulting policy.Roles, so a
// gang rule written for {prefill} is applied to the whole RBG, and minReplicas
// keys are never checked against the rule's own scope. A rule naming roles that
// do not even appear in Roles is honored.
//
// POLARITY: CANARY — asserts today's scope-blind behavior. Flips RED once the
// rule's Roles are respected or validated against minReplicas.
// ---------------------------------------------------------------------------
func TestVerifyF6_GangStrategyIgnoresPolicyRuleRoleScope(t *testing.T) {
	rbg := rbgNamed("rbg-scope", nil)

	// The rule is scoped to {prefill} only, but minReplicas also constrains
	// "decode" — a role outside the rule's declared scope.
	cp := &workloadsv1alpha2.CoordinatedPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: rbg.Name, Namespace: rbg.Namespace},
		Spec: workloadsv1alpha2.CoordinatedPolicySpec{
			Policies: []workloadsv1alpha2.CoordinatedPolicyRule{
				gangRule("gang", []string{"prefill"}, map[string]int32{
					"prefill": 2,
					"decode":  1, // NOT in the rule's Roles
				}),
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(cp).Build()

	got := GetGangStrategy(context.Background(), c, rbg)
	if got == nil {
		t.Fatal("expected a non-nil gang strategy")
	}
	if _, ok := got.MinReplicas["decode"]; !ok {
		t.Fatal("expected the out-of-scope 'decode' key to survive resolution")
	}

	t.Logf("CANARY GREEN: rule Roles=%v but the returned strategy still carries "+
		"minReplicas=%v including out-of-scope role 'decode'; policy.Roles is "+
		"never consulted", cp.Spec.Policies[0].Roles, got.MinReplicas)
}

// ---------------------------------------------------------------------------
// F7 (moderate) — only the FIRST gang rule wins; later ones are silently
// dropped.
//
// GetGangStrategy returns on the first policy carrying Scheduling.Gang. A
// CoordinatedPolicy with two rules — say one per role group, which is exactly
// how CoordinatedPolicy is meant to be used — silently loses every rule after
// the first, with no error and no event.
//
// POLARITY: CONTRACT — asserts that conflicting/multiple gang rules are either
// merged or rejected rather than silently dropped. RED on the PR head.
// ---------------------------------------------------------------------------
func TestVerifyF7_MultipleGangRulesSilentlyDropAllButFirst(t *testing.T) {
	rbg := rbgNamed("rbg-multi", nil)

	cp := &workloadsv1alpha2.CoordinatedPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: rbg.Name, Namespace: rbg.Namespace},
		Spec: workloadsv1alpha2.CoordinatedPolicySpec{
			Policies: []workloadsv1alpha2.CoordinatedPolicyRule{
				gangRule("gang-prefill", []string{"prefill"}, map[string]int32{"prefill": 2}),
				gangRule("gang-decode", []string{"decode"}, map[string]int32{"decode": 1}),
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(cp).Build()

	got := GetGangStrategy(context.Background(), c, rbg)
	if got == nil {
		t.Fatal("expected a non-nil gang strategy")
	}

	if _, ok := got.MinReplicas["decode"]; !ok {
		t.Errorf("CONTRACT RED: two gang rules were declared (prefill and decode) "+
			"but the resolved strategy is %v — the second rule was silently "+
			"discarded. Expected the rules to be merged, or the configuration "+
			"rejected as ambiguous.", got.MinReplicas)
	}
}

// ---------------------------------------------------------------------------
// F8 (minor) — a CoordinatedPolicy read failure is indistinguishable from
// "gang not configured".
//
// GetGangStrategy swallows every error from the Get: `if err := c.Get(...);
// err == nil`. A NotFound is legitimate, but a transient API error, an RBAC
// denial, or a cache miss takes the same branch and falls through to the
// annotation path — so a configured gang can silently evaporate, dropping
// minMember to the annotation default or deleting the PodGroup entirely.
// GetGangStrategy also returns no error, so no caller can react.
//
// POLARITY: CONTRACT — asserts that a non-NotFound read failure is not treated
// as "no gang". RED on the PR head (the signature cannot express it).
// ---------------------------------------------------------------------------
func TestVerifyF8_CoordinatedPolicyReadErrorIsSwallowed(t *testing.T) {
	rbg := rbgNamed("rbg-err", nil)

	// A client whose scheme lacks CoordinatedPolicy makes every Get fail with a
	// non-NotFound error (no kind registered) — standing in for an RBAC denial
	// or a transient API failure.
	brokenScheme := runtime.NewScheme()
	if err := scheme.AddToScheme(brokenScheme); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(brokenScheme).Build()

	got := GetGangStrategy(context.Background(), c, rbg)

	if got == nil {
		t.Errorf("CONTRACT RED: the CoordinatedPolicy read failed with a " +
			"non-NotFound error, yet GetGangStrategy returned nil (\"gang not " +
			"enabled\") and reported no error. A configured gang would silently " +
			"vanish and the PodGroup would be deleted. Expected the error to be " +
			"propagated so the caller can requeue.")
	}
}

// ---------------------------------------------------------------------------
// Baseline sanity — documented resolution order works. Harness-bites control.
//
// POLARITY: CONTRACT (expected GREEN on the PR head).
// ---------------------------------------------------------------------------
func TestVerifyBaseline_GangStrategyResolutionOrder(t *testing.T) {
	s := testScheme(t)

	t.Run("no policy, no annotation -> nil", func(t *testing.T) {
		rbg := rbgNamed("rbg-none", nil)
		c := fake.NewClientBuilder().WithScheme(s).Build()
		if got := GetGangStrategy(context.Background(), c, rbg); got != nil {
			t.Errorf("GetGangStrategy() = %v, want nil", got)
		}
	})

	t.Run("annotation only -> empty strategy", func(t *testing.T) {
		rbg := rbgNamed("rbg-anno", map[string]string{
			constants.GangSchedulingAnnotationKey: "true",
		})
		c := fake.NewClientBuilder().WithScheme(s).Build()
		got := GetGangStrategy(context.Background(), c, rbg)
		if got == nil {
			t.Fatal("GetGangStrategy() = nil, want non-nil empty strategy")
		}
		if len(got.MinReplicas) != 0 {
			t.Errorf("MinReplicas = %v, want empty", got.MinReplicas)
		}
	})

	t.Run("policy takes priority over annotation", func(t *testing.T) {
		rbg := rbgNamed("rbg-both", map[string]string{
			constants.GangSchedulingAnnotationKey: "true",
		})
		cp := &workloadsv1alpha2.CoordinatedPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: rbg.Name, Namespace: rbg.Namespace},
			Spec: workloadsv1alpha2.CoordinatedPolicySpec{
				Policies: []workloadsv1alpha2.CoordinatedPolicyRule{
					gangRule("gang", []string{"prefill"}, map[string]int32{"prefill": 2}),
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(s).WithObjects(cp).Build()
		got := GetGangStrategy(context.Background(), c, rbg)
		if got == nil || got.MinReplicas["prefill"] != 2 {
			t.Errorf("GetGangStrategy() = %v, want MinReplicas[prefill]=2", got)
		}
	})
}
