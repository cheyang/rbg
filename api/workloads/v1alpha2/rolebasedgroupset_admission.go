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

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/rbgs/api/workloads/constants"
)

// RoleBasedGroupSetValidator implements admission.CustomValidator for RoleBasedGroupSet.
//
// +kubebuilder:webhook:path=/validate-workloads-x-k8s-io-v1alpha2-rolebasedgroupset,mutating=false,failurePolicy=fail,sideEffects=None,groups=workloads.x-k8s.io,resources=rolebasedgroupsets,verbs=create;update,versions=v1alpha2,name=vrolebasedgroupset.kb.io,admissionReviewVersions=v1
// +kubebuilder:object:generate=false
type RoleBasedGroupSetValidator struct {
	// Client lists the RBGSet's children so topology updates can be checked against
	// their TopologyConstraintActive markers. It should be cache-backed, with a
	// direct-API fallback only while the manager cache has not started yet.
	Client client.Reader

	// EnableDeprecatedWorkloadTypes reports whether the deprecated workload types
	// (Deployment, StatefulSet, LeaderWorkerSet) are still accepted. When false,
	// RBGSets whose template uses them are rejected.
	EnableDeprecatedWorkloadTypes bool
}

var _ admission.CustomValidator = &RoleBasedGroupSetValidator{}

// ValidateCreate validates a RoleBasedGroupSet on creation.
func (v *RoleBasedGroupSetValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	rbgs, ok := obj.(*RoleBasedGroupSet)
	if !ok {
		return nil, fmt.Errorf("expected *RoleBasedGroupSet but got %T", obj)
	}
	klog.V(4).InfoS("validating RoleBasedGroupSet on create", "name", rbgs.Name, "namespace", rbgs.Namespace)

	var allErrs []error
	if err := validateRoleDependencies("spec.groupTemplate.spec.roles", rbgs.Spec.GroupTemplate.Spec.Roles); err != nil {
		allErrs = append(allErrs, err)
	}
	if err := validateRoleTopologyConstraints("spec.groupTemplate.spec.roles", rbgs.Spec.GroupTemplate.Spec.Roles); err != nil {
		allErrs = append(allErrs, err)
	}
	if !v.EnableDeprecatedWorkloadTypes {
		if err := validateNoDeprecatedWorkloadTypes("spec.groupTemplate.spec.roles", rbgs.Spec.GroupTemplate.Spec.Roles); err != nil {
			allErrs = append(allErrs, err)
		}
	}

	return nil, utilerrors.NewAggregate(allErrs)
}

// ValidateUpdate validates a RoleBasedGroupSet on update.
func (v *RoleBasedGroupSetValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldRBGS, ok := oldObj.(*RoleBasedGroupSet)
	if !ok {
		return nil, fmt.Errorf("expected *RoleBasedGroupSet but got %T", oldObj)
	}
	rbgs, ok := newObj.(*RoleBasedGroupSet)
	if !ok {
		return nil, fmt.Errorf("expected *RoleBasedGroupSet but got %T", newObj)
	}
	klog.V(4).InfoS("validating RoleBasedGroupSet on update", "name", rbgs.Name, "namespace", rbgs.Namespace)

	var allErrs []error
	if err := validateRoleDependencies("spec.groupTemplate.spec.roles", rbgs.Spec.GroupTemplate.Spec.Roles); err != nil {
		allErrs = append(allErrs, err)
	}
	if err := validateRoleTopologyConstraints("spec.groupTemplate.spec.roles", rbgs.Spec.GroupTemplate.Spec.Roles); err != nil {
		allErrs = append(allErrs, err)
	}
	if !v.EnableDeprecatedWorkloadTypes {
		if err := validateNoDeprecatedWorkloadTypes(
			"spec.groupTemplate.spec.roles",
			rbgs.Spec.GroupTemplate.Spec.Roles,
		); err != nil {
			allErrs = append(allErrs, err)
		}
	}
	if err := v.validateTopologyImmutability(ctx, oldRBGS, rbgs); err != nil {
		allErrs = append(allErrs, err)
	}

	return nil, utilerrors.NewAggregate(allErrs)
}

// validateTopologyImmutability prevents an RBGSet template update from propagating a
// forbidden topology edit into children whose placement has already become active.
// Without this parent-level check, the parent would hold a desired spec that child
// admission keeps rejecting and the RBGSet reconciler would retry forever.
func (v *RoleBasedGroupSetValidator) validateTopologyImmutability(
	ctx context.Context,
	oldRBGS, newRBGS *RoleBasedGroupSet,
) error {
	roleName, changed := roleTopologyTemplateChanged(oldRBGS, newRBGS)
	if !changed {
		return nil
	}
	if v.Client == nil {
		return fmt.Errorf(
			"spec.groupTemplate.spec.roles[%s].instanceTopologyConstraint cannot be validated because no API client is configured",
			roleName)
	}

	children := &RoleBasedGroupList{}
	if err := v.Client.List(
		ctx,
		children,
		client.InNamespace(newRBGS.Namespace),
		client.MatchingLabels{constants.GroupSetNameLabelKey: newRBGS.Name},
	); err != nil {
		return fmt.Errorf("list RoleBasedGroupSet %s/%s children: %w", newRBGS.Namespace, newRBGS.Name, err)
	}
	for i := range children.Items {
		child := &children.Items[i]
		if !TopologyConditionActive(child.Status.Conditions) {
			continue
		}
		return fmt.Errorf(
			"spec.groupTemplate.spec.roles[%s].instanceTopologyConstraint is immutable after child RoleBasedGroup %s/%s has active topology constraints; delete and recreate the child workloads to change it",
			roleName, child.Namespace, child.Name)
	}
	return nil
}

func roleTopologyTemplateChanged(oldRBGS, newRBGS *RoleBasedGroupSet) (string, bool) {
	if oldRBGS == nil || newRBGS == nil {
		return "", false
	}
	oldRoles := make(map[string]*RoleSpec, len(oldRBGS.Spec.GroupTemplate.Spec.Roles))
	for i := range oldRBGS.Spec.GroupTemplate.Spec.Roles {
		role := &oldRBGS.Spec.GroupTemplate.Spec.Roles[i]
		oldRoles[role.Name] = role
	}
	newRoles := make(map[string]*RoleSpec, len(newRBGS.Spec.GroupTemplate.Spec.Roles))
	for i := range newRBGS.Spec.GroupTemplate.Spec.Roles {
		role := &newRBGS.Spec.GroupTemplate.Spec.Roles[i]
		newRoles[role.Name] = role
	}

	// A new role may carry a topology constraint, just as a new role may be added to an
	// active RBG. Only topology attached to an already-existing role is immutable.
	// Deleting or renaming a role that never carried topology is an ordinary template
	// change and must not trigger the active-child check.
	for roleName, oldRole := range oldRoles {
		if oldRole.InstanceTopologyConstraint == nil {
			continue
		}
		newRole, exists := newRoles[roleName]
		if !exists || !TopologyConstraintsEqual(oldRole.InstanceTopologyConstraint, newRole.InstanceTopologyConstraint) {
			return roleName, true
		}
	}
	return "", false
}

// ValidateDelete just implements admission.CustomValidator. This verb is currently no-op.
func (v *RoleBasedGroupSetValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
