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

// Package pdbeviction is a review-verification harness (PR #589), not a shipped test.
// It loads the exact PodDisruptionBudget object from deploy/kubectl/manifests.yaml and
// exercises the policy/v1 eviction subresource of a real API server (envtest), proving
// the eviction semantics the PR relies on:
//
//   - a Ready manager pod is protected once the disruption budget is exhausted (429);
//   - a Ready manager pod is evictable while DisruptionsAllowed > 0 (one at a time);
//   - an unready manager pod is evictable even with the budget exhausted, but only
//     because the shipped PDB sets unhealthyPodEvictionPolicy: AlwaysAllow — with the
//     default (IfHealthyBudget) the same pod is refused.
//
// The disruption controller does not run under envtest, so PDB status is seeded
// directly (the eviction handler in kube-apiserver only reads spec.unhealthyPodEvictionPolicy,
// pod readiness, and status.{currentHealthy,desiredHealthy,disruptionsAllowed} — see
// k8s.io/kubernetes pkg/registry/core/pod/storage/eviction.go).
package pdbeviction

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

const namespace = "rbgs-system"

var managerLabels = map[string]string{
	"app.kubernetes.io/name": "rbgs",
	"control-plane":          "rbgs-controller",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	// this file: <repo>/test/envtest/testcase/pdbeviction/pdb_eviction_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))
}

// shippedPDB extracts the manager PodDisruptionBudget from deploy/kubectl/manifests.yaml.
func shippedPDB(t *testing.T) *policyv1.PodDisruptionBudget {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "kubectl", "manifests.yaml"))
	if err != nil {
		t.Fatalf("read manifests.yaml: %v", err)
	}
	for _, doc := range strings.Split(string(raw), "\n---") {
		var meta struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}
		if err := yaml.Unmarshal([]byte(doc), &meta); err != nil {
			continue
		}
		if meta.Kind == "PodDisruptionBudget" && meta.Metadata.Name == "rbgs-controller-manager" {
			var pdb policyv1.PodDisruptionBudget
			if err := yaml.Unmarshal([]byte(doc), &pdb); err != nil {
				t.Fatalf("unmarshal shipped PDB: %v", err)
			}
			return &pdb
		}
	}
	t.Fatal("manager PodDisruptionBudget not found in deploy/kubectl/manifests.yaml")
	return nil
}

func newManagerPod(name string, ready bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: managerLabels},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "rbgs", Image: "controller:latest"}},
		},
	}
	if ready {
		pod.Status.Conditions = []corev1.PodCondition{{
			Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Now(),
		}}
	}
	return pod
}

func seedPDBStatus(t *testing.T, cs kubernetes.Interface, pdb *policyv1.PodDisruptionBudget, currentHealthy, desiredHealthy, disruptionsAllowed int32) {
	t.Helper()
	latest, err := cs.PolicyV1().PodDisruptionBudgets(namespace).Get(context.TODO(), pdb.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pdb: %v", err)
	}
	latest.Status.CurrentHealthy = currentHealthy
	latest.Status.DesiredHealthy = desiredHealthy
	latest.Status.DisruptionsAllowed = disruptionsAllowed
	latest.Status.ObservedGeneration = latest.Generation
	if _, err := cs.PolicyV1().PodDisruptionBudgets(namespace).UpdateStatus(context.TODO(), latest, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("seed pdb status: %v", err)
	}
}

func evict(t *testing.T, cs kubernetes.Interface, podName string) error {
	t.Helper()
	return cs.PolicyV1().Evictions(namespace).Evict(context.TODO(), &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: namespace},
	})
}

