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

// Verification harness for PR #637, finding F1 (reviewer: codex).
//
// PR #637 changes the budget-revision format from the bare sha256 of the
// JSON-serialized components (shipped in v0.9.0-alpha.5 via #560) to
// "<selected ControllerRevision name>/<sha256 of sorted component sizes>".
// Issue #617 item 1 states: "If this lands after a release, it also needs a
// migration that rewrites the old hash format in place without resetting the
// count or exhaustion; otherwise upgrading to the fix triggers this bug."
// v0.9.0-alpha.5 (tagged 2026-11-21) contains #560 without this PR, so stored
// old-format revisions exist on released clusters.
//
// These are CONTRACT tests (polarity: contract). They encode the behavior the
// issue requires on the alpha.5 -> PR upgrade path. On the unpatched PR head
// they FAIL, proving the missing migration; the temporary migration patch in
// docs/verification/pr637-restart-budget-codex/premise/migration-fix.patch
// flips them green (harness-bites proof).

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// legacyGroupRestartRevision replicates groupRestartRevision as shipped in
// v0.9.0-alpha.5 (#560): the bare sha256 of the JSON-serialized, name-sorted
// components. This is what released clusters have persisted in
// status.groupRestartRevision for budgeted RoleInstances.
func legacyGroupRestartRevision(instance *workloadsv1alpha2.RoleInstance) string {
	components := instance.DeepCopy().Spec.Components
	slices.SortFunc(components, func(a, b workloadsv1alpha2.RoleInstanceComponent) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	data, _ := json.Marshal(components)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// F1a: a healthy budgeted RoleInstance written by v0.9.0-alpha.5 has already
// consumed one budget attempt (groupRestartCount=1, limit=2). Its stored
// revision uses the old hash format. Upgrading to this PR must NOT reset the
// consumed budget: the issue requires an in-place re-stamp.
//
// Contract: after reconcile, groupRestartCount stays 1 and no recovery/reset
// side effects occur.
func TestVerifyF1aUpgradeFromAlpha5MustNotResetConsumedBudget(t *testing.T) {
	ctx := context.Background()
	c, instance, pods := budgetFixture(t, ptr.To[int32](2))
	for _, pod := range pods {
		pod.Status.Phase = v1.PodRunning // healthy group, no fault pending
	}
	require.NoError(t, c.Status().Update(ctx, pods[0]))
	require.NoError(t, c.Status().Update(ctx, pods[1]))
	// Simulate the state v0.9.0-alpha.5 persisted after one accepted restart.
	instance.Status.GroupRestartRevision = legacyGroupRestartRevision(instance)
	instance.Status.GroupRestartCount = 1
	require.NoError(t, c.Status().Update(ctx, instance))

	selected := budgetTemplateRevision(t, instance)
	blocked, err := c.reconcileRestartBudget(ctx, instance, selected)
	require.NoError(t, err)
	live := liveBudgetInstance(t, c, instance)
	t.Logf("after upgrade reconcile: blocked=%v count=%d revision=%q (legacy was %q)",
		blocked, live.Status.GroupRestartCount, live.Status.GroupRestartRevision, legacyGroupRestartRevision(instance))
	require.EqualValues(t, 1, live.Status.GroupRestartCount,
		"F1a: upgrading from v0.9.0-alpha.5 must not silently replenish the consumed restart budget")
	require.False(t, live.Status.RestartBudgetExhausted)
}

// F1b: an exhausted RoleInstance written by v0.9.0-alpha.5 (exhaustion
// persisted, group retained under budget finalizers, NO recover annotation).
// The documented rule is that only explicit recovery (recover=true) or a new
// Pod-template revision ends exhaustion. A controller upgrade is neither.
//
// Contract: after reconcile without a recover annotation, the RoleInstance
// must still be exhausted and the retained group must still be present.
func TestVerifyF1bUpgradeFromAlpha5MustNotReleaseExhaustion(t *testing.T) {
	ctx := context.Background()
	c, instance, pods := budgetFixture(t, ptr.To[int32](0))
	selected := budgetTemplateRevision(t, instance)
	// Drive the PR-head code to the exhausted state first (this mimics what
	// alpha.5 persisted, except for the revision format which we then rewind).
	blocked, err := c.reconcileRestartBudget(ctx, instance, selected)
	require.NoError(t, err)
	require.True(t, blocked)
	live := liveBudgetInstance(t, c, instance)
	require.True(t, live.Status.RestartBudgetExhausted)
	// Rewind the stored revision to the alpha.5 wire format, as an upgraded
	// cluster would actually carry.
	live.Status.GroupRestartRevision = legacyGroupRestartRevision(live)
	require.NoError(t, c.Status().Update(ctx, live))
	// Sanity: the retained group still exists and no recovery was requested.
	for _, p := range pods {
		got := &v1.Pod{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(p), got))
	}
	require.Empty(t, live.Annotations[constants.RoleInstanceRecoverAnnotationKey])

	blocked, err = c.reconcileRestartBudget(ctx, liveBudgetInstance(t, c, instance), selected)
	require.NoError(t, err)
	require.True(t, blocked)
	live = liveBudgetInstance(t, c, instance)
	t.Logf("after upgrade reconcile: exhausted=%v count=%d revision=%q", live.Status.RestartBudgetExhausted, live.Status.GroupRestartCount, live.Status.GroupRestartRevision)
	require.True(t, live.Status.RestartBudgetExhausted,
		"F1b: a controller upgrade must not release exhaustion without recover=true")
	for _, p := range pods {
		got := &v1.Pod{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(p), got),
			"F1b: retained group members must survive an upgrade; only recovery may release them")
	}
	// Degraded suppression must stay in place while exhausted.
	require.True(t, slices.ContainsFunc(live.Status.Conditions, func(cond workloadsv1alpha2.RoleInstanceCondition) bool {
		return cond.Type == workloadsv1alpha2.RoleInstanceDegraded && cond.Status == v1.ConditionTrue
	}), "F1b: Degraded suppression must survive the upgrade")
}

// F1-control: the same upgrade reconcile leaves an RI untouched when alpha.5
// never recorded a budget revision (healthy, never restarted). This is the
// unaffected population and must stay unaffected.
func TestVerifyF1ControlNeverRestartedBudgetIsUntouched(t *testing.T) {
	ctx := context.Background()
	c, instance, pods := budgetFixture(t, ptr.To[int32](2))
	for _, pod := range pods {
		pod.Status.Phase = v1.PodRunning
	}
	require.NoError(t, c.Status().Update(ctx, pods[0]))
	require.NoError(t, c.Status().Update(ctx, pods[1]))
	selected := budgetTemplateRevision(t, instance)
	blocked, err := c.reconcileRestartBudget(ctx, instance, selected)
	require.NoError(t, err)
	require.False(t, blocked)
	live := liveBudgetInstance(t, c, instance)
	require.Empty(t, live.Status.GroupRestartRevision)
	require.Zero(t, live.Status.GroupRestartCount)
	require.NotContains(t, live.Finalizers, constants.GroupRestartBudgetCleanupFinalizer)
	_ = metav1.Now()
}
