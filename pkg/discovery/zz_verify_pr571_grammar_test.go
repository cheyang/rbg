/*
Verification harness for PR #571 (KEP-531). Reviewer: codex. Additive only.

F2 (contract): the patch grammar's enforcement points — profile-side root
allowlist, entrypoint-merge rule, RBG_ env prefix reservation, volumeMount
invariants, and the profile-side "may only default a role-owned name" rule.

F5 (canary): cross-profile container-name collision + role-side extraPatches
on the skipped profile's entry — documents where the patches actually land.
*/

package discovery

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func verifyPatch(path string, value any) workloadsv1alpha2.ContainerPatch {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return workloadsv1alpha2.ContainerPatch{
		JSONPatch: &workloadsv1alpha2.ContainerJSONPatch{Op: "add", Path: path, Value: runtime.RawExtension{Raw: raw}},
	}
}

func verifyRoleDeclared() (map[string]struct{}, map[string]struct{}) {
	return map[string]struct{}{"engine": {}}, map[string]struct{}{}
}

// TestVerifyPR571_ProfileSideRootAllowlist: /args and
// /securityContext/capabilities/add are role-side-only; /command/<i> with
// i>0 is role-side-only; /env, /volumeMounts, /command/0 are legal.
func TestVerifyPR571_ProfileSideRootAllowlist(t *testing.T) {
	mkContainer := func() *v1.Container {
		return &v1.Container{Name: "engine", Image: "img:1",
			Command: []string{"python"}, Args: []string{"-m", "x"},
			Env: []v1.EnvVar{{Name: "A", Value: "1"}}}
	}
	cases := []struct {
		name      string
		path      string
		value     any
		roleSide  bool
		wantError string // substring; empty = expect success
	}{
		{"profile /args/- rejected", "/args/-", "--flag", false, "role-side-only"},
		{"profile /command/1 rejected", "/command/1", "x", false, "/command/0 only"},
		{"profile capabilities rejected", "/securityContext/capabilities/add/-", "SYS_PTRACE", false, "role-side-only"},
		{"profile /command/0 allowed", "/command/0", "wrap", true == false, ""},
		{"profile /env/- allowed for a fresh name", "/env/-", v1.EnvVar{Name: "NEW", Value: "v"}, false, ""},
		{"role /args/0 allowed", "/args/0", "--flag", true, ""},
		{"role capabilities allowed", "/securityContext/capabilities/add/-", "SYS_PTRACE", true, ""},
		{"role /command/1 allowed", "/command/1", "tail", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := mkContainer()
			pc := &patchContext{
				volumeNames:   map[string]struct{}{},
				appendedEnv:   &envCreationTracker{},
				source:        profilePatchSource("p"),
				containerName: "engine",
				roleSide:      tc.roleSide,
			}
			if tc.roleSide {
				pc.source = rolePatchSource("role", "p", "engine")
			}
			err := applyJSONPatch(pc, c, *verifyPatch(tc.path, tc.value).JSONPatch, 0)
			if tc.wantError == "" && err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			if tc.wantError != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantError)
				}
				if !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantError)
				}
			}
		})
	}
}

// TestVerifyPR571_EntrypointMergeRule: creating command is always an error;
// creating args is an error only when neither command nor args is set.
func TestVerifyPR571_EntrypointMergeRule(t *testing.T) {
	cases := []struct {
		name      string
		container *v1.Container
		path      string
		value     any
		wantError string
	}{
		{"command insert into command-less container errors",
			&v1.Container{Name: "engine", Args: []string{"-m", "x"}}, "/command/0", "wrap", "declares no command"},
		{"command insert into fully bare container errors",
			&v1.Container{Name: "engine"}, "/command/0", "wrap", "declares no command"},
		{"args insert with command set succeeds",
			&v1.Container{Name: "engine", Command: []string{"python"}}, "/args/0", "--flag", ""},
		{"args insert with neither set errors",
			&v1.Container{Name: "engine"}, "/args/0", "--flag", "declares neither command nor args"},
		{"args append with args set succeeds",
			&v1.Container{Name: "engine", Args: []string{"-m", "x"}}, "/args/-", "--flag", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pc := &patchContext{
				volumeNames:   map[string]struct{}{},
				appendedEnv:   &envCreationTracker{},
				source:        rolePatchSource("role", "p", "engine"),
				containerName: "engine",
				roleSide:      true,
			}
			err := applyJSONPatch(pc, tc.container, *verifyPatch(tc.path, tc.value).JSONPatch, 0)
			if tc.wantError == "" && err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected error containing %q, got %v", tc.wantError, err)
				}
			}
		})
	}
}

