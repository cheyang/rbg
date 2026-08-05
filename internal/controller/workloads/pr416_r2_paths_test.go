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

// PR sgl-project/rbg#416, review round 2.
//
// Round 1 found that `--enable-deprecated-workload-types=false` stranded
// pre-existing objects because the operator's own writes were denied (F2a, F9,
// F10). Round 2 narrows the UPDATE path with
// validateNoNewDeprecatedWorkloadTypes. This file asks whether that narrowing
// covers *every* write the operator performs on a pre-existing object.
//
// Method notes (deliberate, so the results cannot pass vacuously):
//   - The REAL validators are used (v1alpha2.RoleBasedGroupValidator /
//     RoleBasedGroupSetValidator). Nothing is re-implemented here.
//   - The RoleBasedGroupValidator is always given a non-nil (fake) Client,
//     because it has a nil-client guard in ValidateScalingAdapterReplicas that
//     would otherwise contaminate every result with a spurious error.
//   - admissionClient() wraps a fake client so that CREATE goes through
//     ValidateCreate and UPDATE/PATCH go through ValidateUpdate with the stored
//     object as oldObj -- i.e. the same dispatch the API server performs. Status
//     subresource writes are intentionally NOT intercepted, mirroring the fact
//     that the webhook rule lists `rolebasedgroups`, not
//     `rolebasedgroups/status` (config/webhook/manifests.yaml).
//   - Every "this is denied" claim has a control subtest asserting the identical
//     operation is ACCEPTED with EnableDeprecatedWorkloadTypes=true.
package workloads

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func pr416R2Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add v1alpha2 to scheme: %v", err)
	}
	if err := workloadsv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add v1alpha1 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	return s
}

// pr416Role builds a v1alpha2 role carrying workload type wt. An empty wt means
// "no annotation", i.e. the v1alpha2 default (RoleInstanceSet).
func pr416Role(name, wt string, replicas int32) workloadsv1alpha2.RoleSpec {
	r := workloadsv1alpha2.RoleSpec{
		Name:     name,
		Replicas: ptr.To(replicas),
	}
	if wt != "" {
		r.Annotations = map[string]string{constants.RoleWorkloadTypeAnnotationKey: wt}
	}
	return r
}

var (
	pr416RBGGR    = schema.GroupResource{Group: "workloads.x-k8s.io", Resource: "rolebasedgroups"}
	pr416RBGSetGR = schema.GroupResource{Group: "workloads.x-k8s.io", Resource: "rolebasedgroupsets"}
)

// admissionClient returns a client that runs the real validating webhook in
// front of the fake client, dispatching CREATE -> ValidateCreate and
// UPDATE/PATCH -> ValidateUpdate(stored, incoming), exactly as the API server
// does for the rule
// `resources=rolebasedgroups,rolebasedgroupsets verbs=create;update`.
func admissionClient(t *testing.T, inner client.WithWatch, enableDeprecated bool) client.WithWatch {
	t.Helper()
	rbgV := &workloadsv1alpha2.RoleBasedGroupValidator{
		// Never nil: the validator has a nil-client guard that would
		// otherwise inject an unrelated error into every UPDATE.
		Client:                        inner,
		EnableDeprecatedWorkloadTypes: enableDeprecated,
	}
	setV := &workloadsv1alpha2.RoleBasedGroupSetValidator{
		EnableDeprecatedWorkloadTypes: enableDeprecated,
	}

	admitCreate := func(ctx context.Context, obj client.Object) error {
		switch o := obj.(type) {
		case *workloadsv1alpha2.RoleBasedGroup:
			if _, err := rbgV.ValidateCreate(ctx, o); err != nil {
				return apierrors.NewForbidden(pr416RBGGR, o.Name, err)
			}
		case *workloadsv1alpha2.RoleBasedGroupSet:
			if _, err := setV.ValidateCreate(ctx, o); err != nil {
				return apierrors.NewForbidden(pr416RBGSetGR, o.Name, err)
			}
		}
		return nil
	}

	admitUpdate := func(ctx context.Context, obj client.Object) error {
		key := client.ObjectKeyFromObject(obj)
		switch o := obj.(type) {
		case *workloadsv1alpha2.RoleBasedGroup:
			stored := &workloadsv1alpha2.RoleBasedGroup{}
			if err := inner.Get(ctx, key, stored); err != nil {
				return err
			}
			if _, err := rbgV.ValidateUpdate(ctx, stored, o); err != nil {
				return apierrors.NewForbidden(pr416RBGGR, o.Name, err)
			}
		case *workloadsv1alpha2.RoleBasedGroupSet:
			stored := &workloadsv1alpha2.RoleBasedGroupSet{}
			if err := inner.Get(ctx, key, stored); err != nil {
				return err
			}
			if _, err := setV.ValidateUpdate(ctx, stored, o); err != nil {
				return apierrors.NewForbidden(pr416RBGSetGR, o.Name, err)
			}
		}
		return nil
	}

	return interceptor.NewClient(inner, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if err := admitCreate(ctx, obj); err != nil {
				return err
			}
			return c.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if err := admitUpdate(ctx, obj); err != nil {
				return err
			}
			return c.Update(ctx, obj, opts...)
		},
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			// A PATCH is an UPDATE for admission. obj here is the fully
			// mutated desired object the controller built from the stored
			// one, which is what the API server ends up with after applying
			// the patch, so validating (stored, obj) is faithful.
			if err := admitUpdate(ctx, obj); err != nil {
				return err
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
		// SubResource* intentionally left nil: the webhook rule does not
		// match rolebasedgroups/status or rolebasedgroupsets/{status,scale}.
	})
}

