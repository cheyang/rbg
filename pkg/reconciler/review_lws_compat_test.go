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

package reconciler

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/discovery"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// TestReview_LWSNonLWPPatternFragmentRouting is the review-harness contract
// test for KEP-531 compatibility guarantee #1 on the LWS render path.
//
// The claim under test: "references that set only profileName render
// identically to main", and the KEP's default-delivery table — a
// deprecated-path render "keeps main's behavior verbatim for the references
// it can carry". On main (ea5e82ae), the LWS construct path renders its
// leader template with the default injector chain, which includes the
// sidecar injector, unconditionally: a `profileName`-only engineRuntimes
// reference injects the profile's sidecars/volumes into the leader template
// whatever the role's *pattern* (InjectSidecar consults neither).
//
// On this PR, fragments route by positionVocabulary(role), which keys by the
// pattern's vocabulary: {role.Name} under standalonePattern, component names
// under customComponentsPattern. The LWS construct path hardcodes its
// positions as "leader"/"worker" (lws_reconciler.go SetEngineRuntimePlan),
// and deliberately does not collapse the vocabulary for LWS and does not arm
// the position gate — so for an LWS-workload-type role whose pattern is not
// leaderWorkerPattern, FragmentApplications["leader"] is empty and the
// profile's pod-level content (and the legacy containers env/args override)
// is silently dropped: no injection, no event, no error.
//
// The combination is writable today: nothing ties the LWS workload type to
// leaderWorkerPattern (LeaderWorkerSetReconciler.Validate is a no-op and
// handles a nil pattern), and the role-side engine-runtime validation only
// rejects the *new* fields on deprecated surfaces — a bare profileName
// reference passes.
//
// Polarity: CONTRACT — fails on the code under review (the reproduction),
// passes once the LWS render keeps main's fragment delivery for the role's
// pattern (e.g. by collapsing the LWS vocabulary for non-LWP patterns to the
// leader position, where main injected).
func TestReview_LWSNonLWPPatternFragmentRouting(t *testing.T) {
	ctx := context.Background()

	newScheme := func() *runtime.Scheme {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		_ = workloadsv1alpha2.AddToScheme(scheme)
		return scheme
	}

	profile := &workloadsv1alpha2.ClusterEngineRuntimeProfile{
		TypeMeta:   metav1.TypeMeta{APIVersion: "workloads.x-k8s.io/v1alpha2", Kind: "ClusterEngineRuntimeProfile"},
		ObjectMeta: metav1.ObjectMeta{Name: "rt"},
		Spec: workloadsv1alpha2.ClusterEngineRuntimeProfileSpec{
			Containers: []corev1.Container{{Name: "rt-sidecar", Image: "rt:latest"}},
			Volumes: []corev1.Volume{{Name: "rt-vol", VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			}}},
		},
	}

	// lwsRole builds the role exactly as lws_reconciler sees it: a
	// standalonePattern (or customComponentsPattern) role whose workload-type
	// annotation routes through the deprecated LWS surface, carrying a
	// bare profileName-only engineRuntimes reference — the pre-KEP shape.
	lwsRole := func(name string, pattern workloadsv1alpha2.Pattern) *workloadsv1alpha2.RoleSpec {
		return &workloadsv1alpha2.RoleSpec{
			Name:     name,
			Replicas: nil,
			Pattern:  pattern,
			Annotations: map[string]string{
				constants.RoleWorkloadTypeAnnotationKey: constants.LeaderWorkerSetWorkloadType,
			},
			EngineRuntimes: []workloadsv1alpha2.EngineRuntime{{ProfileName: "rt"}},
		}
	}

	renderLWSLeader := func(t *testing.T, role *workloadsv1alpha2.RoleSpec) *corev1.PodTemplateSpec {
		t.Helper()
		scheme := newScheme()
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()
		rbg := wrappersv2.BuildBasicRoleBasedGroup("lws-compat-rbg", "default").Obj()
		rbg.Spec.Roles[0] = *role

		plan, err := discovery.ResolveEngineRuntimes(ctx, client, nil, rbg, role)
		if err != nil {
			t.Fatalf("resolve failed (expected a clean resolve for the pre-KEP reference shape): %v", err)
		}

		// Mirror the lws_reconciler leader wiring verbatim: position
		// "leader", no position gate (the deprecated path leaves it off).
		reconciler := NewPodReconciler(scheme, client)
		reconciler.SetInjectors([]string{})
		reconciler.SetEngineRuntimePlan(string(constants.LeaderComponentType), plan)
		applyCfg, err := reconciler.ConstructPodTemplateSpecApplyConfiguration(ctx, rbg, role, nil)
		if err != nil {
			t.Fatalf("render failed (the silent drop renders cleanly by design): %v", err)
		}
		spec := corev1.PodTemplateSpec{}
		data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(applyCfg)
		if err != nil {
			t.Fatalf("converting apply configuration: %v", err)
		}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(data, &spec); err != nil {
			t.Fatalf("converting back to pod template: %v", err)
		}
		return &spec
	}

	hasContainer := func(spec *corev1.PodTemplateSpec, name string) bool {
		for _, c := range spec.Spec.Containers {
			if c.Name == name {
				return true
			}
		}
		return false
	}
	hasVolume := func(spec *corev1.PodTemplateSpec, name string) bool {
		for _, v := range spec.Spec.Volumes {
			if v.Name == name {
				return true
			}
		}
		return false
	}

	t.Run("standalonePattern role on the LWS surface keeps main's fragment delivery", func(t *testing.T) {
		tpl := wrappersv2.BuildBasicPodTemplateSpec()
		role := lwsRole("solo", workloadsv1alpha2.Pattern{
			StandalonePattern: &workloadsv1alpha2.StandalonePattern{
				TemplateSource: workloadsv1alpha2.TemplateSource{Template: &tpl},
			},
		})
		spec := renderLWSLeader(t, role)
		if !hasContainer(spec, "rt-sidecar") || !hasVolume(spec, "rt-vol") {
			t.Fatalf("profile content silently dropped on the LWS render path: expected main's behavior "+
				"(sidecar %q and volume %q injected into the leader template), got containers=%v volumes=%v. "+
				"The resolver keyed the fragments at the pattern vocabulary while the LWS construct path "+
				"reads position %q.",
				"rt-sidecar", "rt-vol", containerNames(spec), volumeNames(spec),
				string(constants.LeaderComponentType))
		}
	})

	// Note: the customComponentsPattern + LWS combination is omitted as a
	// separate sub-case: GetTemplate() returns nil for customComponents roles,
	// so main's own LWS render degenerates to a zero-value base template there
	// — not a meaningful compatibility baseline. The standalone case above is
	// the shape where main renders coherently (the standalone template is the
	// LWS base template, and the sidecar injector runs on the leader chain).

	// Control: the leaderWorkerPattern combination, where the plan's default
	// set ({leader}) matches the LWS construct positions — unaffected, and the
	// common-case compatibility contract the PR's own tests pin.
	t.Run("control: leaderWorkerPattern role on the LWS surface injects into the leader", func(t *testing.T) {
		template := wrappersv2.BuildBasicPodTemplateSpec()
		role := &workloadsv1alpha2.RoleSpec{
			Name:    "lw",
			Pattern: workloadsv1alpha2.Pattern{LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{
				TemplateSource: workloadsv1alpha2.TemplateSource{Template: &template},
			}},
			Annotations: map[string]string{
				constants.RoleWorkloadTypeAnnotationKey: constants.LeaderWorkerSetWorkloadType,
			},
			EngineRuntimes: []workloadsv1alpha2.EngineRuntime{{ProfileName: "rt"}},
		}
		spec := renderLWSLeader(t, role)
		if !hasContainer(spec, "rt-sidecar") {
			t.Fatalf("leaderWorkerPattern leader injection regressed: containers=%v", containerNames(spec))
		}
	})
}

func containerNames(spec *corev1.PodTemplateSpec) []string {
	names := make([]string, 0, len(spec.Spec.Containers))
	for _, c := range spec.Spec.Containers {
		names = append(names, c.Name)
	}
	return names
}

func volumeNames(spec *corev1.PodTemplateSpec) []string {
	names := make([]string, 0, len(spec.Spec.Volumes))
	for _, v := range spec.Spec.Volumes {
		names = append(names, v.Name)
	}
	return names
}
