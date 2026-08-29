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

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// TestRoleInstanceSetReconciler_RestartPolicyShapePreserved guards Cause 1 of PR #439:
// upgrading a v0.7.0 install (which stored the deprecated restartPolicy string) must not
// rewrite the stored RoleInstanceSet template into restartPolicyConfig, because that flip
// moves the revision hash and rolls the role with nothing to roll to.
//
// The fix keeps the legacy string whenever the role configures no backoff delays (which that
// string cannot express) and only writes restartPolicyConfig when the role actually sets a
// delay. v0.7.0 has no backoff field at all, so every v0.7.0-origin role lands in the string
// branch — byte-for-byte what v0.7.0 stored, hence no spurious rollout.
//
// Polarity: CONTRACT. PASS on the fix. FAIL on the pre-fix code that wrote
// restartPolicyConfig unconditionally (the config object would be non-nil for the no-backoff
// cases).
func TestRoleInstanceSetReconciler_RestartPolicyShapePreserved(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = workloadsv1alpha2.AddToScheme(scheme)

	// Inline-template LeaderWorker role; templates live on the rbg, so no client lookups.
	baseTemplate := wrappersv2.BuildBasicPodTemplateSpec()
	roleTemplate := workloadsv1alpha2.RoleTemplate{
		Name:     "restart-policy-template",
		Template: baseTemplate,
	}
	emptyPatch := &runtime.RawExtension{Raw: []byte("{}")}

	// roleFor builds a LeaderWorker role whose only varying axis is the restart-policy
	// configuration, then runs one reconcile and returns the stored RoleInstanceSet template.
	roleFor := func(t *testing.T, configure func(rw *wrappersv2.LeaderWorkerRoleWrapper)) workloadsv1alpha2.RoleInstanceTemplate {
		t.Helper()
		rw := wrappersv2.BuildLeaderWorkerRole("restart-policy-role").
			WithReplicas(1).
			WithWorkload("workloads.x-k8s.io/v1alpha2", "RoleInstanceSet")
		rw.LeaderWorkerPattern = &workloadsv1alpha2.LeaderWorkerPattern{
			Size:                ptr.To(int32(1)),
			LeaderTemplatePatch: emptyPatch,
			WorkerTemplatePatch: emptyPatch,
			TemplateSource: workloadsv1alpha2.TemplateSource{
				TemplateRef: &workloadsv1alpha2.TemplateRef{
					Name:  "restart-policy-template",
					Patch: emptyPatch,
				},
			},
		}
		configure(rw)
		role := rw.Obj()

		rbg := wrappersv2.BuildBasicRoleBasedGroup("restart-policy-rbg", "default").
			WithRoles([]workloadsv1alpha2.RoleSpec{role}).
			WithRoleTemplates([]workloadsv1alpha2.RoleTemplate{roleTemplate}).
			Obj()

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		reconciler := NewRoleInstanceSetReconciler(scheme, fakeClient)

		ctx := context.Background()
		assert.NoError(t, reconciler.Reconciler(ctx, rbg, &role, nil, "rev-1"))

		ris := &workloadsv1alpha2.RoleInstanceSet{}
		assert.NoError(t, fakeClient.Get(ctx,
			types.NamespacedName{Name: rbg.GetWorkloadName(&role), Namespace: rbg.Namespace}, ris))
		return ris.Spec.RoleInstanceTemplate
	}

	defaultType := workloadsv1alpha2.RecreateRoleInstanceOnPodRestart

	testCases := []struct {
		name             string
		configure        func(rw *wrappersv2.LeaderWorkerRoleWrapper)
		wantString       workloadsv1alpha2.RestartPolicyType // expected deprecated string field; "" => not asserted
		wantConfigNil    bool                               // true => RestartPolicyConfig must be nil (legacy string form)
		wantBaseDelay    *int32                             // for the config-form case
		wantMaxDelay     *int32
	}{
		{
			// The v0.7.0 representative: no backoff field exists at all. Must stay legacy string.
			name:          "no backoff configured keeps the deprecated restartPolicy string",
			configure:     func(rw *wrappersv2.LeaderWorkerRoleWrapper) {},
			wantString:    defaultType,
			wantConfigNil: true,
		},
		{
			// A role that set only the deprecated string (also a v0.7.0 shape) stays a string.
			name:          "legacy restartPolicy string stays the string form",
			configure:     func(rw *wrappersv2.LeaderWorkerRoleWrapper) { rw.WithLegacyRestartPolicy(defaultType) },
			wantString:    defaultType,
			wantConfigNil: true,
		},
		{
			// type-only modern config (no delays): the string can express it losslessly, so the
			// fix stores the string. Stable and lossless; no backoff to lose.
			name:          "restartPolicyConfig with type only stays the string form",
			configure:     func(rw *wrappersv2.LeaderWorkerRoleWrapper) { rw.WithRestartPolicy(workloadsv1alpha2.RestartPolicyNone) },
			wantString:    workloadsv1alpha2.RestartPolicyNone,
			wantConfigNil: true,
		},
		{
			// Real backoff delays: only the config object can express them, so it is written.
			name:          "restartPolicyConfig with delays writes the config form",
			configure:     func(rw *wrappersv2.LeaderWorkerRoleWrapper) {
				rw.WithRestartPolicy(defaultType).WithBaseDelaySeconds(10).WithMaxDelaySeconds(120)
			},
			wantString:    "", // string is not the carrier here
			wantConfigNil: false,
			wantBaseDelay: ptr.To(int32(10)),
			wantMaxDelay:  ptr.To(int32(120)),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := roleFor(t, tc.configure)

			if tc.wantConfigNil {
				assert.Nil(t, tmpl.RestartPolicyConfig,
					"RestartPolicyConfig must be nil so the legacy string form is preserved (no spurious revision flip on upgrade)")
			} else {
				if assert.NotNil(t, tmpl.RestartPolicyConfig, "RestartPolicyConfig must carry the configured backoff") {
					assert.Equal(t, tc.wantBaseDelay, tmpl.RestartPolicyConfig.BaseDelaySeconds, "base delay")
					assert.Equal(t, tc.wantMaxDelay, tmpl.RestartPolicyConfig.MaxDelaySeconds, "max delay")
				}
			}
			if tc.wantString != "" {
				assert.Equal(t, tc.wantString, tmpl.RestartPolicy, "deprecated restartPolicy string field")
			}
		})
	}
}