func isDeniedByWebhook(err error) bool {
	return err != nil && (apierrors.IsForbidden(err) || strings.Contains(err.Error(), "is forbidden"))
}

// preexistingRBGSet builds an RBGSet that was admitted while the deprecated
// types were still enabled (i.e. it is already in etcd), whose template uses a
// deprecated workload type.
func preexistingRBGSet(name string, replicas int32, wt string) *workloadsv1alpha2.RoleBasedGroupSet {
	return &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(replicas),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{pr416Role("worker", wt, 1)},
				},
			},
		},
	}
}

// childOf builds the child RBG that newRBGForSet would have produced for index.
func childOf(set *workloadsv1alpha2.RoleBasedGroupSet, index int) *workloadsv1alpha2.RoleBasedGroup {
	rbg := newRBGForSet(set, index)
	return rbg
}

var pr416DeprecatedTypes = []string{
	constants.DeploymentWorkloadType,
	constants.StatefulSetWorkloadType,
	constants.LeaderWorkerSetWorkloadType,
}

// ---------------------------------------------------------------------------
// A. The three round-1 blockers on the UPDATE path
// ---------------------------------------------------------------------------

// TestPR416R2_F2a_DiscoveryModeAnnotationPatch_NowAccepted drives the real
// ensureDiscoveryConfigMode (rolebasedgroup_controller.go:340-371, patch at
// :363) against a pre-existing RBG with a deprecated workload type.
func TestPR416R2_F2a_DiscoveryModeAnnotationPatch_NowAccepted(t *testing.T) {
	r3RetireGrandfatheringAssertion(t)
	for _, wt := range pr416DeprecatedTypes {
		t.Run(wt, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				name := "toggleOFF_fixed"
				if enabled {
					name = "CONTROL_toggleON"
				}
				t.Run(name, func(t *testing.T) {
					s := pr416R2Scheme(t)
					rbg := &workloadsv1alpha2.RoleBasedGroup{
						ObjectMeta: metav1.ObjectMeta{Name: "preexisting", Namespace: "default"},
						Spec: workloadsv1alpha2.RoleBasedGroupSpec{
							Roles: []workloadsv1alpha2.RoleSpec{pr416Role("worker", wt, 1)},
						},
					}
					inner := fake.NewClientBuilder().WithScheme(s).
						WithObjects(rbg.DeepCopy()).
						WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroup{}).
						Build()
					r := &RoleBasedGroupReconciler{
						client:   admissionClient(t, inner, enabled),
						scheme:   s,
						recorder: record.NewFakeRecorder(32),
					}
					live := &workloadsv1alpha2.RoleBasedGroup{}
					if err := inner.Get(context.Background(),
						types.NamespacedName{Name: "preexisting", Namespace: "default"}, live); err != nil {
						t.Fatalf("seed get: %v", err)
					}
					requeue, err := r.ensureDiscoveryConfigMode(context.Background(), live)
					if err != nil {
						t.Fatalf("ensureDiscoveryConfigMode (requeue=%v) was DENIED, F2a is NOT fixed: %v", requeue, err)
					}
					got := &workloadsv1alpha2.RoleBasedGroup{}
					if err := inner.Get(context.Background(),
						types.NamespacedName{Name: "preexisting", Namespace: "default"}, got); err != nil {
						t.Fatalf("get after patch: %v", err)
					}
					mode := got.GetDiscoveryConfigMode()
					if mode == "" {
						t.Fatalf("discovery config mode annotation was not persisted")
					}
					t.Logf("F2a FIXED (%s, enableDeprecated=%v): rolebasedgroup_controller.go:363 "+
						"MergeFrom patch on the main resource is ACCEPTED; mode=%q", wt, enabled, mode)
				})
			}
		})
	}
}

