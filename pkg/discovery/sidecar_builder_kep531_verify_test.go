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

package discovery

// KEP-531 verification harness (reviewer-side, additive only).
//
// These tests pin the CURRENT (main) behavior of the engine-runtime
// injection that KEP-531 (`keps/531-engine-runtime-profile-injection/`)
// declares byte-for-byte compatible ("pre-existing references render
// byte-for-byte identically to main"). They are contract tests for that
// compatibility guarantee: they pass on main, and the KEP implementation
// MUST keep them passing. A red result after the implementation lands is
// positive evidence of a compatibility break (see F1 in
// docs/verification/kep-531-profile-injection/README.md).
//
// They also serve as premise (P0) evidence for issue #531:
//   - engineRuntimes[].injectContainers is inert (no volumeMounts/env
//     are delivered to the named containers);
//   - the worker template under leaderWorkerPattern skips the sidecar
//     stage entirely (verified at the PodReconciler wiring level, mirroring
//     roleinstanceset_reconciler.go / lws_reconciler.go).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func kep531Scheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = workloadsv1alpha2.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	return scheme
}

func kep531Profile(name string) *workloadsv1alpha2.ClusterEngineRuntimeProfile {
	return &workloadsv1alpha2.ClusterEngineRuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: workloadsv1alpha2.ClusterEngineRuntimeProfileSpec{
			InitContainers: []v1.Container{
				{Name: "runtime-init", Image: "profile/init:latest"},
			},
			Containers: []v1.Container{
				{Name: "runtime-sidecar", Image: "profile/sidecar:latest"},
			},
			Volumes: []v1.Volume{
				{Name: "runtime-volume", VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}}},
			},
		},
	}
}

func kep531Rbg(roles ...workloadsv1alpha2.RoleSpec) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "kep531-rbg"},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: roles},
	}
}

func containerByName(t *testing.T, spec *v1.PodTemplateSpec, name string) *v1.Container {
	t.Helper()
	for i := range spec.Spec.Containers {
		if spec.Spec.Containers[i].Name == name {
			return &spec.Spec.Containers[i]
		}
	}
	return nil
}

func countEnv(spec *v1.PodTemplateSpec, container, envName string) int {
	for i := range spec.Spec.Containers {
		if spec.Spec.Containers[i].Name != container {
			continue
		}
		n := 0
		for _, e := range spec.Spec.Containers[i].Env {
			if e.Name == envName {
				n++
			}
		}
		return n
	}
	return 0
}

// TestKEP531_Verify_InjectContainersIsInert pins issue #531 gap 1: the
// injectContainers field is accepted by the API but never consumed — the
// named container receives no volumeMounts and no env from the profile.
func TestKEP531_Verify_InjectContainersIsInert(t *testing.T) {
	scheme := kep531Scheme()
	role := workloadsv1alpha2.RoleSpec{
		Name: "lg",
		EngineRuntimes: []workloadsv1alpha2.EngineRuntime{
			{
				ProfileName:      "p",
				InjectContainers: []string{"engine"},
			},
		},
	}
	rbg := kep531Rbg(role)
	podSpec := &v1.PodTemplateSpec{Spec: v1.PodSpec{
		Containers: []v1.Container{{Name: "engine", Image: "engine:latest"}},
	}}
	b := NewSidecarBuilder(fake.NewClientBuilder().WithScheme(scheme).WithObjects(kep531Profile("p")).Build(), rbg, &role)
	assert.NoError(t, b.Build(context.TODO(), podSpec))

	engine := containerByName(t, podSpec, "engine")
	assert.NotNil(t, engine, "role engine container must survive")
	assert.Empty(t, engine.VolumeMounts, "injectContainers must stay inert: no volumeMounts delivered (issue #531 gap 1)")
	assert.Empty(t, engine.Env, "injectContainers must stay inert: no env delivered (issue #531 gap 1)")
	// pod-level delivery is the only dimension that fires
	sidecar := containerByName(t, podSpec, "runtime-sidecar")
	assert.NotNil(t, sidecar, "profile sidecar appended at pod level")
	assert.Len(t, podSpec.Spec.Volumes, 1, "profile volume appended at pod level")
}

