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

// Verification harness for PR #572 ledger findings O3 (gang classify) and F4 (stale
// Progressing condition on mixed backends) (Reviewer B / Codex).
// Polarity: CONTRACT tests — they assert the KEP-502 documented classification.

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	gangcommon "sigs.k8s.io/rbgs/pkg/scheduler/common"
)

// F3: classifyGangIncompatibility must map a *configuration* incompatibility — a role
// whose backend cannot participate in subGroupPolicy — to IncompatibleGangConfig.
// KEP-502 reserves SchedulerUnsupported for "an installed scheduler lacking a required
// capability". The role's workload type lacking the instance label is a spec fact.
// The exact message below is produced by buildGangSpec in
// pkg/scheduler/volcano/scheduler.go.
func TestVerifyF3SubGroupPolicyBackendIsNotASchedulerCapability(t *testing.T) {
	err := gangcommon.NewIncompatibleGangConfigError(
		"gang scheduling with subGroupPolicy is not supported for role %q backed by workload type %q: "+
			"the %s pod label is needed to partition the role into per-instance subGroups",
		"decode", "leaderworkersets.x-k8s.io", "roleinstance.workloads.x-k8s.io/name")
	got := classifyGangIncompatibility(err)
	if got != constants.ProgressingReasonIncompatibleGangConfig {
		t.Fatalf("F3 REPRODUCED: subGroupPolicy backend incompatibility classified as %s, want %s (the scheduler capability is fine; the role's backend type is the configuration problem)",
			got, constants.ProgressingReasonIncompatibleGangConfig)
	}
}

// F3-control: a genuine missing scheduler capability keeps SchedulerUnsupported.
// Message from pkg/scheduler/k8s-scheduler-plugin/scheduler.go.
func TestVerifyF3ControlSchedulerCapabilityStaysSchedulerUnsupported(t *testing.T) {
	err := gangcommon.NewIncompatibleGangConfigError(
		"scheduler-plugins does not support per-role minimum gang scheduling (minReplicas); use --scheduler-name=volcano with Volcano >= 1.14")
	if got := classifyGangIncompatibility(err); got != constants.ProgressingReasonSchedulerUnsupported {
		t.Fatalf("control: got %s, want %s", got, constants.ProgressingReasonSchedulerUnsupported)
	}
}

// F4: when an RBG stops qualifying for the new condition model (a role's workload type
// changes away from RoleInstanceSet), KEP-502 requires cleaning up controller-owned
// new-model output ("When an RBG no longer uses RIS for all roles, clean up
// controller-owned new-model output"). updateRBGProgressingCondition must remove the
// previously written Progressing condition instead of leaving a frozen fact.
func TestVerifyF4StaleProgressingConditionRemovedWhenNoLongerAllRIS(t *testing.T) {
	replicas := int32(1)
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "inference", Namespace: "default", Generation: 7},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{
				Name:     "decode",
				Replicas: &replicas,
				Annotations: map[string]string{
					constants.RoleWorkloadTypeAnnotationKey: "leaderworkersets.x-k8s.io",
				},
			}},
		},
		Status: workloadsv1alpha2.RoleBasedGroupStatus{
			Conditions: []metav1.Condition{{
				Type:               string(workloadsv1alpha2.RoleBasedGroupProgressing),
				Status:             metav1.ConditionTrue,
				Reason:             constants.ProgressingReasonRoleInstanceSetUpdated,
				Message:            "decode is updating",
				ObservedGeneration: 6,
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	r := &RoleBasedGroupReconciler{client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	r.updateRBGProgressingCondition(context.Background(), rbg,
		[]workloadsv1alpha2.RoleStatus{{Name: "decode", Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1}}, nil, false, nil)
	for _, condition := range rbg.Status.Conditions {
		if condition.Type == string(workloadsv1alpha2.RoleBasedGroupProgressing) {
			t.Fatalf("F4 REPRODUCED: stale Progressing condition survives on a mixed-backend RBG: %+v", condition)
		}
	}
}