// TestRoleInstanceSetReconciler_RestartPolicyStableAcrossReconciles guards against the form
// flipping between reconciles for a stable role spec. Two reconciles of the same no-backoff
// role must produce the same restart-policy representation, so the revision hash does not move
// on a no-op reconcile.
func TestRoleInstanceSetReconciler_RestartPolicyStableAcrossReconciles(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = workloadsv1alpha2.AddToScheme(scheme)

	baseTemplate := wrappersv2.BuildBasicPodTemplateSpec()
	roleTemplate := workloadsv1alpha2.RoleTemplate{Name: "stability-template", Template: baseTemplate}
	emptyPatch := &runtime.RawExtension{Raw: []byte("{}")}

	rw := wrappersv2.BuildLeaderWorkerRole("stability-role").
		WithReplicas(1).
		WithWorkload("workloads.x-k8s.io/v1alpha2", "RoleInstanceSet")
	rw.LeaderWorkerPattern = &workloadsv1alpha2.LeaderWorkerPattern{
		Size:                ptr.To(int32(1)),
		LeaderTemplatePatch: emptyPatch,
		WorkerTemplatePatch: emptyPatch,
		TemplateSource: workloadsv1alpha2.TemplateSource{
			TemplateRef: &workloadsv1alpha2.TemplateRef{Name: "stability-template", Patch: emptyPatch},
		},
	}
	role := rw.Obj()
	rbg := wrappersv2.BuildBasicRoleBasedGroup("stability-rbg", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).
		WithRoleTemplates([]workloadsv1alpha2.RoleTemplate{roleTemplate}).
		Obj()

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := NewRoleInstanceSetReconciler(scheme, fakeClient)
	ctx := context.Background()

	assert.NoError(t, reconciler.Reconciler(ctx, rbg, &role, nil, "rev-1"))
	ris1 := &workloadsv1alpha2.RoleInstanceSet{}
	assert.NoError(t, fakeClient.Get(ctx,
		types.NamespacedName{Name: rbg.GetWorkloadName(&role), Namespace: rbg.Namespace}, ris1))
	firstRP, firstCfg := ris1.Spec.RoleInstanceTemplate.RestartPolicy, ris1.Spec.RoleInstanceTemplate.RestartPolicyConfig

	assert.NoError(t, reconciler.Reconciler(ctx, rbg, &role, nil, "rev-1")) // same revision, no-op
	ris2 := &workloadsv1alpha2.RoleInstanceSet{}
	assert.NoError(t, fakeClient.Get(ctx,
		types.NamespacedName{Name: rbg.GetWorkloadName(&role), Namespace: rbg.Namespace}, ris2))

	assert.Equal(t, firstRP, ris2.Spec.RoleInstanceTemplate.RestartPolicy,
		"deprecated restartPolicy string must not flip across reconciles")
	assert.Equal(t, firstCfg, ris2.Spec.RoleInstanceTemplate.RestartPolicyConfig,
		"RestartPolicyConfig must not flip across reconciles")
}
