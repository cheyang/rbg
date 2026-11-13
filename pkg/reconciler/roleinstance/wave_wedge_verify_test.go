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

package roleinstance

// Verification harness for PR #573 (review finding F1).
//
// Contract under test: when the pod deletion of a restart-policy recreation
// FAILS (transient API error, admission rejection, ...), a later reconcile must
// retry the deletion so the recreation wave can complete. The restart policy
// must not go permanently inert while the surviving pre-restart pods stay
// running.
//
// This file intentionally compiles against BOTH the PR base and the PR head
// (only stable symbols are used) so the same contract can be run as a control
// on the base branch:
//   - base: the Ready snapshot clears the in-memory marker and the condition is
//     never persisted when nothing was deleted, so the next reconcile retries
//     the recreation (and its delete) -> contract PASSES.
//   - head: the wave marker survives (ClearRestarting is skipped while
//     Restarting=True is propagated) and the guard consumes every re-trigger,
//     while nothing re-issues the failed delete -> contract FAILS (wedge).

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/lru"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	revisioncontrol "sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/revision"
	synccontrol "sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/sync"
	instanceutil "sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/utils"
	pkgutils "sigs.k8s.io/rbgs/pkg/utils"
	"sigs.k8s.io/rbgs/pkg/utils/fieldindex"

	historyutil "github.com/openkruise/kruise/pkg/util/history"
)

// deleteRecordingClient forwards every call to the inner client but records
// Delete calls and fails them for configured pod names.
type failDeleteClient struct {
	client.Client
	failOn      map[string]bool
	deleteCalls []string
}

func (f *failDeleteClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	f.deleteCalls = append(f.deleteCalls, obj.GetName())
	if f.failOn[obj.GetName()] {
		return fmt.Errorf("injected delete failure for pod %s", obj.GetName())
	}
	return f.Client.Delete(ctx, obj, opts...)
}

func waveVerifyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

func waveVerifyInstance() *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name: "wedge-test", Namespace: "default", UID: "wedge-inst-uid", Generation: 1,
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			ReadyPolicy: workloadsv1alpha2.RoleInstanceReadyOnAllPodReady,
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{
				Type: workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
				// Disable backoff so the contract does not depend on wall-clock.
				BaseDelaySeconds: ptr.To(int32(0)),
				MaxDelaySeconds:  ptr.To(int32(0)),
			},
			Components: []workloadsv1alpha2.RoleInstanceComponent{{
				Name: "c",
				Size: ptr.To(int32(2)),
				Template: v1.PodTemplateSpec{
					Spec: v1.PodSpec{Containers: []v1.Container{{Name: "main", Image: "img"}}},
				},
			}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration:  1,
			CurrentRevision:     "r1",
			UpdateRevision:      "r1",
			LastReadyGeneration: 1,
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{{
				Type: workloadsv1alpha2.RoleInstanceReady, Status: v1.ConditionTrue,
			}},
		},
	}
}

func waveVerifyPod(instance *workloadsv1alpha2.RoleInstance, revHash string, id int32, restartCount int32) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instanceutil.FormatComponentPodName(instance.Name, "c", id, instance.GetRoleTemplateType()),
			Namespace: instance.Namespace,
			UID:       types.UID(fmt.Sprintf("wedge-pod-%d", id)),
			Labels: map[string]string{
				constants.RoleInstanceNameLabelKey: instance.Name,
				constants.ComponentNameLabelKey:    "c",
				constants.ComponentIDLabelKey:      fmt.Sprintf("%d", id),
				// Pin the pod to the current revision so the rolling-update
				// path leaves it alone: the only pod-deleting mechanism left is
				// the restart-policy recreation itself.
				appsv1.ControllerRevisionHashLabelKey: revHash,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: workloadsv1alpha2.SchemeGroupVersion.String(), Kind: "RoleInstance",
				Name: instance.Name, UID: instance.UID, Controller: ptr.To(true),
			}},
		},
		Status: v1.PodStatus{
			Phase:      v1.PodRunning,
			Conditions: []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionTrue}},
			ContainerStatuses: []v1.ContainerStatus{{
				Name: "main", RestartCount: restartCount,
			}},
		},
	}
}

