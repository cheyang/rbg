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

package workloads

// Verification harness (L2 integration layer) for PR #474 round 6; reviewer additive
// test, production code untouched.
//
// R6REVDATA (13cbcab4, "canonical JSON revision data"): ControllerRevision.data is
// immutable and validated byte-wise on update, so the garbage collector's orphaning
// patch (a metadata-only strategic-merge patch removing ownerReferences) must not
// change the stored data representation. This test
//  1. CONTRACT: a revision written the way the head writes it (canonical JSON) accepts
//     a GC-style orphan patch, keeping its data byte-identical; and
//  2. PREMISE PROBE: the same patch against a revision whose data is in the pre-13cbcab4
//     (struct-order, json.Marshal) form - the two byte forms must actually differ for the
//     probe to be meaningful, and the patch outcome is recorded either way.
//
// Runs against a real API server (envtest); skips when KUBEBUILDER_ASSETS is unset.
// Polarity: CONTRACT for the head form; the legacy-form probe reports observed behavior.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerify_R6_ControllerRevisionCanonicalDataOrphanPatch(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; skipping envtest integration layer")
	}
	_, thisFile, _, _ := goruntime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(repoRoot, "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	defer func() { _ = env.Stop() }()

	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()

	// A template with both labels and annotations set, so struct-order and canonical
	// serialization observably differ.
	template := normalizedGroupSetTemplate(&workloadsv1alpha2.RoleBasedGroupTemplateSpec{
		Labels:      map[string]string{"app": "verify"},
		Annotations: map[string]string{"team": "verify"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}},
		},
	})
	canonicalData, err := canonicalGroupSetTemplateJSON(template)
	require.NoError(t, err)
	legacyData, err := json.Marshal(template)
	require.NoError(t, err)
	require.NotEqual(t, string(legacyData), string(canonicalData),
		"fixture vacuous: the legacy (pre-13cbcab4) and canonical byte forms must differ")

	// The GC orphaning patch removes the owner reference without touching data.
	ownerUID := types.UID("dead-parent-uid")
	mkRevision := func(name string, data []byte) *appsv1.ControllerRevision {
		return &appsv1.ControllerRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: workloadsv1alpha2.GroupVersion.String(),
					Kind:       "RoleBasedGroupSet",
					Name:       "gone",
					UID:        ownerUID,
					Controller: ptr.To(true),
				}},
			},
			Data: runtime.RawExtension{Raw: data},
		}
	}
	orphanPatch := []byte(`{"metadata":{"ownerReferences":[{"$patch":"delete","uid":"` + string(ownerUID) + `"}]}}`)

	t.Run("canonical data accepts the GC orphan patch", func(t *testing.T) {
		rev := mkRevision("rbgs-canonical", canonicalData)
		require.NoError(t, c.Create(ctx, rev))
		err := c.Patch(ctx, rev, client.RawPatch(types.StrategicMergePatchType, orphanPatch))
		require.NoError(t, err, "the GC orphaning patch must not be rejected as an immutable-data change")
		stored := &appsv1.ControllerRevision{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(rev), stored))
		assert.Empty(t, stored.OwnerReferences, "the owner reference is orphaned away")
		assert.Equal(t, string(canonicalData), string(stored.Data.Raw),
			"the patch must leave the stored data byte-identical")
	})

	t.Run("legacy struct-order data probe", func(t *testing.T) {
		rev := mkRevision("rbgs-legacy", legacyData)
		require.NoError(t, c.Create(ctx, rev))
		err := c.Patch(ctx, rev, client.RawPatch(types.StrategicMergePatchType, orphanPatch))
		if err != nil {
			t.Logf("premise CONFIRMED: the legacy byte form is rejected by the API server: %v", err)
			return
		}
		stored := &appsv1.ControllerRevision{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(rev), stored))
		t.Logf("premise NOT reproduced via this patch path: legacy form patched cleanly; data after: %q",
			string(stored.Data.Raw))
		assert.Empty(t, stored.OwnerReferences)
	})
}