// TestPR416R2_F10_ScalingAdapterReplicaUpdate_NowAccepted drives the real
// updateRoleReplicas (rolebasedgroupscalingadapter_controller.go:499-524,
// Update at :511) -- the HPA / scale path.
func TestPR416R2_F10_ScalingAdapterReplicaUpdate_NowAccepted(t *testing.T) {
	r3RetireGrandfatheringAssertion(t)
	for _, wt := range pr416DeprecatedTypes {
		t.Run(wt, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				name := "toggleOFF_fixed"
				if enabled {
					name = "CONTROL_toggleON"
				}
				t.Run(name, func(t *testing.T) {
					s := pr416R2Scheme(t)
					role := pr416Role("worker", wt, 1)
					role.ScalingAdapter = &workloadsv1alpha2.ScalingAdapter{Enable: true}
					rbg := &workloadsv1alpha2.RoleBasedGroup{
						ObjectMeta: metav1.ObjectMeta{Name: "preexisting", Namespace: "default"},
						Spec: workloadsv1alpha2.RoleBasedGroupSpec{
							Roles: []workloadsv1alpha2.RoleSpec{role},
						},
					}
					// HPA already scaled the adapter to 4; the controller now
					// propagates that onto the RBG role.
					adapter := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{
						ObjectMeta: metav1.ObjectMeta{
							Name:      workloadsv1alpha2.GenerateScalingAdapterName("preexisting", "worker"),
							Namespace: "default",
						},
						Spec: workloadsv1alpha2.RoleBasedGroupScalingAdapterSpec{Replicas: ptr.To(int32(4))},
					}
					inner := fake.NewClientBuilder().WithScheme(s).
						WithObjects(rbg.DeepCopy(), adapter).
						Build()
					r := &RoleBasedGroupScalingAdapterReconciler{
						client:   admissionClient(t, inner, enabled),
						scheme:   s,
						recorder: record.NewFakeRecorder(32),
					}
					live := &workloadsv1alpha2.RoleBasedGroup{}
					if err := inner.Get(context.Background(),
						types.NamespacedName{Name: "preexisting", Namespace: "default"}, live); err != nil {
						t.Fatalf("seed get: %v", err)
					}
					if err := r.updateRoleReplicas(context.Background(), live, "worker", ptr.To(int32(4))); err != nil {
						t.Fatalf("updateRoleReplicas was DENIED, F10 is NOT fixed: %v", err)
					}
					got := &workloadsv1alpha2.RoleBasedGroup{}
					_ = inner.Get(context.Background(),
						types.NamespacedName{Name: "preexisting", Namespace: "default"}, got)
					if got.Spec.Roles[0].Replicas == nil || *got.Spec.Roles[0].Replicas != 4 {
						t.Fatalf("replicas not persisted: %v", got.Spec.Roles[0].Replicas)
					}
					t.Logf("F10 FIXED (%s, enableDeprecated=%v): "+
						"rolebasedgroupscalingadapter_controller.go:511 Update is ACCEPTED; replicas=4", wt, enabled)
				})
			}
		})
	}
}

// TestPR416R2_F9Update_RBGSetTemplateSyncToChild_NowAccepted drives the real
// updateExistingRBGs (rolebasedgroupset_controller.go:436-480, Update at :465).
func TestPR416R2_F9Update_RBGSetTemplateSyncToChild_NowAccepted(t *testing.T) {
	r3RetireGrandfatheringAssertion(t)
	for _, wt := range pr416DeprecatedTypes {
		t.Run(wt, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				name := "toggleOFF_fixed"
				if enabled {
					name = "CONTROL_toggleON"
				}
				t.Run(name, func(t *testing.T) {
					s := pr416R2Scheme(t)
					set := preexistingRBGSet("preexisting", 1, wt)
					// Template gained a label, so the child needs a sync; the
					// sync rewrites Spec.Roles wholesale from the template.
					set.Spec.GroupTemplate.Labels = map[string]string{"team": "infra"}
					child := childOf(set, 0)
					child.Labels = map[string]string{
						constants.GroupSetNameLabelKey:  set.Name,
						constants.GroupSetIndexLabelKey: "0",
					}
					inner := fake.NewClientBuilder().WithScheme(s).
						WithObjects(set.DeepCopy(), child.DeepCopy()).
						WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupSet{}).
						Build()
					r := &RoleBasedGroupSetReconciler{
						client:   admissionClient(t, inner, enabled),
						scheme:   s,
						recorder: record.NewFakeRecorder(32),
					}
					if !r.needsUpdate(set, child) {
						t.Fatalf("precondition: needsUpdate must be true so the Update path is exercised")
					}
					err := r.updateExistingRBGs(context.Background(), set,
						[]*workloadsv1alpha2.RoleBasedGroup{child})
					if err != nil {
						t.Fatalf("updateExistingRBGs was DENIED, F9(update) is NOT fixed: %v", err)
					}
					t.Logf("F9(update) FIXED (%s, enableDeprecated=%v): "+
						"rolebasedgroupset_controller.go:465 template sync Update is ACCEPTED", wt, enabled)
				})
			}
		})
	}
}

