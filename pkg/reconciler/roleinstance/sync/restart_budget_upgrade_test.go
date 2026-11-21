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

// Reviewer verification harness for sgl-project/rbg PR #637 (reviewer A,
// Claude). CONTRACT tests, polarity: assert the INTENDED correct behavior, so
// they FAIL on the PR head wherever the PR has the suspected defect and PASS
// once the defect is fixed.
//
// F1 (upgrade migration gap): builds of this repository up to and including
// the v0.9.0-alpha.5 tag (which contains #560) persist
// status.groupRestartRevision as a bare sha256 hex digest of the serialized
// components. PR #637 changes the format to "<ControllerRevision-name>/<hex
// size digest>". Issue #617 item 1 requires that landing this after a release
// be accompanied by an in-place migration that rewrites the old hash format
// WITHOUT resetting the count or ending exhaustion. No such migration exists
// in the PR, so the first reconcile after upgrading treats every persisted
// old-format revision as a topology revision change:
//
//   - exhausted groups are drained and released without
//     rbg.workloads.x-k8s.io/recover=true (the exact invariant the budget
//     exists to enforce), and
//   - groupRestartCount is silently reset to zero, granting extra restarts.
//
// The old format is recognizable: it contains no "/" separator.

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
)

// oldFormatRevision returns a revision string in the pre-#637 format: a bare
// 64-char hex digest with no ControllerRevision-name prefix.
func oldFormatRevision(seed int) string {
	return fmt.Sprintf("%064x", seed)
}

func TestBudgetUpgradeFromOldRevisionFormatPreservesExhaustion(t *testing.T) {
	ctx := context.Background()
	c, instance, pods := budgetFixture(t, ptr.To[int32](0)) // pods[0] is Failed
	selected := budgetTemplateRevision(t, instance)

	// Reproduce the pre-upgrade state exactly as the old controller left it:
	// exhaustion persisted, RI cleanup owner held, budget revision in the old
	// bare-hex format.
	require.NoError(t, c.exhaustRestartBudget(ctx, instance, selected))
	live := liveBudgetInstance(t, c, instance)
	require.True(t, live.Status.RestartBudgetExhausted)
	live.Status.GroupRestartRevision = oldFormatRevision(1)
	live.Status.GroupRestartCount = 3 // the old controller's durable count
	require.NoError(t, c.Status().Update(ctx, live))

	// First reconcile of the NEW controller build: the selected ControllerRevision
	// is unchanged; only the persisted revision's FORMAT predates this build.
	blocked, err := c.reconcileRestartBudget(ctx, live, selected)
	require.NoError(t, err)
	require.True(t, blocked)

	live = liveBudgetInstance(t, c, instance)
	require.Empty(t, live.Annotations[constants.RoleInstanceRecoverAnnotationKey],
		"no recovery was requested")
	require.True(t, live.Status.RestartBudgetExhausted,
		"F1: a controller upgrade (format-only revision change) must not release an exhausted group without recover=true")
	require.Equal(t, int32(3), live.Status.GroupRestartCount,
		"F1: the durable budget count must survive the format migration in place")
	require.Contains(t, live.Finalizers, constants.GroupRestartBudgetCleanupFinalizer,
		"F1: the RI cleanup owner must survive the format migration")
	for _, p := range pods {
		require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(p), &v1.Pod{})) ||
			slicesContainsFinalizer(c, ctx, p, constants.GroupRestartBudgetCleanupFinalizer),
			"F1: any surviving old-group Pod must still be retained by the budget finalizer")
	}
}

func TestBudgetUpgradeFromOldRevisionFormatPreservesCount(t *testing.T) {
	ctx := context.Background()
	c, instance, _ := budgetFixture(t, ptr.To[int32](2))
	selected := budgetTemplateRevision(t, instance)

	// A healthy (never exhausted) budgeted RI that consumed one attempt under
	// the old controller build.
	instance.Status.GroupRestartRevision = oldFormatRevision(2)
	instance.Status.GroupRestartCount = 1
	require.NoError(t, c.Status().Update(ctx, instance))

	blocked, err := c.reconcileRestartBudget(ctx, instance, selected)
	require.NoError(t, err)
	require.False(t, blocked)

	live := liveBudgetInstance(t, c, instance)
	require.Equal(t, int32(1), live.Status.GroupRestartCount,
		"F1: a format-only revision change must not grant extra restarts by resetting the durable count")
	require.Equal(t, groupRestartRevision(live, selected), live.Status.GroupRestartRevision,
		"F1: the migrated revision must equal the current computed identity")
}

func slicesContainsFinalizer(c *realControl, ctx context.Context, pod *v1.Pod, finalizer string) bool {
	fresh := &v1.Pod{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(pod), fresh); err != nil {
		return false
	}
	for _, f := range fresh.Finalizers {
		if f == finalizer {
			return true
		}
	}
	return false
}

var _ = client.ObjectKeyFromObject // keep the client import for the helper above
