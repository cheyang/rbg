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

package volcano

import (
	"context"
	"strings"
	"sync"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	coreapplyv1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scheduler/common"
	"sigs.k8s.io/rbgs/pkg/utils"
	volcanoschedulingv1beta1 "volcano.sh/apis/pkg/apis/scheduling/v1beta1"

	"github.com/stretchr/testify/require"
)

func TestNetworkTopologyForGroupRequiredAndPreferred(t *testing.T) {
	group := &common.PlacementGroup{
		Topology: &workloadsv1alpha2.TopologyConstraint{
			Pack: &workloadsv1alpha2.TopologyPackConstraint{
				Required:  ptr.To("block"),
				Preferred: ptr.To("rack"),
			},
		},
	}
	got, absorbed := networkTopologyForGroup(group)
	if got == nil || got.Mode != volcanoschedulingv1beta1.HardNetworkTopologyMode || got.HighestTierName != "block" {
		t.Fatalf("unexpected topology: %#v", got)
	}
	if !absorbed {
		t.Fatal("expected preferred level to be reported as absorbed")
	}
}

func TestNetworkTopologyForGroupPreferredOnly(t *testing.T) {
	group := &common.PlacementGroup{
		Topology: &workloadsv1alpha2.TopologyConstraint{
			Pack: &workloadsv1alpha2.TopologyPackConstraint{Preferred: ptr.To("rack")},
		},
	}
	got, absorbed := networkTopologyForGroup(group)
	if got == nil || got.Mode != volcanoschedulingv1beta1.SoftNetworkTopologyMode {
		t.Fatalf("unexpected topology: %#v", got)
	}
	if !absorbed {
		t.Fatal("expected preferred level to be reported as absorbed")
	}
}

func TestInjectPlacementSchedulingFieldsUsesPlanMembership(t *testing.T) {
	rbg := rbgWithRoles(standaloneRole("prefill", 2, ""), standaloneRole("router", 1, ""))
	plan := &common.PlacementPlan{
		Root: &common.PlacementGroup{
			ID: "prefill",
			Scope: common.PlacementScope{
				Roles:       []string{"prefill"},
				PartitionBy: common.PartitionByNone,
			},
		},
	}
	pts := &coreapplyv1.PodTemplateSpecApplyConfiguration{}
	New(nil).InjectPlacementSchedulingFields(rbg, &rbg.Spec.Roles[0], plan, pts)

	if pts.Spec == nil || pts.Spec.SchedulerName == nil || *pts.Spec.SchedulerName != SchedulerName {
		t.Fatalf("expected schedulerName %q, got %#v", SchedulerName, pts.Spec)
	}
	if pts.Annotations == nil || pts.Annotations[AnnotationKey] == "" {
		t.Fatalf("expected PodGroup annotation, got %#v", pts.Annotations)
	}
}

func TestInjectPlacementSchedulingFieldsRoleOutsidePlanGetsNoMembership(t *testing.T) {
	rbg := rbgWithRoles(standaloneRole("prefill", 2, ""), standaloneRole("router", 1, ""))
	plan := &common.PlacementPlan{
		Root: &common.PlacementGroup{
			ID: "prefill",
			Scope: common.PlacementScope{
				Roles:       []string{"prefill"},
				PartitionBy: common.PartitionByNone,
			},
		},
	}
	pts := &coreapplyv1.PodTemplateSpecApplyConfiguration{}
	New(nil).InjectPlacementSchedulingFields(rbg, &rbg.Spec.Roles[1], plan, pts)

	if pts.ObjectMetaApplyConfiguration != nil && pts.Annotations[AnnotationKey] != "" {
		t.Fatalf("expected no PodGroup annotation, got %#v", pts.Annotations)
	}
}

func TestMergeSubGroupPolicies(t *testing.T) {
	gang := []volcanoschedulingv1beta1.SubGroupPolicySpec{{
		Name:         "prefill",
		MinSubGroups: ptr.To(int32(2)),
	}}
	topology := []volcanoschedulingv1beta1.SubGroupPolicySpec{{
		Name:            "prefill",
		SubGroupSize:    ptr.To(int32(4)),
		NetworkTopology: &volcanoschedulingv1beta1.NetworkTopologySpec{HighestTierName: "rack"},
	}}
	got := mergeSubGroupPolicies(gang, topology)
	if len(got) != 1 {
		t.Fatalf("expected one policy, got %d", len(got))
	}
	if ptr.Deref(got[0].MinSubGroups, 0) != 2 || ptr.Deref(got[0].SubGroupSize, 0) != 4 {
		t.Fatalf("unexpected merged policy: %#v", got[0])
	}
	if got[0].NetworkTopology == nil || got[0].NetworkTopology.HighestTierName != "rack" {
		t.Fatalf("expected topology on merged policy: %#v", got[0])
	}
}