// ---------------------------------------------------------------------------
// B. THE GAP: the RBGSet controller CREATEs child RBGs, and CREATE is still strict
// ---------------------------------------------------------------------------

// TestPR416R2_F9Create_RBGSetScaleUpChildCreateIsDenied runs the REAL
// RoleBasedGroupSetReconciler.Reconcile on a pre-existing RBGSet whose template
// uses a deprecated workload type and whose replicas were raised from 1 to 2.
// The controller must CREATE child index 1
// (rolebasedgroupset_controller.go:231, payload from newRBGForSet at :510-541,
// which copies GroupTemplate.Spec.Roles verbatim). CREATE goes through the
// strict validateNoDeprecatedWorkloadTypes, so it is denied.
func TestPR416R2_F9Create_RBGSetScaleUpChildCreateIsDenied(t *testing.T) {
	for _, wt := range pr416DeprecatedTypes {
		t.Run(wt, func(t *testing.T) {
			run := func(t *testing.T, enabled bool) (error, client.WithWatch) {
				s := pr416R2Scheme(t)
				// Pre-existing: admitted while the toggle was still on, now
				// scaled 1 -> 2 (spec.replicas is a scale subresource on
				// RBGSet, so even an HPA can do this without the webhook
				// seeing it).
				set := preexistingRBGSet("preexisting", 2, wt)
				child0 := childOf(set, 0)
				child0.Labels = map[string]string{
					constants.GroupSetNameLabelKey:  set.Name,
					constants.GroupSetIndexLabelKey: "0",
				}
				inner := fake.NewClientBuilder().WithScheme(s).
					WithObjects(set.DeepCopy(), child0.DeepCopy()).
					WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupSet{}).
					WithIndex(&workloadsv1alpha2.RoleBasedGroup{}, "metadata.name",
						func(o client.Object) []string { return []string{o.GetName()} }).
					Build()
				r := &RoleBasedGroupSetReconciler{
					client:   admissionClient(t, inner, enabled),
					scheme:   s,
					recorder: record.NewFakeRecorder(32),
				}
				_, err := r.Reconcile(context.Background(), ctrl.Request{
					NamespacedName: types.NamespacedName{Name: "preexisting", Namespace: "default"},
				})
				return err, inner
			}

			t.Run("toggleOFF_DENIED", func(t *testing.T) {
				err, inner := run(t, false)
				if err == nil {
					t.Fatalf("expected Reconcile to fail: the child RBG CREATE should be denied")
				}
				if !isDeniedByWebhook(err) {
					t.Fatalf("Reconcile failed but not via admission: %v", err)
				}
				if !strings.Contains(err.Error(), "is deprecated and not enabled on this cluster") {
					t.Fatalf("denial did not come from the deprecated-workload-type check: %v", err)
				}
				// The create-path message, not the update-path one: proof the
				// strict create check fired.
				if strings.Contains(err.Error(), "newly added role") ||
					strings.Contains(err.Error(), "roles that already use a deprecated workload type keep working") {
					t.Fatalf("expected the CREATE-path message, got the UPDATE-path one: %v", err)
				}
				missing := &workloadsv1alpha2.RoleBasedGroup{}
				gerr := inner.Get(context.Background(),
					types.NamespacedName{Name: "preexisting-1", Namespace: "default"}, missing)
				if !apierrors.IsNotFound(gerr) {
					t.Fatalf("child preexisting-1 should not exist, get err=%v", gerr)
				}
				// The status update at :332 is never reached, because scaleUp's
				// error short-circuits Reconcile at :184-189.
				set := &workloadsv1alpha2.RoleBasedGroupSet{}
				if err := inner.Get(context.Background(),
					types.NamespacedName{Name: "preexisting", Namespace: "default"}, set); err != nil {
					t.Fatalf("get set: %v", err)
				}
				t.Logf("F9(create) REPRODUCED (%s): rolebasedgroupset_controller.go:231 "+
					"r.client.Create(ctx, rbg) is DENIED -> %v", wt, err)
				t.Logf("  consequence: child preexisting-1 is never created (scale-up wedged), and "+
					"Reconcile returns before updateStatus at :332, so status stays at "+
					"replicas=%d readyReplicas=%d conditions=%d with no explanation on the object",
					set.Status.Replicas, set.Status.ReadyReplicas, len(set.Status.Conditions))
			})

			t.Run("CONTROL_toggleON_accepted", func(t *testing.T) {
				err, inner := run(t, true)
				if err != nil {
					t.Fatalf("CONTROL failed: with EnableDeprecatedWorkloadTypes=true the same "+
						"reconcile must succeed, got %v", err)
				}
				created := &workloadsv1alpha2.RoleBasedGroup{}
				if err := inner.Get(context.Background(),
					types.NamespacedName{Name: "preexisting-1", Namespace: "default"}, created); err != nil {
					t.Fatalf("CONTROL: child preexisting-1 should exist: %v", err)
				}
				gotWT := created.Spec.Roles[0].GetWorkloadType()
				if gotWT != wt {
					t.Fatalf("CONTROL: child role workload type = %q, want %q", gotWT, wt)
				}
				t.Logf("CONTROL PASSES (%s): the identical reconcile creates preexisting-1 with "+
					"workload type %q when the deprecated types are enabled, so the denial above is "+
					"attributable to the flag and not to a malformed child object", wt, gotWT)
			})
		})
	}
}

