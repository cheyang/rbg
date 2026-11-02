//go:build podcacheunion

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

// Independent reviewer harness (verification branch only; not part of PR #545).
// Contract tests: they assert the intended behavior of the tooling, so they
// FAIL if the tooling regresses.

package podcacheunion

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestVerifyAdmissionLabelWiring pins the prototype's admission labels to the
// production constants. The prototype re-declares the strings; silent drift
// would make the harness measure a different admission set than production
// uses. managedLabel has no production constant yet (the KEP introduces the
// marker), so it is pinned to the KEP's literal by comment.
func TestVerifyAdmissionLabelWiring(t *testing.T) {
	if groupLabel != constants.GroupNameLabelKey {
		t.Fatalf("groupLabel %q != constants.GroupNameLabelKey %q", groupLabel, constants.GroupNameLabelKey)
	}
	if instanceLabel != constants.RoleInstanceNameLabelKey {
		t.Fatalf("instanceLabel %q != constants.RoleInstanceNameLabelKey %q", instanceLabel, constants.RoleInstanceNameLabelKey)
	}
	// LabelWarmupName lives in internal/controller/workloads/rolebasedgroupwarmup_controller.go;
	// pinned here by value to avoid importing the controller package.
	if warmupLabel != "workloads.x-k8s.io/warmup-name" {
		t.Fatalf("warmupLabel drifted from production warmup label: %q", warmupLabel)
	}
	// KEP-284 proposes this marker; no production constant exists yet.
	if managedLabel != "rbg.workloads.x-k8s.io/managed" {
		t.Fatalf("managedLabel drifted from the KEP-284 marker: %q", managedLabel)
	}
}

