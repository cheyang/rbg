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

// Round 7 harness for PR #474 — the carried blocker C4358788576.
//
// Round 6 adjudicated C4358788576 CONFIRMED (the round-6 refutation was
// withdrawn): a declarative write (kubectl apply / Helm-rendered YAML) of a
// set whose role carries an explicitly empty `annotations: {}` keeps the
// empty map in the stored set template — the apiserver does not re-marshal
// stored objects through the Go struct's omitempty — while the controller's
// typed child write drops it. rolesEqual then says nil != {} (DeepEqual)
// while the JSON-normalized groupSetTemplatesEqual says equal;
// groupSetMatchesRevision ANDs both, so the child is classified outdated on
// every pass: rewritten in place with a fresh rollout-request token each
// time, the budget never frees, and RolloutComplete is never reached.
//
// Polarity: CONTRACT. These tests assert the intended behavior (an
// explicitly empty map must not wedge the rollout) and are RED on code that
// still has the bug; they flip green when rolesEqual (or both conjuncts of
// groupSetMatchesRevision) normalizes empty maps/slices.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestVerifyPR474_EmptyRoleAnnotationsMapNoWedge (contract, integration:
// real apiserver) — a set written declaratively with `roles[].annotations:
// {}` must produce a child the revision matcher accepts, so the rollout can
// converge instead of being rewritten forever.
func TestVerifyPR474_EmptyRoleAnnotationsMapNoWedge(t *testing.T) {
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		assets = "/home/ecs-user/envtest-assets/k8s/1.31.0-linux-amd64"
	}
	if _, err := os.Stat(assets); err != nil {
		t.Skipf("envtest assets not available at %s", assets)
	}
	_, thisFile, _, _ := runtime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "config", "crd", "bases")
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		BinaryAssetsDirectory: assets,
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err)
	defer func() { _ = testEnv.Stop() }()

	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme.Scheme))
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	require.NoError(t, err)
	ctx := context.Background()

	// Declarative (unstructured) write — the shape Helm/kubectl render.
	setU := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "workloads.x-k8s.io/v1alpha2",
		"kind":       "RoleBasedGroupSet",
		"metadata":   map[string]interface{}{"name": "r7-empty-ann-set", "namespace": "default"},
		"spec": map[string]interface{}{
			"replicas": int64(1),
			"groupTemplate": map[string]interface{}{
				"spec": map[string]interface{}{
					"roles": []interface{}{
						map[string]interface{}{
							"name":        "prefill",
							"replicas":    int64(1),
							"annotations": map[string]interface{}{},
							"standalonePattern": map[string]interface{}{
								"template": map[string]interface{}{
									"spec": map[string]interface{}{
										"containers": []interface{}{
											map[string]interface{}{
												"name":  "prefill",
												"image": "nginx:1.14.1",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}}
	setU.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "workloads.x-k8s.io", Version: "v1alpha2", Kind: "RoleBasedGroupSet",
	})
	require.NoError(t, k8sClient.Create(ctx, setU))

	// Read the set back through the typed client: the premise is that the
	// stored set keeps a non-nil empty map (this is observed behavior on a
	// real apiserver, pinned here so the contract cannot silently stop
	// exercising the shape it exists for).
	set := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "r7-empty-ann-set"}, set))
	storedAnn := set.Spec.GroupTemplate.Spec.Roles[0].Annotations
	require.NotNil(t, storedAnn, "premise: the stored set keeps the explicitly empty annotations map")
	require.Empty(t, storedAnn)

	// Build the revision exactly as ensureGroupSetRevision does, then the
	// child exactly as the controller does, write it typed, read it back.
	revTemplate := normalizedGroupSetTemplate(&set.Spec.GroupTemplate)
	rev := &groupSetRevision{name: "r7-empty-ann-rev", template: revTemplate}
	child := newRBGForSetRevision(set, 0, rev)
	require.NoError(t, k8sClient.Create(ctx, child))
	readBack := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(child), readBack))

	r := &RoleBasedGroupSetReconciler{}

	// THE CONTRACT: the freshly created, revision-correct child must match
	// the revision it was minted from. On unfixed code this is false
	// (rolesEqual DeepEqual: child nil vs revision {}), which is what wedges
	// the rollout into perpetual rewrite.
	assert.True(t, r.groupSetMatchesRevision(readBack, rev),
		"C4358788576: the child minted from this revision must match it; "+
			"rolesEqual must treat an explicitly empty annotations map as equal to an omitted one")
}

// TestVerifyPR474_RolesEqual_EmptyMapEqualsOmitted (contract, unit) — the
// comparison-primitive half of the same finding: rolesEqual must compare
// equal a role with an explicitly empty annotations/labels map and the same
// role with the map omitted (both directions), because declarative tooling
// writes the former while the typed child write produces the latter.
func TestVerifyPR474_RolesEqual_EmptyMapEqualsOmitted(t *testing.T) {
	r := &RoleBasedGroupSetReconciler{}
	base := workloadsv1alpha2.RoleSpec{
		Name:     "prefill",
		Replicas: ptr.To(int32(1)),
	}
	withEmptyAnn := base
	withEmptyAnn.Annotations = map[string]string{}
	withEmptyLabels := base
	withEmptyLabels.Labels = map[string]string{}

	assert.True(t, r.rolesEqual([]workloadsv1alpha2.RoleSpec{base}, []workloadsv1alpha2.RoleSpec{withEmptyAnn}),
		"empty annotations map must compare equal to an omitted one (annotations)")
	assert.True(t, r.rolesEqual([]workloadsv1alpha2.RoleSpec{withEmptyAnn}, []workloadsv1alpha2.RoleSpec{base}),
		"empty annotations map must compare equal to an omitted one (both directions)")
	assert.True(t, r.rolesEqual([]workloadsv1alpha2.RoleSpec{base}, []workloadsv1alpha2.RoleSpec{withEmptyLabels}),
		"empty labels map must compare equal to an omitted one (labels)")

	// Control: a genuinely different value must still compare unequal, so
	// the contract cannot be satisfied by making rolesEqual trivially true.
	different := base
	different.Annotations = map[string]string{"k": "v"}
	assert.False(t, r.rolesEqual([]workloadsv1alpha2.RoleSpec{base}, []workloadsv1alpha2.RoleSpec{different}),
		"a real annotation value must still compare unequal")
}

// TestVerifyPR474_EmptyMapSerializationPremise (canary, unit) — pins the
// premise that distinguishes this round from the withdrawn round-6
// refutation: a Go-struct marshal drops the empty map (which is what the
// round-6 refutation tested), but the JSON text of the two forms is what the
// normalized comparison sees, and the apiserver retains the empty map in
// stored objects written unstructured. Kept as documentation of why the
// refutation was wrong; this test PASSES on any code and never gates.
func TestVerifyPR474_EmptyMapSerializationPremise(t *testing.T) {
	withEmpty := workloadsv1alpha2.RoleSpec{Name: "w", Annotations: map[string]string{}}
	data, err := json.Marshal(withEmpty)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "annotations",
		"the round-6 refutation was right about this half: Go-struct omitempty drops the map")
	// ...and wrong about the other half: the stored set from a declarative
	// write never passes through Go-struct marshaling — see the envtest
	// contract above, which pins that half on a real apiserver.
}
