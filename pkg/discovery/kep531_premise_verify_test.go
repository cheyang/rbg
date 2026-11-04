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

// Review harness for PR #548 (KEP-531, docs-only). These tests pin the
// main-branch behaviors that the KEP's "Compatibility Guarantees" and
// "Design Details" sections assert about the CURRENT renderer, so the
// follow-up implementation PRs can detect any drift in the premises they
// build on. Additive only: no production code is changed.
//
// All tests are CONTRACT polarity: they assert the intended/observed main
// behavior and must PASS on the PR head (which changes no code). If a future
// implementation PR changes renderer behavior, the affected test flips red.

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func kep531Scheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = workloadsv1alpha2.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	return scheme
}

func kep531Profile(name, containerImage string) *workloadsv1alpha2.ClusterEngineRuntimeProfile {
	return &workloadsv1alpha2.ClusterEngineRuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: workloadsv1alpha2.ClusterEngineRuntimeProfileSpec{
			InitContainers: []v1.Container{
				{Name: name + "-init", Image: name + "-init-image"},
			},
			Containers: []v1.Container{
				{Name: name + "-rt", Image: containerImage},
			},
			Volumes: []v1.Volume{
				{Name: name + "-vol", VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}}},
			},
		},
	}
}

func findContainer(podSpec *v1.PodTemplateSpec, name string) *v1.Container {
	for i := range podSpec.Spec.Containers {
		if podSpec.Spec.Containers[i].Name == name {
			return &podSpec.Spec.Containers[i]
		}
	}
	return nil
}

func findInitContainer(podSpec *v1.PodTemplateSpec, name string) *v1.Container {
	for i := range podSpec.Spec.InitContainers {
		if podSpec.Spec.InitContainers[i].Name == name {
			return &podSpec.Spec.InitContainers[i]
		}
	}
	return nil
}

func findVolume(podSpec *v1.PodTemplateSpec, name string) *v1.Volume {
	for i := range podSpec.Spec.Volumes {
		if podSpec.Spec.Volumes[i].Name == name {
			return &podSpec.Spec.Volumes[i]
		}
	}
	return nil
}

func envValues(c *v1.Container, name string) []string {
	var out []string
	for _, e := range c.Env {
		if e.Name == name {
			out = append(out, e.Value)
		}
	}
	return out
}

// C2: the legacy `containers` merge is env/args-only; every other field of
// the override entry (command, image, volumeMounts) is silently ignored.
func TestKEP531_LegacyOverrideMergesEnvArgsOnly(t *testing.T) {
	scheme := kep531Scheme()
	profile := kep531Profile("p1", "profile-image")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()

	podSpec := &v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "main", Image: "m"}}}}
	rt := workloadsv1alpha2.EngineRuntime{
		ProfileName: "p1",
		Containers: []v1.Container{
			{
				Name:         "p1-rt",
				Command:      []string{"/bin/override"},
				Image:        "override-image",
				Args:         []string{"--extra"},
				Env:          []v1.EnvVar{{Name: "A", Value: "1"}},
				VolumeMounts: []v1.VolumeMount{{Name: "x", MountPath: "/x"}},
			},
		},
	}
	b := NewSidecarBuilder(cl, &workloadsv1alpha2.RoleBasedGroup{}, &workloadsv1alpha2.RoleSpec{Name: "r"})
	if err := b.injectRuntime(context.Background(), podSpec, rt); err != nil {
		t.Fatalf("injectRuntime failed: %v", err)
	}
	c := findContainer(podSpec, "p1-rt")
	if c == nil {
		t.Fatal("profile container p1-rt not injected")
	}
	if len(c.Command) != 0 {
		t.Errorf("override Command leaked into rendered container: %v", c.Command)
	}
	if c.Image != "profile-image" {
		t.Errorf("override Image leaked into rendered container: %v", c.Image)
	}
	if len(c.VolumeMounts) != 0 {
		t.Errorf("override VolumeMounts leaked into rendered container: %v", c.VolumeMounts)
	}
	if got := envValues(c, "A"); len(got) != 1 || got[0] != "1" {
		t.Errorf("env override not appended, got %v", got)
	}
	if len(c.Args) != 1 || c.Args[0] != "--extra" {
		t.Errorf("args override not appended, got %v", c.Args)
	}
}