func newWaveVerifyReconciler(c client.Client, scheme *runtime.Scheme) *reconciler {
	recorder := record.NewFakeRecorder(100)
	return &reconciler{
		Client:                c,
		scheme:                scheme,
		recorder:              recorder,
		controllerHistory:     historyutil.NewHistory(c),
		statusUpdater:         newStatusUpdater(c),
		revisionControl:       revisioncontrol.NewRevisionControl(),
		syncControl:           synccontrol.New(c, c, recorder, synccontrol.NewNodeBindingStore()),
		revisionEqualityCache: lru.New(pkgutils.MaxRevisionEqualityCacheEntries),
	}
}

// Contract: a recreation wave whose pod deletion failed must be retried by a
// later reconcile once the API error is gone. Failing the delete must not
// permanently disarm the restart policy while the pre-restart pods survive.
func TestVerifyWaveDeletionRetriedAfterFailure(t *testing.T) {
	scheme := waveVerifyScheme(t)
	instance := waveVerifyInstance()
	// Compute the revision the first reconcile will create, so the fixture pods
	// can be pinned to it (otherwise the rolling-update path recreates them and
	// masks the restart-policy behavior under test).
	collisionCount := int32(0)
	rev, err := revisioncontrol.NewRevisionControl().NewRevision(instance, 1, &collisionCount)
	require.NoError(t, err)
	revHash := instanceutil.GetShortHash(rev.Name)
	// pod-0 carries the trigger (unexpected container restart); both pods are
	// Running and Ready, i.e. the pod snapshot still shows the instance Ready.
	pod0 := waveVerifyPod(instance, revHash, 0, 1)
	pod1 := waveVerifyPod(instance, revHash, 1, 0)

	inner := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(instance, pod0, pod1).
		WithStatusSubresource(&workloadsv1alpha2.RoleInstance{}, &v1.Pod{}).
		WithIndex(&v1.Pod{}, fieldindex.IndexNameForOwnerRefUID, func(obj client.Object) []string {
			owners := make([]string, 0, len(obj.GetOwnerReferences()))
			for _, ref := range obj.GetOwnerReferences() {
				owners = append(owners, string(ref.UID))
			}
			return owners
		}).
		Build()
	failing := &failDeleteClient{Client: inner, failOn: map[string]bool{pod0.Name: true, pod1.Name: true}}
	r := newWaveVerifyReconciler(failing, scheme)
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)}

	// Reconcile 1: the recreation trigger fires, the wave starts, and the pod
	// deletion fails (transient API error).
	_, err = r.Reconcile(context.Background(), req)
	require.Error(t, err, "reconcile 1 must surface the injected delete failure")
	require.NotEmpty(t, failing.deleteCalls, "reconcile 1 must have attempted the recreation deletion")

	// Reconcile 2 (and 3): the API error is gone. The failed deletion must be
	// retried so the wave can finish. On the PR head the wave guard consumes
	// the re-trigger while the recorded pods survive and nothing re-issues the
	// delete: the restart policy is wedged.
	healthy := &failDeleteClient{Client: inner}
	r2 := newWaveVerifyReconciler(healthy, scheme)
	_, err = r2.Reconcile(context.Background(), req)
	require.NoError(t, err)
	_, err = r2.Reconcile(context.Background(), req)
	require.NoError(t, err)

	retried := false
	for _, name := range healthy.deleteCalls {
		if name == pod0.Name || name == pod1.Name {
			retried = true
		}
	}
	require.True(t, retried,
		"the failed recreation deletion must be retried once the API recovers; "+
			"otherwise the wave guard blocks every later failure while the recorded pods survive (restart policy wedged)")

	// Persisted state sanity: log the Restarting condition the retries left behind.
	live := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, inner.Get(context.Background(), client.ObjectKeyFromObject(instance), live))
	for _, cond := range live.Status.Conditions {
		if cond.Type == workloadsv1alpha2.RoleInstanceRestarting {
			t.Logf("persisted Restarting condition after retries: status=%s reason=%s", cond.Status, cond.Reason)
		}
	}
	// The original pre-restart pods (identified by UID; replacements reuse the
	// ordinal names) must be gone or terminating once the retry ran.
	for _, p := range []*v1.Pod{pod0, pod1} {
		livePod := &v1.Pod{}
		err := inner.Get(context.Background(), client.ObjectKeyFromObject(p), livePod)
		if err != nil {
			continue
		}
		require.NotEqualf(t, p.UID, livePod.UID,
			"pre-restart pod %s (uid %s) survived although its recreation deletion was retried", p.Name, p.UID)
	}
}
