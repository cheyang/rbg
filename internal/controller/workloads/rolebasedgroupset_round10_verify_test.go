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

// Verification harness for PR #474 round 10 (reviewer additive tests; production code
// untouched). Covers the delta 8f07c37e..cb0a201a: a rebase onto c2896cc0 plus three new
// commits (d923c387 role-template order-insensitive compare, 76df3fe3 partition godoc,
// cb0a201a upgrade-convergence tests + a production change to leaderWorkerSetRolloutComplete).
//
//   R10LWSACK  - cb0a201a changed the LWS completion check to take the target hash from the
//                highest PERSISTED ControllerRevision instead of recomputing it from the live
//                LWS spec. Between the RBG controller's apply of a new leaderWorkerTemplate
//                (the LWS object carries the new role-revision label immediately) and the LWS
//                controller persisting the matching new revision, the highest persisted
//                revision still describes the OLD template and the old leader/pod/worker chain
//                still matches it, so the role reports complete in the very reconcile that
//                issued the change. finalizeRolloutStatus then publishes
//                RollingUpdateInProgress=False and acknowledges the outstanding rollout-request
//                token; the set's groupSetChildReady passes and the availability budget moves
//                to the next ordinal before any pod of this one has rolled. Contract: the
//                check must stay incomplete until a revision for the CURRENT template exists
//                and the chain matches it. Polarity: contract (RED at cb0a201a = live bug;
//                GREEN at 8f07c37e where the hash was recomputed from the desired spec).
//   R10TPLORDER - d923c387 fixes F6's order half: needsUpdate must treat a reordered
//                roleTemplates list as unchanged (sorted copies, semantic compare), while a
//                real content change must still be detected. Contract; GREEN at cb0a201a,
//                RED at 8f07c37e (order-sensitive apiequality.Semantic.DeepEqual).

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	lwsv1 "sigs.k8s.io/lws/api/leaderworkerset/v1"
	lwsrevision "sigs.k8s.io/lws/pkg/utils/revision"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/reconciler"
)

func verifyR10Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, lwsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

// verifyR10LWS builds the live LWS as the RBG controller's apply left it: template
// carrying the given image, the fresh role-revision label, and a stale-but-complete
// status from before the change (the LWS controller has not reconciled it yet).
func verifyR10LWS(rbg *workloadsv1alpha2.RoleBasedGroup, role *workloadsv1alpha2.RoleSpec, image, roleHash string) *lwsv1.LeaderWorkerSet {
	return &lwsv1.LeaderWorkerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: rbg.GetWorkloadName(role), Namespace: rbg.Namespace, UID: types.UID("lws-uid"), Generation: 2,
			Labels:          map[string]string{fmt.Sprintf(constants.RoleRevisionLabelKeyFmt, role.Name): roleHash},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(rbg, workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))},
		},
		Spec: lwsv1.LeaderWorkerSetSpec{
			Replicas: ptr.To(int32(1)),
			LeaderWorkerTemplate: lwsv1.LeaderWorkerTemplate{
				Size: ptr.To(int32(2)),
				WorkerTemplate: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "worker", Image: image}},
				}},
			},
		},
		Status: lwsv1.LeaderWorkerSetStatus{Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
	}
}

func verifyR10Chain(t *testing.T, lws *lwsv1.LeaderWorkerSet, hash string) (*appsv1.StatefulSet, *corev1.Pod, *appsv1.StatefulSet) {
	t.Helper()
	leader := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: lws.Name, Namespace: lws.Namespace, UID: types.UID("leader-uid"), Generation: 2,
			Labels:          map[string]string{lwsv1.RevisionKey: hash},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(lws, lwsv1.GroupVersion.WithKind("LeaderWorkerSet"))},
		},
		Spec:   appsv1.StatefulSetSpec{Replicas: ptr.To(int32(1))},
		Status: appsv1.StatefulSetStatus{ObservedGeneration: 2, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: lws.Name + "-0", Namespace: lws.Namespace, UID: types.UID("pod-uid"),
			Labels:          map[string]string{lwsv1.RevisionKey: hash},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(leader, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
	}
	worker := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: pod.Name, Namespace: pod.Namespace, UID: types.UID("worker-uid"), Generation: 2,
			Labels:          map[string]string{lwsv1.RevisionKey: hash},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(pod, corev1.SchemeGroupVersion.WithKind("Pod"))},
		},
		Spec:   appsv1.StatefulSetSpec{Replicas: ptr.To(int32(1))},
		Status: appsv1.StatefulSetStatus{ObservedGeneration: 2, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
	}
	return leader, pod, worker
}

