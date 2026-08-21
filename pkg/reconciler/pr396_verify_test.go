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

// Executable proof for the four Copilot review comments on sgl-project/rbg#396.
//
// Copilot's claim (x4): `maps.Clone` returns nil for a nil source map, so the
// four new "clone then write" sites in roleinstanceset_reconciler.go
//
//	componentLabels := maps.Clone(podTemplateApplyConfiguration.Labels)  // ~line 303
//	leaderLabels    := maps.Clone(leaderTemplateApplyCfg.Labels)         // ~line 396
//	workerLabels    := maps.Clone(workerTemplateApplyCfg.Labels)         // ~line 400
//	podLabels       := maps.Clone(podTemplateApplyConfiguration.Labels)  // ~line 440
//
// will panic with "assignment to entry in nil map".
//
// The tests below separate MECHANISM from REACHABILITY:
//   - TestPR396_MapsCloneNilThenWritePanics  -> the mechanism is REAL.
//   - TestPR396_GetCommonLabelsFromRole_...  -> the linchpin that makes it unreachable.
//   - TestPR396_ConstructPodTemplateSpec_... -> .Labels is non-nil in production,
//     WITH a control proving the test would detect the nil case.
//   - TestPR396_ProductionPaths_NoPanic      -> the real functions do not panic.
//   - TestPR396_LeaderWorker_NoLabelCross... -> regression guard for the behaviour change.
//   - TestPR396_MatchLabelsNotMutated...     -> what maps.Clone(matchLabels) actually fixes.

import (
	"context"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	workloadsv1alpha2client "sigs.k8s.io/rbgs/client-go/applyconfiguration/workloads/v1alpha2"
)

// ---------------------------------------------------------------------------
// helpers (idiom copied from roleinstanceset_reconciler_test.go: runtime.NewScheme
// + corev1/workloadsv1alpha2 AddToScheme + fake.NewClientBuilder)
// ---------------------------------------------------------------------------

func pr396Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

func pr396Reconciler(t *testing.T) *RoleInstanceSetReconciler {
	t.Helper()
	scheme := pr396Scheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	return NewRoleInstanceSetReconciler(scheme, fakeClient)
}

// pr396LabelLessTemplate returns a PodTemplateSpec with NO metadata.labels at all.
// This is the worst case for the Copilot finding: nothing in the template can
// populate the apply-config's .Labels, so .Labels is non-nil only if podLabels is.
func pr396LabelLessTemplate() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{}, // deliberately no Labels
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "nginx", Image: "nginx:latest", ImagePullPolicy: corev1.PullIfNotPresent},
			},
		},
	}
}

func pr396RBG(name, ns string, roles ...workloadsv1alpha2.RoleSpec) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		TypeMeta:   metav1.TypeMeta{APIVersion: "workloads.x-k8s.io/v1alpha2", Kind: "RoleBasedGroup"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: roles},
	}
}

func pr396StandaloneRole(name string) workloadsv1alpha2.RoleSpec {
	tpl := pr396LabelLessTemplate()
	return workloadsv1alpha2.RoleSpec{
		Name:     name,
		Replicas: ptr.To(int32(1)),
		Pattern: workloadsv1alpha2.Pattern{
			StandalonePattern: &workloadsv1alpha2.StandalonePattern{
				TemplateSource: workloadsv1alpha2.TemplateSource{Template: &tpl},
			},
		},
	}
}

// pr396LeaderWorkerRole builds a leader/worker role with a label-less base template
// and NO leader/worker template patches, so neither side contributes any labels of
// its own. (test/wrappers BuildLeaderWorkerRole injects role=leader/role=worker
// label patches, which would mask the nil-.Labels case we are probing.)
func pr396LeaderWorkerRole(name string, size int32) workloadsv1alpha2.RoleSpec {
	tpl := pr396LabelLessTemplate()
	return workloadsv1alpha2.RoleSpec{
		Name:     name,
		Replicas: ptr.To(int32(1)),
		Pattern: workloadsv1alpha2.Pattern{
			LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{
				Size:           ptr.To(size),
				TemplateSource: workloadsv1alpha2.TemplateSource{Template: &tpl},
			},
		},
	}
}

