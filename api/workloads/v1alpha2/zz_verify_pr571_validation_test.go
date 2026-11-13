/*
Verification harness for PR #571 (KEP-531). Reviewer: codex. Additive only.

F3 (contract): ValidateEngineRuntimes — the shared role-side validation that
rides the RBG/RBGSet webhooks at write time and the resolver at render time.
Pins the presence rule (D7), scope ownership (D6) with the compatibility
exemption, the component vocabulary check, the deprecated-surface rejection,
and the intra-list duplicate env-name rule.
*/

package v1alpha2

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/rbgs/api/workloads/constants"
)

func vPatch(path string, value any) ContainerPatch {
	raw, _ := json.Marshal(value)
	return ContainerPatch{JSONPatch: &ContainerJSONPatch{Op: "add", Path: path, Value: runtime.RawExtension{Raw: raw}}}
}

func vLwpRole(name string, ers ...EngineRuntime) *RoleSpec {
	return &RoleSpec{
		Name:           name,
		Pattern:        Pattern{LeaderWorkerPattern: &LeaderWorkerPattern{}},
		EngineRuntimes: ers,
	}
}

func vStandaloneRole(name string, ers ...EngineRuntime) *RoleSpec {
	return &RoleSpec{
		Name:           name,
		Pattern:        Pattern{StandalonePattern: &StandalonePattern{}},
		EngineRuntimes: ers,
	}
}

func vCcpRole(name string, components []string, ers ...EngineRuntime) *RoleSpec {
	comps := make([]InstanceComponent, len(components))
	for i, c := range components {
		comps[i] = InstanceComponent{Name: c}
	}
	return &RoleSpec{
		Name:           name,
		Pattern:        Pattern{CustomComponentsPattern: &CustomComponentsPattern{Components: comps}},
		EngineRuntimes: ers,
	}
}