// TestVerifyPR571_ProfileSideEnvDefaultOnly: a profile-side entry addressing
// a role-owned env name is legal only at /env/0; append is a render error.
// A role-side append onto the same name is the override gesture and lands.
func TestVerifyPR571_ProfileSideEnvDefaultOnly(t *testing.T) {
	mkContainer := func() *v1.Container {
		return &v1.Container{Name: "engine", Env: []v1.EnvVar{{Name: "ROLE", Value: "role"}}}
	}
	// profile-side append onto role-owned name -> error
	pc := &patchContext{volumeNames: map[string]struct{}{}, appendedEnv: &envCreationTracker{},
		source: profilePatchSource("p"), containerName: "engine"}
	err := applyJSONPatch(pc, mkContainer(), *verifyPatch("/env/-", v1.EnvVar{Name: "ROLE", Value: "p"}).JSONPatch, 0)
	if err == nil || !strings.Contains(err.Error(), "may only default") {
		t.Fatalf("profile append onto role-owned name: expected 'may only default' error, got %v", err)
	}
	// profile-side non-zero index onto role-owned name -> error
	pc = &patchContext{volumeNames: map[string]struct{}{}, appendedEnv: &envCreationTracker{},
		source: profilePatchSource("p"), containerName: "engine"}
	err = applyJSONPatch(pc, mkContainer(), *verifyPatch("/env/1", v1.EnvVar{Name: "ROLE", Value: "p"}).JSONPatch, 0)
	if err == nil {
		t.Fatalf("profile /env/1 insert onto role-owned name: expected error, got nil")
	}
	// profile-side /env/0 head insert -> allowed, lands before, role's entry wins by position
	c := mkContainer()
	pc = &patchContext{volumeNames: map[string]struct{}{}, appendedEnv: &envCreationTracker{},
		source: profilePatchSource("p"), containerName: "engine"}
	if err := applyJSONPatch(pc, c, *verifyPatch("/env/0", v1.EnvVar{Name: "ROLE", Value: "p"}).JSONPatch, 0); err != nil {
		t.Fatalf("profile /env/0 default: %v", err)
	}
	if len(c.Env) != 2 || c.Env[0].Value != "p" || c.Env[1].Value != "role" {
		t.Fatalf("head insert geometry wrong: %+v", c.Env)
	}
	// role-side append wins by position
	pc = &patchContext{volumeNames: map[string]struct{}{},
		source: rolePatchSource("role", "p", "engine"), containerName: "engine", roleSide: true}
	if err := applyJSONPatch(pc, c, *verifyPatch("/env/-", v1.EnvVar{Name: "ROLE", Value: "override"}).JSONPatch, 0); err != nil {
		t.Fatalf("role override append: %v", err)
	}
	if c.Env[len(c.Env)-1].Value != "override" {
		t.Fatalf("role-side append did not land last: %+v", c.Env)
	}
}

// TestVerifyPR571_RBGPrefixReserved on both sides.
func TestVerifyPR571_RBGPrefixReserved(t *testing.T) {
	for _, roleSide := range []bool{false, true} {
		c := &v1.Container{Name: "engine"}
		src := profilePatchSource("p")
		if roleSide {
			src = rolePatchSource("role", "p", "engine")
		}
		pc := &patchContext{volumeNames: map[string]struct{}{}, appendedEnv: &envCreationTracker{},
			source: src, containerName: "engine", roleSide: roleSide}
		err := applyJSONPatch(pc, c, *verifyPatch("/env/-", v1.EnvVar{Name: "RBG_ROLE_NAME", Value: "x"}).JSONPatch, 0)
		if err == nil || !strings.Contains(err.Error(), "RBG_ prefix is reserved") {
			t.Fatalf("roleSide=%v: expected RBG_ reservation error, got %v", roleSide, err)
		}
	}
}

// TestVerifyPR571_VolumeMountInvariants: dangling volume, reserved config
// volume, duplicate mountPath across volumes, partial re-add, equal re-add.
func TestVerifyPR571_VolumeMountInvariants(t *testing.T) {
	mk := func() (*v1.Container, *patchContext) {
		c := &v1.Container{Name: "engine", VolumeMounts: []v1.VolumeMount{{Name: "data", MountPath: "/data"}}}
		pc := &patchContext{
			volumeNames:   map[string]struct{}{"data": {}, "cache": {}, "rbg-cluster-config": {}},
			appendedEnv:   &envCreationTracker{},
			source:        rolePatchSource("role", "p", "engine"),
			containerName: "engine", roleSide: true,
		}
		return c, pc
	}
	// dangling volume name
	c, pc := mk()
	err := applyJSONPatch(pc, c, *verifyPatch("/volumeMounts/-", v1.VolumeMount{Name: "ghost", MountPath: "/g"}).JSONPatch, 0)
	if err == nil || !strings.Contains(err.Error(), "declared by neither") {
		t.Fatalf("dangling volume: %v", err)
	}
	// reserved config volume
	c, pc = mk()
	err = applyJSONPatch(pc, c, *verifyPatch("/env/-", v1.EnvVar{Name: "Z", Value: "1"}).JSONPatch, 0)
	_ = err
	c, pc = mk()
	err = applyJSONPatch(pc, c, *verifyPatch("/volumeMounts/-", v1.VolumeMount{Name: "rbg-cluster-config", MountPath: "/cfg"}).JSONPatch, 0)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved config volume: %v", err)
	}
	// mountPath already used by a different volume
	c, pc = mk()
	err = applyJSONPatch(pc, c, *verifyPatch("/volumeMounts/-", v1.VolumeMount{Name: "cache", MountPath: "/data"}).JSONPatch, 0)
	if err == nil || !strings.Contains(err.Error(), "already used by a different volume") {
		t.Fatalf("mountPath collision: %v", err)
	}
	// partial re-add (same key, differing fields) -> error
	c, pc = mk()
	err = applyJSONPatch(pc, c, *verifyPatch("/volumeMounts/-", v1.VolumeMount{Name: "data", MountPath: "/data", ReadOnly: true}).JSONPatch, 0)
	if err == nil || !strings.Contains(err.Error(), "differing") {
		t.Fatalf("partial re-add: %v", err)
	}
	// full semantic re-add -> idempotent no-op
	c, pc = mk()
	if err := applyJSONPatch(pc, c, *verifyPatch("/volumeMounts/-", v1.VolumeMount{Name: "data", MountPath: "/data"}).JSONPatch, 0); err != nil {
		t.Fatalf("equal re-add: %v", err)
	}
	if len(c.VolumeMounts) != 1 {
		t.Fatalf("equal re-add should be a no-op, got %+v", c.VolumeMounts)
	}
	// same volume at a different mountPath -> legal second mount
	c, pc = mk()
	if err := applyJSONPatch(pc, c, *verifyPatch("/volumeMounts/-", v1.VolumeMount{Name: "data", MountPath: "/data2"}).JSONPatch, 0); err != nil {
		t.Fatalf("second mount of same volume: %v", err)
	}
	if len(c.VolumeMounts) != 2 {
		t.Fatalf("second mount did not land: %+v", c.VolumeMounts)
	}
}

