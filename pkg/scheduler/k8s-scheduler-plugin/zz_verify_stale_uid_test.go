/*
Verification harness for PR #500 review (Reviewer B / Codex).
Additive test file — production code untouched.

Polarity: CONTRACT tests (assert intended correct behavior).
- TestVerifyPremiseForeignPodGroupRejected: FAILS on base, PASSES on the PR head.
- TestVerifyF1*: FAIL on the PR head — they pin finding F1 (same-spec orphan
  PodGroup is never adopted and therefore leaks past deletePodGroup).
*/

package kubeschedulerplugin

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	schedv1alpha1 "sigs.k8s.io/scheduler-plugins/apis/scheduling/v1alpha1"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scheduler/common"
)

// P0-e: a PodGroup controlled by a previous same-named RBG's UID must be
// rejected, not rewritten in place.
func TestVerifyPremiseForeignPodGroupRejected(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, schedv1alpha1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	rbg := testRBG()
	rbg.UID = "new-rbg-uid"
	oldRBG := rbg.DeepCopy()
	oldRBG.UID = "old-rbg-uid"

	pg := &schedv1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:            rbg.Name,
			Namespace:       rbg.Namespace,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(oldRBG, workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))},
		},
		Spec: schedv1alpha1.PodGroupSpec{MinMember: 7},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg).Build()

	err := New(c, "").createOrUpdate(context.Background(), rbg, &common.GangStrategy{})
	assert.Error(t, err, "contract: a PodGroup controlled by a different UID must be rejected")

	after := &schedv1alpha1.PodGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pg), after))
	assert.Equal(t, int32(7), after.Spec.MinMember, "contract: foreign PodGroup spec must not be rewritten")
}

// F1-c: same-spec orphan PodGroup must be adopted even though no spec update
// is needed. Desired state for testRBG(): MinMember=2, ScheduleTimeoutSeconds=60.
func TestVerifyF1SameSpecOrphanPodGroupAdopted(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, schedv1alpha1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	rbg := testRBG()
	rbg.UID = "new-rbg-uid"

	pg := &schedv1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{Name: rbg.Name, Namespace: rbg.Namespace},
		Spec: schedv1alpha1.PodGroupSpec{
			MinMember:              2,
			ScheduleTimeoutSeconds: ptr.To[int32](60),
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg).Build()

	require.NoError(t, New(c, "").createOrUpdate(context.Background(), rbg, &common.GangStrategy{}))

	after := &schedv1alpha1.PodGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pg), after))
	assert.True(t, metav1.IsControlledBy(after, rbg),
		"contract: a same-spec orphan PodGroup must be adopted, not left ownerless")
}

// F1-d: consequence — the unadopted orphan is skipped by deletePodGroup and leaks.
func TestVerifyF1SameSpecOrphanLeaksPastDeletePodGroup(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, schedv1alpha1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	rbg := testRBG()
	rbg.UID = "new-rbg-uid"

	pg := &schedv1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{Name: rbg.Name, Namespace: rbg.Namespace},
		Spec: schedv1alpha1.PodGroupSpec{
			MinMember:              2,
			ScheduleTimeoutSeconds: ptr.To[int32](60),
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg).Build()

	require.NoError(t, New(c, "").createOrUpdate(context.Background(), rbg, &common.GangStrategy{}))

	watched := &sync.Map{}
	watched.LoadOrStore(CrdName, struct{}{})
	require.NoError(t, New(c, "").deletePodGroup(context.Background(), rbg, watched))

	err := c.Get(context.Background(), client.ObjectKeyFromObject(pg), &schedv1alpha1.PodGroup{})
	assert.True(t, apierrors.IsNotFound(err),
		"contract: the PodGroup managed for this RBG must be deleted when gang scheduling is disabled; got err=%v", err)
}
