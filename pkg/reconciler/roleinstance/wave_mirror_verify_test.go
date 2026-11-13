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

// Verification harness for PR #573 (review finding F2).
//
// Contract under test: when a restart wave completes, the flipped
// Restarting=False (RestartCompleted) condition computed during Scale must be
// persisted — even when the status write of the wave-completion reconcile
// previously failed and a quiescent later reconcile recomputes the same flip.
//
// Suspected mechanism: syncInstance calls mirrorRestartingCondition to copy the
// authoritative Restarting view onto the informer copy BEFORE
// UpdateInstanceStatus. UpdateInstanceStatus diffs newStatus against that
// already-mirrored copy (inconsistentStatus), so the mirror erases the very
// difference that would trigger the repair write, and a stale persisted
// Restarting=True can survive indefinitely on a quiescent instance.
//
// The test drives the real syncInstance (with a fake sync.Interface whose Scale
// flips the condition, exactly like sync.releaseCompletedRestartWave does), so
// it stays glued to the production seam. Head-only: mirrorRestartingCondition
// does not exist on the base branch.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	revisioncontrol "sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/revision"
	instanceutil "sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/utils"
)

// completedWaveSyncControl fakes sync.Interface: Scale applies the wave
// completion flip to the revision-patched copy, exactly like
// sync.releaseCompletedRestartWave does when every pre-restart pod is gone.
type completedWaveSyncControl struct {
	flipTo workloadsv1alpha2.RoleInstanceCondition
}

func (f *completedWaveSyncControl) Scale(_ context.Context, updateInstance *workloadsv1alpha2.RoleInstance, _, _ *apps.ControllerRevision, _ []*apps.ControllerRevision, _, _ []*v1.Pod) (bool, time.Duration, error) {
	for i := range updateInstance.Status.Conditions {
		if updateInstance.Status.Conditions[i].Type == workloadsv1alpha2.RoleInstanceRestarting {
			updateInstance.Status.Conditions[i] = f.flipTo
			return false, 0, nil
		}
	}
	updateInstance.Status.Conditions = append(updateInstance.Status.Conditions, f.flipTo)
	return false, 0, nil
}

func (f *completedWaveSyncControl) Update(_ context.Context, _ *workloadsv1alpha2.RoleInstance, _ *workloadsv1alpha2.RoleInstanceStatus, _, _ *apps.ControllerRevision, _ []*apps.ControllerRevision, _ []*v1.Pod) (time.Duration, error) {
	return 0, nil
}

func (f *completedWaveSyncControl) ClearRestarting(_ *workloadsv1alpha2.RoleInstance) {}

func TestVerifyCompletedWaveConditionFlipIsPersisted(t *testing.T) {
	ctx := context.Background()
	scheme := waveVerifyScheme(t)

	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "wave-mirror", Namespace: "default", UID: types.UID("uid-wave-mirror"), Generation: 1},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			ReadyPolicy:         workloadsv1alpha2.RoleInstanceReadyOnAllPodReady,
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{Type: workloadsv1alpha2.RecreateRoleInstanceOnPodRestart},
			Components: []workloadsv1alpha2.RoleInstanceComponent{{
				Name: "c", Size: ptr.To(int32(1)),
				Template: v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "main", Image: "img"}}}},
			}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1, CurrentRevision: "r1", UpdateRevision: "r1", LastReadyGeneration: 1,
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(instance).
		WithStatusSubresource(&workloadsv1alpha2.RoleInstance{}).
		Build()
	updater := newStatusUpdater(c)
	revControl := revisioncontrol.NewRevisionControl()

	// The revision name is stable across reconciles (same spec); compute it up
	// front so both simulated reconciles report the same revisions, exactly like
	// the real Reconcile does.
	collisionCount := int32(0)
	rev, err := revControl.NewRevision(instance, 1, &collisionCount)
	require.NoError(t, err)

	// Reconcile A: a wave started; Restarting=True was persisted. This stands in
	// for the state left behind when the wave-completion write later fails.
	waveStartLRT := metav1.NewTime(time.Now().Add(-time.Minute))
	{
		inst := &workloadsv1alpha2.RoleInstance{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(instance), inst))
		ns := &workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1, CurrentRevision: rev.Name, UpdateRevision: rev.Name, LastReadyGeneration: 1,
			RestartCount: 1, LastRestartTime: &waveStartLRT,
			LabelSelector: instanceutil.GetSelector(inst).String(),
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{{
				Type: workloadsv1alpha2.RoleInstanceRestarting, Status: v1.ConditionTrue,
				Reason: "RestartPolicyTriggered", Message: "Instance is being recreated due to restart policy",
			}},
		}
		require.NoError(t, updater.UpdateInstanceStatus(ctx, inst, ns, nil))
		live := &workloadsv1alpha2.RoleInstance{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(instance), live))
		cond := findVerifyCondition(live, workloadsv1alpha2.RoleInstanceRestarting)
		require.NotNil(t, cond)
		require.Equal(t, v1.ConditionTrue, cond.Status, "wave-start reconcile must persist Restarting=True")
	}

	// Reconcile B: quiescent instance (no pods changed), the wave-completion
	// flip is recomputed by Scale. syncInstance must persist it.
	informerCopy := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(instance), informerCopy))

	flipped := workloadsv1alpha2.RoleInstanceCondition{
		Type: workloadsv1alpha2.RoleInstanceRestarting, Status: v1.ConditionFalse,
		Reason: "RestartCompleted", Message: "Restart policy recreation completed; new failures re-arm after backoff",
	}
	r := &reconciler{
		Client:            c,
		scheme:            scheme,
		recorder:          nil,
		statusUpdater:     updater,
		revisionControl:   revControl,
		syncControl:       &completedWaveSyncControl{flipTo: flipped},
		controllerHistory: nil,
	}
	newStatus := &workloadsv1alpha2.RoleInstanceStatus{
		ObservedGeneration:  informerCopy.Generation,
		CurrentRevision:     rev.Name,
		UpdateRevision:      rev.Name,
		CollisionCount:      new(int32),
		LabelSelector:       instanceutil.GetSelector(informerCopy).String(),
		RestartCount:        informerCopy.Status.RestartCount,
		LastRestartTime:     informerCopy.Status.LastRestartTime,
		LastReadyGeneration: informerCopy.Status.LastReadyGeneration,
	}
	res := r.syncInstance(ctx, informerCopy, newStatus, rev, rev, []*apps.ControllerRevision{rev}, nil, nil)
	require.NoError(t, res.err)
	require.NoError(t, updater.UpdateInstanceStatus(ctx, informerCopy, newStatus, nil))

	live := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(instance), live))
	cond := findVerifyCondition(live, workloadsv1alpha2.RoleInstanceRestarting)
	require.NotNil(t, cond, "the Restarting condition must remain present (False) after wave completion")
	require.Equalf(t, v1.ConditionFalse, cond.Status,
		"the completed-wave flip must be persisted even when nothing else changed; got stale %s/%s",
		cond.Status, cond.Reason)
	require.Equal(t, "RestartCompleted", cond.Reason)
}

func findVerifyCondition(instance *workloadsv1alpha2.RoleInstance, condType workloadsv1alpha2.RoleInstanceConditionType) *workloadsv1alpha2.RoleInstanceCondition {
	for i := range instance.Status.Conditions {
		if instance.Status.Conditions[i].Type == condType {
			return &instance.Status.Conditions[i]
		}
	}
	return nil
}