// TestPR416R2_F9Create_SelfHealRecreateOfDeletedChildIsDenied is the same write
// site reached without any user action at all: replicas are unchanged, but a
// child RBG was deleted (node/GC/operator error, or a user cleaning up). The
// RBGSet controller must recreate it and cannot.
func TestPR416R2_F9Create_SelfHealRecreateOfDeletedChildIsDenied(t *testing.T) {
	wt := constants.StatefulSetWorkloadType

	run := func(t *testing.T, enabled bool) (error, client.WithWatch) {
		s := pr416R2Scheme(t)
		set := preexistingRBGSet("preexisting", 1, wt) // replicas never changed
		inner := fake.NewClientBuilder().WithScheme(s).
			WithObjects(set.DeepCopy()). // child index 0 is GONE
			WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupSet{}).
			Build()
		r := &RoleBasedGroupSetReconciler{
			client:   admissionClient(t, inner, enabled),
			scheme:   s,
			recorder: record.NewFakeRecorder(32),
		}
		_, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{Name: "preexisting", Namespace: "default"},
		})
		return err, inner
	}

	t.Run("toggleOFF_DENIED", func(t *testing.T) {
		err, _ := run(t, false)
		if !isDeniedByWebhook(err) {
			t.Fatalf("expected an admission denial, got %v", err)
		}
		t.Logf("REPRODUCED: with no user action at all, a pre-existing RBGSet can no longer "+
			"replace a lost child. rolebasedgroupset_controller.go:231 -> %v", err)
	})

	t.Run("CONTROL_toggleON_accepted", func(t *testing.T) {
		err, inner := run(t, true)
		if err != nil {
			t.Fatalf("CONTROL failed: %v", err)
		}
		got := &workloadsv1alpha2.RoleBasedGroup{}
		if err := inner.Get(context.Background(),
			types.NamespacedName{Name: "preexisting-0", Namespace: "default"}, got); err != nil {
			t.Fatalf("CONTROL: child should have been recreated: %v", err)
		}
		t.Logf("CONTROL PASSES: the same self-heal succeeds when the deprecated types are enabled")
	})
}

// TestPR416R2_CreatePathIsStrictForTheExactControllerPayload isolates the same
// gap at the validator boundary: the object newRBGForSet builds is rejected by
// ValidateCreate but would be accepted by ValidateUpdate against the very roles
// it was copied from. That asymmetry is the whole finding.
func TestPR416R2_CreatePathIsStrictForTheExactControllerPayload(t *testing.T) {
	r3RetireGrandfatheringAssertion(t)
	s := pr416R2Scheme(t)
	inner := fake.NewClientBuilder().WithScheme(s).Build()
	v := &workloadsv1alpha2.RoleBasedGroupValidator{Client: inner, EnableDeprecatedWorkloadTypes: false}
	vOn := &workloadsv1alpha2.RoleBasedGroupValidator{Client: inner, EnableDeprecatedWorkloadTypes: true}

	for _, wt := range pr416DeprecatedTypes {
		t.Run(wt, func(t *testing.T) {
			set := preexistingRBGSet("preexisting", 2, wt)
			child := newRBGForSet(set, 1)

			if _, err := v.ValidateCreate(context.Background(), child); err == nil {
				t.Fatalf("expected ValidateCreate to reject the controller's child payload")
			} else {
				t.Logf("ValidateCreate DENIES the exact newRBGForSet payload: %v", err)
			}
			if _, err := vOn.ValidateCreate(context.Background(), child); err != nil {
				t.Fatalf("CONTROL: ValidateCreate must accept it with the toggle on: %v", err)
			}
			t.Logf("CONTROL PASSES: same payload accepted with EnableDeprecatedWorkloadTypes=true")

			// Same payload, same roles as the parent template, as an UPDATE:
			// allowed. Only the verb differs.
			if _, err := v.ValidateUpdate(context.Background(), child.DeepCopy(), child); err != nil {
				t.Fatalf("ValidateUpdate on identical roles must be allowed by the new rule: %v", err)
			}
			t.Logf("ASYMMETRY: identical roles are ACCEPTED on UPDATE and DENIED on CREATE")
		})
	}
}

