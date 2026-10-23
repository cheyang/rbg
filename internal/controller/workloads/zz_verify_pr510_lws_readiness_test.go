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

// Verification harness for PR #510 review (finding F1).
//
// Claim under test: for an RBG role backed by a LeaderWorkerSet, the new
// roleWorkloadStatus/readiness aggregation can never report ready, because it
// requires the LWS Available condition to carry status.observedGeneration >=
// the LWS metadata.generation, while the real LWS controller (vendored
// sigs.k8s.io/lws v0.7.0, see makeCondition in
// pkg/controllers/leaderworkerset_controller.go) never sets observedGeneration
// on any condition. LWS status does not even have an ObservedGeneration field.
//
// These are CONTRACT tests: they assert the intended behavior and FAIL on the
// code under review. They are expected to pass once the freshness check is
// fixed (e.g. by not requiring condition observedGeneration for LWS).

package workloads

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	lwsv1 "sigs.k8s.io/lws/api/leaderworkerset/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// lwsConvergedFixture builds an RBG with a single LWS role and an LWS object
// whose status is exactly what the real LWS v0.7.0 controller writes for a
// fully rolled-out, fully ready group set:
//   - status.replicas / readyReplicas / updatedReplicas all equal spec.replicas
//   - an Available=True condition with reason AllGroupsReady
//   - NO observedGeneration on the condition (LWS makeCondition never sets it)
//
// If stampObservedGeneration is true, the condition additionally carries
// observedGeneration == lws.Generation, modeling a hypothetical LWS that
// maintains it; this control case isolates the freshness gate.
func lwsConvergedFixture(stampObservedGeneration bool) (*workloadsv1alpha2.RoleBasedGroup, *lwsv1.LeaderWorkerSet) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-lws", Namespace: "default", UID: "rbg-uid", Generation: 2},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{
				Name:     "worker",
				Replicas: ptr.To[int32](2),
			}},
		},
	}
	rbg.Spec.Roles[0].Annotations = map[string]string{
		constants.RoleWorkloadTypeAnnotationKey: constants.LeaderWorkerSetWorkloadType,
	}
	available := metav1.Condition{
		Type:               string(lwsv1.LeaderWorkerSetAvailable),
		Status:             metav1.ConditionTrue,
		Reason:             "AllGroupsReady",
		Message:            "All replicas are ready",
		LastTransitionTime: metav1.Now(),
	}
	lws := &lwsv1.LeaderWorkerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: rbg.GetWorkloadName(&rbg.Spec.Roles[0]), Namespace: rbg.Namespace, Generation: 1,
			Labels: map[string]string{
				fmt.Sprintf(constants.RoleRevisionLabelKeyFmt, "worker"): "role-revision",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: workloadsv1alpha2.GroupVersion.String(), Kind: "RoleBasedGroup",
				Name: rbg.Name, UID: rbg.UID, Controller: ptr.To(true),
			}},
		},
		Spec: lwsv1.LeaderWorkerSetSpec{Replicas: ptr.To[int32](2)},
		Status: lwsv1.LeaderWorkerSetStatus{
			Replicas:        2,
			ReadyReplicas:   2,
			UpdatedReplicas: 2,
		},
	}
	if stampObservedGeneration {
		available.ObservedGeneration = lws.Generation
	}
	lws.Status.Conditions = []metav1.Condition{available}
	return rbg, lws
}

func lwsVerifyClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, lwsv1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// Contract: a converged LWS role, as reported by the real LWS controller,
// must be reported ready by the RBG status aggregation.
// Fails on PR head: fresh = available.ObservedGeneration(0) >= generation(1)
// is always false, so ready is never true.
func TestVerifyPR510_LWSRealWorldStatusReady(t *testing.T) {
	rbg, lws := lwsConvergedFixture(false)
	c := lwsVerifyClient(t, lws)
	r := &RoleBasedGroupReconciler{client: c}

	phase, reason, ready, err := r.roleWorkloadStatus(context.Background(), rbg, &rbg.Spec.Roles[0], "role-revision")
	require.NoError(t, err)
	require.True(t, ready,
		"converged LWS role must be ready; got phase=%s reason=%s (real LWS never sets condition observedGeneration)", phase, reason)
}

// Control: the same object with observedGeneration stamped by a hypothetical
// LWS passes the freshness gate. Proves the harness isolates exactly the
// observedGeneration comparison and is not red for an unrelated reason.
func TestVerifyPR510_LWSStampedObservedGenerationReady(t *testing.T) {
	rbg, lws := lwsConvergedFixture(true)
	c := lwsVerifyClient(t, lws)
	r := &RoleBasedGroupReconciler{client: c}

	_, _, ready, err := r.roleWorkloadStatus(context.Background(), rbg, &rbg.Spec.Roles[0], "role-revision")
	require.NoError(t, err)
	require.True(t, ready, "control case with stamped observedGeneration should be ready")
}

// Contract: end-to-end through updateRBGStatus, an RBG whose only role is a
// converged LWS must report Ready=True. This is the user-visible symptom the
// e2e suites wait on (test/e2e/framework/rbg_v2_expect.go:86).
func TestVerifyPR510_RBGReadyWithRealWorldLWS(t *testing.T) {
	rbg, lws := lwsConvergedFixture(false)
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, lwsv1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(rbg).WithObjects(rbg, lws).Build()
	r := &RoleBasedGroupReconciler{client: c}

	statuses := []workloadsv1alpha2.RoleStatus{{
		Name: "worker", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2,
	}}
	err := r.updateRBGStatus(context.Background(), rbg, statuses, map[string]string{"worker": "role-revision"}, true)
	require.NoError(t, err)
	require.Equal(t, metav1.ConditionTrue, func() metav1.ConditionStatus {
		for _, cond := range rbg.Status.Conditions {
			if cond.Type == string(workloadsv1alpha2.RoleBasedGroupReady) {
				return cond.Status
			}
		}
		return metav1.ConditionStatus("MISSING")
	}(), "RBG Ready must become True for a converged LWS role")
}