// TestVerifyPR571_CollisionSkipThenRolePatch (F5, canary): two profiles
// declare the same sidecar name; the earlier reference wins the fragment
// (KEP Duplicate Delivery). A targetContainers entry on the LATER reference
// naming that container is profile-declared for it; its extraPatches resolve
// by name against the rendered pod and land on the EARLIER profile's
// container. The KEP text says a name "injected only by a different
// reference or profile" is a render error — this canary documents that the
// implementation instead patches the other profile's container.
func TestVerifyPR571_CollisionSkipThenRolePatch(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	profileA := verifyProfile("a")
	profileA.Spec.Containers = append(profileA.Spec.Containers, v1.Container{Name: "shared", Image: "a:1"})
	profileB := verifyProfile("b")
	profileB.Spec.Containers = append(profileB.Spec.Containers, v1.Container{Name: "shared", Image: "b:1"})

	role := ccpRoleForGrammar("multi", []string{"main"},
		workloadsv1alpha2.EngineRuntime{ProfileName: "a"},
		workloadsv1alpha2.EngineRuntime{
			ProfileName: "b",
			Component:   "main",
			TargetContainers: []workloadsv1alpha2.EngineRuntimeTargetContainer{{
				Name:         "shared",
				ExtraPatches: []workloadsv1alpha2.ContainerPatch{verifyPatch("/env/-", v1.EnvVar{Name: "FROM_B", Value: "yes"})},
			}},
		})
	rbg := verifyRBGLight(role)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profileA, profileB).Build()
	plan, err := ResolveEngineRuntimes(context.Background(), cl, nil, rbg, role)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	spec := renderNewPosition(t, plan, "main", verifyBaseTemplateLight())

	var shared *v1.Container
	for i := range spec.Spec.Containers {
		if spec.Spec.Containers[i].Name == "shared" {
			shared = &spec.Spec.Containers[i]
		}
	}
	if shared == nil {
		t.Fatal("shared container missing")
	}
	if shared.Image != "a:1" {
		t.Fatalf("expected earlier reference's container to win, got image %q", shared.Image)
	}
	// CANARY: the role-side patch of reference b landed on profile a's container.
	found := false
	for _, e := range shared.Env {
		if e.Name == "FROM_B" {
			found = true
		}
	}
	if !found {
		t.Fatalf("canary flipped: reference b's extraPatches no longer land on profile a's container: %+v", shared.Env)
	}
	t.Logf("CANARY OBSERVED: reference b's extraPatches landed on the container injected by profile a (image a:1)")
}

func ccpRoleForGrammar(name string, components []string, runtimes ...workloadsv1alpha2.EngineRuntime) *workloadsv1alpha2.RoleSpec {
	comps := make([]workloadsv1alpha2.InstanceComponent, len(components))
	for i, c := range components {
		tpl := verifyBaseTemplateLight()
		comps[i] = workloadsv1alpha2.InstanceComponent{Name: c, Template: tpl}
	}
	return &workloadsv1alpha2.RoleSpec{
		Name:           name,
		Pattern:        workloadsv1alpha2.Pattern{CustomComponentsPattern: &workloadsv1alpha2.CustomComponentsPattern{Components: comps}},
		EngineRuntimes: runtimes,
	}
}

func verifyBaseTemplateLight() v1.PodTemplateSpec {
	return v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{
		Name: "engine", Image: "sglang:1", Command: []string{"python"},
	}}}}
}

func verifyRBGLight(role *workloadsv1alpha2.RoleSpec) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify", Namespace: "default"},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{*role}},
	}
}

var _ = client.Object(nil)
