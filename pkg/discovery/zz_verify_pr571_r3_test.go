/*
Round-3 additions to the PR #571 verification harness (reviewer: codex).
Additive test file — no production code changed.

These tests pin the fixes and the new behavior reviewed in round 3 (head
c87a2e1e, previous head f3d4849a). Contract tests must be GREEN on c87a2e1e
and were RED on f3d4849a where the behavior was added this round (bites
evidence: results/round3/bites-previous-head-f3d4849a.log). The canary
documents a behavior the review files as a nit; it flips when the behavior
changes.

R3-F1 (contract): F-EVENTS-REEMIT fix — successful-render warnings
(UnaimedContainerPatch here) are deduped across reconciles of an unchanged
object (keyed by UID+generation+message), re-armed by a generation bump, and
render failures keep firing per reconcile. UID-less fixtures keep the old
always-emit behavior.

R3-F2 (canary): F-DEDUPE-ROLE-COLLAPSE — the dedupe fingerprint is
reason+message and the UnaimedContainerPatch message does not name the role,
so two roles of the same RBG tripping the identical warning collapse into one
event per generation. Canary: passes while the collapse is present; flip when
the message or key names the role.
*/

package discovery

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerifyPR571R3_SuccessfulRenderWarningDedupe(t *testing.T) {
	profile := makeProfile("zzr3-p", []workloadsv1alpha2.ContainerPatch{envPatch("IK_SITE", "/opt/ik/site")})
	role := leaderWorkerRole([]workloadsv1alpha2.EngineRuntime{{ProfileName: "zzr3-p", Component: string(constants.LeaderComponentType)}})
	rbg := basicRBG()
	rbg.UID = "zz-r3-dedupe"
	rbg.Generation = 1

	resolve := func() *record.FakeRecorder {
		recorder := record.NewFakeRecorder(16)
		cli := fake.NewClientBuilder().WithScheme(resolveScheme()).WithObjects(profile).Build()
		if _, err := ResolveEngineRuntimes(context.Background(), cli, recorder, rbg, role); err != nil {
			t.Fatalf("ResolveEngineRuntimes: %v", err)
		}
		return recorder
	}

	if got := countEventsContaining(t, resolve(), ReasonUnaimedContainerPatch); got != 1 {
		t.Fatalf("first resolve must emit the unaimed warning once, got %d", got)
	}
	if got := countEventsContaining(t, resolve(), ReasonUnaimedContainerPatch); got != 0 {
		t.Fatalf("re-reconcile of the unchanged object must not re-emit the warning, got %d", got)
	}

	rbg.Generation = 2
	if got := countEventsContaining(t, resolve(), ReasonUnaimedContainerPatch); got != 1 {
		t.Fatalf("a generation bump must re-arm the warning, got %d", got)
	}
}

func TestVerifyPR571R3_RenderFailureNotDeduped(t *testing.T) {
	role := leaderWorkerRole([]workloadsv1alpha2.EngineRuntime{{ProfileName: "zzr3-ghost"}})
	rbg := basicRBG()
	rbg.UID = "zz-r3-fail"
	rbg.Generation = 1
	for i := 0; i < 2; i++ {
		recorder := record.NewFakeRecorder(16)
		cli := fake.NewClientBuilder().WithScheme(resolveScheme()).Build()
		if _, err := ResolveEngineRuntimes(context.Background(), cli, recorder, rbg, role); err == nil {
			t.Fatalf("render against a missing profile must fail")
		}
		if got := countEventsContaining(t, recorder, ReasonEngineRuntimeRenderFailed); got != 1 {
			t.Fatalf("render failure %d must keep firing per reconcile, got %d", i+1, got)
		}
	}
}

