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

// P0 premise verification for PR #637 (runs against the BASE branch).
// Claim: the budget revision depends on the controller's JSON encoding of the
// typed components, so the same stored spec can hash differently after a
// controller upgrade, and a mismatch silently releases an exhausted group
// without recover=true (issue #617 item 1).
// This test PASSES on base by demonstrating the buggy behavior.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerifyP0BudgetRevisionIsRawEncodingHash(t *testing.T) {
	_, instance, _ := budgetFixture(t, ptr.To[int32](0))
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
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(data)), groupRestartRevision(instance),
		"P0: base budget revision is a bare sha256 of this binary's JSON encoding of the typed components")
}

func TestVerifyP0BudgetRevisionMismatchReleasesExhaustedGroup(t *testing.T) {
	ctx := context.Background()
	c, instance, pods := budgetFixture(t, ptr.To[int32](0)) // pods[0] is Failed
	blocked, err := c.reconcileRestartBudget(ctx, instance)
	require.NoError(t, err)
	require.True(t, blocked)
	live := liveBudgetInstance(t, c, instance)
	require.True(t, live.Status.RestartBudgetExhausted)

	// Same spec; a hash written by a controller build that encodes it differently.
	live.Status.GroupRestartRevision = "hash-from-previous-controller-build"
	require.NoError(t, c.Status().Update(ctx, live))
	for i := 0; i < 3; i++ {
		_, err = c.reconcileRestartBudget(ctx, liveBudgetInstance(t, c, instance))
		require.NoError(t, err)
	}

	live = liveBudgetInstance(t, c, instance)
	require.Empty(t, live.Annotations[constants.RoleInstanceRecoverAnnotationKey])
	require.False(t, live.Status.RestartBudgetExhausted) // released without recover=true
	for _, p := range pods {                             // the retained Pods are gone
		require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(p), &v1.Pod{})))
	}
}
