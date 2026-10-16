/*
Verification harness for PR #500 review (Reviewer B / Codex).
Additive test file — production code untouched.

Polarity: CONTRACT tests (assert intended correct behavior).
- TestVerifyPremiseForeignPodGroupRejected: FAILS on base (foreign PodGroup was
  silently rewritten), PASSES on the PR head.
- TestVerifyF1*: FAIL on the PR head — they pin finding F1 (same-spec orphan
  PodGroup is never adopted and therefore leaks past deletePodGroup). They go
  green only once adoption happens even when the spec already matches.
*/

package volcano

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	volcanoschedulingv1beta1 "volcano.sh/apis/pkg/apis/scheduling/v1beta1"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scheduler/common"
)

// P0-d: a PodGroup controlled by a previous same-named RBG's UID must be
// rejected, not rewritten in place.
//
// Base behavior (bug): createOrUpdate found the PodGroup by name and rewrote
// its spec while keeping the old controller reference.
// Fixed behavior: an error is returned and the foreign PodGroup is untouched.
func TestVerifyPremiseForeignPodGroupRejected(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, volcanoschedulingv1beta1.AddToScheme(scheme))

	rbg := rbgWithRoles(standaloneRole("prefill", 2, ""))
	rbg.UID = "new-rbg-uid"
	oldRBG := rbg.DeepCopy()
	oldRBG.UID = "old-rbg-uid"
	strategy := &common.GangStrategy{Roles: sets.New("prefill")}

	pg := &volcanoschedulingv1beta1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:            rbg.Name,
			Namespace:       rbg.Namespace,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(oldRBG, workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))},
		},
		Spec: volcanoschedulingv1beta1.PodGroupSpec{MinMember: 7},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg).Build()

	err := New(c).createOrUpdate(context.Background(), rbg, strategy, c)
	assert.Error(t, err, "contract: a PodGroup controlled by a different UID must be rejected")

	after := &volcanoschedulingv1beta1.PodGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pg), after))
	assert.Equal(t, int32(7), after.Spec.MinMember, "contract: foreign PodGroup spec must not be rewritten")
}

// F1-a: an orphaned PodGroup whose spec already matches the desired spec must
// still be adopted (owner reference re-attached). This is the
// delete-with-cascade=orphan + recreate-with-same-spec path: nothing about the
// spec differs, so an adoption that only happens inside the spec-changed branch
// never runs.
func TestVerifyF1SameSpecOrphanPodGroupAdopted(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, volcanoschedulingv1beta1.AddToScheme(scheme))

	rbg := rbgWithRoles(standaloneRole("prefill", 2, ""))
	rbg.UID = "new-rbg-uid"
	strategy := &common.GangStrategy{Roles: sets.New("prefill")}

	// Same-spec orphan: MinMember matches the desired 2, no queue/priority/
	// subGroupPolicy differences — nothing triggers the update branch.
	pg := &volcanoschedulingv1beta1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{Name: rbg.Name, Namespace: rbg.Namespace},
		Spec:       volcanoschedulingv1beta1.PodGroupSpec{MinMember: 2},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg).Build()

	require.NoError(t, New(c).createOrUpdate(context.Background(), rbg, strategy, c))

	after := &volcanoschedulingv1beta1.PodGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pg), after))
	assert.True(t, metav1.IsControlledBy(after, rbg),
		"contract: a same-spec orphan PodGroup must be adopted, not left ownerless")
}

// F1-b: the downstream consequence — an orphan that was never adopted is
// skipped by the IsControlledBy-gated deletePodGroup, so it leaks when gang
// scheduling is disabled or the RBG is deleted.
func TestVerifyF1SameSpecOrphanLeaksPastDeletePodGroup(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, volcanoschedulingv1beta1.AddToScheme(scheme))

	rbg := rbgWithRoles(standaloneRole("prefill", 2, ""))
	rbg.UID = "new-rbg-uid"
	strategy := &common.GangStrategy{Roles: sets.New("prefill")}

	pg := &volcanoschedulingv1beta1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{Name: rbg.Name, Namespace: rbg.Namespace},
		Spec:       volcanoschedulingv1beta1.PodGroupSpec{MinMember: 2},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg).Build()

	// Reconcile with gang enabled (orphan present, spec already matches).
	require.NoError(t, New(c).createOrUpdate(context.Background(), rbg, strategy, c))

	// Gang disabled (or RBG being torn down): the PodGroup should be removed.
	watched := &sync.Map{}
	watched.LoadOrStore(CrdName, struct{}{})
	require.NoError(t, New(c).deletePodGroup(context.Background(), rbg, watched))

	err := c.Get(context.Background(), client.ObjectKeyFromObject(pg), &volcanoschedulingv1beta1.PodGroup{})
	assert.True(t, apierrors.IsNotFound(err),
		"contract: the PodGroup managed for this RBG must be deleted when gang scheduling is disabled; got err=%v", err)
}