// TestVerify_R10_LWSCompletionAwaitsCurrentRevision pins the completion barrier for LWS
// roles: after the RBG controller applies a new leaderWorkerTemplate, the rollout must NOT
// be reported complete (and the rollout-request token must NOT be acknowledged) until the
// LWS controller has persisted a revision for that template and the leader/pod/worker chain
// has caught up to it. The LWS controller is asynchronous, so the window between the apply
// and the new revision is real.
func TestVerify_R10_LWSCompletionAwaitsCurrentRevision(t *testing.T) {
	ctx := context.Background()
	scheme := verifyR10Scheme(t)
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "group", Namespace: "default", UID: types.UID("rbg-uid"), Generation: 2,
			Annotations: map[string]string{constants.RolloutRequestAnnotationKey: "2"},
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{{
			Name: "role", Replicas: ptr.To(int32(1)),
			Annotations: map[string]string{constants.RoleWorkloadTypeAnnotationKey: lwsv1.GroupVersion.String() + "/LeaderWorkerSet"},
		}}},
		Status: workloadsv1alpha2.RoleBasedGroupStatus{ObservedRolloutRequest: "1"},
	}
	role := &rbg.Spec.Roles[0]
	apimeta.SetStatusCondition(&rbg.Status.Conditions, metav1.Condition{
		Type: string(workloadsv1alpha2.RoleBasedGroupRollingUpdateInProgress), Status: metav1.ConditionTrue,
		ObservedGeneration: 2, Reason: "RolesUpdating",
	})

	// The world as revision 1 left it: template test:v1 fully rolled and serving.
	oldLWS := verifyR10LWS(rbg, role, "test:v1", "oldhash")
	emptyClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	rev1, err := lwsrevision.NewRevision(ctx, emptyClient, oldLWS, "")
	require.NoError(t, err)
	rev1.UID = types.UID("rev-1-uid")
	hashOld := rev1.Labels[lwsv1.RevisionKey]
	require.NotEmpty(t, hashOld)
	leader, pod, worker := verifyR10Chain(t, oldLWS, hashOld)

	// The RBG controller has just applied the new template (test:v2); the LWS object
	// already carries the new role-revision label, its status is still the stale complete
	// one, and the LWS controller has NOT persisted a revision for the new template yet.
	liveLWS := verifyR10LWS(rbg, role, "test:v2", "newhash")
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(rbg, liveLWS, rev1, leader, pod, worker).WithStatusSubresource(rbg).Build()
	r := &RoleBasedGroupReconciler{client: c, apiReader: c, scheme: scheme, recorder: record.NewFakeRecorder(10),
		workloadReconciler: make(map[string]reconciler.WorkloadReconciler)}
	expected := map[string]string{"role": "newhash"}

	complete, err := r.roleWorkloadRolloutComplete(ctx, rbg, role, "newhash")
	require.NoError(t, err)
	assert.False(t, complete,
		"no revision for the current template exists yet; the old chain must not satisfy the new rollout")
	require.NoError(t, r.finalizeRolloutStatus(ctx, rbg, expected))
	assert.Equal(t, "1", rbg.Status.ObservedRolloutRequest,
		"the outstanding rollout request must not be acknowledged before the new template rolls")
	assert.True(t, apimeta.IsStatusConditionTrue(rbg.Status.Conditions,
		string(workloadsv1alpha2.RoleBasedGroupRollingUpdateInProgress)),
		"the completion barrier must stay up while the LWS controller has not reacted")

	// Control: the LWS controller reacts — it persists revision 2 for the new template and
	// rolls the leader/pod/worker chain onto it. Completion must now be reported.
	rev2, err := lwsrevision.NewRevision(ctx, c, liveLWS, "")
	require.NoError(t, err)
	rev2.UID = types.UID("rev-2-uid")
	hashNew := rev2.Labels[lwsv1.RevisionKey]
	require.NotEmpty(t, hashNew)
	require.NotEqual(t, hashOld, hashNew, "premise: the template change must produce a new revision hash")
	require.NoError(t, c.Create(ctx, rev2))
	for _, obj := range []client.Object{leader, pod, worker} {
		obj.GetLabels()[lwsv1.RevisionKey] = hashNew
		require.NoError(t, c.Update(ctx, obj))
	}
	complete, err = r.roleWorkloadRolloutComplete(ctx, rbg, role, "newhash")
	require.NoError(t, err)
	assert.True(t, complete, "once the new revision is persisted and the chain caught up, completion must be reported")
	require.NoError(t, r.finalizeRolloutStatus(ctx, rbg, expected))
	assert.Equal(t, "2", rbg.Status.ObservedRolloutRequest, "a caught-up chain must acknowledge the request")
}

// TestVerify_R10_NeedsUpdateRoleTemplateOrder pins d923c387 (F6's order half): a child whose
// stored roleTemplates are the same templates in a different order must NOT be marked as
// needing an update on the static path, while a real content change must. Polarity:
// contract; RED at 8f07c37e (order-sensitive comparison), GREEN at cb0a201a.
func TestVerify_R10_NeedsUpdateRoleTemplateOrder(t *testing.T) {
	set := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default", UID: types.UID("test-set")},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(1)),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}},
					RoleTemplates: []workloadsv1alpha2.RoleTemplate{
						{Name: "decode", Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "worker", Image: "test:v2"}}}}},
						{Name: "prefill", Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "worker", Image: "test:v1"}}}}},
					},
				},
			},
		},
	}
	// Simulate apiserver storage of a child written when the template list was ordered
	// differently (e.g. an older controller or manual edit): same templates, swapped order.
	data, err := json.Marshal(set.Spec.GroupTemplate.Spec)
	require.NoError(t, err)
	child := &workloadsv1alpha2.RoleBasedGroup{ObjectMeta: metav1.ObjectMeta{Name: "test-0", Namespace: "default"}}
	require.NoError(t, json.Unmarshal(data, &child.Spec))
	child.Spec.RoleTemplates[0], child.Spec.RoleTemplates[1] =
		child.Spec.RoleTemplates[1], child.Spec.RoleTemplates[0]

	r := &RoleBasedGroupSetReconciler{}
	assert.False(t, r.needsUpdate(set, child),
		"a reordered roleTemplates list must not read as drift (permanent write churn otherwise)")

	// Control: a real template change must still be detected.
	changedSet := set.DeepCopy()
	changedSet.Spec.GroupTemplate.Spec.RoleTemplates[0].Template.Spec.Containers[0].Image = "test:v3"
	assert.True(t, r.needsUpdate(changedSet, child), "real roleTemplates changes must still require an update")
}
