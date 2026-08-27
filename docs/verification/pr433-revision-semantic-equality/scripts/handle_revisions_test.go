package workloads

import (
	"context"
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/lru"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/reconciler"
	"sigs.k8s.io/rbgs/pkg/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// injectLegacyCreationTimestamp recursively adds "creationTimestamp": null into
// every "metadata" object in the JSON tree, reproducing how older client-go
// versions serialized ControllerRevision data.
func injectLegacyCreationTimestamp(t *testing.T, data []byte) []byte {
	t.Helper()
	var tree interface{}
	require.NoError(t, json.Unmarshal(data, &tree))

	var inject func(node interface{})
	inject = func(node interface{}) {
		switch n := node.(type) {
		case map[string]interface{}:
			for key, child := range n {
				if meta, ok := child.(map[string]interface{}); ok && key == "metadata" {
					meta["creationTimestamp"] = nil
				}
				inject(child)
			}
		case []interface{}:
			for _, child := range n {
				inject(child)
			}
		}
	}
	inject(tree)

	out, err := json.Marshal(tree)
	require.NoError(t, err)
	return out
}

// TestHandleRevisions_SemanticEquality_ReturnsPersistedRevisionHash verifies
// that when SetMatchesRevision detects semantic equality between the persisted
// and newly-computed revisions, handleRevisions returns the role hash derived
// from the PERSISTED revision (not the freshly-computed one).
//
// This is a regression test for the bug where handleRevisions skips creating a
// new ControllerRevision but still computes GetRolesRevisionHash from the
// uncommitted expectedRevision, causing downstream role labels to see a hash
// that doesn't match any persisted revision — triggering the spurious rollout
// that PR #433 aims to prevent.
func TestHandleRevisions_SemanticEquality_ReturnsPersistedRevisionHash(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(testScheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(testScheme))

	// 1. Build a minimal RBG
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "workloads.x-k8s.io/v1alpha2",
			Kind:       "RoleBasedGroup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-rbg",
			Namespace:  "default",
			UID:        types.UID("test-rbg-uid-123"),
			Generation: 1,
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{
					Name:     "worker",
					Replicas: ptr.To(int32(2)),
					Pattern: workloadsv1alpha2.Pattern{
						StandalonePattern: &workloadsv1alpha2.StandalonePattern{
							TemplateSource: workloadsv1alpha2.TemplateSource{
								Template: &corev1.PodTemplateSpec{
									ObjectMeta: metav1.ObjectMeta{
										Labels: map[string]string{"app": "worker"},
									},
									Spec: corev1.PodSpec{
										Containers: []corev1.Container{
											{Name: "main", Image: "nginx:1.25"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// 2. Compute the "fresh" revision (what the current serializer produces)
	ctx := ctrl.LoggerInto(context.Background(), zap.New())
	dummyClient := fake.NewClientBuilder().WithScheme(testScheme).Build()
	freshRevision, err := utils.NewRevision(ctx, dummyClient, rbg, nil)
	require.NoError(t, err)

	// 3. Inject legacy creationTimestamp:null drift into the patch data
	legacyPatch := injectLegacyCreationTimestamp(t, freshRevision.Data.Raw)
	require.NotEqual(t, freshRevision.Data.Raw, legacyPatch,
		"test premise: legacy patch must differ in bytes from fresh patch")

	// 4. Build the "persisted" ControllerRevision (what's stored in the cluster)
	persistedRevision := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:            freshRevision.Name,
			Namespace:       rbg.Namespace,
			ResourceVersion: "999",
			Labels: map[string]string{
				constants.GroupNameLabelKey:    rbg.Name,
				constants.GroupRevisionLabelKey: freshRevision.Labels[constants.GroupRevisionLabelKey],
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: rbg.APIVersion,
					Kind:       rbg.Kind,
					Name:       rbg.Name,
					UID:        rbg.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Data:     runtime.RawExtension{Raw: legacyPatch},
		Revision: 1,
	}

	// 5. Verify test premise: hashes from legacy vs fresh data must differ
	hashFromPersisted, err := utils.GetRolesRevisionHash(persistedRevision)
	require.NoError(t, err)
	hashFromFresh, err := utils.GetRolesRevisionHash(freshRevision)
	require.NoError(t, err)
	require.NotEqual(t, hashFromPersisted, hashFromFresh,
		"test premise: role hashes must differ between legacy and fresh revision data")

	// 6. Setup fake client with the RBG and the persisted revision
	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(rbg, persistedRevision).
		Build()

	// 7. Create reconciler with cache enabled
	r := &RoleBasedGroupReconciler{
		client:                fakeClient,
		apiReader:             fakeClient,
		scheme:                testScheme,
		recorder:              record.NewFakeRecorder(10),
		workloadReconciler:    make(map[string]reconciler.WorkloadReconciler),
		revisionEqualityCache: lru.New(utils.MaxRevisionEqualityCacheEntries),
	}

	// 8. Call handleRevisions
	returnedHash, err := r.handleRevisions(ctx, rbg)
	require.NoError(t, err)

	// 9. ASSERT: returned hash should match the PERSISTED revision's hash.
	// The bug: current code returns hashFromFresh instead of hashFromPersisted.
	assert.Equal(t, hashFromPersisted, returnedHash,
		"handleRevisions must return hash derived from the persisted (current) revision, "+
			"not the freshly-computed expectedRevision — otherwise downstream role labels "+
			"will see a hash that doesn't match any stored ControllerRevision, triggering "+
			"the spurious rollout this fix is supposed to prevent")

	// 10. ASSERT: no new ControllerRevision should have been created
	var revList appsv1.ControllerRevisionList
	require.NoError(t, fakeClient.List(ctx, &revList, client.InNamespace(rbg.Namespace)))
	assert.Equal(t, 1, len(revList.Items),
		"SetMatchesRevision returned true — no new revision should be created")
}

// TestHandleRevisions_TrulyDifferent_CreatesNewRevision is a sanity check that
// when revisions are genuinely different, a new ControllerRevision is created.
func TestHandleRevisions_TrulyDifferent_CreatesNewRevision(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(testScheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(testScheme))

	rbg := &workloadsv1alpha2.RoleBasedGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "workloads.x-k8s.io/v1alpha2",
			Kind:       "RoleBasedGroup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-rbg",
			Namespace:  "default",
			UID:        types.UID("test-rbg-uid-123"),
			Generation: 2,
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{
					Name:     "worker",
					Replicas: ptr.To(int32(2)),
					Pattern: workloadsv1alpha2.Pattern{
						StandalonePattern: &workloadsv1alpha2.StandalonePattern{
							TemplateSource: workloadsv1alpha2.TemplateSource{
								Template: &corev1.PodTemplateSpec{
									ObjectMeta: metav1.ObjectMeta{
										Labels: map[string]string{"app": "worker"},
									},
									Spec: corev1.PodSpec{
										Containers: []corev1.Container{
											{Name: "main", Image: "nginx:1.26"}, // UPDATED image
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Create an "old" revision with the previous image
	oldRbg := rbg.DeepCopy()
	oldRbg.Spec.Roles[0].StandalonePattern.Template.Spec.Containers[0].Image = "nginx:1.25"
	ctx := ctrl.LoggerInto(context.Background(), zap.New())
	dummyClient := fake.NewClientBuilder().WithScheme(testScheme).Build()
	oldRevision, err := utils.NewRevision(ctx, dummyClient, oldRbg, nil)
	require.NoError(t, err)

	persistedRevision := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:            oldRevision.Name,
			Namespace:       rbg.Namespace,
			ResourceVersion: "100",
			Labels: map[string]string{
				constants.GroupNameLabelKey:    rbg.Name,
				constants.GroupRevisionLabelKey: oldRevision.Labels[constants.GroupRevisionLabelKey],
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: rbg.APIVersion,
					Kind:       rbg.Kind,
					Name:       rbg.Name,
					UID:        rbg.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Data:     oldRevision.Data,
		Revision: 1,
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(rbg, persistedRevision).
		Build()

	r := &RoleBasedGroupReconciler{
		client:                fakeClient,
		apiReader:             fakeClient,
		scheme:                testScheme,
		recorder:              record.NewFakeRecorder(10),
		workloadReconciler:    make(map[string]reconciler.WorkloadReconciler),
		revisionEqualityCache: lru.New(utils.MaxRevisionEqualityCacheEntries),
	}

	returnedHash, err := r.handleRevisions(ctx, rbg)
	require.NoError(t, err)
	require.NotNil(t, returnedHash)

	// A new revision should have been created
	var revList appsv1.ControllerRevisionList
	require.NoError(t, fakeClient.List(ctx, &revList, client.InNamespace(rbg.Namespace)))
	assert.Equal(t, 2, len(revList.Items),
		"genuinely different spec should create a new revision")
}
