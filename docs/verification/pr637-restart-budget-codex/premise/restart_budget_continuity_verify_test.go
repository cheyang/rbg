package sync

// Base-side control for F1: on the base branch, an alpha.5-written (native-format)
// stored revision reconciles WITHOUT resetting the count or releasing exhaustion.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func TestVerifyF1BaseControlNativeFormatIsStable(t *testing.T) {
	ctx := context.Background()
	// Non-exhausted with consumed budget: count preserved.
	c, instance, pods := budgetFixture(t, ptr.To[int32](2))
	for _, pod := range pods {
		pod.Status.Phase = v1.PodRunning
	}
	require.NoError(t, c.Status().Update(ctx, pods[0]))
	require.NoError(t, c.Status().Update(ctx, pods[1]))
	instance.Status.GroupRestartRevision = groupRestartRevision(instance)
	instance.Status.GroupRestartCount = 1
	require.NoError(t, c.Status().Update(ctx, instance))
	blocked, err := c.reconcileRestartBudget(ctx, instance)
	require.NoError(t, err)
	require.False(t, blocked)
	require.EqualValues(t, 1, liveBudgetInstance(t, c, instance).Status.GroupRestartCount)

	// Exhausted: stays exhausted without recover=true.
	c2, instance2, _ := budgetFixture(t, ptr.To[int32](0))
	blocked, err = c2.reconcileRestartBudget(ctx, instance2)
	require.NoError(t, err)
	require.True(t, blocked)
	live := liveBudgetInstance(t, c2, instance2)
	require.True(t, live.Status.RestartBudgetExhausted)
	for i := 0; i < 3; i++ {
		_, err = c2.reconcileRestartBudget(ctx, liveBudgetInstance(t, c2, instance2))
		require.NoError(t, err)
	}
	require.True(t, liveBudgetInstance(t, c2, instance2).Status.RestartBudgetExhausted)
}
