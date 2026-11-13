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

package v1alpha2

// Verification harness for PR #572 round-2 finding F14 (Reviewer B / Codex).
// Polarity: CONTRACT — the previous head (95ddf06a) and the merge-base rejected an
// update that makes an RBG RoleBasedGroupSet-managed while it carries a
// pre-existing excluded-ordinals annotation ("Removing or replacing the owner in
// this update must not bypass placement validation"). The round-2 ratchet validates
// the OLD object under the NEW managed flag, so the identical violation message is
// classified as pre-existing and the adoption is admitted.

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
)

// F14: adopting an RBG that carries excluded ordinals into an RBGSet must remain
// rejected: the managed placement gate exists exactly for this transition.
func TestVerifyF14BecomingManagedWithExcludedOrdinalsRejected(t *testing.T) {
	role := RoleSpec{Name: "worker", Annotations: map[string]string{
		constants.ExcludedOrdinalsAnnotationKey: "1,3",
	}}
	oldRBG := &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "group"},
		Spec:       RoleBasedGroupSpec{Roles: []RoleSpec{role}},
	}
	newRBG := oldRBG.DeepCopy()
	newRBG.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: GroupVersion.String(), Kind: "RoleBasedGroupSet", Name: "set",
	}}
	validator := &RoleBasedGroupValidator{EnableDeprecatedWorkloadTypes: true, Client: fake.NewClientBuilder().Build()}
	_, err := validator.ValidateUpdate(context.Background(), oldRBG, newRBG)
	if err == nil {
		t.Fatalf("F14 REPRODUCED: becoming RBGSet-managed with a pre-existing excluded-ordinals annotation is admitted; the placement gate is ratcheted away because check(old) runs under the new managed flag and yields the identical message")
	}
}

// F14-control: the legitimate ratchet case stays admitted — the same (invalid)
// annotation carried unchanged keeps the object fixable.
func TestVerifyF14ControlUnchangedViolationRatcheted(t *testing.T) {
	role := RoleSpec{Name: "worker", Annotations: map[string]string{
		constants.ExcludedOrdinalsAnnotationKey: "3-1",
	}}
	oldRBG := &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "group"},
		Spec:       RoleBasedGroupSpec{Roles: []RoleSpec{role}},
	}
	validator := &RoleBasedGroupValidator{EnableDeprecatedWorkloadTypes: true, Client: fake.NewClientBuilder().Build()}
	if _, err := validator.ValidateUpdate(context.Background(), oldRBG, oldRBG.DeepCopy()); err != nil {
		t.Fatalf("control: unchanged pre-existing violation must be ratcheted, got %v", err)
	}
}