func TestShippedManagerPDBEvictionSemantics(t *testing.T) {
	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	defer func() {
		if err := env.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	}()

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}
	ctx := context.TODO()

	if _, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	pdb := shippedPDB(t)
	if got := ptr.Deref(pdb.Spec.UnhealthyPodEvictionPolicy, ""); got != policyv1.AlwaysAllow {
		t.Fatalf("shipped PDB unhealthyPodEvictionPolicy = %q, want %q (test premise)", got, policyv1.AlwaysAllow)
	}
	if _, err := cs.PolicyV1().PodDisruptionBudgets(namespace).Create(ctx, pdb, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create shipped pdb: %v", err)
	}

	mkPod := func(name string, ready bool) {
		pod := newManagerPod(name, ready)
		created, err := cs.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("create pod %s: %v", name, err)
		}
		created.Status = pod.Status
		created.Status.Phase = corev1.PodRunning
		if _, err := cs.CoreV1().Pods(namespace).UpdateStatus(ctx, created, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("seed pod %s status: %v", name, err)
		}
	}

	// Case 1: a Ready manager pod with the budget exhausted must be refused (429),
	// which is what serializes a drain to one pod at a time.
	mkPod("manager-ready-blocked", true)
	seedPDBStatus(t, cs, pdb, 1, 1, 0)
	err = evict(t, cs, "manager-ready-blocked")
	if !apierrors.IsTooManyRequests(err) {
		t.Fatalf("case 1 (ready pod, disruptionsAllowed=0): want 429 TooManyRequests, got %v", err)
	}
	if _, err := cs.CoreV1().Pods(namespace).Get(ctx, "manager-ready-blocked", metav1.GetOptions{}); err != nil {
		t.Fatalf("case 1: pod must survive the refused eviction: %v", err)
	}
	t.Log("case 1: ready pod protected at disruptionsAllowed=0 (429), pod still present")

	// Case 2: a Ready manager pod is evictable while the budget allows it, and the
	// eviction decrements DisruptionsAllowed (that decrement is what blocks the next one).
	mkPod("manager-ready-allowed", true)
	seedPDBStatus(t, cs, pdb, 2, 1, 1)
	if err := evict(t, cs, "manager-ready-allowed"); err != nil {
		t.Fatalf("case 2 (ready pod, disruptionsAllowed=1): want success, got %v", err)
	}
	after, err := cs.PolicyV1().PodDisruptionBudgets(namespace).Get(ctx, pdb.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("case 2: get pdb: %v", err)
	}
	if after.Status.DisruptionsAllowed != 0 {
		t.Fatalf("case 2: disruptionsAllowed = %d after eviction, want 0 (decremented)", after.Status.DisruptionsAllowed)
	}
	t.Log("case 2: ready pod evicted at disruptionsAllowed=1, budget decremented to 0")

	// Case 3: an unready manager pod is evictable even at disruptionsAllowed=0, because
	// the shipped PDB sets unhealthyPodEvictionPolicy: AlwaysAllow (the all-replicas-
	// unready drain cannot be held up).
	mkPod("manager-unready-alwaysallow", false)
	seedPDBStatus(t, cs, pdb, 0, 1, 0)
	if err := evict(t, cs, "manager-unready-alwaysallow"); err != nil {
		t.Fatalf("case 3 (unready pod, AlwaysAllow, disruptionsAllowed=0): want success, got %v", err)
	}
	t.Log("case 3: unready pod evicted at disruptionsAllowed=0 under AlwaysAllow")

	// Case 4 (control): the same unready pod under the default IfHealthyBudget policy is
	// refused, proving case 3 is decided by the shipped AlwaysAllow field and not by
	// some other property of the fixture.
	pdbDefault := shippedPDB(t)
	pdbDefault.Name = "rbgs-controller-manager-defaultpolicy"
	pdbDefault.ResourceVersion = ""
	pdbDefault.Spec.UnhealthyPodEvictionPolicy = ptr.To(policyv1.IfHealthyBudget)
	if _, err := cs.PolicyV1().PodDisruptionBudgets(namespace).Create(ctx, pdbDefault, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create default-policy pdb: %v", err)
	}
	// A pod must not see two PDBs (the eviction handler rejects that), so remove the
	// shipped one first.
	if err := cs.PolicyV1().PodDisruptionBudgets(namespace).Delete(ctx, pdb.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete shipped pdb: %v", err)
	}
	mkPod("manager-unready-defaultpolicy", false)
	seedPDBStatus(t, cs, pdbDefault, 0, 1, 0)
	err = evict(t, cs, "manager-unready-defaultpolicy")
	if !apierrors.IsTooManyRequests(err) {
		t.Fatalf("case 4 (unready pod, IfHealthyBudget, disruptionsAllowed=0): want 429 TooManyRequests, got %v", err)
	}
	t.Log("case 4: unready pod refused at disruptionsAllowed=0 under default policy (control)")

	// Give etcd a beat so deferred cleanup cannot flake env.Stop.
	time.Sleep(100 * time.Millisecond)
}
