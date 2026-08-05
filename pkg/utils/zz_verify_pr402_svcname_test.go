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

// zz_verify_pr402_svcname_test.go is a REVIEW VERIFICATION HARNESS for PR #402
// finding F5.
// Topic: pr402-port-alloc-doc   Code under review: b412ed6c
// See docs/verification/pr402-port-alloc-doc/README.md for the polarity table.
package utils

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func vfxPR402Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, workloadsv1alpha2.AddToScheme(s))
	return s
}

func vfxPR402RBGRole(rbgName, roleName, ns string) (*workloadsv1alpha2.RoleBasedGroup, *workloadsv1alpha2.RoleSpec) {
	one := int32(1)
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: rbgName, Namespace: ns},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{Name: roleName, Replicas: &one}},
		},
	}
	return rbg, &rbg.Spec.Roles[0]
}

// TestVerifyPR402_F5_HeadlessServiceNameHasLegacyFallback_Contract
//
// POLARITY: contract  (green on the code under review; it pins what the doc SHOULD say)
//
// CLAIM (F5, minor): PR #402's doc states the naming rule unconditionally
// (zh:32-38 / en:33-39): "the naming rule is s-{rbgName}-{roleName}", with the example
// pd-inference/prefill -> s-pd-inference-prefill.
//
// ACTUALLY GetCompatibleHeadlessServiceName (pkg/utils/service_utils.go:30-45) is
// backward-compatibility-aware: it first Gets a Service named with the LEGACY scheme
// {rbgName}-{roleName} (no s- prefix) and, IF THAT SERVICE ALREADY EXISTS, keeps the
// legacy name. Only when the legacy Service is absent does it return the new
// s--prefixed name. So on a cluster upgraded from an older RBG version, the actual
// Service name is {rbgName}-{roleName} and the doc's rule does not hold.
//
// This is a DOCUMENTATION defect (the compat branch is intentional); the test PASSES
// on the reviewed code and pins both branches so a future removal of the compat path
// is visible.
func TestVerifyPR402_F5_HeadlessServiceNameHasLegacyFallback_Contract(t *testing.T) {
	const ns = "default"
	scheme := vfxPR402Scheme(t)

	cases := []struct {
		name string
		// pre-existing Services in the cluster before the call
		preexisting []string
		rbgName     string
		roleName    string
		// what the implementation returns
		want string
		// what the doc's unconditional rule predicts
		docPredicts string
	}{
		{
			name:        "no legacy Service -> new s- prefixed name (doc's rule holds)",
			preexisting: nil,
			rbgName:     "pd-inference",
			roleName:    "prefill",
			want:        "s-pd-inference-prefill",
			docPredicts: "s-pd-inference-prefill",
		},
		{
			name:        "legacy Service EXISTS -> legacy name is kept (doc's rule does NOT hold)",
			preexisting: []string{"pd-inference-prefill"},
			rbgName:     "pd-inference",
			roleName:    "prefill",
			want:        "pd-inference-prefill",
			docPredicts: "s-pd-inference-prefill",
		},
		{
			name: "an unrelated s- Service existing does not change the outcome",
			// GetCompatibleHeadlessServiceName only probes the LEGACY name.
			preexisting: []string{"s-pd-inference-prefill"},
			rbgName:     "pd-inference",
			roleName:    "prefill",
			want:        "s-pd-inference-prefill",
			docPredicts: "s-pd-inference-prefill",
		},
		{
			name:        "legacy Service for a DIFFERENT role does not leak across roles",
			preexisting: []string{"pd-inference-decode"},
			rbgName:     "pd-inference",
			roleName:    "prefill",
			want:        "s-pd-inference-prefill",
			docPredicts: "s-pd-inference-prefill",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objs := make([]client.Object, 0, len(tc.preexisting))
			for _, n := range tc.preexisting {
				objs = append(objs, &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: n, Namespace: ns},
					Spec:       corev1.ServiceSpec{ClusterIP: corev1.ClusterIPNone},
				})
			}
			kclient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

			rbg, role := vfxPR402RBGRole(tc.rbgName, tc.roleName, ns)
			got, err := GetCompatibleHeadlessServiceName(context.TODO(), kclient, rbg, role)
			require.NoError(t, err)

			t.Logf("preexisting=%v -> impl=%q  doc-predicts=%q  legacyName=%q newName=%q",
				tc.preexisting, got, tc.docPredicts,
				rbg.GetWorkloadName(role), rbg.GetServiceName(role))

			assert.Equal(t, tc.want, got)
			assert.Empty(t, validation.IsDNS1035Label(got),
				"a Service name must be a valid DNS-1035 label")

			if tc.want != tc.docPredicts {
				assert.NotEqual(t, tc.docPredicts, got,
					"F5: the doc states s-{rbgName}-{roleName} unconditionally, but the "+
						"legacy-compat branch returns %q", tc.want)
			}
		})
	}
}

