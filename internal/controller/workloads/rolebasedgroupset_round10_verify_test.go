package workloads

// Round 10 (head cb0a201a) contracts for the delta commits
//   d923c387 "fix(rbgs): compare role templates by name in legacy drift check"
//   cb0a201a "test(rbgs): cover legacy hash and revision upgrade convergence"
// (cb0a201a also rewrites leaderWorkerSetRolloutComplete).
//
//  - TestVerifyPR474_LWSBarrier_HoldsUntilNewRevisionPersisted pins the LWS
//    completion barrier's behavior in the window every real rollout passes
//    through: the RBG reconciler applies the new LWS template and
//    finalizeRolloutStatus runs in the SAME reconcile (controller.go step 8 ->
//    step 9), so the completion check always executes before the LWS
//    controller has created the ControllerRevision for the new template.
//    Until 8f07c37e the check recomputed the target hash from the current LWS
//    spec, so in that window it returned false (barrier holds). cb0a201a
//    switched it to the highest *persisted* revision, which in that window is
//    still the pre-update revision that matches the deployed leader/pod/worker
//    chain - the barrier reports complete before the LWS controller has seen
//    the new spec, and the set controller advances the availability budget.
//    This contract is GREEN on 8f07c37e and RED on cb0a201a
//    (results/differential-r10.log).
//  - TestVerifyPR474_NeedsUpdate_ReorderedRoleTemplates pins d923c387: a
//    reordered roleTemplates list must not read as drift (order-insensitive
//    comparison by name). RED on 8f07c37e (order-sensitive Semantic.DeepEqual),
//    GREEN at head.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	lwsv1 "sigs.k8s.io/lws/api/leaderworkerset/v1"
	lwsrevision "sigs.k8s.io/lws/pkg/utils/revision"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func round10Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, lwsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

// round10LWS builds the steady state of one LWS role: spec replicas 1, group
// size 2, template pinned to `image`, status fully ready, carrying the
// expected role-revision label "target" and controlled by the RBG.
func round10LWS(rbg *workloadsv1alpha2.RoleBasedGroup, role *workloadsv1alpha2.RoleSpec, image string) *lwsv1.LeaderWorkerSet {
	return &lwsv1.LeaderWorkerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: rbg.GetWorkloadName(role), Namespace: rbg.Namespace, UID: "workload-uid", Generation: 2,
			Labels: map[string]string{fmt.Sprintf(constants.RoleRevisionLabelKeyFmt, role.Name): "target"},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(rbg, workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup")),
			},
		},
		Spec: lwsv1.LeaderWorkerSetSpec{
			Replicas: ptr.To(int32(1)),
			LeaderWorkerTemplate: lwsv1.LeaderWorkerTemplate{
				Size:           ptr.To(int32(2)),
				LeaderTemplate: &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "leader", Image: image}}}},
				WorkerTemplate: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "worker", Image: image}}}},
			},
		},
		Status: lwsv1.LeaderWorkerSetStatus{Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
	}
}

