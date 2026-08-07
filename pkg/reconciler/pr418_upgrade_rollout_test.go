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

// pr418_upgrade_rollout_test.go is a reviewer-private verification harness for
// https://github.com/sgl-project/rbg/pull/418 finding F5. It is NOT part of the PR.
//
// F5: a role that ALREADY sets sharedServiceSelection=All gains a worker-component
// serviceName as a result of this PR. That rewrites the RoleInstanceSet template, which
// produces a new ControllerRevision, and a serviceName change is explicitly classified as
// not-in-place-updatable. So merely upgrading the controller replaces the worker pods of every
// existing All role, with no spec change by the user.
//
// This half records the template side of the claim; the recreate decision is covered by
// pkg/reconciler/roleinstance/inplaceupdate/pr418_recreate_test.go.
package reconciler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// TestVerifyPR418_F5_WorkerServiceNameUnderAll is the [CANARY] for F5.
//
// Run it on the PR head and the worker component carries the shared service name; run the very
// same test on the base commit and the field is empty. That difference IS the upgrade delta:
// the stored RoleInstanceSet template changes underneath an unmodified RBG.
//
// It PASSES on the code under review. It is a canary because binding the worker under All is
// the intended fix -- what needs review is the undocumented rollout it causes on upgrade, not
// the binding itself.
func TestVerifyPR418_F5_WorkerServiceNameUnderAll(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	role := wrappersv2.BuildLeaderWorkerRole("pr418-all-role").Obj()
	role.LeaderWorkerPattern.SharedServiceSelection = ptr.To(
		workloadsv1alpha2.SharedServiceSelectionAll,
	)

	rbg := wrappersv2.BuildBasicRoleBasedGroup("pr418-all-rbg", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()

	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	require.NoError(t, NewRoleInstanceSetReconciler(scheme, cl).
		Reconciler(context.Background(), rbg, &role, nil, "pr418-revision"))

	ris := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{
		Name: rbg.GetWorkloadName(&role), Namespace: rbg.Namespace,
	}, ris))

	serviceNames := map[string]string{}
	for _, component := range ris.Spec.RoleInstanceTemplate.Components {
		serviceNames[component.Name] = component.ServiceName
	}
	t.Logf("F5 component serviceName under All: %v", serviceNames)

	assert.Equal(t, rbg.GetServiceName(&role),
		serviceNames[string(constants.WorkerComponentType)],
		"F5 canary: the worker component is now bound to the shared service under All. On the "+
			"base commit this is empty, so upgrading the controller rewrites the "+
			"RoleInstanceSet template of every existing All role")
}
