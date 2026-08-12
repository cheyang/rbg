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

// Reviewer-private verification harness for sgl-project/rbg#417.
// Production code is untouched; this file only observes isNodeAvailable.
package v1alpha2

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func verifyNode(name string, ready corev1.ConditionStatus, cordoned bool, taints ...corev1.Taint) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: cordoned, Taints: taints},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
				{Type: corev1.NodeReady, Status: ready},
			},
		},
	}
}

// F1 -- CONTRACT test (fails on PR head, passes once an effect filter is passed).
//
// kube-scheduler's TaintToleration plugin filters with
// `t.Effect == NoSchedule || t.Effect == NoExecute`, so a PreferNoSchedule taint
// never makes a node unschedulable -- it is a soft preference only. isNodeAvailable
// passes a nil inclusionFilter to FindMatchingUntoleratedTaint, which considers
// every taint regardless of effect, so a Ready, uncordoned node carrying only a
// PreferNoSchedule taint is reported unavailable.
//
// Consequence: on a cluster whose nodes carry a PreferNoSchedule taint, both loops
// in getFirstAvailableNodeName fall through and all five warmup cases die in
// ginkgo.Fail("no Ready and schedulable nodes found in the cluster"), even though
// the pods would have scheduled there fine.
func TestVerifyF1_PreferNoScheduleMustNotBlockNodeSelection(t *testing.T) {
	node := verifyNode("prefer-no-schedule-worker", corev1.ConditionTrue, false, corev1.Taint{
		Key:    "example.com/soft",
		Value:  "true",
		Effect: corev1.TaintEffectPreferNoSchedule,
	})

	if got := isNodeAvailable(node); !got {
		t.Errorf("isNodeAvailable(PreferNoSchedule node) = %v, want true "+
			"(kube-scheduler treats PreferNoSchedule as a soft signal; a tolerationless pod still schedules there)", got)
	}
}

// Baseline table: everything isNodeAvailable is meant to reject, it does reject.
// These must stay green -- they show F1 is a filter-argument bug, not a broken helper.
func TestVerifyF1_BaselineRejectionsStillHold(t *testing.T) {
	cases := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{"ready untainted worker", verifyNode("ok", corev1.ConditionTrue, false), true},
		{"cordoned", verifyNode("cordoned", corev1.ConditionTrue, true), false},
		{"not ready", verifyNode("notready", corev1.ConditionFalse, false), false},
		{"ready unknown", verifyNode("unknown", corev1.ConditionUnknown, false), false},
		{"no ready condition at all", &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "bare"}}, false},
		{"NoSchedule taint", verifyNode("nosched", corev1.ConditionTrue, false, corev1.Taint{
			Key: "nvidia.com/gpu", Value: "true", Effect: corev1.TaintEffectNoSchedule}), false},
		{"NoExecute taint", verifyNode("noexec", corev1.ConditionTrue, false, corev1.Taint{
			Key: "example.com/drain", Effect: corev1.TaintEffectNoExecute}), false},
	}
	for _, tc := range cases {
		if got := isNodeAvailable(tc.node); got != tc.want {
			t.Errorf("%s: isNodeAvailable() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// F2 -- OBSERVATION test (expected green on PR head, and still green after the F1 fix).
//
// defaultPodTolerations only tolerates node.kubernetes.io/not-ready and
// node.kubernetes.io/unreachable, which the node controller adds exactly when Ready
// is False or Unknown -- states the Ready gate already rejects. So the toleration
// list cannot change any verdict: dropping it entirely leaves every result identical.
// The inclusionFilter argument, not the toleration list, is what decides anything here.
func TestVerifyF2_DefaultPodTolerationsAreANoOp(t *testing.T) {
	saved := defaultPodTolerations
	t.Cleanup(func() { defaultPodTolerations = saved })

	nodes := []*corev1.Node{
		verifyNode("ready", corev1.ConditionTrue, false),
		verifyNode("cordoned", corev1.ConditionTrue, true),
		verifyNode("notready", corev1.ConditionFalse, false, corev1.Taint{
			Key: corev1.TaintNodeNotReady, Effect: corev1.TaintEffectNoExecute}),
		verifyNode("unreachable", corev1.ConditionUnknown, false, corev1.Taint{
			Key: corev1.TaintNodeUnreachable, Effect: corev1.TaintEffectNoExecute}),
		verifyNode("gpu", corev1.ConditionTrue, false, corev1.Taint{
			Key: "nvidia.com/gpu", Effect: corev1.TaintEffectNoSchedule}),
		// The only node shape where the tolerations could ever matter: Ready=True yet
		// still carrying the not-ready taint. The node controller does not produce this.
		verifyNode("contrived", corev1.ConditionTrue, false, corev1.Taint{
			Key: corev1.TaintNodeNotReady, Effect: corev1.TaintEffectNoExecute}),
	}

	for _, node := range nodes {
		defaultPodTolerations = saved
		withTolerations := isNodeAvailable(node)
		defaultPodTolerations = nil
		withoutTolerations := isNodeAvailable(node)

		if node.Name == "contrived" {
			if withTolerations == withoutTolerations {
				t.Errorf("contrived node: expected the tolerations to matter only for this shape, got %v both ways", withTolerations)
			}
			continue
		}
		if withTolerations != withoutTolerations {
			t.Errorf("node %s: with tolerations = %v, without = %v; expected the tolerations to be a no-op",
				node.Name, withTolerations, withoutTolerations)
		}
	}
}
