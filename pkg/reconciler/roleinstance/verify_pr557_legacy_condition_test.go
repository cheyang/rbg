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

// Verification harness for PR #557 (reviewer: codex). Additive test file.
//
// Upgrade compatibility (contract polarity): an instance that was mid-restart
// under the pre-PR controller carries a persisted Restarting=True condition
// with no status.retiringPods. The new controller must not let that legacy
// condition suppress future recreations and must clear it on the next status
// write (updateStatus projects Restarting from the live retiring list only).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestPR557LegacyRestartingConditionCleared(t *testing.T) {
	ctx := context.Background()
	informer := newReadinessTestInstance()
	informer.Status.Conditions = append(informer.Status.Conditions,
		workloadsv1alpha2.RoleInstanceCondition{
			Type: workloadsv1alpha2.RoleInstanceRestarting, Status: v1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		})
	live := informer.DeepCopy()
	c := fake.NewClientBuilder().WithScheme(readinessTestScheme(t)).
		WithStatusSubresource(&workloadsv1alpha2.RoleInstance{}).WithObjects(live).Build()
	updater := &realStatusUpdater{Client: c, apiReader: c}
	computed := informer.Status.DeepCopy()
	computed.ObservedGeneration = informer.Generation
	computed.Conditions = nil // this reconcile did not project Restarting
	require.NoError(t, updater.updateStatus(ctx, informer, computed))
	actual := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(live), actual))
	for _, condition := range actual.Status.Conditions {
		if condition.Type == workloadsv1alpha2.RoleInstanceRestarting && condition.Status == v1.ConditionTrue {
			t.Fatalf("legacy Restarting=True survived a status write with an empty retiring list: %+v", actual.Status.Conditions)
		}
	}
	require.Empty(t, actual.Status.RetiringPods)
}
