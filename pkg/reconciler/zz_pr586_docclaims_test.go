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

// Review-verification harness for PR #586 (docs: v1alpha1->v1alpha2 migration
// guides). Pins the controller-behavior claims the guides make about the
// v1alpha1 InstanceSet selector and about orphan cleanup. CONTRACT polarity.
// Additive only; no production code is touched.

package reconciler

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func pr586Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		appsv1.AddToScheme, corev1.AddToScheme, workloadsv1alpha2.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	return scheme
}

// Guide claim ("v1alpha1 InstanceSet roles"): "the v0.7.0-and-later controller
// has no InstanceSet reconciler". Contract: the reconciler factory rejects the
// v1alpha1 InstanceSet workload type, which is what makes a RoleBasedGroup
// carrying such a role fail preflight as a whole (the RBG controller's
// preCheck aggregates this error and aborts before reconciling any role).
func TestDocClaimInstanceSetHasNoReconciler(t *testing.T) {
	scheme := pr586Scheme(t)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	_, err := NewWorkloadReconciler(
		workloadsv1alpha2.WorkloadSpec{APIVersion: "workloads.x-k8s.io/v1alpha1", Kind: "InstanceSet"},
		scheme, client,
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported workload type") {
		t.Fatalf("guide claims the controller has no InstanceSet reconciler; factory returned err=%v", err)
	}
}

// Guide claim (step 2, "Observe the replacement"): after a role moves to
// RoleInstanceSet, "the controller treats the old Deployment, StatefulSet, or
// LeaderWorkerSet as an orphan and deletes it". Contract: an RBG-owned
// Deployment whose name no longer matches any Deployment-typed role in the
// spec is deleted by CleanupOrphanedWorkloads.
func TestDocClaimOrphanCleanupDeletesAbandonedDeployment(t *testing.T) {
	scheme := pr586Scheme(t)
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "g", Namespace: "default", UID: types.UID("uid-g")},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{{
			Name: "worker", // already migrated: no deprecated annotation -> RoleInstanceSet
		}}},
	}
	orphan := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "g-worker", // name a Deployment-typed "worker" role would own
			Namespace: "default",
			Labels:    map[string]string{constants.GroupNameLabelKey: "g"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: workloadsv1alpha2.GroupVersion.String(),
				Kind:       "RoleBasedGroup",
				Name:       "g",
				UID:        rbg.UID,
				Controller: ptr.To(true),
			}},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(orphan).Build()
	rec := NewDeploymentReconciler(scheme, client)
	if err := rec.CleanupOrphanedWorkloads(context.Background(), rbg); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	got := &appsv1.Deployment{}
	err := client.Get(context.Background(), types.NamespacedName{Name: "g-worker", Namespace: "default"}, got)
	if err == nil {
		t.Fatal("guide claims the abandoned Deployment is deleted as an orphan; it still exists")
	}
}

// Control for the orphan-cleanup claim: a Deployment still referenced by a
// Deployment-typed role must survive cleanup.
func TestDocClaimOrphanCleanupKeepsActiveDeployment(t *testing.T) {
	scheme := pr586Scheme(t)
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "g", Namespace: "default", UID: types.UID("uid-g")},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{{
			Name:        "worker",
			Annotations: map[string]string{constants.RoleWorkloadTypeAnnotationKey: constants.DeploymentWorkloadType},
		}}},
	}
	active := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "g-worker",
			Namespace: "default",
			Labels:    map[string]string{constants.GroupNameLabelKey: "g"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: workloadsv1alpha2.GroupVersion.String(),
				Kind:       "RoleBasedGroup",
				Name:       "g",
				UID:        rbg.UID,
				Controller: ptr.To(true),
			}},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(active).Build()
	rec := NewDeploymentReconciler(scheme, client)
	if err := rec.CleanupOrphanedWorkloads(context.Background(), rbg); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	got := &appsv1.Deployment{}
	if err := client.Get(context.Background(), types.NamespacedName{Name: "g-worker", Namespace: "default"}, got); err != nil {
		t.Fatalf("active Deployment must survive orphan cleanup: %v", err)
	}
}
