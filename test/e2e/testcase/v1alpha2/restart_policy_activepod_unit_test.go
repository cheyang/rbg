/*
Copyright 2026 The RBG Authors.

Licensed under the Apache License, Version 2.0 (the "License"); you may not
use this file except in compliance with the License. You may obtain a copy of
the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
License for the specific language governing permissions and limitations under
the License.
*/

// Unit harness for the active-pod selection added by PR #467.
// Lives in package v1alpha2 so it can reach the unexported findActivePod and
// filterActivePods helpers directly. It does NOT touch production code and does
// not require a cluster; run with:
//   GOFLAGS=-mod=vendor go test ./test/e2e/testcase/v1alpha2/ -run 'TestActivePod' -count=1 -v
package v1alpha2

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// podWith returns a named Pod in the given phase, optionally deletion-marked.
func podWith(name string, phase corev1.PodPhase, deleting bool) corev1.Pod {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Status:     corev1.PodStatus{Phase: phase},
	}
	if deleting {
		t := metav1.NewTime(time.Now())
		p.DeletionTimestamp = &t
	}
	return p
}

// TestActivePodFindActiveSkipsTerminalAndDeleting is a CONTRACT test for the
// fix: findActivePod must never return a terminal or terminating Pod, and must
// return nil when no active Pod exists. On a naive "&items[0]" selection this
// would fail whenever a terminal Pod leads the slice.
func TestActivePodFindActiveSkipsTerminalAndDeleting(t *testing.T) {
	cases := []struct {
		name    string
		items   []corev1.Pod
		want    string // expected Pod name, "" means nil expected
		wantNil bool
	}{
		{
			name:  "terminal-leading_then_running",
			items: []corev1.Pod{podWith("p-failed", corev1.PodFailed, false), podWith("p-run", corev1.PodRunning, false)},
			want:  "p-run",
		},
		{
			name:  "deleting-leading_then_running",
			items: []corev1.Pod{podWith("p-del", corev1.PodRunning, true), podWith("p-run", corev1.PodRunning, false)},
			want:  "p-run",
		},
		{
			name:  "succeeded_present_then_running",
			items: []corev1.Pod{podWith("p-ok", corev1.PodSucceeded, false), podWith("p-run", corev1.PodRunning, false)},
			want:  "p-run",
		},
		{
			name:    "all_terminal_returns_nil",
			items:   []corev1.Pod{podWith("p-failed", corev1.PodFailed, false), podWith("p-ok", corev1.PodSucceeded, false)},
			wantNil: true,
		},
		{
			name:    "all_deleting_returns_nil",
			items:   []corev1.Pod{podWith("p-del1", corev1.PodRunning, true), podWith("p-del2", corev1.PodRunning, true)},
			wantNil: true,
		},
		{
			name:  "single_running",
			items: []corev1.Pod{podWith("p-run", corev1.PodRunning, false)},
			want:  "p-run",
		},
		{
			name:    "empty_returns_nil",
			items:   []corev1.Pod{},
			wantNil: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := findActivePod(c.items)
			if c.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %q (phase=%s, deleting=%v)", got.Name, got.Status.Phase, got.DeletionTimestamp != nil)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %q, got nil", c.want)
			}
			if got.Name != c.want {
				t.Errorf("expected %q, got %q", c.want, got.Name)
			}
			// The core invariant: the returned Pod must be active by the same
			// predicate the controller uses.
			if !isPodActiveInline(got) {
				t.Errorf("findActivePod returned a non-active Pod %q (phase=%s, deleting=%v)", got.Name, got.Status.Phase, got.DeletionTimestamp != nil)
			}
		})
	}
}

