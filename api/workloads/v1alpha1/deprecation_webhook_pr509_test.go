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

// PR #509 verification harness (F2), unit layer.
//
// Claim under test (contract polarity): when the admission request's oldObj
// already carries a grandfathered deprecated workload role, an update that
// keeps that role must be admitted in compatibility mode, regardless of the
// outcome of the validator's supplementary client.Get for the persisted object.
//
// The production wiring hands deprecatedWorkloadValidator a controller-runtime
// manager client, which is backed by an eventually-consistent informer cache.
// Two failure shapes are proven here:
//
//  1. read failure  - a non-NotFound Get error rejects the write outright
//     (fail-closed amplification) even though oldObj was sufficient.
//  2. stale read    - a cache object older than the request's oldObj silently
//     replaces the authoritative oldRoles, wrongly rejecting a grandfathered
//     update (or wrongly exempting a new use).
//
// Both tests FAIL on the PR head (red = reproduction). With a fix that falls
// back to oldObj on read failure/staleness instead of failing the write, they
// go green.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"sigs.k8s.io/rbgs/api/workloads/constants"
)

// pr509RBG builds a v1alpha1 RoleBasedGroup with one role of the given workload.
func pr509RBG(name, ns, apiVersion, kind string) *RoleBasedGroup {
	return &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: RoleBasedGroupSpec{
			Roles: []RoleSpec{
				{
					Name:     "worker",
					Replicas: ptrToInt32(1),
					Workload: WorkloadSpec{APIVersion: apiVersion, Kind: kind},
				},
			},
		},
	}
}

func ptrToInt32(i int32) *int32 { return &i }

// fakeClientWithGet builds a fake client seeded with seed (if non-nil) and an
// optional Get interceptor, standing in for the manager's cached client.
func fakeClientWithGet(
	t *testing.T,
	getFunc func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error,
	seed client.Object,
) client.Client {
	t.Helper()
	sch := runtime.NewScheme()
	if err := AddToScheme(sch); err != nil {
		t.Fatal(err)
	}
	builder := fake.NewClientBuilder().WithScheme(sch)
	if seed != nil {
		builder = builder.WithObjects(seed)
	}
	if getFunc != nil {
		builder = builder.WithInterceptorFuncs(interceptor.Funcs{Get: getFunc})
	}
	return builder.Build()
}

// TestPR509F2ReadFailureRejectsGrandfatheredUpdate is a CONTRACT test:
// a cached-client read failure must not fail an otherwise-admitted update.
func TestPR509F2ReadFailureRejectsGrandfatheredUpdate(t *testing.T) {
	oldObj := pr509RBG("legacy", "ns", "apps/v1", "StatefulSet")
	newObj := oldObj.DeepCopy()
	newObj.Labels = map[string]string{"touch": "yes"} // unrelated field change

	c := fakeClientWithGet(t, func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		return apierrors.NewServerTimeout(schema.GroupResource{Group: "workloads.x-k8s.io", Resource: "rolebasedgroups"}, "get", 1)
	}, oldObj)

	v := &deprecatedWorkloadValidator{
		Client:                        c,
		EnableDeprecatedWorkloadTypes: true,
	}
	warnings, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	assert.Len(t, warnings, 1, "the deprecation warning must still be emitted")
	assert.NoError(t, err, "oldObj carries the grandfathered role, so the update must be admitted even when the persisted-object read fails; got rejection: %v", err)
}

// TestPR509F2StaleCacheOverridesRequestOldObj is a CONTRACT test:
// a stale cached object must not silently replace the request's oldObj.
func TestPR509F2StaleCacheOverridesRequestOldObj(t *testing.T) {
	// oldObj per the admission request: the role is grandfathered on StatefulSet.
	oldObj := pr509RBG("legacy", "ns", "apps/v1", "StatefulSet")
	newObj := oldObj.DeepCopy()
	newObj.Labels = map[string]string{"touch": "yes"}

	// The informer cache lags: it still shows the role as RoleInstanceSet
	// (persisted an instant ago, before the object was written back with the
	// legacy annotation spelling v1alpha1 objects carry).
	staleView := pr509RBG("legacy", "ns", "workloads.x-k8s.io/v1alpha2", "RoleInstanceSet")

	c := fakeClientWithGet(t, nil, staleView)

	v := &deprecatedWorkloadValidator{
		Client:                        c,
		EnableDeprecatedWorkloadTypes: true,
	}
	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	assert.NoError(t, err, "the request's oldObj is authoritative and grants the exemption; the stale cache copy must not override it; got rejection: %v", err)
}

// TestPR509GateCreateNewDeprecatedUseRejected is the positive control for the
// gate itself: a create (no old roles) with a deprecated workload type is
// rejected in compatibility mode. This is intended behavior (contract, green
// on the PR head); it also proves these fake-client tests reach the validator.
func TestPR509GateCreateNewDeprecatedUseRejected(t *testing.T) {
	obj := pr509RBG("fresh", "ns", "apps/v1", "StatefulSet")
	v := &deprecatedWorkloadValidator{
		Client:                        nil, // create path never reads the client
		EnableDeprecatedWorkloadTypes: true,
	}
	warnings, err := v.ValidateCreate(context.Background(), obj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new uses are not accepted")
	assert.Contains(t, err.Error(), constants.StatefulSetWorkloadType)
	assert.Len(t, warnings, 1)

	// And the grandfathered update without any client: admitted.
	oldObj := pr509RBG("legacy", "ns", "apps/v1", "StatefulSet")
	newObj := oldObj.DeepCopy()
	_, err = v.ValidateUpdate(context.Background(), oldObj, newObj)
	assert.NoError(t, err, "without the supplementary read the exemption works")
}
