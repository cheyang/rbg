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

// Reviewer-B (Codex) premise check (P0) for PR #508 / issue #482.
// This file compiles ONLY against the merge-base (pre-fix) code: it references
// the process-local signal map that the PR removes. Run it by copying it into
// pkg/reconciler/roleinstanceset/statefulmode/ on a base checkout.
//
// Contract polarity: it asserts the DESIRED behavior (surge retained across a
// controller restart). On the base code it must FAIL — that failure is the
// reproduction of issue #482.

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestCodexP0RestartLosesReplacementSignal(t *testing.T) {
	resetEarlyRollbackReplacementSignals()
	t.Cleanup(resetEarlyRollbackReplacementSignals)

	set := buildSet("codex-p0", 1,
		ptr.To(intstr.FromInt32(1)), ptr.To(intstr.FromInt32(0)))
	set.UID = "uid-codex-p0"
	const revA = "codex-rev-a"

	// Issue #482 step 6-7 state: the stale B base has been deleted, the
	// replacement base (ord 0) exists at A but is not Ready, and the existing
	// surge (ord 1) is Ready at A.
	replacement := buildInst(set.Name, 0, revA, false, true)
	surge := buildInst(set.Name, 1, revA, true, true)
	instances := []*workloadsv1alpha2.RoleInstance{replacement, surge}

	// Sanity: with the process-local signal recorded (pre-restart), the
	// controller retains the surge.
	recordEarlyRollbackReplacementSignal(set, 0, revA)
	topo, err := computeTopologyForReconcile(set, instances, revA, revA)
	if err != nil {
		t.Fatalf("computeTopologyForReconcile (pre-restart): %v", err)
	}
	if topo.endOrdinal != 2 || topo.activeSurge != 1 {
		t.Fatalf("pre-restart sanity failed: endOrdinal=%d activeSurge=%d, want 2/1",
			topo.endOrdinal, topo.activeSurge)
	}

	// Simulate the controller restart: process-local memory is gone.
	resetEarlyRollbackReplacementSignals()

	topo, err = computeTopologyForReconcile(set, instances, revA, revA)
	if err != nil {
		t.Fatalf("computeTopologyForReconcile (post-restart): %v", err)
	}
	// DESIRED (contract): the surge must be retained until the replacement is
	// Ready, i.e. endOrdinal must still cover ordinal 1.
	if topo.endOrdinal != 2 || topo.activeSurge != 1 || !topo.inRollout {
		t.Fatalf("issue #482 reproduced: after a restart the replacement signal is lost; "+
			"topology collapses to endOrdinal=%d activeSurge=%d inRollout=%v, so the Ready surge "+
			"at ordinal 1 falls out of range and is condemned while the replacement base is unready",
			topo.endOrdinal, topo.activeSurge, topo.inRollout)
	}
}
