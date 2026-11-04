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

// KEP-531 verification harness (reviewer-side, additive only).
//
// Pins issue #531 gap 2 at the wiring level: a PodReconciler configured like
// the leaderWorkerPattern worker chain (roleinstanceset_reconciler.go /
// lws_reconciler.go: SetInjectors without sidecarInjector) injects zero
// engine-runtime profile content, while the leader chain (sidecarInjector
// present, or the default set) injects it. This is the asymmetry KEP-531's
// "absent component = leader-only default under leaderWorkerPattern" records.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	coreapplyv1 "k8s.io/client-go/applyconfigurations/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func containerNames(spec *coreapplyv1.PodSpecApplyConfiguration) []string {
	if spec == nil || spec.Containers == nil {
		return nil
	}
	names := make([]string, 0, len(spec.Containers))
	for _, c := range spec.Containers {
		names = append(names, *c.Name)
	}
	return names
}

func TestKEP531_Verify_WorkerChainSkipsSidecarInjection(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = workloadsv1alpha2.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)

	profile := &workloadsv1alpha2.ClusterEngineRuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p"},
		Spec: workloadsv1alpha2.ClusterEngineRuntimeProfileSpec{
			InitContainers: []v1.Container{{Name: "runtime-init", Image: "profile/init:latest"}},
			Containers:     []v1.Container{{Name: "runtime-sidecar", Image: "profile/sidecar:latest"}},
			Volumes:        []v1.Volume{{Name: "runtime-volume"}},
		},
	}
	tpl := v1.PodTemplateSpec{Spec: v1.PodSpec{
		Containers: []v1.Container{{Name: "w", Image: "w"}},
	}}
	role := workloadsv1alpha2.RoleSpec{
		Name: "lg",
		Pattern: workloadsv1alpha2.Pattern{
			StandalonePattern: &workloadsv1alpha2.StandalonePattern{
				TemplateSource: workloadsv1alpha2.TemplateSource{Template: &tpl},
			},
		},
		EngineRuntimes: []workloadsv1alpha2.EngineRuntime{{ProfileName: "p"}},
	}
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "kep531-rbg"},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{role}},
	}

	build := func(injectors []string) []string {
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()
		r := NewPodReconciler(scheme, client)
		if injectors != nil {
			r.SetInjectors(injectors)
		}
		applyCfg, err := r.ConstructPodTemplateSpecApplyConfiguration(
			context.TODO(), rbg, &role, map[string]string{},
		)
		assert.NoError(t, err)
		assert.NotNil(t, applyCfg)
		return containerNames(applyCfg.Spec)
	}

	// The worker-style chain (exactly what roleinstanceset_reconciler.go:426
	// and lws_reconciler.go:282 configure for the worker template):
	// config + commonEnv, no sidecarInjector.
	worker := build([]string{configInjector, commonEnvInjector})
	assert.Equal(t, []string{"w"}, worker,
		"worker chain (no sidecarInjector) must receive zero profile containers (issue #531 gap 2)")

	// The leader-style chain (roleinstanceset_reconciler.go:407):
	// config + sidecar + commonEnv.
	leader := build([]string{configInjector, sidecarInjector, commonEnvInjector})
	assert.ElementsMatch(t, []string{"w", "runtime-sidecar"}, leader,
		"leader chain (sidecarInjector present) must receive the profile sidecar")
}