// TestActivePodFilterExcludesTerminalAndDeleting is a CONTRACT test for the
// refactored filterActivePods: it keeps exactly the active Pods.
func TestActivePodFilterExcludesTerminalAndDeleting(t *testing.T) {
	items := []corev1.Pod{
		podWith("run-1", corev1.PodRunning, false),
		podWith("failed-1", corev1.PodFailed, false),
		podWith("del-1", corev1.PodRunning, true),
		podWith("succ-1", corev1.PodSucceeded, false),
		podWith("pend-1", corev1.PodPending, false),
		podWith("run-2", corev1.PodRunning, false),
	}
	got := filterActivePods(items)
	if len(got) != 3 {
		t.Fatalf("expected 3 active pods, got %d: %+v", len(got), podNames(got))
	}
	for _, p := range got {
		if !isPodActiveInline(&p) {
			t.Errorf("filterActivePods kept non-active pod %q (phase=%s, deleting=%v)", p.Name, p.Status.Phase, p.DeletionTimestamp != nil)
		}
	}
}

// TestActivePodFilterEquivalenceToInlinePredicate proves the refactor to
// kubecontroller.IsPodActive is behavior-preserving against the pre-PR inline
// predicate (DeletionTimestamp==nil && Phase!=Failed && Phase!=Succeeded).
// CONTRACT: any divergence would mean the refactor changed semantics.
func TestActivePodFilterEquivalenceToInlinePredicate(t *testing.T) {
	items := []corev1.Pod{
		podWith("run", corev1.PodRunning, false),
		podWith("pend", corev1.PodPending, false),
		podWith("failed", corev1.PodFailed, false),
		podWith("succ", corev1.PodSucceeded, false),
		podWith("delrun", corev1.PodRunning, true),
		podWith("delfail", corev1.PodFailed, true),
	}
	got := filterActivePods(items)
	var inline []corev1.Pod
	for i := range items {
		if isPodActiveInline(&items[i]) {
			inline = append(inline, items[i])
		}
	}
	if len(got) != len(inline) {
		t.Fatalf("filterActivePods diverged from inline predicate: got %d, inline %d", len(got), len(inline))
	}
	gotNames := map[string]struct{}{}
	for _, p := range got {
		gotNames[p.Name] = struct{}{}
	}
	for _, p := range inline {
		if _, ok := gotNames[p.Name]; !ok {
			t.Errorf("filterActivePods dropped %q which the inline predicate keeps", p.Name)
		}
	}
}

// TestActivePodPremiseNaiveItemsZeroCanPickTerminal is a CANARY for the PR's
// premise (P0): it documents the flake mechanism on the BASE behavior. With a
// terminal Pod at index 0 (the realistic shape at the second-crash selection
// point, where a recreated instance still lists the lingering Failed/terminating
// Pod alongside the new Running one), the pre-PR selection &items[0] returns a
// non-active Pod. findActivePod must NOT. This test passes today because both
// facts hold simultaneously; if findActivePod ever regressed to &items[0], the
// TestActivePodFindActive* cases would flip to fail (the bite).
func TestActivePodPremiseNaiveItemsZeroCanPickTerminal(t *testing.T) {
	items := []corev1.Pod{
		podWith("lingering-failed", corev1.PodFailed, false),
		podWith("fresh-running", corev1.PodRunning, false),
	}
	// Base-style selection (what the PR replaced).
	naive := &items[0]
	if naive.Status.Phase == corev1.PodFailed || naive.Status.Phase == corev1.PodSucceeded || naive.DeletionTimestamp != nil {
		// expected: naive selection picked a non-active Pod -> this is the flake mechanism
	} else {
		t.Fatalf("premise setup wrong: items[0] is active, cannot demonstrate the mechanism")
	}
	// Fix-style selection must pick the active Pod instead.
	got := findActivePod(items)
	if got == nil || got.Name != "fresh-running" {
		t.Fatalf("findActivePod should return fresh-running, got %+v", got)
	}
}

// isPodActiveInline is the pre-PR inline predicate, copied verbatim for the
// equivalence test. Must not be used by production code.
func isPodActiveInline(p *corev1.Pod) bool {
	return p.DeletionTimestamp == nil &&
		p.Status.Phase != corev1.PodFailed &&
		p.Status.Phase != corev1.PodSucceeded
}

func podNames(pods []corev1.Pod) []string {
	out := make([]string, 0, len(pods))
	for _, p := range pods {
		out = append(out, p.Name)
	}
	return out
}