// TestVerify_R6_EmptyRoleAnnotationsMapNoWedge pins the carried reviewer hypothesis
// C4358788576 (roles[].annotations: {} wedges rolesEqual into a perpetual-outdated
// loop) with a real-apiserver repro. The empty map is written through a raw merge
// patch (bypassing the typed client's omitempty) so it survives in the stored set
// template; children are written through the typed client, which omits it.
//
// Observed on af9dd84b BEFORE any fix (this is a CONTRACT test, RED until fixed):
//   stored set template: roles[0].annotations = {} (non-nil)
//   child:               roles[0].annotations = nil
//   rolesEqual           = false -> groupSetMatchesRevision = false -> perpetual outdated
//   each reconcile rewrites the child: rollout-request token 3 -> 4 in one quiescent
//   pass, generation unchanged, RV climbing every pass - a write/ack churn loop whose
//   rollout never completes (the budget is permanently consumed by the re-stamped
//   request). groupSetTemplatesEqual (JSON-normalized) tolerates the empty map; the
//   DeepEqual-based rolesEqual does not - the two predicates disagree.
//
// Polarity: CONTRACT - asserts the intended behavior (no churn once the child matches);
// RED on af9dd84b, must flip GREEN once rolesEqual normalizes empty maps.
func TestVerify_R6_EmptyRoleAnnotationsMapNoWedge(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; skipping envtest integration layer")
	}
	_, thisFile, _, _ := goruntime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(repoRoot, "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	defer func() { _ = env.Stop() }()

	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()

	// Create the set through the typed client, then patch the role's annotations to an
	// explicit empty map through a raw merge patch - the only way an empty map can reach
	// storage past the typed client's omitempty.
	patch := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "empty-ann", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas:        ptr.To(int32(1)),
			RolloutStrategy: &workloadsv1alpha2.GroupSetRolloutStrategy{Type: workloadsv1alpha2.InPlaceUpdateStrategyType},
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}},
				},
			},
		},
	}
	require.NoError(t, c.Create(ctx, patch))
	raw := []byte(`{"spec":{"groupTemplate":{"spec":{"roles":[{"name":"worker","replicas":1,"annotations":{}}]}}}}`)
	require.NoError(t, c.Patch(ctx, patch, client.RawPatch(types.MergePatchType, raw)))

	stored := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(patch), stored))
	require.NotNil(t, stored.Spec.GroupTemplate.Spec.Roles[0].Annotations,
		"premise of the probe: the apiserver must actually store the explicit empty map")
	require.Len(t, stored.Spec.GroupTemplate.Spec.Roles[0].Annotations, 0)

	r := &RoleBasedGroupSetReconciler{client: c, apiReader: c, scheme: scheme}
	key := client.ObjectKeyFromObject(stored)
	require.NoError(t, c.Get(ctx, key, stored))

	// Drive reconcile passes; children never become ready (no child controller here),
	// but the QUESTION is whether the controller rewrites a matching child forever.
	// Let creation settle first.
	for i := 0; i < 10; i++ {
		_, err := r.reconcileRollingGroupSet(ctx, key)
		require.NoError(t, err)
	}
	children := &workloadsv1alpha2.RoleBasedGroupList{}
	require.NoError(t, c.List(ctx, children, client.InNamespace("default")))
	require.Len(t, children.Items, 1)
	childRV := children.Items[0].ResourceVersion
	childGen := children.Items[0].Generation

	for i := 0; i < 5; i++ {
		_, err := r.reconcileRollingGroupSet(ctx, key)
		require.NoError(t, err)
	}
	child := &children.Items[0]
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(child), child))
	assert.Equal(t, childGen, child.Generation,
		"the empty-annotations asymmetry must not rewrite the child forever")
	assert.Equal(t, childRV, child.ResourceVersion,
		"no writes at all once the child matches the revision (observed on af9dd84b: one rewrite per pass, rollout-request token climbing)")

	// The mechanism, pinned: the two matching predicates disagree on the empty map.
	set2 := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, c.Get(ctx, key, set2))
	assert.Equal(t, set2.Status.UpdateRevision, child.Labels["rbg.workloads.x-k8s.io/groupset-revision"])
	_, update, err := r.groupSetRevisions(ctx, set2, children)
	require.NoError(t, err)
	assert.True(t, r.groupSetMatchesRevision(child, update),
		"the child must match the update revision; rolesEqual must not distinguish nil from an empty annotations map")
}
