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

// pr418_instance_layer_test.go is a reviewer-private verification harness for
// https://github.com/sgl-project/rbg/pull/418 finding F3. It is NOT part of the PR.
//
// PR#418 claims that switching sharedServiceSelection "triggers a rolling replacement of the
// role instances". That outcome depends on TWO independent layers agreeing, and only one of
// them knows about serviceName:
//
//	RoleInstanceSet level (this package)      canNotInPlaceUpdate -> compares component SIZES only
//	RoleInstance/pod level (pkg/reconciler/…) isComponentExtensionSpecChanged -> compares serviceName
//
// This test pins down the first layer so the division of responsibility is recorded rather
// than assumed. It is the reason F3's missing All -> LeaderOnly coverage is worth more than a
// symmetry nit: the replacement guarantee rests entirely on the pod-level check.
package inplaceupdate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestVerifyPR418_F3_InstanceLayerIgnoresServiceName is a [CANARY].
//
// It records that the RoleInstanceSet-level in-place decision does NOT consider a component
// serviceName change: two templates that differ only in the worker's serviceName are judged
// in-place updatable. The pod-level check is the only thing that turns the policy switch into
// a pod replacement.
func TestVerifyPR418_F3_InstanceLayerIgnoresServiceName(t *testing.T) {
	components := func(workerSvc string) []workloadsv1alpha2.RoleInstanceComponent {
		return []workloadsv1alpha2.RoleInstanceComponent{
			{Name: "leader", Size: ptr.To(int32(1)), ServiceName: "s-rbg-role"},
			{Name: "worker", Size: ptr.To(int32(1)), ServiceName: workerSvc},
		}
	}

	oldTemplate := &workloadsv1alpha2.RoleInstanceTemplate{
		RoleInstanceSpec: workloadsv1alpha2.RoleInstanceSpec{Components: components("")},
	}
	newTemplate := &workloadsv1alpha2.RoleInstanceTemplate{
		RoleInstanceSpec: workloadsv1alpha2.RoleInstanceSpec{Components: components("s-rbg-role")},
	}

	blocked := canNotInPlaceUpdate(newTemplate, oldTemplate)
	t.Logf("F3 RoleInstanceSet layer: worker serviceName \"\" -> \"s-rbg-role\", "+
		"canNotInPlaceUpdate=%v", blocked)

	assert.False(t, blocked,
		"F3 canary: the RoleInstanceSet-level check only compares component sizes, so it does "+
			"not by itself force a replacement when serviceName changes")

	// Control: a size change IS caught at this layer, proving the function is reachable and
	// the False above is a real negative rather than a broken call.
	sizeChanged := canNotInPlaceUpdate(
		&workloadsv1alpha2.RoleInstanceTemplate{
			RoleInstanceSpec: workloadsv1alpha2.RoleInstanceSpec{
				Components: []workloadsv1alpha2.RoleInstanceComponent{
					{Name: "leader", Size: ptr.To(int32(1))},
					{Name: "worker", Size: ptr.To(int32(3))},
				},
			},
		},
		oldTemplate,
	)
	t.Logf("F3 control: worker size 1 -> 3, canNotInPlaceUpdate=%v", sizeChanged)
	assert.True(t, sizeChanged, "F3 control: a component size change must block in-place update")
}
