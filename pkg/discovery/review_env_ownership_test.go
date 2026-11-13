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

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestReview_EnvOwnershipAfterDefault is the review-harness contract test for
// the KEP-531 profile-side env ownership rule across stacked profiles.
//
// KEP-531 Patch Grammar ("Profile-side entries may only default"): "A
// profile-side entry addressing a role-owned name is legal only at index 0; a
// `-` append or an insert at any other index is a render error." Role-owned
// names are the names the role's own template declares — that status is not
// changed by another profile having defaulted the same name earlier in the
// same render. The envCreationTracker in applyEnvPatch keys its exemption on
// "inserted by a profile-side pass of this render", which conflates a fresh
// name creation (cross-profile positional resolution, any gesture legal) with
// a default onto a role-owned name — so after profile A defaults the
// role-owned name X at /env/0, profile B's `-` append onto X is admitted, and
// B's value lands last and wins the name under kubelet's last-occurrence
// resolution: profile content overriding role content, the exact outcome the
// ownership boundary exists to refuse.
//
// Polarity: CONTRACT — fails on the code under review (the reproduction),
// passes once the tracker records only names the container did not already
// carry.
func TestReview_EnvOwnershipAfterDefault(t *testing.T) {
	ctx := log.IntoContext(context.Background(), logr.Discard())

	build := func() *v1.PodTemplateSpec {
		spec := basePodSpec()
		spec.Spec.Containers[0].Env = []v1.EnvVar{{Name: "PYTHONPATH", Value: "/usr/lib/python"}}
		return spec
	}

	entries := func() []EngineRuntimePatch {
		return []EngineRuntimePatch{
			{
				ProfileName: "accel",
				Targets:     []string{"engine"},
				Patches: []v1alpha2.ContainerPatch{
					jp("/env/0", v1.EnvVar{Name: "PYTHONPATH", Value: "/opt/accel"}),
				},
			},
			{
				ProfileName: "telemetry",
				Targets:     []string{"engine"},
				Patches: []v1alpha2.ContainerPatch{
					jp("/env/-", v1.EnvVar{Name: "PYTHONPATH", Value: "/opt/telemetry"}),
				},
			},
		}
	}

	t.Run("profile B appending a role-owned name is a render error even after profile A defaulted it", func(t *testing.T) {
		spec := build()
		_, err := ApplyEngineRuntimePatches(ctx, spec, roleContainers("engine"), nil, "worker",
			entries(), &EngineRuntimePlan{}, nil, nil)
		if err == nil {
			t.Fatalf("expected a render error: profile %q appends env PYTHONPATH, a name the role's own "+
				"template declares, at /env/- — KEP-531 makes that a render error regardless of profile "+
				"%q having defaulted the same name at /env/0 earlier in this render.\n"+
				"Rendered env (profile content won the role-owned name): %v",
				"telemetry", "accel", spec.Spec.Containers[0].Env)
		}
	})

	// The single-profile case the PR's own tests already pin — the control
	// proving the harness exercises the same rule.
	t.Run("control: a lone profile appending a role-owned name errors", func(t *testing.T) {
		spec := build()
		_, err := ApplyEngineRuntimePatches(ctx, spec, roleContainers("engine"), nil, "worker",
			[]EngineRuntimePatch{{
				ProfileName: "accel",
				Targets:     []string{"engine"},
				Patches:     []v1alpha2.ContainerPatch{jp("/env/-", v1.EnvVar{Name: "PYTHONPATH", Value: "/opt/accel"})},
			}}, &EngineRuntimePlan{}, nil, nil)
		if err == nil {
			t.Fatal("expected the known-good single-profile rejection to fire; harness is not biting")
		}
	})

	// The legal case the fix must keep: a name no side carried before is
	// profile-authored vocabulary, and cross-profile positional resolution
	// applies — B may append onto a name A created fresh.
	t.Run("control: cross-profile stacking on a fresh name stays legal", func(t *testing.T) {
		spec := basePodSpec()
		_, err := ApplyEngineRuntimePatches(ctx, spec, roleContainers("engine"), nil, "worker",
			[]EngineRuntimePatch{
				{
					ProfileName: "accel",
					Targets:     []string{"engine"},
					Patches:     []v1alpha2.ContainerPatch{jp("/env/-", v1.EnvVar{Name: "IK_SITE", Value: "/opt/accel"})},
				},
				{
					ProfileName: "telemetry",
					Targets:     []string{"engine"},
					Patches:     []v1alpha2.ContainerPatch{jp("/env/-", v1.EnvVar{Name: "IK_SITE", Value: "/opt/telemetry"})},
				},
			}, &EngineRuntimePlan{}, nil, nil)
		if err != nil {
			t.Fatalf("fresh-name cross-profile stacking must stay legal: %v", err)
		}
	})
}