// TestVerifyPR474_LWSBarrier_HoldsUntilNewRevisionPersisted is the round-10
// contract: a freshly applied LWS template whose ControllerRevision the LWS
// controller has not yet created must NOT satisfy the rollout completion
// barrier, exactly like a Deployment/StatefulSet whose observed generation
// lags (their checks compare against the just-written generation; the LWS
// check must compare against the just-written template).
func TestVerifyPR474_LWSBarrier_HoldsUntilNewRevisionPersisted(t *testing.T) {
	ctx := context.Background()
	scheme := round10Scheme(t)
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "group", Namespace: "default", UID: "rbg-uid", Generation: 2},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{{
			Name:     "role",
			Replicas: ptr.To(int32(1)),
			Annotations: map[string]string{
				constants.RoleWorkloadTypeAnnotationKey: lwsv1.GroupVersion.String() + "/LeaderWorkerSet",
			},
		}}},
	}
	role := &rbg.Spec.Roles[0]

	// Steady state before the rollout step: the LWS controller has persisted a
	// revision for the app:v1 template and the deployed chain is on its hash.
	settled := round10LWS(rbg, role, "app:v1")
	revision, err := lwsrevision.NewRevision(ctx, fake.NewClientBuilder().WithScheme(scheme).Build(), settled, "")
	require.NoError(t, err)
	hash := revision.Labels[lwsv1.RevisionKey]
	require.NotEmpty(t, hash)

	leader := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: settled.Name, Namespace: settled.Namespace, UID: "leader-uid", Generation: 2,
			Labels: map[string]string{lwsv1.RevisionKey: hash},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(settled, lwsv1.GroupVersion.WithKind("LeaderWorkerSet")),
			},
		},
		Spec:   appsv1.StatefulSetSpec{Replicas: ptr.To(int32(1))},
		Status: appsv1.StatefulSetStatus{ObservedGeneration: 2, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: settled.Name + "-0", Namespace: settled.Namespace, UID: "pod-uid",
			Labels: map[string]string{lwsv1.RevisionKey: hash},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(leader, appsv1.SchemeGroupVersion.WithKind("StatefulSet")),
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		}},
	}
	worker := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: pod.Name, Namespace: pod.Namespace, UID: "worker-uid", Generation: 2,
			Labels: map[string]string{lwsv1.RevisionKey: hash},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(pod, corev1.SchemeGroupVersion.WithKind("Pod")),
			},
		},
		Spec:   appsv1.StatefulSetSpec{Replicas: ptr.To(int32(1))},
		Status: appsv1.StatefulSetStatus{ObservedGeneration: 2, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
	}

	// The RBG reconciler applies the new template in the same reconcile that
	// runs finalizeRolloutStatus (reconcileRoles step 8 -> step 9). The LWS
	// object now carries app:v2 and the expected role-revision label; the LWS
	// controller has NOT yet created the app:v2 ControllerRevision, so the only
	// persisted revision is still the app:v1 one the deployed chain runs.
	applied := round10LWS(rbg, role, "app:v2")

	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(applied, leader, pod, worker, revision).Build()
	r := &RoleBasedGroupReconciler{client: c, apiReader: c}

	complete, err := r.roleWorkloadRolloutComplete(ctx, rbg, role, "target")
	require.NoError(t, err)
	assert.False(t, complete,
		"the LWS completion barrier must hold until the LWS controller persists the new revision and rolls the leader/worker chain onto it")

	// Control: once the LWS controller HAS persisted the new revision and the
	// deployed chain carries its hash, the barrier passes (guards against a
	// contract that merely asserts permanent incompleteness).
	newRevision, err := lwsrevision.NewRevision(ctx, fake.NewClientBuilder().WithScheme(scheme).Build(), applied, "")
	require.NoError(t, err)
	newHash := newRevision.Labels[lwsv1.RevisionKey]
	require.NotEqual(t, hash, newHash, "the new template must hash differently from the settled one")
	for _, obj := range []client.Object{leader, pod, worker} {
		obj.GetLabels()[lwsv1.RevisionKey] = newHash
	}
	c2 := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(applied, leader, pod, worker, newRevision).Build()
	r2 := &RoleBasedGroupReconciler{client: c2, apiReader: c2}
	complete, err = r2.roleWorkloadRolloutComplete(ctx, rbg, role, "target")
	require.NoError(t, err)
	assert.True(t, complete,
		"once the new revision is persisted and the chain runs it, the barrier must pass")
}

// TestVerifyPR474_NeedsUpdate_ReorderedRoleTemplates pins d923c387: a
// roleTemplates list written in a different order than the stored child's must
// not read as drift.
func TestVerifyPR474_NeedsUpdate_ReorderedRoleTemplates(t *testing.T) {
	set := &workloadsv1alpha2.RoleBasedGroupSet{}
	set.Spec.GroupTemplate.Spec.RoleTemplates = []workloadsv1alpha2.RoleTemplate{
		{Name: "alpha", Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "alpha", Image: "test:v1"}},
		}}},
		{Name: "beta", Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "beta", Image: "test:v1"}},
		}}},
	}

	data, err := json.Marshal(newRBGForSet(set, 0))
	require.NoError(t, err)
	child := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, json.Unmarshal(data, child))

	// Same templates, opposite order: semantically the same spec.
	child.Spec.RoleTemplates = []workloadsv1alpha2.RoleTemplate{
		child.Spec.RoleTemplates[1], child.Spec.RoleTemplates[0]}

	r := &RoleBasedGroupSetReconciler{}
	assert.False(t, r.needsUpdate(set, child),
		"a reordered roleTemplates list must not read as drift")

	child.Spec.RoleTemplates[0].Template.Spec.Containers[0].Image = "test:v2"
	assert.True(t, r.needsUpdate(set, child),
		"a real template change must still require an update")
}
