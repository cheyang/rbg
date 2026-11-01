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

package statefulmode

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// P0 premise check (reviewer A harness, PR #538).
//
// KEP-499 / issue #499 claims (quoted):
//
//	"Operators must retire a selected RoleInstance, including all component
//	 pods, while preserving healthy higher-ordinal instances and preventing
//	 the retired identity from being recreated."
//	"Stateless RoleInstanceSet has a specified-delete flow, but Stateful mode
//	 does not expose persistent ordinal retirement."
//
// This test documents the BASE behavior (it must PASS on the merge-base
// 4a0765f, where it also compiles): scaling a stateful set down always
// removes the HIGHEST ordinals and scale-up refills them, so there is no
// input that can retire ordinal 1 while keeping 2, 3, 4 alive. That is the
// user-visible capability gap the PR claims to close. It keeps passing on
// the PR head whenever the exclusion annotation is absent, which is exactly
// the "without the feature, behavior is unchanged" contract.
func TestClaudePremiseDenseScaleDownCannotRetireMiddleOrdinal(t *testing.T) {
	h := newRollbackTestHarness(t, "premise-dense", 5, 0, 0)

	// Five members [0,1,2,3,4], all at current revision.
	instances := h.instancesAt(t, 0, 1, 2, 3, 4)
	h.reconcile(t, instances...)
	h.assertDeleted(t)
	require.Empty(t, h.objects.created, "steady state must not create anything")

	// Scale down 5 -> 3 with no exclusion input available on base.
	h.set.Spec.Replicas = ptr.To[int32](3)
	h.reconcile(t, instances...)

	// Dense semantics: the highest ordinals die, 0,1,2 survive. Retiring
	// ordinal 1 requires also destroying 3 and 4.
	h.assertDeleted(t, "premise-dense-4", "premise-dense-3")

	// Scale back up: ordinals 3 and 4 are recreated — the retired identity is
	// not remembered anywhere, so identity churn is unavoidable on base.
	h.set.Spec.Replicas = ptr.To[int32](5)
	h.reconcile(t, h.instancesAt(t, 0, 1, 2)...)
	require.ElementsMatch(t, []string{"premise-dense-4", "premise-dense-3"}, h.objects.created,
		"scale-up refills the dense tail; no persistent hole exists on base")
}

func (h *rollbackTestHarness) instancesAt(t *testing.T, ordinals ...int) []*workloadsv1alpha2.RoleInstance {
	t.Helper()
	instances := make([]*workloadsv1alpha2.RoleInstance, 0, len(ordinals))
	for _, ordinal := range ordinals {
		instances = append(instances, h.instance(ordinal, h.current.Name, true))
	}
	return instances
}