func TestReconcilePlacementNoTopologyDeletesStaleTopologyPodGroups(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, volcanoschedulingv1beta1.AddToScheme(scheme))
	rbg := rbgWithRoles(standaloneRole("prefill", 2, ""), standaloneRole("decode", 2, ""))
	rbg.UID = "rbg-uid"

	controllerRef := *metav1.NewControllerRef(rbg, utils.GetRbgGVK())
	stale := &volcanoschedulingv1beta1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:            rbg.Name + "-prefill",
			Namespace:       rbg.Namespace,
			OwnerReferences: []metav1.OwnerReference{controllerRef},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build()

	watched := sync.Map{}
	watched.Store(CrdName, struct{}{})
	plan := &common.PlacementPlan{
		Root: &common.PlacementGroup{
			ID:    rbg.Name,
			Scope: common.PlacementScope{Roles: []string{"prefill", "decode"}},
			Gang:  &common.GangStrategy{Roles: sets.New("prefill", "decode")},
		},
	}
	_, err := New(c).ReconcilePlacement(
		context.Background(),
		rbg,
		plan,
		&builder.TypedBuilder[reconcile.Request]{},
		&watched,
		c,
	)
	require.NoError(t, err)

	getErr := c.Get(context.Background(), client.ObjectKey{Name: stale.Name, Namespace: stale.Namespace}, stale)
	if !apierrors.IsNotFound(getErr) {
		t.Fatalf("expected stale topology PodGroup to be deleted, got %v", getErr)
	}
}

func TestBoundedPlacementPodGroupName(t *testing.T) {
	longName := strings.Repeat("a", 100)
	suffix := strings.Repeat("b", 32)
	got := boundedPlacementPodGroupName(longName, suffix)
	if len(got) != 63 {
		t.Fatalf("expected bounded name length 63, got %d", len(got))
	}
	if got != longName[:30]+"-"+suffix {
		t.Fatalf("unexpected bounded name %q", got)
	}
}

func TestValidateTopologyLevelOrderDoesNotCompareRequiredAndPreferred(t *testing.T) {
	levels := map[string]int{"block": 2, "rack": 1}
	constraint := &workloadsv1alpha2.TopologyConstraint{
		Pack: &workloadsv1alpha2.TopologyPackConstraint{
			Required:  ptr.To("block"),
			Preferred: ptr.To("rack"),
		},
	}
	if err := validateTopologyLevelOrder(constraint, levels); err != nil {
		t.Fatalf("expected required=block/preferred=rack to be accepted, got %v", err)
	}
}

func TestLoadTopologyLevelsReportsMissingHyperNodeCRD(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, apiextensionsv1.AddToScheme(testScheme))
	c := fake.NewClientBuilder().WithScheme(testScheme).Build()

	_, err := New(c).loadTopologyLevels(context.Background(), c)
	require.Error(t, err)
	require.True(t, common.IsSchedulerUnsupported(err), "expected SchedulerUnsupportedError, got %v", err)
	require.Contains(t, err.Error(), HyperNodeCrdName)
}

func TestBuildTopologySubGroupsRejectsZeroSizeRole(t *testing.T) {
	role := standaloneRole("prefill", 1, constants.RoleInstanceSetWorkloadType)
	role.Pattern = workloadsv1alpha2.Pattern{
		CustomComponentsPattern: &workloadsv1alpha2.CustomComponentsPattern{
			Components: []workloadsv1alpha2.InstanceComponent{{Name: "engine", Size: ptr.To(int32(0))}},
		},
	}
	rbg := rbgWithRoles(role)
	group := &common.PlacementGroup{
		Scope: common.PlacementScope{
			Roles:       []string{"prefill"},
			PartitionBy: common.PartitionByRoleInstance,
		},
		Topology: &workloadsv1alpha2.TopologyConstraint{
			Pack: &workloadsv1alpha2.TopologyPackConstraint{Required: ptr.To("rack")},
		},
	}

	_, _, err := buildTopologySubGroups(rbg, group)
	require.Error(t, err)
	require.True(t, common.IsTopologyTranslationError(err), "expected TopologyTranslationError, got %v", err)
	require.Contains(t, err.Error(), "subgroup size must be at least 1")
}
