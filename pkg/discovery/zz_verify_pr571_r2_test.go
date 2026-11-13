/*
Round-2 additions to the PR #571 verification harness (reviewer: codex).
Additive test file — no production code changed.

These contract tests pin the fixes reviewed in round 2 (head f3d4849a) for
the two round-1 findings, so later rounds re-run them as the regression
suite. On the round-1 head 1810f5c6 both were RED (see
results/round2/bites-previous-head.out); on f3d4849a they must be GREEN.

R2-F1 (contract): F-LWS-DROP — a LeaderWorkerSet-typed role whose pattern is
not leaderWorkerPattern (a legal, grandfathered shape) must key fragments to
the LWS construct positions {leader, worker} with leader the absent-component
default, reproducing main's published behavior, instead of silently dropping
all engine-runtime content.

R2-F2 (contract): F-ENV-OWNERSHIP — a profile-side append onto a role-owned
env name stays a render error even after another profile's sanctioned /env/0
default of the same name; profile content never wins a same-name contest with
role content.

R2-F3 (contract): consent-only targetContainers entries (Copilot C4557947790)
— every listed target is validated even when no patch fires for it.
*/

package discovery

import (
	"context"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func lwsAnnotate(role *workloadsv1alpha2.RoleSpec) *workloadsv1alpha2.RoleSpec {
	if role.Annotations == nil {
		role.Annotations = map[string]string{}
	}
	role.Annotations[constants.RoleWorkloadTypeAnnotationKey] = constants.LeaderWorkerSetWorkloadType
	return role
}

func TestVerifyPR571R2_LWSVocabularyCollapse(t *testing.T) {
	profile := verifyProfile("ik")
	scheme := testScheme(t)

	resolve := func(t *testing.T, role *workloadsv1alpha2.RoleSpec) *EngineRuntimePlan {
		t.Helper()
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()
		plan, err := ResolveEngineRuntimes(context.Background(), cl, nil, verifyRBG(role), role)
		if err != nil {
			t.Fatalf("ResolveEngineRuntimes: %v", err)
		}
		return plan
	}

	t.Run("LWS x standalonePattern keys fragments to leader", func(t *testing.T) {
		role := lwsAnnotate(verifyStandaloneRole("solo", workloadsv1alpha2.EngineRuntime{ProfileName: "ik"}))
		plan := resolve(t, role)
		if got := plan.FragmentApplications["leader"]; len(got) != 1 {
			t.Fatalf("expected exactly one fragment application at position leader, got %d (all positions: %v)",
				len(got), plan.FragmentApplications)
		}
		if got := plan.FragmentApplications["worker"]; len(got) != 0 {
			t.Fatalf("main skipped the worker template on the LWS path; expected no worker application, got %d", len(got))
		}
	})

	t.Run("LWS x customComponentsPattern keys fragments to leader", func(t *testing.T) {
		role := lwsAnnotate(ccpRole("multi", []string{"engine-comp", "aux-comp"}, workloadsv1alpha2.EngineRuntime{ProfileName: "ik"}))
		plan := resolve(t, role)
		if got := plan.FragmentApplications["leader"]; len(got) != 1 {
			t.Fatalf("expected exactly one fragment application at position leader, got %d (all positions: %v)",
				len(got), plan.FragmentApplications)
		}
		for _, pos := range []string{"engine-comp", "aux-comp"} {
			if got := plan.FragmentApplications[pos]; len(got) != 0 {
				t.Fatalf("position %q is not an LWS construct position; expected no application, got %d", pos, len(got))
			}
		}
	})

	t.Run("control: LWS x leaderWorkerPattern keeps the pattern vocabulary", func(t *testing.T) {
		role := lwsAnnotate(lwpRole("lw", workloadsv1alpha2.EngineRuntime{ProfileName: "ik"}))
		plan := resolve(t, role)
		if got := plan.FragmentApplications["leader"]; len(got) != 1 {
			t.Fatalf("expected the leader application, got %d (all positions: %v)", len(got), plan.FragmentApplications)
		}
	})
}

func TestVerifyPR571R2_EnvOwnershipAcrossStackedProfiles(t *testing.T) {
	ctx := log.IntoContext(context.Background(), klog.NewKlogr())
	jp := verifyPatch // shared with the grammar harness file
	envVar := v1.EnvVar{Name: "X", Value: "role"}
	newSpec := func() *v1.PodTemplateSpec {
		return &v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{
			Name: "engine", Image: "sglang:1", Env: []v1.EnvVar{envVar},
		}}}}
	}
	roleNames := map[string]struct{}{"engine": {}}

	t.Run("append onto a role-owned name after another profile defaulted it is a render error", func(t *testing.T) {
		spec := newSpec()
		entries := []EngineRuntimePatch{
			{ProfileName: "first", Targets: []string{"engine"},
				Patches: []workloadsv1alpha2.ContainerPatch{jp("/env/0", v1.EnvVar{Name: "X", Value: "first-default"})}},
			{ProfileName: "second", Targets: []string{"engine"},
				Patches: []workloadsv1alpha2.ContainerPatch{jp("/env/-", v1.EnvVar{Name: "X", Value: "second-appended"})}},
		}
		_, err := ApplyEngineRuntimePatches(ctx, spec, roleNames, nil, "engine", entries, nil, nil, nil)
		if err == nil {
			t.Fatalf("profile content positionally won a same-name contest with role content: env=%v", spec.Spec.Containers[0].Env)
		}
		if !strings.Contains(err.Error(), "second") {
			t.Fatalf("the error must name the offending profile, got: %v", err)
		}
	})

	t.Run("control: a lone profile append onto a role-owned name is a render error", func(t *testing.T) {
		spec := newSpec()
		entries := []EngineRuntimePatch{
			{ProfileName: "only", Targets: []string{"engine"},
				Patches: []workloadsv1alpha2.ContainerPatch{jp("/env/-", v1.EnvVar{Name: "X", Value: "only-appended"})}},
		}
		if _, err := ApplyEngineRuntimePatches(ctx, spec, roleNames, nil, "engine", entries, nil, nil, nil); err == nil {
			t.Fatal("a lone profile append onto a role-owned name must error")
		}
	})

	t.Run("control: cross-profile stacking on a genuinely created name stays legal", func(t *testing.T) {
		spec := newSpec()
		entries := []EngineRuntimePatch{
			{ProfileName: "first", Targets: []string{"engine"},
				Patches: []workloadsv1alpha2.ContainerPatch{jp("/env/-", v1.EnvVar{Name: "FRESH", Value: "first-created"})}},
			{ProfileName: "second", Targets: []string{"engine"},
				Patches: []workloadsv1alpha2.ContainerPatch{jp("/env/-", v1.EnvVar{Name: "FRESH", Value: "second-appended"})}},
		}
		if _, err := ApplyEngineRuntimePatches(ctx, spec, roleNames, nil, "engine", entries, nil, nil, nil); err != nil {
			t.Fatalf("fresh-name cross-profile stacking must stay legal, got: %v", err)
		}
	})
}