// C3: a legacy override naming a profile INIT container silently no-ops —
// the override loop admits init-container names but only scans
// podSpec.Spec.Containers.
func TestKEP531_LegacyOverrideInitContainerIsNoop(t *testing.T) {
	scheme := kep531Scheme()
	profile := kep531Profile("p1", "img")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()

	podSpec := &v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "main"}}}}
	rt := workloadsv1alpha2.EngineRuntime{
		ProfileName: "p1",
		Containers: []v1.Container{
			{Name: "p1-init", Env: []v1.EnvVar{{Name: "A", Value: "1"}}, Args: []string{"--x"}},
		},
	}
	b := NewSidecarBuilder(cl, &workloadsv1alpha2.RoleBasedGroup{}, &workloadsv1alpha2.RoleSpec{Name: "r"})
	if err := b.injectRuntime(context.Background(), podSpec, rt); err != nil {
		t.Fatalf("injectRuntime failed: %v", err)
	}
	ic := findInitContainer(podSpec, "p1-init")
	if ic == nil {
		t.Fatal("profile init container p1-init not injected")
	}
	if len(ic.Env) != 0 || len(ic.Args) != 0 {
		t.Errorf("override on init container should no-op, got env=%v args=%v", ic.Env, ic.Args)
	}
}

// C4: a legacy override naming a container the reference's own profile does
// NOT declare is skipped silently (V(1) log), without failing the render.
func TestKEP531_LegacyOverrideUnknownContainerSkipped(t *testing.T) {
	scheme := kep531Scheme()
	profile := kep531Profile("p1", "img")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()

	podSpec := &v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "ghost"}}}}
	rt := workloadsv1alpha2.EngineRuntime{
		ProfileName: "p1",
		Containers:  []v1.Container{{Name: "ghost", Env: []v1.EnvVar{{Name: "A", Value: "1"}}}},
	}
	b := NewSidecarBuilder(cl, &workloadsv1alpha2.RoleBasedGroup{}, &workloadsv1alpha2.RoleSpec{Name: "r"})
	if err := b.injectRuntime(context.Background(), podSpec, rt); err != nil {
		t.Fatalf("override of undeclared container must not fail render, got: %v", err)
	}
	if c := findContainer(podSpec, "ghost"); len(c.Env) != 0 {
		t.Errorf("override should have been skipped, ghost env = %v", c.Env)
	}
}

// C5: pod-level injection is append-if-missing keyed by name, and the role's
// own same-named container/volume always wins.
func TestKEP531_AppendIfMissingRoleWins(t *testing.T) {
	scheme := kep531Scheme()
	profile := kep531Profile("p1", "img")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()

	podSpec := &v1.PodTemplateSpec{Spec: v1.PodSpec{
		Containers: []v1.Container{{Name: "p1-rt", Image: "role-image"}},
		Volumes: []v1.Volume{
			{Name: "p1-vol", VolumeSource: v1.VolumeSource{HostPath: &v1.HostPathVolumeSource{Path: "/role"}}},
		},
	}}
	rt := workloadsv1alpha2.EngineRuntime{ProfileName: "p1"}
	b := NewSidecarBuilder(cl, &workloadsv1alpha2.RoleBasedGroup{}, &workloadsv1alpha2.RoleSpec{Name: "r"})
	if err := b.injectRuntime(context.Background(), podSpec, rt); err != nil {
		t.Fatalf("injectRuntime failed: %v", err)
	}
	count := 0
	for _, c := range podSpec.Spec.Containers {
		if c.Name == "p1-rt" {
			count++
			if c.Image != "role-image" {
				t.Errorf("role-declared container must win, got image %v", c.Image)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 p1-rt container, got %d", count)
	}
	v := findVolume(podSpec, "p1-vol")
	if v == nil || v.HostPath == nil || v.HostPath.Path != "/role" {
		t.Errorf("role-declared volume must win, got %+v", v)
	}
	// init container not present in role template -> injected
	if findInitContainer(podSpec, "p1-init") == nil {
		t.Error("profile init container missing")
	}
}

// C6: duplicate same-profile references never double-inject pod-level
// content, but the legacy env/args merge appends once per reference.
func TestKEP531_DuplicateReferencesDedupPodLevelLegacyAppendsTwice(t *testing.T) {
	scheme := kep531Scheme()
	profile := kep531Profile("p1", "img")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()

	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{
					Name: "r",
					EngineRuntimes: []workloadsv1alpha2.EngineRuntime{
						{ProfileName: "p1", Containers: []v1.Container{{Name: "p1-rt", Env: []v1.EnvVar{{Name: "A", Value: "1"}}}}},
						{ProfileName: "p1", Containers: []v1.Container{{Name: "p1-rt", Env: []v1.EnvVar{{Name: "A", Value: "1"}}}}},
					},
				},
			},
		},
	}
	podSpec := &v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "main"}}}}
	b := NewSidecarBuilder(cl, rbg, &workloadsv1alpha2.RoleSpec{Name: "r"})
	if err := b.Build(context.Background(), podSpec); err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	count := 0
	for _, c := range podSpec.Spec.Containers {
		if c.Name == "p1-rt" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("pod-level injection must dedup duplicate references, got %d p1-rt containers", count)
	}
	if got := envValues(findContainer(podSpec, "p1-rt"), "A"); len(got) != 2 {
		t.Errorf("legacy merge must append once per reference, got A=%v", got)
	}
}

