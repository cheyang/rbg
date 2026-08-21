package workloads

import (
	"context"
	"fmt"
	"math"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// Verification harness for PR sgl-project/rbg#396 ("Enable HPA scaling for
// RoleBasedGroupSet"), review round 1.
//
// The PR makes RoleBasedGroupSet targetable by HPA/KEDA by publishing a scale
// subresource selector:
//
//	+kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas,selectorpath=.status.selector
//	newStatus.Selector = labels.Set{constants.GroupSetNameLabelKey: rbgset.Name}.String()
//
// and by propagating the groupset labels down onto Pod templates so that
// selector actually matches something.
//
// THE QUESTION THIS FILE SETTLES: the three scale-subresource paths must agree on
// a unit. `spec.replicas` and `status.replicas` count child RoleBasedGroups
// (groups), but `status.selector` selects Pods -- and the HPA controller derives
// the replica count it writes back from the number of PODS it matched, not from
// the currentReplicas it read. Verbatim from Kubernetes
// pkg/controller/podautoscaler/replica_calculator.go (release-1.32),
// GetResourceReplicas:
//
//	podList, err := c.podLister.Pods(namespace).List(selector)
//	...
//	if len(podList) == 0 {
//	    return 0, 0, 0, time.Time{}, fmt.Errorf("no pods returned by selector while calculating replica count")
//	}
//	readyPodCount, unreadyPods, missingPods, ignoredPods := groupPods(podList, ...)
//	...
//	if math.Abs(1.0-usageRatio) <= c.tolerance {
//	    return currentReplicas, ...            // inside the tolerance band: no change
//	}
//	return int32(math.Ceil(usageRatio * float64(readyPodCount))), ...
//
// So HPA computes ceil(usageRatio * podCount) and writes it to spec.replicas,
// which this CRD defines as a number of GROUPS. Whenever a group contains more
// than one Pod the two units differ and every scale decision is wrong by the
// pods-per-group factor.
//
// hpaDesiredReplicas below is that formula, not a model of it.

// hpaDesiredReplicas reproduces GetResourceReplicas' arithmetic for the
// non-degenerate case (all pods ready, metrics present for all of them).
// tolerance is the HPA default (--horizontal-pod-autoscaler-tolerance=0.1).
func hpaDesiredReplicas(currentReplicas int32, readyPodCount int, usageRatio float64) int32 {
	const tolerance = 0.1
	if math.Abs(1.0-usageRatio) <= tolerance {
		return currentReplicas
	}
	return int32(math.Ceil(usageRatio * float64(readyPodCount)))
}

func pr396Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add v1alpha2 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	return s
}

// pr396Child builds a child RoleBasedGroup exactly as the RoleBasedGroupSet
// controller labels it (groupset-name + groupset-index).
func pr396Child(setName, ns string, index int, ready bool) workloadsv1alpha2.RoleBasedGroup {
	cond := metav1.ConditionFalse
	if ready {
		cond = metav1.ConditionTrue
	}
	return workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%d", setName, index),
			Namespace: ns,
			Labels: map[string]string{
				constants.GroupSetNameLabelKey:  setName,
				constants.GroupSetIndexLabelKey: fmt.Sprintf("%d", index),
			},
		},
		Status: workloadsv1alpha2.RoleBasedGroupStatus{
			Conditions: []metav1.Condition{
				{Type: string(workloadsv1alpha2.RoleBasedGroupReady), Status: cond},
			},
		},
	}
}

// pr396RunUpdateStatus drives the REAL updateStatus and returns the resulting
// status, so both numbers under test come from production code.
func pr396RunUpdateStatus(
	t *testing.T, set *workloadsv1alpha2.RoleBasedGroupSet, children []workloadsv1alpha2.RoleBasedGroup,
) workloadsv1alpha2.RoleBasedGroupSetStatus {
	t.Helper()
	s := pr396Scheme(t)
	objs := []runtime.Object{set}
	for i := range children {
		objs = append(objs, &children[i])
	}
	r := &RoleBasedGroupSetReconciler{
		client: fake.NewClientBuilder().WithScheme(s).
			WithRuntimeObjects(objs...).
			WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupSet{}).Build(),
		scheme: s,
	}
	list := &workloadsv1alpha2.RoleBasedGroupList{Items: children}
	if err := r.updateStatus(context.Background(), set, list); err != nil {
		t.Fatalf("updateStatus: %v", err)
	}
	got := &workloadsv1alpha2.RoleBasedGroupSet{}
	if err := r.client.Get(context.Background(),
		client.ObjectKeyFromObject(set), got); err != nil {
		t.Fatalf("get after updateStatus: %v", err)
	}
	return got.Status
}

