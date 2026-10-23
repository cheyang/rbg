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

package v1alpha1

// Verification harness for PR #509 review (verify/pr-509-deprecation-window-codex).
// Additive review evidence; production code untouched.
//
// Polarity: CONTRACT — asserts the documented compat-window semantics
// ("a role that migrated away from a deprecated workload type may not
// reintroduce one"). Red on the reviewed head = finding reproduced.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func pr509Scheme() *runtime.Scheme {
	sch := runtime.NewScheme()
	if err := AddToScheme(sch); err != nil {
		panic(err)
	}
	return sch
}

func pr509Role(workload WorkloadSpec) RoleSpec {
	return RoleSpec{Name: "worker", Replicas: ptr.To(int32(1)), Workload: workload}
}

func pr509RBG(name string, roles ...RoleSpec) *RoleBasedGroup {
	return &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       RoleBasedGroupSpec{Roles: roles},
	}
}

// The v1alpha1 deprecation validator prefers the persisted object read through
// the manager's cache-backed client over the admission request's oldObj. The
// cache can lag the API server: after a role migrates to RoleInstanceSet, there
// is a window where the cached copy still shows the deprecated workload. In
// that window the exemption check runs against the stale object and lets a
// write reintroduce the deprecated type — exactly the "new use" the compat
// gate exists to reject.
//
// Expected: the update is rejected (the request's oldObj shows the role already
// migrated; reintroducing the deprecated type is a new use).
// Suspected actual: admitted, because the stale persisted copy still shows the
// deprecated type and the validator overrides oldRoles with it.
func TestPR509Contract_StalePersistedReadDoesNotGrantExemption(t *testing.T) {
	sts := WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"}
	ris := WorkloadSpec{APIVersion: "workloads.x-k8s.io/v1alpha2", Kind: "RoleInstanceSet"}

	// Stale persisted object (what a lagging informer cache returns): role
	// still on StatefulSet.
	stalePersisted := pr509RBG("rbg", pr509Role(sts))
	// Authoritative oldObj from the admission request: the role has already
	// been migrated to RoleInstanceSet.
	oldObj := pr509RBG("rbg", pr509Role(ris))
	// The update reintroduces the deprecated type.
	newObj := pr509RBG("rbg", pr509Role(sts))

	cl := fake.NewClientBuilder().WithScheme(pr509Scheme()).WithObjects(stalePersisted).Build()
	validator := &deprecatedWorkloadValidator{Client: cl, EnableDeprecatedWorkloadTypes: true}
	_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
	assert.Error(t, err,
		"reintroducing a deprecated workload type into a migrated role must be rejected; "+
			"a stale cache read must not grant the existing-role exemption (PR #509 F3)")
}

// Control: with an up-to-date persisted read the same update IS rejected,
// proving the divergence above comes from cache staleness, not the policy.
func TestPR509Control_FreshPersistedReadRejectsReintroduction(t *testing.T) {
	sts := WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"}
	ris := WorkloadSpec{APIVersion: "workloads.x-k8s.io/v1alpha2", Kind: "RoleInstanceSet"}

	freshPersisted := pr509RBG("rbg", pr509Role(ris))
	oldObj := pr509RBG("rbg", pr509Role(ris))
	newObj := pr509RBG("rbg", pr509Role(sts))

	cl := fake.NewClientBuilder().WithScheme(pr509Scheme()).WithObjects(freshPersisted).Build()
	validator := &deprecatedWorkloadValidator{Client: cl, EnableDeprecatedWorkloadTypes: true}
	_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new uses are not accepted")
}