func pr396CustomComponentsRole(name string) workloadsv1alpha2.RoleSpec {
	return workloadsv1alpha2.RoleSpec{
		Name:     name,
		Replicas: ptr.To(int32(1)),
		Pattern: workloadsv1alpha2.Pattern{
			CustomComponentsPattern: &workloadsv1alpha2.CustomComponentsPattern{
				Components: []workloadsv1alpha2.InstanceComponent{
					{Name: "prefill", Size: ptr.To(int32(2)), Template: pr396LabelLessTemplate()},
					{Name: "decode", Size: ptr.To(int32(3)), Template: pr396LabelLessTemplate()},
				},
			},
		},
	}
}

func pr396PanicString(v any) string {
	if err, ok := v.(error); ok {
		return err.Error()
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ---------------------------------------------------------------------------
// 1. THE LINCHPIN
// ---------------------------------------------------------------------------

// TestPR396_GetCommonLabelsFromRole_IsNeverNilOrEmpty is THE LINCHPIN of the whole
// dismissal of the four Copilot findings.
//
// Every production call to ConstructPodTemplateSpecApplyConfiguration passes
// rbg.GetCommonLabelsFromRole(role) (directly, or via maps.Clone of it) as
// podLabels. ConstructPodTemplateSpecApplyConfiguration ends with
// `.WithLabels(podLabels)`, and the generated WithLabels only allocates b.Labels
// when len(entries) > 0. So the ONLY way .Labels can be nil at the four clone
// sites is for GetCommonLabelsFromRole to return a nil or empty map.
//
// IF THIS TEST EVER FAILS, THE "FALSE POSITIVE" VERDICT ON ALL FOUR COPILOT
// COMMENTS COLLAPSES AND THEY BECOME REAL, REACHABLE NIL-MAP PANICS.
func TestPR396_GetCommonLabelsFromRole_IsNeverNilOrEmpty(t *testing.T) {
	cases := []struct {
		name string
		rbg  *workloadsv1alpha2.RoleBasedGroup
		role *workloadsv1alpha2.RoleSpec
	}{
		{
			name: "normal rbg and role",
			rbg:  pr396RBG("test-rbg", "default"),
			role: ptr.To(pr396StandaloneRole("worker")),
		},
		{
			name: "zero-valued RoleBasedGroup and zero-valued RoleSpec (empty names)",
			rbg:  &workloadsv1alpha2.RoleBasedGroup{},
			role: &workloadsv1alpha2.RoleSpec{},
		},
		{
			name: "zero-valued RoleBasedGroup with a named role",
			rbg:  &workloadsv1alpha2.RoleBasedGroup{},
			role: &workloadsv1alpha2.RoleSpec{Name: "only-role-name"},
		},
		{
			name: "named rbg with zero-valued role",
			rbg:  pr396RBG("rbg-only", "ns-only"),
			role: &workloadsv1alpha2.RoleSpec{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.rbg.GetCommonLabelsFromRole(tc.role)

			require.NotNil(t, got,
				"LINCHPIN VIOLATED: GetCommonLabelsFromRole returned nil; the four "+
					"maps.Clone sites in roleinstanceset_reconciler.go would then panic")
			require.NotEmpty(t, got,
				"LINCHPIN VIOLATED: GetCommonLabelsFromRole returned an empty map; "+
					"WithLabels would not allocate .Labels and the four maps.Clone "+
					"sites would then panic")
			// It is a 3-key map literal; the keys are always present regardless of values.
			require.Len(t, got, 3, "expected exactly the 3 common label keys")
			for _, k := range []string{
				constants.GroupNameLabelKey,
				constants.RoleNameLabelKey,
				constants.GroupUIDLabelKey,
			} {
				_, ok := got[k]
				assert.Truef(t, ok, "missing common label key %q", k)
			}
			// GroupUIDLabelKey is a sha1 hash of "<ns>/<name>", so it is non-empty even
			// for a zero-valued RBG: len(entries) > 0 is therefore guaranteed twice over.
			assert.NotEmpty(t, got[constants.GroupUIDLabelKey],
				"group-uid is a sha1 hash and must never be empty")
			t.Logf("labels = %v", got)
		})
	}
}

// ---------------------------------------------------------------------------
// 2. THE MECHANISM IS REAL
// ---------------------------------------------------------------------------

// TestPR396_MapsCloneNilThenWritePanics proves Copilot's MECHANISM is correct.
// The finding must NOT be dismissed as "maps.Clone is safe" - it is not.
// It is dismissed only because the input is never nil (see the linchpin test).
func TestPR396_MapsCloneNilThenWritePanics(t *testing.T) {
	t.Run("maps.Clone(nil) returns nil", func(t *testing.T) {
		var src map[string]string // nil
		clone := maps.Clone(src)
		assert.Nil(t, clone, "maps.Clone of a nil map must return nil")
	})

	t.Run("writing to the nil clone panics", func(t *testing.T) {
		var src map[string]string // nil
		clone := maps.Clone(src)

		var recovered any
		func() {
			defer func() { recovered = recover() }()
			// This is exactly the shape of the four flagged sites:
			//   x := maps.Clone(someLabels); x[key] = value
			clone[constants.ComponentSizeLabelKey] = "1"
		}()
		require.NotNil(t, recovered,
			"expected a panic writing to a nil map - Copilot's mechanism is real")
		assert.Contains(t, pr396PanicString(recovered), "nil map",
			"expected an 'assignment to entry in nil map' runtime error")
		t.Logf("MECHANISM CONFIRMED: panic = %v", recovered)
	})

	t.Run("maps.Clone of a non-nil EMPTY map returns non-nil and is writable", func(t *testing.T) {
		src := map[string]string{}
		clone := maps.Clone(src)
		require.NotNil(t, clone,
			"maps.Clone of a non-nil empty map returns a non-nil map (only nil in -> nil out)")
		assert.NotPanics(t, func() { clone["k"] = "v" })
	})
}

// ---------------------------------------------------------------------------
// 3. .Labels IS NON-NIL AFTER THE REAL ConstructPodTemplateSpecApplyConfiguration
//    (with CONTROLS showing the test detects the nil case)
// ---------------------------------------------------------------------------

// TestPR396_ConstructPodTemplateSpec_LabelsNonNil drives the REAL
// PodReconciler.ConstructPodTemplateSpecApplyConfiguration (no reimplementation)
// with a pod template that carries NO labels of its own.
//
// Case A (production): podLabels = rbg.GetCommonLabelsFromRole(role) -> .Labels non-nil.
// Case B/C (control):  podLabels = nil / empty                       -> .Labels IS nil,
//
//	and maps.Clone of it panics on write. That is the hypothetical Copilot needed,
//	and it proves this test is sensitive enough to have caught the bug if any
//	production caller could reach it.
func TestPR396_ConstructPodTemplateSpec_LabelsNonNil(t *testing.T) {
	scheme := pr396Scheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	ctx := context.Background()

	role := pr396StandaloneRole("worker")
	rbg := pr396RBG("test-rbg", "default", role)
	labelLess := pr396LabelLessTemplate()
	require.Nil(t, labelLess.Labels, "precondition: the input template has no labels")

	t.Run("A_production_podLabels_from_GetCommonLabelsFromRole", func(t *testing.T) {
		pr := NewPodReconciler(scheme, fakeClient)
		cfg, err := pr.ConstructPodTemplateSpecApplyConfiguration(
			ctx, rbg, &role, rbg.GetCommonLabelsFromRole(&role), labelLess)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.NotNil(t, cfg.Labels,
			"PROOF TARGET: .Labels must be non-nil in production, so maps.Clone(.Labels) "+
				"at the four flagged sites is non-nil and writable")
		assert.GreaterOrEqual(t, len(cfg.Labels), 3)
		// And the clone-then-write shape from the flagged sites is safe here:
		assert.NotPanics(t, func() {
			c := maps.Clone(cfg.Labels)
			c[constants.ComponentSizeLabelKey] = "1"
		}, "the exact flagged code shape must not panic on production input")
		t.Logf("production .Labels = %v", cfg.Labels)
	})

	t.Run("B_control_nil_podLabels_leaves_Labels_nil_and_clone_write_panics", func(t *testing.T) {
		pr := NewPodReconciler(scheme, fakeClient)
		cfg, err := pr.ConstructPodTemplateSpecApplyConfiguration(
			ctx, rbg, &role, nil, labelLess)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		// CONTROL: this is the state Copilot assumed. It IS achievable in principle.
		require.Nil(t, cfg.Labels,
			"CONTROL: with nil podLabels and a label-less template, .Labels stays nil "+
				"(generated WithLabels only allocates when len(entries) > 0)")

		var recovered any
		func() {
			defer func() { recovered = recover() }()
			c := maps.Clone(cfg.Labels)
			c[constants.ComponentSizeLabelKey] = "1"
		}()
		require.NotNil(t, recovered,
			"CONTROL: the flagged code shape DOES panic when .Labels is nil - "+
				"this test would have caught the bug if a production caller reached it")
		t.Logf("CONTROL panic reproduced: %v", recovered)
	})

	t.Run("C_control_empty_podLabels_also_leaves_Labels_nil", func(t *testing.T) {
		pr := NewPodReconciler(scheme, fakeClient)
		cfg, err := pr.ConstructPodTemplateSpecApplyConfiguration(
			ctx, rbg, &role, map[string]string{}, labelLess)
		require.NoError(t, err)
		require.Nil(t, cfg.Labels,
			"CONTROL: an empty (non-nil) podLabels also leaves .Labels nil")
	})

	t.Run("D_template_labels_alone_make_Labels_non_nil", func(t *testing.T) {
		withLabels := pr396LabelLessTemplate()
		withLabels.Labels = map[string]string{"app": "demo"}
		pr := NewPodReconciler(scheme, fakeClient)
		cfg, err := pr.ConstructPodTemplateSpecApplyConfiguration(
			ctx, rbg, &role, nil, withLabels)
		require.NoError(t, err)
		require.NotNil(t, cfg.Labels,
			"a template that carries labels independently makes .Labels non-nil")
	})
}

// ---------------------------------------------------------------------------
// 4. NO PANIC THROUGH THE REAL PRODUCTION PATHS
// ---------------------------------------------------------------------------

// TestPR396_ProductionPaths_NoPanic drives the three real (unexported) construct
// functions that contain the four flagged maps.Clone sites, using label-less pod
// templates - the input most likely to leave .Labels nil.
func TestPR396_ProductionPaths_NoPanic(t *testing.T) {
	ctx := context.Background()

	t.Run("constructRoleInstanceTemplateFromStandalonePattern_line440", func(t *testing.T) {
		r := pr396Reconciler(t)
		role := pr396StandaloneRole("worker")
		rbg := pr396RBG("test-rbg", "default", role)
		matchLabels := rbg.GetCommonLabelsFromRole(&role)
		cfg := workloadsv1alpha2client.RoleInstanceTemplate()

		var err error
		require.NotPanics(t, func() {
			err = r.constructRoleInstanceTemplateFromStandalonePattern(ctx, rbg, &role, matchLabels, cfg)
		}, "flagged site ~line 440: podLabels := maps.Clone(podTemplateApplyConfiguration.Labels)")
		require.NoError(t, err)
		require.Len(t, cfg.Components, 1)
		require.NotNil(t, cfg.Components[0].Template)
		assert.Equal(t, "1", cfg.Components[0].Template.Labels[constants.ComponentSizeLabelKey])
		t.Logf("standalone component labels = %v", cfg.Components[0].Template.Labels)
	})

	t.Run("constructRoleInstanceTemplateByLeaderWorkerPattern_line396_400", func(t *testing.T) {
		r := pr396Reconciler(t)
		role := pr396LeaderWorkerRole("lwp", 4)
		rbg := pr396RBG("test-rbg", "default", role)
		matchLabels := rbg.GetCommonLabelsFromRole(&role)
		cfg := workloadsv1alpha2client.RoleInstanceTemplate()

		var err error
		require.NotPanics(t, func() {
			err = r.constructRoleInstanceTemplateByLeaderWorkerPattern(ctx, rbg, &role, matchLabels, cfg)
		}, "flagged sites ~lines 396/400: leaderLabels/workerLabels := maps.Clone(...Labels)")
		require.NoError(t, err)
		require.Len(t, cfg.Components, 2)
		t.Logf("leader labels = %v", cfg.Components[0].Template.Labels)
		t.Logf("worker labels = %v", cfg.Components[1].Template.Labels)
	})

	t.Run("constructRoleInstanceTemplateByCustomComponentsPattern_line303", func(t *testing.T) {
		r := pr396Reconciler(t)
		role := pr396CustomComponentsRole("cc")
		rbg := pr396RBG("test-rbg", "default", role)
		matchLabels := rbg.GetCommonLabelsFromRole(&role)
		cfg := workloadsv1alpha2client.RoleInstanceTemplate()

		var err error
		require.NotPanics(t, func() {
			err = r.constructRoleInstanceTemplateByCustomComponentsPattern(ctx, rbg, &role, matchLabels, cfg)
		}, "flagged site ~line 303: componentLabels := maps.Clone(podTemplateApplyConfiguration.Labels)")
		require.NoError(t, err)
		require.Len(t, cfg.Components, 2)
		assert.Equal(t, "2", cfg.Components[0].Template.Labels[constants.ComponentSizeLabelKey])
		assert.Equal(t, "3", cfg.Components[1].Template.Labels[constants.ComponentSizeLabelKey])
		t.Logf("component[0] labels = %v", cfg.Components[0].Template.Labels)
		t.Logf("component[1] labels = %v", cfg.Components[1].Template.Labels)
	})

	// The same three paths, but through the full caller, with an RBG that carries
	// RoleBasedGroupSet labels - which is what activates the new
	// GetGroupSetLabels()/podLabels mutation this PR adds in pod_reconciler.go.
	t.Run("full_constructRoleInstanceSetApplyConfiguration_with_groupset_labels", func(t *testing.T) {
		for _, role := range []workloadsv1alpha2.RoleSpec{
			pr396StandaloneRole("sa"),
			pr396LeaderWorkerRole("lwp", 3),
			pr396CustomComponentsRole("cc"),
		} {
			role := role
			t.Run(role.Name, func(t *testing.T) {
				r := pr396Reconciler(t)
				rbg := pr396RBG("test-rbg", "default", role)
				rbg.Labels = map[string]string{
					constants.GroupSetNameLabelKey:  "my-set",
					constants.GroupSetIndexLabelKey: "0",
				}
				var out *workloadsv1alpha2client.RoleInstanceSetApplyConfiguration
				var err error
				require.NotPanics(t, func() {
					out, err = r.constructRoleInstanceSetApplyConfiguration(
						ctx, rbg, &role, nil, "rev-1", nil)
				})
				require.NoError(t, err)
				require.NotNil(t, out)
				for i := range out.Spec.RoleInstanceTemplate.Components {
					lbls := out.Spec.RoleInstanceTemplate.Components[i].Template.Labels
					assert.Equal(t, "my-set", lbls[constants.GroupSetNameLabelKey],
						"groupset labels must reach the pod template")
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// 5. REGRESSION GUARD: leader/worker component labels do not cross-contaminate
// ---------------------------------------------------------------------------

// TestPR396_LeaderWorker_NoLabelCrossContamination is the regression guard for the
// behaviour the PR is meant to protect: the leader component's pod template must be
// labelled component-name=leader and the worker's component-name=worker, neither
// carrying the other's value, and the two label maps must not be the same
// underlying map (no aliasing).
func TestPR396_LeaderWorker_NoLabelCrossContamination(t *testing.T) {
	ctx := context.Background()
	r := pr396Reconciler(t)
	role := pr396LeaderWorkerRole("lwp", 4)
	rbg := pr396RBG("test-rbg", "default", role)
	matchLabels := rbg.GetCommonLabelsFromRole(&role)
	cfg := workloadsv1alpha2client.RoleInstanceTemplate()

	require.NoError(t, r.constructRoleInstanceTemplateByLeaderWorkerPattern(
		ctx, rbg, &role, matchLabels, cfg))
	require.Len(t, cfg.Components, 2)

	var leader, worker map[string]string
	for i := range cfg.Components {
		require.NotNil(t, cfg.Components[i].Name)
		switch *cfg.Components[i].Name {
		case string(constants.LeaderComponentType):
			leader = cfg.Components[i].Template.Labels
		case string(constants.WorkerComponentType):
			worker = cfg.Components[i].Template.Labels
		}
	}
	require.NotNil(t, leader, "leader component not found")
	require.NotNil(t, worker, "worker component not found")

	t.Logf("leader template labels = %v", leader)
	t.Logf("worker template labels = %v", worker)

	assert.Equal(t, string(constants.LeaderComponentType), leader[constants.ComponentNameLabelKey],
		"leader template must be labelled component-name=leader")
	assert.Equal(t, string(constants.WorkerComponentType), worker[constants.ComponentNameLabelKey],
		"worker template must be labelled component-name=worker")
	assert.NotEqual(t, string(constants.WorkerComponentType), leader[constants.ComponentNameLabelKey],
		"leader must not carry the worker's component-name value")
	assert.NotEqual(t, string(constants.LeaderComponentType), worker[constants.ComponentNameLabelKey],
		"worker must not carry the leader's component-name value")

	// Both carry the same component-size (the total lwp size) - that is intended.
	assert.Equal(t, "4", leader[constants.ComponentSizeLabelKey])
	assert.Equal(t, "4", worker[constants.ComponentSizeLabelKey])

	// Aliasing check: mutating one map must not be visible through the other.
	leader["pr396-sentinel"] = "leader-only"
	assert.NotContains(t, worker, "pr396-sentinel",
		"leader and worker label maps are aliased - a write to one leaks into the other")
}

// ---------------------------------------------------------------------------
// 6. WHAT maps.Clone(matchLabels) ACTUALLY FIXES (the reachable bug in this PR)
// ---------------------------------------------------------------------------

// TestPR396_MatchLabelsNotMutatedByPodReconciler documents the real reason the PR
// added maps.Clone(matchLabels) at the ConstructPodTemplateSpecApplyConfiguration
// call sites.
//
// This PR adds to pod_reconciler.go:
//
//	groupSetLabels := rbg.GetGroupSetLabels()
//	if len(groupSetLabels) > 0 && podLabels == nil { podLabels = make(...) }
//	for k, v := range groupSetLabels { podLabels[k] = v }
//
// which WRITES INTO THE CALLER'S map. In constructRoleInstanceSetApplyConfiguration
// the same matchLabels map is later used for spec.selector.matchLabels
// (WithMatchLabels(maps.Clone(matchLabels))). Without the clone at the call site,
// the groupset labels would leak into the RoleInstanceSet's immutable selector.
//
// Sub-test A is the CONTROL: it calls the real function without a clone and shows
// the caller's map IS mutated. Sub-test B shows the production path is protected.
func TestPR396_MatchLabelsNotMutatedByPodReconciler(t *testing.T) {
	ctx := context.Background()
	scheme := pr396Scheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	role := pr396StandaloneRole("worker")
	rbg := pr396RBG("test-rbg", "default", role)
	rbg.Labels = map[string]string{
		constants.GroupSetNameLabelKey:  "my-set",
		constants.GroupSetIndexLabelKey: "0",
	}

	t.Run("A_control_uncloned_matchLabels_IS_mutated", func(t *testing.T) {
		matchLabels := rbg.GetCommonLabelsFromRole(&role)
		require.Len(t, matchLabels, 3)
		pr := NewPodReconciler(scheme, fakeClient)
		_, err := pr.ConstructPodTemplateSpecApplyConfiguration(
			ctx, rbg, &role, matchLabels /* NOT cloned */, pr396LabelLessTemplate())
		require.NoError(t, err)
		assert.Contains(t, matchLabels, constants.GroupSetNameLabelKey,
			"CONTROL: the real function mutates the caller's podLabels map, which is "+
				"exactly why the PR wraps every call site in maps.Clone(matchLabels)")
		t.Logf("CONTROL: uncloned matchLabels after the call = %v", matchLabels)
	})

	t.Run("B_production_path_leaves_selector_matchLabels_clean", func(t *testing.T) {
		r := NewRoleInstanceSetReconciler(scheme, fakeClient)
		out, err := r.constructRoleInstanceSetApplyConfiguration(ctx, rbg, &role, nil, "rev-1", nil)
		require.NoError(t, err)
		sel := out.Spec.Selector.MatchLabels
		require.NotNil(t, sel)
		assert.NotContains(t, sel, constants.GroupSetNameLabelKey,
			"groupset labels must NOT leak into the immutable RoleInstanceSet selector")
		assert.NotContains(t, sel, constants.GroupSetIndexLabelKey)
		assert.Len(t, sel, 3, "selector must stay exactly the 3 common labels")
		// but they DO reach the pod template
		lbls := out.Spec.RoleInstanceTemplate.Components[0].Template.Labels
		assert.Equal(t, "my-set", lbls[constants.GroupSetNameLabelKey])
		t.Logf("selector = %v", sel)
		t.Logf("pod template labels = %v", lbls)
	})
}
