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

package sync

// Premise checks (claim P0) for sgl-project/rbg PR #637, run against the PR
// BASE (45e6e1c2) only. They reproduce the two release-critical problems that
// issue #617 reports against #560's restart budget:
//
//   - P0-a (issue item 2): a healthy, never-faulted budgeted RoleInstance
//     acquires the rbg.workloads.x-k8s.io/group-restart-budget-cleanup
//     finalizer on its first reconcile, making RI deletion depend on the
//     controller.
//   - P0-b (issue item 1 mechanism): when the persisted groupRestartRevision
//     no longer matches what the controller computes (as happens after a
//     serializer/encoding change across controller builds), an exhausted
//     group is released without rbg.workloads.x-k8s.io/recover=true, its
//     retained Pods are deleted, and a new group is created.
//   - P0-c (F1 attribution control): a plain controller upgrade with NO
//     encoding change leaves the persisted revision matching what the base
//     controller computes, so exhaustion is preserved on base. PR #637's
//     format change breaks exactly this case (see the head-side contract
//     tests TestBudgetUpgradeFromOldRevisionFormat*).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	"k8s.io/utils/ptr"
)

func TestPremiseP0aHealthyBudgetedInstanceAcquiresCleanupFinalizerOnBase(t *testing.T) {
	ctx := context.Background()
	c, instance, _ := budgetFixture(t, ptr.To[int32](2)) // healthy: no restart accepted yet
	blocked, err := c.reconcileRestartBudget(ctx, instance)
	require.NoError(t, err)
	require.False(t, blocked, "healthy budgeted instance must not be blocked")

	live := liveBudgetInstance(t, c, instance)
	require.Contains(t, live.Finalizers, constants.GroupRestartBudgetCleanupFinalizer,
		"P0-a: healthy budgeted RoleInstance carries the cleanup finalizer on base (issue #617 item 2)")
}

func TestPremiseP0bRevisionMismatchReleasesExhaustedGroupOnBase(t *testing.T) {
	ctx := context.Background()
	c, instance, pods := budgetFixture(t, ptr.To[int32](0)) // pods[0] is Failed
	blocked, err := c.reconcileRestartBudget(ctx, instance)
	require.NoError(t, err)
	require.True(t, blocked)

	live := liveBudgetInstance(t, c, instance)
	require.True(t, live.Status.RestartBudgetExhausted)
	require.Contains(t, live.Finalizers, constants.GroupRestartBudgetCleanupFinalizer)

	// Same spec; a revision written by a controller build that encodes it
	// differently (issue #617 item 1: the hash depends on JSON encoding).
	live.Status.GroupRestartRevision = "hash-from-previous-controller-build"
	require.NoError(t, c.Status().Update(ctx, live))
	for i := 0; i < 3; i++ {
		_, err = c.reconcileRestartBudget(ctx, liveBudgetInstance(t, c, instance))
		require.NoError(t, err)
	}

	live = liveBudgetInstance(t, c, instance)
	require.Empty(t, live.Annotations[constants.RoleInstanceRecoverAnnotationKey])
	require.False(t, live.Status.RestartBudgetExhausted,
		"P0-b: exhausted group is released without recover=true on base (issue #617 item 1)")
	require.Zero(t, live.Status.GroupRestartCount)
	for _, p := range pods {
		require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(p), &v1.Pod{})),
			"P0-b: retained Pods of the exhausted group are deleted on base")
	}
}

func TestPremiseP0cPlainUpgradeKeepsExhaustionOnBase(t *testing.T) {
	ctx := context.Background()
	c, instance, _ := budgetFixture(t, ptr.To[int32](0)) // pods[0] is Failed
	blocked, err := c.reconcileRestartBudget(ctx, instance)
	require.NoError(t, err)
	require.True(t, blocked)

	live := liveBudgetInstance(t, c, instance)
	require.True(t, live.Status.RestartBudgetExhausted)
	storedRevision := live.Status.GroupRestartRevision
	require.NotEmpty(t, storedRevision)

	// A plain controller upgrade (no spec change, no encoding change): the
	// persisted revision still matches what the (base) controller computes.
	for i := 0; i < 3; i++ {
		blocked, err = c.reconcileRestartBudget(ctx, liveBudgetInstance(t, c, instance))
		require.NoError(t, err)
		require.True(t, blocked)
	}
	live = liveBudgetInstance(t, c, instance)
	require.True(t, live.Status.RestartBudgetExhausted,
		"P0-c: a plain upgrade must keep exhaustion on base; PR #637's format change breaks this")
	require.Equal(t, storedRevision, live.Status.GroupRestartRevision)
}
