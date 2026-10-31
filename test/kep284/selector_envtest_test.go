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

package kep284

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestAdmissionRuleAgainstRealAPIServer is the L2 integration contract for
// KEP-284: the four server-side filtered List streams of the admission rule,
// taken serially (as the KEP mandates) and unioned by name, must return
// exactly the supported pods and nothing else — including the edge cases the
// KEP calls out (managed=false with a legacy label stays admitted;
// managed!=true alone is not).
//
// Runs against a real kube-apiserver (1.31.0, the version the KEP validated).
// Skips when the envtest binaries are unavailable.
func TestAdmissionRuleAgainstRealAPIServer(t *testing.T) {
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		candidate := filepath.Join("..", "..", "bin", "k8s", "1.31.0-linux-amd64")
		if st, err := os.Stat(filepath.Join(candidate, "kube-apiserver")); err == nil && !st.IsDir() {
			abs, err := filepath.Abs(candidate)
			if err != nil {
				t.Fatalf("resolve envtest assets: %v", err)
			}
			assets = abs
		}
	}
	if assets == "" {
		t.Skip("envtest binaries not found (set KUBEBUILDER_ASSETS or provide bin/k8s/1.31.0-linux-amd64)")
	}

	env := &envtest.Environment{BinaryAssetsDirectory: assets}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	defer func() {
		if err := env.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	}()

	cli, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kep284-admission"}}
	if err := cli.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	pod := func(name string, lbls map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns.Name, Labels: lbls},
			Spec: corev1.PodSpec{
				Containers:    []corev1.Container{{Name: "c", Image: "registry.k8s.io/pause:3.9"}},
				RestartPolicy: corev1.RestartPolicyNever,
			},
		}
	}
	// One pod per admission shape of the KEP table, plus foreign controls.
	shapes := map[string]map[string]string{
		"p-group-only":            {kepGroupNameLabel: "g"},
		"p-instance-only":         {kepRoleInstanceLabel: "ri"},
		"p-warmup-only":           {kepWarmupLabel: "w"},
		"p-managed-only":          {kepManagedLabel: "true"},
		"p-legacy-managed-false":  {kepGroupNameLabel: "g", kepManagedLabel: "false"},
		"p-foreign-unlabeled":     {"app": "x"},
		"p-foreign-managed-false": {kepManagedLabel: "false"},
		"p-foreign-managed-1":     {kepManagedLabel: "1"},
	}
	wantAdmitted := []string{
		"p-group-only", "p-instance-only", "p-warmup-only", "p-managed-only", "p-legacy-managed-false",
	}
	for name, lbls := range shapes {
		if err := cli.Create(ctx, pod(name, lbls)); err != nil {
			t.Fatalf("create pod %s: %v", name, err)
		}
	}

	exists := func(key string) labels.Selector {
		req, err := labels.NewRequirement(key, selection.Exists, nil)
		if err != nil {
			t.Fatalf("selector %q: %v", key, err)
		}
		return labels.NewSelector().Add(*req)
	}
	managedReq, err := labels.NewRequirement(kepManagedLabel, selection.Equals, []string{"true"})
	if err != nil {
		t.Fatalf("managed selector: %v", err)
	}
	sources := map[string]labels.Selector{
		"group-name":         exists(kepGroupNameLabel),
		"role-instance-name": exists(kepRoleInstanceLabel),
		"warmup-name":        exists(kepWarmupLabel),
		"managed":            labels.NewSelector().Add(*managedReq),
	}

	// Fetch the four sources SERIALLY and take the union by name, exactly as
	// the KEP's cache-implementation section mandates.
	union := map[string]corev1.Pod{}
	perSource := map[string][]string{}
	for sourceName, sel := range sources {
		var list corev1.PodList
		if err := cli.List(ctx, &list, client.InNamespace(ns.Name),
			client.MatchingLabelsSelector{Selector: sel}); err != nil {
			t.Fatalf("list source %s: %v", sourceName, err)
		}
		for _, p := range list.Items {
			union[p.Name] = p
			perSource[sourceName] = append(perSource[sourceName], p.Name)
		}
	}

	got := make([]string, 0, len(union))
	for name := range union {
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(wantAdmitted)
	if len(got) != len(wantAdmitted) {
		t.Fatalf("union = %v, want %v", got, wantAdmitted)
	}
	for i := range got {
		if got[i] != wantAdmitted[i] {
			t.Fatalf("union = %v, want %v", got, wantAdmitted)
		}
	}

	// Per-source expectations, incl. managed=false being accepted by the
	// legacy source and rejected by the managed source.
	wantPerSource := map[string][]string{
		"group-name":         {"p-group-only", "p-legacy-managed-false"},
		"role-instance-name": {"p-instance-only"},
		"warmup-name":        {"p-warmup-only"},
		"managed":            {"p-managed-only"},
	}
	for sourceName, want := range wantPerSource {
		gotSource := perSource[sourceName]
		sort.Strings(gotSource)
		sort.Strings(want)
		if len(gotSource) != len(want) {
			t.Errorf("source %s = %v, want %v", sourceName, gotSource, want)
			continue
		}
		for i := range gotSource {
			if gotSource[i] != want[i] {
				t.Errorf("source %s = %v, want %v", sourceName, gotSource, want)
				break
			}
		}
	}
	t.Logf("serial union of 4 filtered sources admitted %v; per-source %v", got, perSource)
}
