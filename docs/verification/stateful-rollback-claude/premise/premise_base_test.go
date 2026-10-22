//go:build rbg_premise_base

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

// Reviewer A (Claude) P0 premise check for PR #508 / issue #482.
//
// This file deliberately references BASE-ONLY symbols
// (recordEarlyRollbackReplacementSignal / earlyRollbackReplacementSignals),
// which the PR removes. It is compiled only against the PR base branch via
// the rbg_premise_base build tag — see scripts/run-premise-base.sh. It must
// NOT be compiled into normal head runs; the guard tag keeps it out.
//
// Claim under test (issue #482, quoted):
//   "The current proposed recovery approach tracks that replacement-in-progress
//    state in controller process memory (earlyRollbackReplacementUIDs). If the
//    controller restarts after the stale base instance has disappeared but
//    before the replacement is Ready, that state is lost. Topology then treats
//    the rollout as complete, collapses endOrdinal to replicas, and the
//    existing surge can be condemned and deleted while the replacement is
//    still unavailable."
//
// Polarity: bug-canary — it documents the pre-fix (wrong) behavior on base.
// It PASSES on base iff the symptom reproduces there.

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestClaudePremiseRestartLosesSurgeProtection walks issue #482's scenario on
// the base branch: early rollback A -> B -> A, the stale B base was deleted,
// its A replacement is not Ready yet, and the existing surge at ordinal 1 is
// the only ready instance.
func TestClaudePremiseRestartLosesSurgeProtection(t *testing.T) {
	set := buildSet("premise", 1,
		ptr.To(intstr.FromInt32(1)), ptr.To(intstr.FromInt32(0)))
	set.UID = "premise-set-uid" // the base in-memory signal is keyed on set UID
	set.Status.CurrentRevision = testUpdateRev
	set.Status.UpdateRevision = testUpdateRev

	// Replacement base (ordinal 0) exists but is not Ready at A yet; the
	// surge (ordinal 1) is Ready at A.
	replacement := buildInst(set.Name, 0, testUpdateRev, false, true)
	surge := buildInst(set.Name, 1, testUpdateRev, true, true)
	instances := []*workloadsv1alpha2.RoleInstance{replacement, surge}

	// 1) While the controller process is alive, the in-memory replacement
	//    signal protects the surge.
	recordEarlyRollbackReplacementSignal(set, 0, testUpdateRev)
	if !earlyRollbackReplacementSignalActive(set, instances, 0, 1, testUpdateRev) {
		t.Fatal("in-memory signal should protect surge while the replacement is unready")
	}
	topo, err := computeTopologyForReconcile(set, instances, testUpdateRev, testUpdateRev)
	if err != nil {
		t.Fatal(err)
	}
	if topo.endOrdinal < 2 {
		t.Fatalf("with the in-memory signal, surge must stay in range: %+v", topo)
	}

	// 2) Controller restart: the process-local signal is gone. The set status
	//    on base carries no replacement state, so nothing survives.
	earlyRollbackReplacementSignals.Delete(getInstanceSetKey(set))

	if earlyRollbackReplacementSignalActive(set, instances, 0, 1, testUpdateRev) {
		t.Fatal("after a restart the in-memory signal must be gone (process-local)")
	}
	topo, err = computeTopologyForReconcile(set, instances, testUpdateRev, testUpdateRev)
	if err != nil {
		t.Fatal(err)
	}
	if topo.inRollout {
		t.Fatalf("post-restart topology must treat the rollout as complete: %+v", topo)
	}
	// SYMPTOM (issue #482): endOrdinal collapses to replicas, so the existing
	// surge falls out of range and gets condemned while the replacement base
	// is still unavailable — violating maxUnavailable=0.
	if topo.endOrdinal != 1 || topo.activeSurge != 0 {
		t.Fatalf("post-restart topology must condemn the surge: %+v", topo)
	}
}