// TestKEP531_Verify_LegacyMergeAppendsPerReferenceWithoutConflictCheck pins
// the main behavior KEP-531's compatibility guarantee rests on: two duplicate
// references of one profile, each carrying the same legacy containers env
// override, append the env var TWICE — main has no env-name conflict check in
// the legacy merge (sidecar_builder.go). If the KEP implementation makes this
// a render error ("the shared env-name conflict check spanning all three
// passes", Compatibility item 3), this pre-existing object shape stops
// rendering after upgrade — a byte-for-byte guarantee break (finding F1).
func TestKEP531_Verify_LegacyMergeAppendsPerReferenceWithoutConflictCheck(t *testing.T) {
	scheme := kep531Scheme()
	override := workloadsv1alpha2.EngineRuntime{
		ProfileName: "p",
		Containers: []v1.Container{
			{Name: "runtime-sidecar", Env: []v1.EnvVar{{Name: "FOO", Value: "bar"}}},
		},
	}
	role := workloadsv1alpha2.RoleSpec{
		Name:           "lg",
		EngineRuntimes: []workloadsv1alpha2.EngineRuntime{override, override},
	}
	rbg := kep531Rbg(role)
	podSpec := &v1.PodTemplateSpec{}
	b := NewSidecarBuilder(fake.NewClientBuilder().WithScheme(scheme).WithObjects(kep531Profile("p")).Build(), rbg, &role)
	assert.NoError(t, b.Build(context.TODO(), podSpec))

	assert.Equal(t, 2, countEnv(podSpec, "runtime-sidecar", "FOO"),
		"main appends the legacy override once per reference with no duplicate-env-name check; "+
			"the KEP's byte-for-byte guarantee (scenario 1, Duplicate Delivery) requires this exact output")
}

// TestKEP531_Verify_LegacyMergeAppendsToRoleDeclaredContainer pins the second
// main shape F1 protects: a role-declared container with the same name as a
// profile-declared one ("the role's own same-named container always wins")
// still receives legacy override env appends without any conflict check
// against the role template's own env of the same name.
func TestKEP531_Verify_LegacyMergeAppendsToRoleDeclaredContainer(t *testing.T) {
	scheme := kep531Scheme()
	role := workloadsv1alpha2.RoleSpec{
		Name: "lg",
		EngineRuntimes: []workloadsv1alpha2.EngineRuntime{
			{
				ProfileName: "p",
				Containers: []v1.Container{
					{Name: "runtime-sidecar", Env: []v1.EnvVar{{Name: "FOO", Value: "override"}}},
				},
			},
		},
	}
	rbg := kep531Rbg(role)
	podSpec := &v1.PodTemplateSpec{Spec: v1.PodSpec{
		Containers: []v1.Container{
			{Name: "runtime-sidecar", Image: "role-image:latest", Env: []v1.EnvVar{{Name: "FOO", Value: "role"}}},
		},
	}}
	b := NewSidecarBuilder(fake.NewClientBuilder().WithScheme(scheme).WithObjects(kep531Profile("p")).Build(), rbg, &role)
	assert.NoError(t, b.Build(context.TODO(), podSpec))

	sidecar := containerByName(t, podSpec, "runtime-sidecar")
	assert.NotNil(t, sidecar)
	assert.Equal(t, "role-image:latest", sidecar.Image, "role's same-named container wins the append")
	assert.Equal(t, 2, countEnv(podSpec, "runtime-sidecar", "FOO"),
		"main appends the override next to the role template's own FOO with no conflict check")
	assert.Len(t, podSpec.Spec.Containers, 1, "profile's same-named container is never appended")
}