// ---------------------------------------------------------------------------
// C. rename / reorder
// ---------------------------------------------------------------------------

// TestPR416R2_RoleReorderOnUpdateIsAccepted confirms the map is keyed by name,
// so reordering roles is not mistaken for adding them.
func TestPR416R2_RoleReorderOnUpdateIsAccepted(t *testing.T) {
	r3RetireGrandfatheringAssertion(t)
	s := pr416R2Scheme(t)
	inner := fake.NewClientBuilder().WithScheme(s).Build()
	v := &workloadsv1alpha2.RoleBasedGroupValidator{Client: inner, EnableDeprecatedWorkloadTypes: false}

	old := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{
			pr416Role("prefill", constants.StatefulSetWorkloadType, 1),
			pr416Role("decode", constants.LeaderWorkerSetWorkloadType, 1),
		}},
	}
	newObj := old.DeepCopy()
	newObj.Spec.Roles[0], newObj.Spec.Roles[1] = old.Spec.Roles[1], old.Spec.Roles[0]

	if _, err := v.ValidateUpdate(context.Background(), old, newObj); err != nil {
		t.Fatalf("reorder must be accepted (map is keyed by name): %v", err)
	}
	t.Logf("OK: reordering roles is accepted; the name-keyed map handles it correctly")
}

// TestPR416R2_RoleRenameOnUpdateIsDenied documents that renaming a role that
// legitimately carries a deprecated type reads as "newly added".
func TestPR416R2_RoleRenameOnUpdateIsDenied(t *testing.T) {
	r3RetireGrandfatheringAssertion(t)
	s := pr416R2Scheme(t)
	inner := fake.NewClientBuilder().WithScheme(s).Build()

	mk := func(roleName string) *workloadsv1alpha2.RoleBasedGroup {
		return &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{
				pr416Role(roleName, constants.StatefulSetWorkloadType, 1),
			}},
		}
	}

	t.Run("toggleOFF_DENIED", func(t *testing.T) {
		v := &workloadsv1alpha2.RoleBasedGroupValidator{Client: inner, EnableDeprecatedWorkloadTypes: false}
		_, err := v.ValidateUpdate(context.Background(), mk("worker"), mk("workers"))
		if err == nil {
			t.Fatalf("expected the rename to be denied")
		}
		if !strings.Contains(err.Error(), "newly added role") {
			t.Fatalf("expected the newly-added-role message, got: %v", err)
		}
		t.Logf("REPRODUCED: renaming worker->workers on a role that already used %q is denied as a "+
			"'newly added role': %v", constants.StatefulSetWorkloadType, err)
	})

	t.Run("CONTROL_toggleON_accepted", func(t *testing.T) {
		v := &workloadsv1alpha2.RoleBasedGroupValidator{Client: inner, EnableDeprecatedWorkloadTypes: true}
		if _, err := v.ValidateUpdate(context.Background(), mk("worker"), mk("workers")); err != nil {
			t.Fatalf("CONTROL failed: %v", err)
		}
		t.Logf("CONTROL PASSES: the same rename is accepted with the deprecated types enabled")
	})
}

// TestPR416R2_RBGSetTemplateRoleRenameIsDeniedAtTheParent shows the rename is
// stopped at the RBGSet, so it never reaches the child-sync path.
func TestPR416R2_RBGSetTemplateRoleRenameIsDeniedAtTheParent(t *testing.T) {
	mk := func(roleName string) *workloadsv1alpha2.RoleBasedGroupSet {
		set := preexistingRBGSet("preexisting", 1, constants.StatefulSetWorkloadType)
		set.Spec.GroupTemplate.Spec.Roles[0].Name = roleName
		return set
	}

	t.Run("toggleOFF_DENIED", func(t *testing.T) {
		v := &workloadsv1alpha2.RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: false}
		_, err := v.ValidateUpdate(context.Background(), mk("worker"), mk("workers"))
		if err == nil {
			t.Fatalf("expected the template rename to be denied")
		}
		t.Logf("REPRODUCED: renaming a role in a pre-existing RBGSet template is denied at the "+
			"parent (so the child never diverges): %v", err)
	})

	t.Run("CONTROL_toggleON_accepted", func(t *testing.T) {
		v := &workloadsv1alpha2.RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: true}
		if _, err := v.ValidateUpdate(context.Background(), mk("worker"), mk("workers")); err != nil {
			t.Fatalf("CONTROL failed: %v", err)
		}
		t.Logf("CONTROL PASSES")
	})
}

