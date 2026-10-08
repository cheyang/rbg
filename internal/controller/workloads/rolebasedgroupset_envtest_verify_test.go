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

package workloads

// Verification harness (L2 integration layer) for PR #474; reviewer additive test,
// production code untouched.
//
// Drives the full Reconcile entry point of the rolling RoleBasedGroupSet controller
// against a REAL API server (envtest) with the REAL CRDs installed, so API-server
// defaulting and schema validation apply. The two things the fake-client layer cannot
// prove:
//
//  1. Revision matching is stable against API-server-defaulted child specs - if a CRD
//     default lands on the child but not on the stored groupTemplate (or vice versa),
//     groupSetMatchesRevision never matches and the controller would recreate the same
//     children forever. The convergence + recreate-count assertions below catch that.
//  2. Foreground deletion + UID preconditions behave against a real API server; the
//     harness plays the garbage collector (children own no dependents here, so removing
//     the foregroundDeletion finalizer is what kube-controller-manager would do).
//
// The test skips when KUBEBUILDER_ASSETS is unset so plain go test keeps working on
// machines without envtest binaries.
//
// Polarity: CONTRACT - asserts the intended behavior; red on buggy code.

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerify_Envtest_RollingAgainstRealAPIServer(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; skipping envtest integration layer")
	}
	_, thisFile, _, _ := goruntime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(repoRoot, "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	defer func() { _ = env.Stop() }()

	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	ctx := context.Background()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "verify-roll"}}
	require.NoError(t, c.Create(ctx, ns))

	replicas := int32(2)
	set := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "roll", Namespace: ns.Name},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: &replicas,
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{
						Name:     "worker",
						Replicas: ptr.To(int32(1)),
						Pattern: workloadsv1alpha2.Pattern{
							StandalonePattern: &workloadsv1alpha2.StandalonePattern{
								TemplateSource: workloadsv1alpha2.TemplateSource{Template: premisePodTemplate("registry.test/image:v1")},
							},
						},
					}},
				},
			},
			RolloutStrategy: &workloadsv1alpha2.GroupSetRolloutStrategy{
				Type:           workloadsv1alpha2.RecreateStrategyType,
				MaxUnavailable: ptr.To(intstr.FromInt(1)),
				MaxSurge:       ptr.To(intstr.FromInt(0)),
			},
		},
	}
	require.NoError(t, c.Create(ctx, set))

	r := &RoleBasedGroupSetReconciler{client: c, apiReader: c, scheme: scheme}
	key := client.ObjectKeyFromObject(set)

	// seenOnce tracks children observed at a previous poll, so a child only turns Ready
	// after one full extra reconcile (simulated startup latency).
	seenOnce := map[string]bool{}
	recreates := map[string]int{}
	lastUID := map[string]types.UID{}

	listChildren := func() []workloadsv1alpha2.RoleBasedGroup {
		children := &workloadsv1alpha2.RoleBasedGroupList{}
		require.NoError(t, c.List(ctx, children, client.InNamespace(ns.Name),
			client.MatchingLabels{constants.GroupSetNameLabelKey: set.Name}))
		return children.Items
	}

	simulate := func() {
		for _, item := range listChildren() {
			item := item
			if !item.DeletionTimestamp.IsZero() {
				// Play the GC: no dependents exist in envtest, so a foreground delete
				// completes as soon as the finalizer is lifted.
				if len(item.Finalizers) > 0 {
					item.Finalizers = nil
					require.NoError(t, c.Update(ctx, &item))
				}
				continue
			}
			if !seenOnce[item.Name] {
				seenOnce[item.Name] = true
				continue
			}
			latest := &workloadsv1alpha2.RoleBasedGroup{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(&item), latest); err != nil {
				continue
			}
			if latest.Status.ObservedGeneration >= latest.Generation &&
				meta.IsStatusConditionTrue(latest.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupReady)) {
				continue
			}
			latest.Status.ObservedGeneration = latest.Generation
			latest.Status.RoleStatuses = []workloadsv1alpha2.RoleStatus{
				{Name: "worker", ReadyReplicas: 1, Replicas: 1, UpdatedReplicas: 1},
			}
			meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
				Type: string(workloadsv1alpha2.RoleBasedGroupReady), Status: metav1.ConditionTrue,
				Reason: "Ready", ObservedGeneration: latest.Generation,
			})
			require.NoError(t, c.Status().Update(ctx, latest))
		}
	}

	everFullyReady := false
	reconcile := func() ctrl.Result {
		simulate()
		for _, item := range listChildren() {
			if uid, ok := lastUID[item.Name]; ok && uid != item.UID {
				recreates[item.Name]++
			}
			lastUID[item.Name] = item.UID
		}
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		require.NoError(t, err)
		// The budget only protects EXISTING serving capacity: once the set has been
		// fully ready, a rollout must never take serving groups below
		// replicas-maxUnavailable=1 at a step boundary.
		readyNow := countReadyNonTerminating(listChildren())
		if everFullyReady {
			assert.GreaterOrEqual(t, readyNow, 1,
				"serving groups dropped below replicas-maxUnavailable")
		}
		if readyNow == 2 {
			everFullyReady = true
		}
		return res
	}

	// Phase 1: initial creation converges (2 children, quiet steady state).
	deadline := time.Now().Add(2 * time.Minute)
	var res ctrl.Result
	for {
		res = reconcile()
		children := listChildren()
		if res.RequeueAfter == 0 && !res.Requeue && len(children) == 2 &&
			countReadyNonTerminating(children) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial creation did not converge")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, item := range listChildren() {
		assert.Zero(t, recreates[item.Name], "creation must not recreate %s", item.Name)
	}

	// Phase 2: template change -> paced recreate; each child replaced exactly once.
	latest := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, c.Get(ctx, key, latest))
	latest.Spec.GroupTemplate.Spec.Roles[0].StandalonePattern.Template = premisePodTemplate("registry.test/image:v2")
	require.NoError(t, c.Update(ctx, latest))

	deadline = time.Now().Add(3 * time.Minute)
	for {
		res = reconcile()
		children := listChildren()
		updated := 0
		for i := range children {
			if children[i].Spec.Roles[0].StandalonePattern.Template.Spec.Containers[0].Image == "registry.test/image:v2" {
				updated++
			}
		}
		if res.RequeueAfter == 0 && !res.Requeue && len(children) == 2 && updated == 2 &&
			countReadyNonTerminating(children) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rollout did not converge; recreates=%v", recreates)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for name, n := range recreates {
		assert.Equal(t, 1, n, "child %s must be recreated exactly once (a loop would show more)", name)
	}

	final := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, c.Get(ctx, key, final))
	rolling := meta.FindStatusCondition(final.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupSetRolling))
	require.NotNil(t, rolling)
	assert.Equal(t, metav1.ConditionFalse, rolling.Status)
	assert.Equal(t, "RolloutComplete", rolling.Reason)
	assert.Equal(t, final.Status.CurrentRevision, final.Status.UpdateRevision)

	// Phase 3: steady state stays quiet (no hot requeue, no further child churn).
	for i := 0; i < 3; i++ {
		res = reconcile()
		assert.False(t, res.Requeue)
		assert.Zero(t, res.RequeueAfter, "steady state must not requeue")
	}
	for name, n := range recreates {
		assert.Equal(t, 1, n, "child %s must not churn after completion", name)
	}
}