// TestVerifyAdmissionContract checks transformAdmitted against the KEP-284
// admission table: any of the three legacy labels, managed=true, or any
// ownerReference into the RBG API group admits; everything else stubs.
func TestVerifyAdmissionContract(t *testing.T) {
	rbgRef := metav1.OwnerReference{APIVersion: workloadsv1alpha2.GroupVersion.String(), Kind: "RoleInstance", Name: "i", UID: "u"}
	foreignRef := metav1.OwnerReference{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "r", UID: "u"}
	cases := []struct {
		name   string
		labels map[string]string
		owners []metav1.OwnerReference
		want   bool
	}{
		{"group label", map[string]string{groupLabel: "g"}, nil, true},
		{"instance label", map[string]string{instanceLabel: "i"}, nil, true},
		{"warmup label", map[string]string{warmupLabel: "w"}, nil, true},
		{"managed true", map[string]string{managedLabel: "true"}, nil, true},
		{"managed false alone", map[string]string{managedLabel: "false"}, nil, false},
		{"managed false with legacy label", map[string]string{managedLabel: "false", groupLabel: "g"}, nil, true},
		{"empty label value admits", map[string]string{groupLabel: ""}, nil, true},
		{"rbg controller ref", nil, []metav1.OwnerReference{rbgRef}, true},
		{"rbg non-controller ref", nil, []metav1.OwnerReference{{APIVersion: rbgRef.APIVersion, Kind: "RoleInstance", Name: "i", UID: "u", Controller: ptr.To(false)}}, true},
		{"foreign controller only", nil, []metav1.OwnerReference{foreignRef}, false},
		{"no labels no owners", nil, nil, false},
		{"unrelated labels", map[string]string{"app": "x"}, nil, false},
		{"malformed owner apiversion alone", nil, []metav1.OwnerReference{{APIVersion: "not-a-group-version", Kind: "X"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := pod("ns", "x", tc.labels)
			p.OwnerReferences = tc.owners
			if got := transformAdmitted(p); got != tc.want {
				t.Fatalf("transformAdmitted = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestVerifyTransformMutationContract pins the in-place mutation contract the
// stream wrapper relies on: admitted Pods keep their pointer (managedFields
// stripped in place), stubs are new objects sharing the label/owner maps, and
// a second pass over either form is a fixed point.
func TestVerifyTransformMutationContract(t *testing.T) {
	transform := PodCacheTransform(StubIdentityLabelsOwners)

	admittedPod := pod("ns", "a", map[string]string{groupLabel: "g"})
	admittedPod.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "m"}}
	out, err := transform(admittedPod)
	if err != nil {
		t.Fatal(err)
	}
	if out != interface{}(admittedPod) {
		t.Fatal("admitted Pod must keep its pointer: the stream wrapper replaces the event object in place")
	}
	if admittedPod.ManagedFields != nil {
		t.Fatal("managedFields not stripped in place")
	}

	other := pod("ns", "b", map[string]string{"app": "x"})
	other.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "r", UID: "u"}}
	stubOut, err := transform(other)
	if err != nil {
		t.Fatal(err)
	}
	stub := stubOut.(*corev1.Pod)
	if stub == other {
		t.Fatal("stub must be a new object")
	}
	if len(stub.Spec.Containers) != 0 || stub.Labels["app"] != "x" || len(stub.OwnerReferences) != 1 {
		t.Fatalf("stub fields wrong: %+v", stub)
	}
	// Fixed point: transforming the stub again must not change admission or fields.
	again, err := transform(stub)
	if err != nil {
		t.Fatal(err)
	}
	stub2 := again.(*corev1.Pod)
	if !isStub(stub2) || stub2.Labels["app"] != "x" || len(stub2.OwnerReferences) != 1 {
		t.Fatalf("second transform changed the stub: %+v", stub2)
	}
}

// TestVerifyCompareRVBoundaries pins the union probe's resourceVersion order
// assumptions: numeric comparison across digit counts, and a panic (not a
// silently wrong order) on non-numeric input.
func TestVerifyCompareRVBoundaries(t *testing.T) {
	if compareRV("9", "10") >= 0 {
		t.Fatal("9 must sort before 10")
	}
	if compareRV("100", "99") <= 0 {
		t.Fatal("100 must sort after 99")
	}
	if compareRV("42", "42") != 0 {
		t.Fatal("equal RVs must compare equal")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("non-numeric RV must panic instead of imposing a wrong order")
		}
	}()
	compareRV("abc", "1")
}

// TestVerifyUnionBoundaryResolution drives the union prototype's merge core
// deterministically (the standard suite exercises the union only through
// opt-in perf runs): selector-exit resolution via boundary GET, stale-replay
// rejection, watermark pruning, and feed overflow forcing a resnapshot.
func TestVerifyUnionBoundaryResolution(t *testing.T) {
	ctx := context.Background()
	var getPod *corev1.Pod
	var getErr error
	u := newState(1, func(_ context.Context, _, _ string) (*corev1.Pod, error) {
		if getErr != nil {
			return nil, getErr
		}
		return getPod.DeepCopy(), nil
	})
	srcs := make([]*sourceStore, 4)
	for i := range srcs {
		srcs[i] = &sourceStore{union: u, source: i}
	}
	record := func(key string) record {
		u.mu.Lock()
		defer u.mu.Unlock()
		return u.records[key]
	}
	// Initial snapshots: source 0 sees one admitted Pod; the rest are empty.
	p := pod("ns", "x", map[string]string{groupLabel: "g"})
	p.ResourceVersion = "5"
	if err := srcs[0].Replace([]interface{}{p}, "5"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 4; i++ {
		if err := srcs[i].Replace(nil, "5"); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-u.ready:
	default:
		t.Fatal("union not ready after all four sources synced")
	}

	// Selector exit (filtered-watch DELETE), resolved by a boundary GET that
	// finds the Pod gone: the record must become a deletion watermark.
	exit := pod("ns", "x", map[string]string{groupLabel: "g"})
	exit.ResourceVersion = "7"
	if err := srcs[0].Delete(exit); err != nil {
		t.Fatal(err)
	}
	getErr = apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "x")
	if err := u.resolve(ctx, "ns/x"); err != nil {
		t.Fatal(err)
	}
	if rec := record("ns/x"); rec.pod != nil || rec.rv != "7" {
		t.Fatalf("deleted Pod must become a watermark at the boundary RV, got %+v", rec)
	}

	// A stale replay of the old version must not resurrect the Pod.
	stale := pod("ns", "x", map[string]string{groupLabel: "g"})
	stale.ResourceVersion = "6"
	if err := srcs[0].Update(stale); err != nil {
		t.Fatal(err)
	}
	if rec := record("ns/x"); rec.pod != nil || rec.rv != "7" {
		t.Fatalf("stale replay resurrected the Pod: %+v", rec)
	}

	// Recreation admitted again via boundary resolution.
	getErr = nil
	getPod = pod("ns", "x", map[string]string{groupLabel: "g"})
	getPod.ResourceVersion = "9"
	u.mu.Lock()
	u.boundaryLocked("ns/x", "8")
	u.mu.Unlock()
	if err := u.resolve(ctx, "ns/x"); err != nil {
		t.Fatal(err)
	}
	if rec := record("ns/x"); rec.pod == nil || rec.rv != "9" {
		t.Fatalf("recreated admitted Pod not restored: %+v", rec)
	}

	// Deletion watermarks survive until every source passes the deletion RV.
	gone := pod("ns", "y", map[string]string{groupLabel: "g"})
	gone.ResourceVersion = "6"
	if err := srcs[1].Update(gone); err != nil {
		t.Fatal(err)
	}
	del := pod("ns", "y", map[string]string{groupLabel: "g"})
	del.ResourceVersion = "10"
	if err := srcs[1].Delete(del); err != nil {
		t.Fatal(err)
	}
	getErr = apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "y")
	if err := u.resolve(ctx, "ns/y"); err != nil {
		t.Fatal(err)
	}
	u.mu.Lock()
	_, kept := u.records["ns/y"]
	u.mu.Unlock()
	if !kept {
		t.Fatal("watermark pruned before all sources passed it")
	}
	for _, s := range srcs {
		s.UpdateResourceVersion("11")
	}
	u.mu.Lock()
	_, kept = u.records["ns/y"]
	u.mu.Unlock()
	if kept {
		t.Fatal("watermark not pruned after all sources passed it")
	}

	// A slow consumer must force an in-memory resnapshot, never unbounded
	// buffering: buffer 1, two events, then the next watch must get 410.
	if _, err := u.list(ctx, metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	e1 := pod("ns", "e1", map[string]string{groupLabel: "g"})
	e1.ResourceVersion = "12"
	e2 := pod("ns", "e2", map[string]string{groupLabel: "g"})
	e2.ResourceVersion = "13"
	u.mu.Lock()
	u.applyLocked(e1)
	u.applyLocked(e2)
	overflowed := !u.feedAvailable
	u.mu.Unlock()
	if !overflowed {
		t.Fatal("feed must be closed after overflow")
	}
	if _, err := u.watch(ctx, metav1.ListOptions{}); !apierrors.IsResourceExpired(err) {
		t.Fatalf("watch after overflow must force a resnapshot, got %v", err)
	}
}