// ---------------------------------------------------------------------------
// D. conversion, defaulting, and the shape of an empty oldRoles
// ---------------------------------------------------------------------------

// convertV1alpha1RBG runs the real conversion webhook direction v1alpha1 -> v1alpha2.
func convertV1alpha1RBG(t *testing.T, src *workloadsv1alpha1.RoleBasedGroup) *workloadsv1alpha2.RoleBasedGroup {
	t.Helper()
	dst := &workloadsv1alpha2.RoleBasedGroup{}
	if err := src.ConvertTo(dst); err != nil {
		t.Fatalf("ConvertTo: %v", err)
	}
	return dst
}

// TestPR416R2_V1alpha1ConversionIsStableAcrossUpdate checks the question that
// decides whether "previousType != wt" can misfire: does GetWorkloadType()
// return the same string for the stored object and for the same object
// re-submitted through the v1alpha1 endpoint?
func TestPR416R2_V1alpha1ConversionIsStableAcrossUpdate(t *testing.T) {
	r3RetireGrandfatheringAssertion(t)
	s := pr416R2Scheme(t)
	inner := fake.NewClientBuilder().WithScheme(s).Build()
	v := &workloadsv1alpha2.RoleBasedGroupValidator{Client: inner, EnableDeprecatedWorkloadTypes: false}

	mkV1 := func(apiVersion, kind string) *workloadsv1alpha1.RoleBasedGroup {
		return &workloadsv1alpha1.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
			Spec: workloadsv1alpha1.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha1.RoleSpec{{
					Name:     "worker",
					Replicas: ptr.To(int32(1)),
					// The v1alpha1 CRD defaults this to apps/v1 StatefulSet
					// (api/workloads/v1alpha1/rolebasedgroup_types.go:341,
					// config/crd/bases/...rolebasedgroups.yaml). Setting it
					// explicitly is what the API server would have done.
					Workload: workloadsv1alpha1.WorkloadSpec{APIVersion: apiVersion, Kind: kind},
				}},
			},
		}
	}

	t.Run("v1alpha1_defaulted_STS_reapplied_over_itself_accepted", func(t *testing.T) {
		stored := convertV1alpha1RBG(t, mkV1("apps/v1", "StatefulSet"))
		if got := stored.Spec.Roles[0].GetWorkloadType(); got != constants.StatefulSetWorkloadType {
			t.Fatalf("conversion produced %q, want %q", got, constants.StatefulSetWorkloadType)
		}
		incoming := convertV1alpha1RBG(t, mkV1("apps/v1", "StatefulSet"))
		if _, err := v.ValidateUpdate(context.Background(), stored, incoming); err != nil {
			t.Fatalf("re-applying an unchanged v1alpha1 manifest must be accepted: %v", err)
		}
		t.Logf("OK: conversion is deterministic; oldObj and newObj both yield %q, so the "+
			"previousType!=wt branch does not misfire", constants.StatefulSetWorkloadType)
	})

	t.Run("v1alpha2_RoleInstanceSet_round_tripped_through_v1alpha1_accepted", func(t *testing.T) {
		// A v1alpha2-native object read via v1alpha1 (ConvertFrom) and written
		// back (ConvertTo): the annotation goes from absent to the explicit
		// RoleInstanceSet string, which is not deprecated, so nothing fires.
		stored := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{pr416Role("worker", "", 1)},
			},
		}
		v1view := &workloadsv1alpha1.RoleBasedGroup{}
		if err := v1view.ConvertFrom(stored.DeepCopy()); err != nil {
			t.Fatalf("ConvertFrom: %v", err)
		}
		back := convertV1alpha1RBG(t, v1view)
		if _, err := v.ValidateUpdate(context.Background(), stored, back); err != nil {
			t.Fatalf("v1alpha1 round-trip of a RoleInstanceSet role must be accepted: %v", err)
		}
		t.Logf("OK: v1alpha2 default -> v1alpha1 workload -> back yields %q on both sides",
			back.Spec.Roles[0].GetWorkloadType())
	})

	t.Run("handwritten_v1alpha1_manifest_omitting_workload_over_a_LWS_role_is_denied", func(t *testing.T) {
		// The trap the author documents in deprecatedWorkloadTypeUpdateHint.
		// Stored role is LeaderWorkerSet; a v1alpha1 manifest that omits
		// spec.roles[].workload gets it defaulted to apps/v1 StatefulSet, so
		// the update reads as a type change between two deprecated types.
		stored := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{
					pr416Role("worker", constants.LeaderWorkerSetWorkloadType, 1),
				},
			},
		}
		incoming := convertV1alpha1RBG(t, mkV1("apps/v1", "StatefulSet")) // schema default
		_, err := v.ValidateUpdate(context.Background(), stored, incoming)
		if err == nil {
			t.Fatalf("expected denial: the defaulted type differs from the stored one")
		}
		if !strings.Contains(err.Error(), "workload type cannot be changed") {
			t.Fatalf("expected the type-change message, got: %v", err)
		}
		t.Logf("CONFIRMED (documented, and the type really does change): %v", err)

		vOn := &workloadsv1alpha2.RoleBasedGroupValidator{Client: inner, EnableDeprecatedWorkloadTypes: true}
		if _, err := vOn.ValidateUpdate(context.Background(), stored, incoming); err != nil {
			t.Fatalf("CONTROL failed: %v", err)
		}
		t.Logf("CONTROL PASSES: accepted with the deprecated types enabled")
	})
}