func TestVerifyPR571_PresenceRule(t *testing.T) {
	withTargets := EngineRuntime{
		ProfileName:      "p",
		TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}},
	}
	cases := []struct {
		name    string
		role    *RoleSpec
		wantErr string
	}{
		{"lwp: targetContainers without component rejected", vLwpRole("lg", withTargets), "targetContainers requires component"},
		{"ccp: targetContainers without component rejected", vCcpRole("m", []string{"a"}, withTargets), "targetContainers requires component"},
		{"standalone: targetContainers without component accepted (exemption)", vStandaloneRole("solo", withTargets), ""},
		{"lwp: targetContainers with component accepted", vLwpRole("lg", EngineRuntime{
			ProfileName: "p", Component: "leader",
			TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}},
		}), ""},
		{"no engine runtimes passes", vLwpRole("lg"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEngineRuntimes(tc.role)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("expected %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestVerifyPR571_ScopeOwnership(t *testing.T) {
	cases := []struct {
		name    string
		role    *RoleSpec
		wantErr string
	}{
		{
			"same profile, overlapping unscoped refs with targetContainers rejected",
			vStandaloneRole("solo",
				EngineRuntime{ProfileName: "p", TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}}},
				EngineRuntime{ProfileName: "p", TargetContainers: []EngineRuntimeTargetContainer{{Name: "other"}}}),
			"must not have overlapping effective component sets",
		},
		{
			"compatibility pair: both unscoped, no targetContainers — accepted",
			vLwpRole("lg",
				EngineRuntime{ProfileName: "p"},
				EngineRuntime{ProfileName: "p"}),
			"",
		},
		{
			"lwp: unscoped (leader) + component worker — disjoint, accepted",
			vLwpRole("lg",
				EngineRuntime{ProfileName: "p"},
				EngineRuntime{ProfileName: "p", Component: "worker"}),
			"",
		},
		{
			"lwp: unscoped (leader) + component leader — overlap, rejected",
			vLwpRole("lg",
				EngineRuntime{ProfileName: "p"},
				EngineRuntime{ProfileName: "p", Component: "leader"}),
			"overlapping",
		},
		{
			"ccp: unscoped covers all components, so any scoped same-profile ref overlaps",
			vCcpRole("m", []string{"a", "b"},
				EngineRuntime{ProfileName: "p"},
				EngineRuntime{ProfileName: "p", Component: "a"}),
			"overlapping",
		},
		{
			"different profiles never overlap",
			vLwpRole("lg",
				EngineRuntime{ProfileName: "p1"},
				EngineRuntime{ProfileName: "p2"}),
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEngineRuntimes(tc.role)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("expected %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestVerifyPR571_ComponentVocabulary(t *testing.T) {
	cases := []struct {
		name    string
		role    *RoleSpec
		wantErr string
	}{
		{"lwp ghost component rejected", vLwpRole("lg", EngineRuntime{ProfileName: "p", Component: "ghost"}), "not defined by the role's pattern"},
		{"lwp leader accepted", vLwpRole("lg", EngineRuntime{ProfileName: "p", Component: "leader"}), ""},
		{"lwp worker accepted", vLwpRole("lg", EngineRuntime{ProfileName: "p", Component: "worker"}), ""},
		{"standalone component must equal role name", vStandaloneRole("solo", EngineRuntime{ProfileName: "p", Component: "other"}), "must equal the role's name"},
		{"standalone component equal to role name accepted", vStandaloneRole("solo", EngineRuntime{ProfileName: "p", Component: "solo"}), ""},
		{"standalone non-DNS role name as component accepted", vStandaloneRole("MyRole", EngineRuntime{ProfileName: "p", Component: "MyRole"}), ""},
		{"ccp declared component accepted", vCcpRole("m", []string{"a"}, EngineRuntime{ProfileName: "p", Component: "a"}), ""},
		{"ccp undeclared component rejected", vCcpRole("m", []string{"a"}, EngineRuntime{ProfileName: "p", Component: "b"}), "not defined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEngineRuntimes(tc.role)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("expected %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestVerifyPR571_DeprecatedSurfaceRejection(t *testing.T) {
	deprecated := []string{
		constants.DeploymentWorkloadType,
		constants.StatefulSetWorkloadType,
		constants.LeaderWorkerSetWorkloadType,
	}
	for _, wt := range deprecated {
		t.Run(wt, func(t *testing.T) {
			role := vStandaloneRole("solo", EngineRuntime{ProfileName: "p", Component: "solo"})
			role.Annotations = map[string]string{constants.RoleWorkloadTypeAnnotationKey: wt}
			err := ValidateEngineRuntimes(role)
			if err == nil || !strings.Contains(err.Error(), "RoleInstanceSet render path") {
				t.Fatalf("expected deprecated-surface rejection, got %v", err)
			}
		})
	}
	t.Run("scenario-1 reference on deprecated surface stays legal", func(t *testing.T) {
		role := vStandaloneRole("solo", EngineRuntime{ProfileName: "p"})
		role.Annotations = map[string]string{constants.RoleWorkloadTypeAnnotationKey: constants.StatefulSetWorkloadType}
		if err := ValidateEngineRuntimes(role); err != nil {
			t.Fatalf("scenario-1 reference must stay legal on deprecated surfaces, got %v", err)
		}
	})
	t.Run("RIS default (no annotation) accepted", func(t *testing.T) {
		role := vStandaloneRole("solo", EngineRuntime{ProfileName: "p", Component: "solo"})
		if err := ValidateEngineRuntimes(role); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})
}

func TestVerifyPR571_IntraListDuplicateEnvCreation(t *testing.T) {
	dup := EngineRuntime{
		ProfileName: "p", Component: "solo",
		TargetContainers: []EngineRuntimeTargetContainer{{
			Name: "engine",
			ExtraPatches: []ContainerPatch{
				vPatch("/env/-", corev1.EnvVar{Name: "X", Value: "1"}),
				vPatch("/env/-", corev1.EnvVar{Name: "X", Value: "2"}),
			},
		}},
	}
	err := ValidateEngineRuntimes(vStandaloneRole("solo", dup))
	if err == nil || !strings.Contains(err.Error(), "duplicate env name") {
		t.Fatalf("expected duplicate env name error, got %v", err)
	}

	// same name across DIFFERENT entries' lists is legal (reference order resolves)
	ok := EngineRuntime{
		ProfileName: "p", Component: "solo",
		TargetContainers: []EngineRuntimeTargetContainer{
			{Name: "engine", ExtraPatches: []ContainerPatch{vPatch("/env/-", corev1.EnvVar{Name: "X", Value: "1"})}},
			{Name: "other", ExtraPatches: []ContainerPatch{vPatch("/env/-", corev1.EnvVar{Name: "X", Value: "2"})}},
		},
	}
	if err := ValidateEngineRuntimes(vStandaloneRole("solo", ok)); err != nil {
		t.Fatalf("same name across different entries must be legal, got %v", err)
	}
}