// C7: when two DIFFERENT profiles declare the same container name, the
// earlier reference wins and the later profile's entry is silently skipped.
func TestKEP531_CrossProfileCollisionFirstReferenceWins(t *testing.T) {
	scheme := kep531Scheme()
	p1 := kep531Profile("p1", "img")
	p2 := kep531Profile("p2", "img")
	// make both profiles declare a container with the SAME name
	p2.Spec.Containers[0].Name = "p1-rt"
	p2.Spec.Containers[0].Image = "p2-image"
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(p1, p2).Build()

	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{
					Name:           "r",
					EngineRuntimes: []workloadsv1alpha2.EngineRuntime{{ProfileName: "p1"}, {ProfileName: "p2"}},
				},
			},
		},
	}
	podSpec := &v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "main"}}}}
	b := NewSidecarBuilder(cl, rbg, &workloadsv1alpha2.RoleSpec{Name: "r"})
	if err := b.Build(context.Background(), podSpec); err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	count := 0
	for _, c := range podSpec.Spec.Containers {
		if c.Name == "p1-rt" {
			count++
			if c.Image != "img" {
				t.Errorf("earlier reference must win cross-profile collision, got image %v", c.Image)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 p1-rt container, got %d", count)
	}
	// p2's init container and volume (distinct names) still arrive.
	if findInitContainer(podSpec, "p2-init") == nil || findVolume(podSpec, "p2-vol") == nil {
		t.Error("non-colliding content from p2 should still be injected")
	}
}

// C8: the built-in env stage runs after the sidecar stage and covers every
// container of the template, profile-injected ones included.
func TestKEP531_InjectEnvCoversProfileInjectedContainer(t *testing.T) {
	scheme := kep531Scheme()
	profile := kep531Profile("p1", "img")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()

	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{Name: "r", EngineRuntimes: []workloadsv1alpha2.EngineRuntime{{ProfileName: "p1"}}},
			},
		},
	}
	podSpec := &v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "main"}}}}
	injector := NewDefaultInjector(scheme, cl)
	// same order as pod_reconciler.go: sidecar stage, then env stage
	if err := injector.InjectSidecar(context.Background(), podSpec, rbg, &rbg.Spec.Roles[0]); err != nil {
		t.Fatalf("InjectSidecar failed: %v", err)
	}
	if err := injector.InjectEnv(context.Background(), podSpec, rbg, &rbg.Spec.Roles[0]); err != nil {
		t.Fatalf("InjectEnv failed: %v", err)
	}
	c := findContainer(podSpec, "p1-rt")
	if c == nil {
		t.Fatal("profile container not injected")
	}
	if got := envValues(c, constants.EnvRBGRoleName); len(got) != 1 {
		t.Errorf("profile-injected container must receive built-in RBG_* env, got %v", got)
	}
}

// C9: mergeEnvVars deduplicates last-wins per name and reorders
// (RBG_* base -> this batch -> RBG_LWP_* -> user vars).
func TestKEP531_MergeEnvVarsLastWinsAndReorders(t *testing.T) {
	existing := []v1.EnvVar{
		{Name: "USER_VAR", Value: "u"},
		{Name: "RBG_LWP_X", Value: "lwp"},
		{Name: "RBG_ROLE_NAME", Value: "r"},
		{Name: "DUP", Value: "old"},
	}
	injected := []v1.EnvVar{
		{Name: "INJ", Value: "1"},
		{Name: "DUP", Value: "new"},
	}
	merged := mergeEnvVars(existing, injected)
	var order []string
	val := map[string]string{}
	for _, e := range merged {
		order = append(order, e.Name)
		val[e.Name] = e.Value
	}
	want := []string{"RBG_ROLE_NAME", "INJ", "DUP", "RBG_LWP_X", "USER_VAR"}
	if len(order) != len(want) {
		t.Fatalf("merged env = %v, want order %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("merged env order = %v, want %v", order, want)
		}
	}
	if val["DUP"] != "new" {
		t.Errorf("dedup must be last-wins, DUP=%v", val["DUP"])
	}
}