// TestPR416R2_EmptyOldRolesWouldRejectEverything records the failure shape if an
// UPDATE ever arrived with an empty oldRoles. This is the mechanism only -- see
// the report: no reachable path was found (the API server always supplies the
// full stored object as oldObject for UPDATE and PATCH, controller-runtime
// errors out rather than passing an empty oldObj, and both CRDs have
// storageversion v1alpha2 so oldObject is never conversion-derived).
func TestPR416R2_EmptyOldRolesWouldRejectEverything(t *testing.T) {
	r3RetireGrandfatheringAssertion(t)
	s := pr416R2Scheme(t)
	inner := fake.NewClientBuilder().WithScheme(s).Build()
	v := &workloadsv1alpha2.RoleBasedGroupValidator{Client: inner, EnableDeprecatedWorkloadTypes: false}

	old := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: nil},
	}
	newObj := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{
			pr416Role("worker", constants.StatefulSetWorkloadType, 1),
		}},
	}
	_, err := v.ValidateUpdate(context.Background(), old, newObj)
	if err == nil || !strings.Contains(err.Error(), "newly added role") {
		t.Fatalf("expected the newly-added-role message, got: %v", err)
	}
	t.Logf("MECHANISM: an empty oldRoles makes every role look newly added -> %v", err)

	vOn := &workloadsv1alpha2.RoleBasedGroupValidator{Client: inner, EnableDeprecatedWorkloadTypes: true}
	if _, err := vOn.ValidateUpdate(context.Background(), old, newObj); err != nil {
		t.Fatalf("CONTROL failed: %v", err)
	}
	t.Logf("CONTROL PASSES: accepted with the deprecated types enabled")
}

// TestPR416R2_StatusSubresourceWritesAreNotIntercepted records that the RBG
// status SSA patch (rolebasedgroup_controller.go:782 -> pkg/utils/utils.go:83)
// and the RBGSet status update (rolebasedgroupset_controller.go:332) target
// subresources, which the webhook rule does not match.
func TestPR416R2_StatusSubresourceWritesAreNotIntercepted(t *testing.T) {
	s := pr416R2Scheme(t)
	set := preexistingRBGSet("preexisting", 1, constants.StatefulSetWorkloadType)
	inner := fake.NewClientBuilder().WithScheme(s).
		WithObjects(set.DeepCopy()).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupSet{}).
		Build()
	c := admissionClient(t, inner, false)

	live := &workloadsv1alpha2.RoleBasedGroupSet{}
	if err := c.Get(context.Background(),
		types.NamespacedName{Name: "preexisting", Namespace: "default"}, live); err != nil {
		t.Fatalf("get: %v", err)
	}
	live.Status.Replicas = 1
	live.Status.ReadyReplicas = 1
	if err := c.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("status update must not be gated by the webhook: %v", err)
	}
	t.Logf("OK: status subresource writes bypass the webhook (rule lists `rolebasedgroupsets`, " +
		"not `rolebasedgroupsets/status`), so no status write is denied")
}

// r3RetireGrandfatheringAssertion retires a round-2 assertion that the design
// reversal at aac6056d made meaningless.
//
// Round 2 narrowed ValidateUpdate to a delta check
// (validateNoNewDeprecatedWorkloadTypes) so a pre-existing deprecated role stayed
// writable. Commit aac6056d deletes that function and restores the strict
// whole-object check on both create and update, for RoleBasedGroup and
// RoleBasedGroupSet alike.
//
// So these tests are NOT evidence of a regression -- the contract they encode no
// longer exists, deliberately. The replacement pins for the current design are the
// TestPR416R3_* tests in api/workloads/v1alpha2/pr416_r3_design_test.go. The
// question the reversal actually raises -- whether the "fresh installation only"
// premise that justifies it is enforced anywhere (it is not) -- is R3-F22, proved by
// docs/verification/pr416-api-compat-toggle/scripts/10-fresh-install-invariant.sh.
func r3RetireGrandfatheringAssertion(t *testing.T) {
	t.Helper()
	t.Skip("superseded: grandfathering removed at aac6056d; see TestPR416R3_* pins and R3-F22")
}