// TestKEP531_Verify_LegacyOverrideForInitContainerIsSilentNoOp pins the main
// behavior the KEP documents ("the override loop admits init-container names
// but only ever scans the pod's containers list"): a legacy override naming a
// profile init container silently no-ops.
func TestKEP531_Verify_LegacyOverrideForInitContainerIsSilentNoOp(t *testing.T) {
	scheme := kep531Scheme()
	role := workloadsv1alpha2.RoleSpec{
		Name: "lg",
		EngineRuntimes: []workloadsv1alpha2.EngineRuntime{
			{
				ProfileName: "p",
				Containers: []v1.Container{
					{Name: "runtime-init", Env: []v1.EnvVar{{Name: "X", Value: "y"}}, Args: []string{"--flag"}},
				},
			},
		},
	}
	rbg := kep531Rbg(role)
	podSpec := &v1.PodTemplateSpec{}
	b := NewSidecarBuilder(fake.NewClientBuilder().WithScheme(scheme).WithObjects(kep531Profile("p")).Build(), rbg, &role)
	assert.NoError(t, b.Build(context.TODO(), podSpec))

	assert.Len(t, podSpec.Spec.InitContainers, 1)
	assert.Empty(t, podSpec.Spec.InitContainers[0].Env,
		"legacy override naming an init container must stay a silent no-op (KEP: preserved verbatim)")
	assert.Empty(t, podSpec.Spec.InitContainers[0].Args)
}

// TestKEP531_Verify_OverrideSkippedForContainerNotInThisReferenceProfile pins
// the main behavior the KEP documents ("a legacy override naming a container
// the reference's own profile does not declare is skipped ... including when a
// later reference would inject it").
func TestKEP531_Verify_OverrideSkippedForContainerNotInThisReferenceProfile(t *testing.T) {
	scheme := kep531Scheme()
	p2 := kep531Profile("p2")
	p2.Spec.Containers[0].Name = "other-sidecar"
	role := workloadsv1alpha2.RoleSpec{
		Name: "lg",
		EngineRuntimes: []workloadsv1alpha2.EngineRuntime{
			{
				ProfileName: "p1",
				Containers: []v1.Container{
					{Name: "other-sidecar", Env: []v1.EnvVar{{Name: "X", Value: "y"}}},
				},
			},
			{ProfileName: "p2"},
		},
	}
	rbg := kep531Rbg(role)
	podSpec := &v1.PodTemplateSpec{}
	b := NewSidecarBuilder(
		fake.NewClientBuilder().WithScheme(scheme).WithObjects(kep531Profile("p1"), p2).Build(), rbg, &role,
	)
	assert.NoError(t, b.Build(context.TODO(), podSpec))

	assert.NotNil(t, containerByName(t, podSpec, "other-sidecar"),
		"the later reference still injects its own container")
	assert.Equal(t, 0, countEnv(podSpec, "other-sidecar", "X"),
		"ref1's override naming ref2's container must be skipped (per-reference name set, V(1) log)")
}

// TestKEP531_Verify_AppendIfMissingPodLevelDelivery pins the pod-level
// append-if-missing semantics (init containers, containers, volumes) the KEP
// preserves verbatim.
func TestKEP531_Verify_AppendIfMissingPodLevelDelivery(t *testing.T) {
	scheme := kep531Scheme()
	role := workloadsv1alpha2.RoleSpec{
		Name:           "lg",
		EngineRuntimes: []workloadsv1alpha2.EngineRuntime{{ProfileName: "p"}, {ProfileName: "p"}},
	}
	rbg := kep531Rbg(role)
	podSpec := &v1.PodTemplateSpec{}
	b := NewSidecarBuilder(fake.NewClientBuilder().WithScheme(scheme).WithObjects(kep531Profile("p")).Build(), rbg, &role)
	assert.NoError(t, b.Build(context.TODO(), podSpec))

	assert.Len(t, podSpec.Spec.InitContainers, 1, "duplicate references never double-deliver init containers")
	assert.Len(t, podSpec.Spec.Containers, 1, "duplicate references never double-deliver sidecars")
	assert.Len(t, podSpec.Spec.Volumes, 1, "duplicate references never double-deliver volumes")
}