// TestVerifyPR396_ScaleSubresourceUnitsDisagree is a CONTRACT test: the units of
// status.replicas and status.selector must match, because HPA multiplies a ratio
// by the pod count and writes the product into spec.replicas.
//
// Shapes are the ones the PR description itself names: "1P1D" (a prefill role and
// a decode role, 1 pod each) and "2P3D" (2 prefill + 3 decode).
//
// Expected on the PR head: RED.
func TestVerifyPR396_ScaleSubresourceUnitsDisagree(t *testing.T) {
	const ns = "default"

	cases := []struct {
		name         string
		groups       int
		podsPerGroup int // sum of role replicas within one child RoleBasedGroup
	}{
		{"1P1D (2 pods per group)", 2, 2},
		{"2P3D (5 pods per group)", 2, 5},
		{"single-pod group (the only shape that works)", 3, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setName := "rbgs"
			set := &workloadsv1alpha2.RoleBasedGroupSet{
				ObjectMeta: metav1.ObjectMeta{Name: setName, Namespace: ns},
				Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
					Replicas: ptr.To(int32(tc.groups)),
				},
			}
			var children []workloadsv1alpha2.RoleBasedGroup
			for i := 0; i < tc.groups; i++ {
				children = append(children, pr396Child(setName, ns, i, true))
			}

			status := pr396RunUpdateStatus(t, set, children)

			// (1) status.replicas is a count of GROUPS -- straight from production code.
			if int(status.Replicas) != tc.groups {
				t.Fatalf("precondition: status.replicas = %d, want the group count %d",
					status.Replicas, tc.groups)
			}

			// (2) status.selector is parsed and applied to Pods exactly as the HPA
			// controller does (podLister.Pods(ns).List(selector)).
			sel, err := labels.Parse(status.Selector)
			if err != nil {
				t.Fatalf("status.selector %q does not parse as a label selector: %v",
					status.Selector, err)
			}

			// Build the Pods the propagated labels produce: every Pod of every role of
			// every child carries groupset-name, so the selector matches all of them.
			// The label set comes from the REAL GetGroupSetLabels, not hand-written.
			matched := 0
			for i := range children {
				groupSetLabels := children[i].GetGroupSetLabels()
				if len(groupSetLabels) == 0 {
					t.Fatalf("precondition: child %s carries no groupset labels, so the"+
						" selector could not match its Pods at all", children[i].Name)
				}
				for p := 0; p < tc.podsPerGroup; p++ {
					podLabels := labels.Set{}
					for k, v := range groupSetLabels {
						podLabels[k] = v
					}
					if sel.Matches(podLabels) {
						matched++
					}
				}
			}

			wantPods := tc.groups * tc.podsPerGroup
			if matched != wantPods {
				t.Fatalf("precondition: selector %q matched %d pods, want %d",
					status.Selector, matched, wantPods)
			}

			// (3) The consequence. Pick a usage ratio outside the tolerance band so
			// HPA actually recomputes, and compare what it writes to spec.replicas
			// against what a correct group-level autoscaler should write.
			const usageRatio = 1.5
			hpaWrites := hpaDesiredReplicas(status.Replicas, matched, usageRatio)
			correct := int32(math.Ceil(usageRatio * float64(tc.groups)))

			t.Logf("groups=%d podsPerGroup=%d -> status.replicas=%d, selector matched %d pods",
				tc.groups, tc.podsPerGroup, status.Replicas, matched)
			t.Logf("at usageRatio=%.2f: HPA writes spec.replicas=%d (groups); correct would be %d",
				usageRatio, hpaWrites, correct)

			if hpaWrites != correct {
				t.Errorf("R1-F1 REPRODUCED: scale-subresource units disagree."+
					" status.replicas counts GROUPS (%d) but status.selector matches PODS (%d),"+
					" and HPA computes ceil(usageRatio*podCount) then writes it to spec.replicas,"+
					" which this CRD defines as a group count. At usageRatio=%.2f it would set"+
					" spec.replicas=%d instead of %d -- a %.1fx overshoot, i.e. off by the"+
					" pods-per-group factor (%d).",
					status.Replicas, matched, usageRatio, hpaWrites, correct,
					float64(hpaWrites)/float64(correct), tc.podsPerGroup)
			}
		})
	}
}