func TestVerifyPR571R2_ConsentOnlyTargetsValidated(t *testing.T) {
	// The profile carries no containerPatches, so nothing fires — but every
	// listed target is still validated against the role's and the reference's
	// own profile declarations.
	profile := verifyProfile("plain")
	profile.Spec.InitContainers = nil
	profile.Spec.Containers = []v1.Container{{Name: "plain-sidecar", Image: "plain:1"}}
	profile.Spec.Volumes = nil
	scheme := testScheme(t)

	resolveErr := func(t *testing.T, role *workloadsv1alpha2.RoleSpec) error {
		t.Helper()
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()
		_, err := ResolveEngineRuntimes(context.Background(), cl, nil, verifyRBG(role), role)
		return err
	}

	consentRef := func(name string) workloadsv1alpha2.EngineRuntime {
		return workloadsv1alpha2.EngineRuntime{
			ProfileName:      "plain",
			TargetContainers: []workloadsv1alpha2.EngineRuntimeTargetContainer{{Name: name}},
		}
	}

	t.Run("an unknown consent-only target is a render error", func(t *testing.T) {
		role := verifyStandaloneRole("solo", consentRef("typo-container"))
		if err := resolveErr(t, role); err == nil {
			t.Fatal("a consent-only entry naming an unknown container must fail the render")
		}
	})
	t.Run("a role-declared consent-only target passes", func(t *testing.T) {
		role := verifyStandaloneRole("solo", consentRef("engine"))
		if err := resolveErr(t, role); err != nil {
			t.Fatalf("role-declared target must pass: %v", err)
		}
	})
	t.Run("a profile-declared consent-only target passes", func(t *testing.T) {
		role := verifyStandaloneRole("solo", consentRef("plain-sidecar"))
		if err := resolveErr(t, role); err != nil {
			t.Fatalf("profile-declared target must pass: %v", err)
		}
	})
}