func TestVerifyPR571R3_UIDlessFixtureAlwaysEmits(t *testing.T) {
	profile := makeProfile("zzr3-p2", []workloadsv1alpha2.ContainerPatch{envPatch("A", "b")})
	role := leaderWorkerRole([]workloadsv1alpha2.EngineRuntime{{ProfileName: "zzr3-p2", Component: string(constants.LeaderComponentType)}})
	rbg := basicRBG() // no UID: nothing to dedupe against
	for i := 0; i < 2; i++ {
		recorder := record.NewFakeRecorder(16)
		cli := fake.NewClientBuilder().WithScheme(resolveScheme()).WithObjects(profile).Build()
		if _, err := ResolveEngineRuntimes(context.Background(), cli, recorder, rbg, role); err != nil {
			t.Fatalf("ResolveEngineRuntimes: %v", err)
		}
		if got := countEventsContaining(t, recorder, ReasonUnaimedContainerPatch); got != 1 {
			t.Fatalf("UID-less fixture resolve %d must always emit, got %d", i+1, got)
		}
	}
}

// TestVerifyPR571R3_DedupeCollapsesIdenticalCrossRoleWarnings is a CANARY for
// F-DEDUPE-ROLE-COLLAPSE: two roles of one RBG tripping the identical unaimed
// warning (the message names the profile, not the role) collapse into one
// event per generation. Pass = collapse present. Flip this when the message
// or the dedupe key names the role.
func TestVerifyPR571R3_DedupeCollapsesIdenticalCrossRoleWarnings(t *testing.T) {
	profile := makeProfile("zzr3-p3", []workloadsv1alpha2.ContainerPatch{envPatch("C", "d")})
	mkRole := func(name string) *workloadsv1alpha2.RoleSpec {
		tmpl := corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "engine"}}}}
		return &workloadsv1alpha2.RoleSpec{
			Name: name,
			Pattern: workloadsv1alpha2.Pattern{
				LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{
					Size:           ptr.To(int32(2)),
					TemplateSource: workloadsv1alpha2.TemplateSource{Template: &tmpl},
				},
			},
			EngineRuntimes: []workloadsv1alpha2.EngineRuntime{{ProfileName: "zzr3-p3", Component: string(constants.LeaderComponentType)}},
		}
	}
	rbg := basicRBG()
	rbg.UID = "zz-r3-roles"
	rbg.Generation = 1

	total := 0
	for _, name := range []string{"role-a", "role-b"} {
		recorder := record.NewFakeRecorder(16)
		cli := fake.NewClientBuilder().WithScheme(resolveScheme()).WithObjects(profile).Build()
		if _, err := ResolveEngineRuntimes(context.Background(), cli, recorder, rbg, mkRole(name)); err != nil {
			t.Fatalf("ResolveEngineRuntimes(%s): %v", name, err)
		}
		total += countEventsContaining(t, recorder, ReasonUnaimedContainerPatch)
	}
	if total != 1 {
		t.Fatalf("canary: identical warnings from two roles collapse to one event per generation, got %d", total)
	}
}

// TestVerifyPR571R3_DedupeCoverageGap is a CANARY for F-DEDUPE-COVERAGE:
// EngineRuntimeCapabilityFormHazard (and EngineRuntimeWrapShadowed, emitted
// from the pod reconciler outside emitEngineRuntimeWarning) keep the
// re-fire-per-reconcile property the round-3 dedupe removed for the other
// three successful-render warnings. Pass = gap present; flip when the
// reason joins the dedupe set.
func TestVerifyPR571R3_DedupeCoverageGap(t *testing.T) {
	rbg := basicRBG()
	rbg.UID = "zz-r3-coverage"
	rbg.Generation = 1
	for i := 0; i < 2; i++ {
		recorder := record.NewFakeRecorder(16)
		emitEngineRuntimeWarning(context.Background(), recorder, rbg,
			ReasonEngineRuntimeCapabilityFormHazard, "capability SYS_ADMIN in a non-canonical form")
		if got := countEventsContaining(t, recorder, ReasonEngineRuntimeCapabilityFormHazard); got != 1 {
			t.Fatalf("canary: CapabilityFormHazard is outside the dedupe set and must fire per reconcile, resolve %d got %d", i+1, got)
		}
	}
}