// TestVerifyPR396_EquilibriumIsOffByPodsPerGroup states the same defect the way an
// operator would experience it: the set does not settle where the target says it
// should. HPA keeps adjusting until ceil(usageRatio*podCount) == currentGroups,
// i.e. until usageRatio ~= 1/podsPerGroup -- so utilization settles at a fraction
// of target and the set is over-provisioned by roughly podsPerGroup.
func TestVerifyPR396_EquilibriumIsOffByPodsPerGroup(t *testing.T) {
	const podsPerGroup = 2 // 1P1D

	// Fixed load that needs `demandPods` pods to sit exactly at the target
	// utilization. The correct answer is therefore demandPods/podsPerGroup groups.
	const demandPods = 8
	const correctGroups = demandPods / podsPerGroup // 4

	// Start away from equilibrium, otherwise HPA's tolerance band short-circuits
	// the loop and the test proves nothing (an earlier version of this test
	// started at usageRatio=1.0 and passed vacuously).
	groups := int32(2)
	seen := map[int32]bool{}
	for i := 0; i < 20; i++ {
		pods := int(groups) * podsPerGroup
		// Utilization is inversely proportional to provisioned capacity.
		usageRatio := float64(demandPods) / float64(pods)
		next := hpaDesiredReplicas(groups, pods, usageRatio)
		t.Logf("iter %d: groups=%d pods=%d usageRatio=%.3f -> HPA wants %d groups",
			i, groups, pods, usageRatio, next)
		if next == groups {
			break
		}
		if seen[next] {
			t.Logf("oscillating; stopping at %d groups", next)
			groups = next
			break
		}
		seen[groups] = true
		groups = next
	}

	if groups != correctGroups {
		t.Errorf("R1-F1 CONSEQUENCE: with %d pods per group, a load needing %d pods"+
			" (= %d groups) drives the loop to settle at %d groups -- %.1fx"+
			" over-provisioned. HPA multiplies the ratio by the POD count but writes"+
			" the product into spec.replicas, which counts GROUPS, so the loop's fixed"+
			" point is off by the pods-per-group factor and utilization settles at"+
			" ~1/%d of target.",
			podsPerGroup, demandPods, correctGroups, groups,
			float64(groups)/float64(correctGroups), podsPerGroup)
	}
}

// TestVerifyPR396_PreexistingPodsBreakHPAEntirely covers the upgrade path. The
// selector is published as soon as the new controller reconciles, but the
// groupset labels only reach Pods when their templates are re-rendered. Until
// then the selector matches nothing, and GetResourceReplicas returns the hard
// error "no pods returned by selector while calculating replica count" -- HPA
// cannot scale the set at all, in either direction.
func TestVerifyPR396_PreexistingPodsBreakHPAEntirely(t *testing.T) {
	const ns = "default"
	setName := "rbgs"
	set := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: setName, Namespace: ns},
		Spec:       workloadsv1alpha2.RoleBasedGroupSetSpec{Replicas: ptr.To(int32(2))},
	}
	children := []workloadsv1alpha2.RoleBasedGroup{
		pr396Child(setName, ns, 0, true),
		pr396Child(setName, ns, 1, true),
	}

	status := pr396RunUpdateStatus(t, set, children)
	sel, err := labels.Parse(status.Selector)
	if err != nil {
		t.Fatalf("parse selector: %v", err)
	}

	// Pods created by the PREVIOUS build: they carry the per-role labels but not
	// the groupset labels this PR adds.
	oldPod := labels.Set{
		constants.GroupNameLabelKey: setName + "-0",
		constants.RoleNameLabelKey:  "prefill",
	}
	if sel.Matches(oldPod) {
		t.Fatalf("precondition: a pre-upgrade Pod unexpectedly matches the selector")
	}

	// CONTROL: a Pod from the new build does match, so the selector is not simply broken.
	newPod := labels.Set{
		constants.GroupNameLabelKey:     setName + "-0",
		constants.RoleNameLabelKey:      "prefill",
		constants.GroupSetNameLabelKey:  setName,
		constants.GroupSetIndexLabelKey: "0",
	}
	if !sel.Matches(newPod) {
		t.Fatalf("HARNESS PROBLEM: a Pod carrying the propagated groupset labels does not"+
			" match selector %q, so the negative result above proves nothing", status.Selector)
	}

	t.Errorf("R1-F2 REPRODUCED: on upgrade, status.selector (%q) is published immediately but"+
		" no existing Pod carries %s, so the selector matches 0 pods and HPA fails with"+
		" \"no pods returned by selector while calculating replica count\" until every Pod in"+
		" the set has been re-created. Nothing in the PR sequences this, warns about it, or"+
		" documents that adopting the feature triggers a full re-roll of every"+
		" RoleBasedGroupSet-owned Pod.", status.Selector, constants.GroupSetNameLabelKey)
}