// TestVerifyPR402_F5_ServiceNameTruncatedAt63_Contract
//
// POLARITY: contract
//
// The second half of F5: GetServiceName / GetWorkloadName
// (api/workloads/v1alpha2/helper.go:106-116) truncate to 63 characters and then strip
// trailing hyphens, so for long rbg/role names the actual Service name is NOT
// s-{rbgName}-{roleName}. The doc states the rule with no length caveat.
func TestVerifyPR402_F5_ServiceNameTruncatedAt63_Contract(t *testing.T) {
	cases := []struct {
		name     string
		rbgName  string
		roleName string
		// expected properties rather than a magic string
		wantTruncated  bool
		wantTrimmedDash bool
	}{
		{
			name:     "short names are not truncated",
			rbgName:  "pd-inference",
			roleName: "prefill",
		},
		{
			// s- + 58 chars + - + 7 = 68 > 63
			name:          "long rbg name is truncated to 63",
			rbgName:       strings.Repeat("a", 58),
			roleName:      "prefill",
			wantTruncated: true,
		},
		{
			// Craft the case where char 63 lands on the joining hyphen so the trailing
			// dash has to be trimmed: "s-" (2) + rbg (60) = 62, then "-" at index 62
			// => truncation to 63 keeps the trailing "-", which is then stripped to 62.
			name:            "truncation landing on a hyphen strips it (DNS-1035 validity)",
			rbgName:         strings.Repeat("b", 60),
			roleName:        "worker",
			wantTruncated:   true,
			wantTrimmedDash: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rbg, role := vfxPR402RBGRole(tc.rbgName, tc.roleName, "default")
			svcName := rbg.GetServiceName(role)
			wlName := rbg.GetWorkloadName(role)
			naive := fmt.Sprintf("s-%s-%s", tc.rbgName, tc.roleName)

			t.Logf("naive=%q (len=%d)  GetServiceName=%q (len=%d)  GetWorkloadName=%q (len=%d)",
				naive, len(naive), svcName, len(svcName), wlName, len(wlName))

			assert.LessOrEqual(t, len(svcName), 63,
				"Service name must fit the 63-char DNS label limit")
			assert.Empty(t, validation.IsDNS1035Label(svcName),
				"truncated Service name must remain a valid DNS-1035 label")
			assert.False(t, strings.HasSuffix(svcName, "-"),
				"trailing hyphens must be stripped after truncation")

			if tc.wantTruncated {
				assert.NotEqual(t, naive, svcName,
					"F5: the doc's rule s-{rbgName}-{roleName} yields %q (len=%d) but the "+
						"implementation truncates to %q (len=%d)",
					naive, len(naive), svcName, len(svcName))
				assert.True(t, strings.HasPrefix(naive, svcName),
					"the truncated name must be a prefix of the naive name")
			} else {
				assert.Equal(t, naive, svcName)
			}
			if tc.wantTrimmedDash {
				assert.Less(t, len(svcName), 63,
					"a name truncated onto a hyphen ends up SHORTER than 63 after trimming")
			}
		})
	}
}
