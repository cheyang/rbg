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

// zz_verify_pr402_fqdn_test.go is a REVIEW VERIFICATION HARNESS for PR #402 finding F3.
// Topic: pr402-port-alloc-doc   Code under review: b412ed6c
// See docs/verification/pr402-port-alloc-doc/README.md for the polarity table.
package componentdiscovery

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// fqdnShape matches <podName>.<svcName>.<namespace>.svc.cluster.local where every
// label is a DNS-1123 label. Deliberately strict: the truncated forms printed in the
// PR #402 doc (which contain "...") cannot match.
var fqdnShape = regexp.MustCompile(
	`^[a-z0-9]([-a-z0-9]*[a-z0-9])?\.[a-z0-9]([-a-z0-9]*[a-z0-9])?\.[a-z0-9]([-a-z0-9]*[a-z0-9])?\.svc\.cluster\.local$`)

// TestVerifyPR402_F3_ComponentAddressFQDNShape_Contract
//
// POLARITY: contract  (green on the code under review; it pins what the doc SHOULD say)
//
// CLAIM (F3, minor): PR #402's doc prints LEADER_ADDR as
//
//	pd-server-prefill-0-...s-pd-server-prefill.default.svc.cluster.local
//
// (zh:597/606/614/616/618, en:600/609/617/619/621). The ellipsis swallows the
// "leader-0" pod-name segment AND the "." that separates pod name from service name,
// so the printed string is not a legal FQDN. Worse, in the router table the three
// rows LEADER_ADDR / WORKER_0_ADDR / WORKER_1_ADDR are truncated to the SAME string -
// the ellipsis ate exactly the part that distinguishes them.
//
// ACTUAL implementation (component_discovery.go:140-141):
//
//	<instanceName>-<component>-<index>.<serviceName>.<namespace>.svc.cluster.local
//
// This is a DOCUMENTATION defect: the implementation is correct. This test therefore
// PASSES on the reviewed code, and its job is to (a) nail down the exact string the
// doc should print, and (b) catch a future regression in the FQDN template.
func TestVerifyPR402_F3_ComponentAddressFQDNShape_Contract(t *testing.T) {
	const (
		instanceName = "cd-demo-prefill-0"
		ns           = "pr402-verify"
		svcName      = "s-cd-demo-prefill"
	)

	cases := []struct {
		name      string
		env       string
		component string
		index     int32
		// The exact value the doc SHOULD print (no ellipsis).
		wantFQDN string
		// The truncated string the doc actually prints, for the observed-vs-expected
		// record. Not asserted as an output - asserted to be an INVALID FQDN.
		docPrints string
	}{
		{
			name:      "leader pod 0",
			env:       "LEADER_ADDR",
			component: "leader",
			index:     0,
			wantFQDN:  "cd-demo-prefill-0-leader-0.s-cd-demo-prefill.pr402-verify.svc.cluster.local",
			docPrints: "pd-server-prefill-0-...s-pd-server-prefill.default.svc.cluster.local",
		},
		{
			name:      "worker pod 0",
			env:       "WORKER_0_ADDR",
			component: "worker",
			index:     0,
			wantFQDN:  "cd-demo-prefill-0-worker-0.s-cd-demo-prefill.pr402-verify.svc.cluster.local",
			docPrints: "pd-server-prefill-0-...s-pd-server-prefill.default.svc.cluster.local",
		},
		{
			name:      "worker pod 1 - must differ from worker pod 0",
			env:       "WORKER_1_ADDR",
			component: "worker",
			index:     1,
			wantFQDN:  "cd-demo-prefill-0-worker-1.s-cd-demo-prefill.pr402-verify.svc.cluster.local",
			docPrints: "pd-server-prefill-0-...s-pd-server-prefill.default.svc.cluster.local",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instance := &workloadsv1alpha2.RoleInstance{
				ObjectMeta: metav1.ObjectMeta{Name: instanceName, Namespace: ns},
				Spec: workloadsv1alpha2.RoleInstanceSpec{
					Components: []workloadsv1alpha2.RoleInstanceComponent{
						{Name: "leader", ServiceName: svcName},
						{Name: "worker", ServiceName: svcName},
					},
				},
			}
			cfg := fmt.Sprintf(
				`{"addressRefs":[{"env":%q,"component":%q,"index":%d}]}`,
				tc.env, tc.component, tc.index)
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        instanceName + "-router-0",
					Namespace:   ns,
					Annotations: map[string]string{ComponentDiscoveryAnnotationKey: cfg},
				},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main"}}},
			}

			require.NoError(t, InjectComponentDiscovery(pod, instance))

			got := ""
			for _, e := range pod.Spec.Containers[0].Env {
				if e.Name == tc.env {
					got = e.Value
				}
			}
			t.Logf("%s: impl=%q  doc-prints=%q", tc.env, got, tc.docPrints)

			// 1. The implementation produces exactly the documented-correct value.
			assert.Equal(t, tc.wantFQDN, got)

			// 2. It is a structurally valid FQDN with DNS-1123 labels.
			assert.Regexp(t, fqdnShape, got, "injected address must be a legal FQDN")
			for _, label := range strings.Split(got, ".") {
				assert.Empty(t, validation.IsDNS1123Label(label),
					"label %q of %q must be a valid DNS-1123 label", label, got)
			}
			assert.LessOrEqual(t, len(got), 253, "FQDN must fit the 253-byte DNS limit")

			// 3. The value the DOC prints is NOT a valid FQDN - this is finding F3.
			assert.NotRegexp(t, fqdnShape, tc.docPrints,
				"the ellipsis-truncated string in the doc is not a legal FQDN")
			assert.NotEqual(t, tc.wantFQDN, tc.docPrints)

			// 4. The annotation is consumed, not left on the live Pod.
			assert.NotContains(t, pod.Annotations, ComponentDiscoveryAnnotationKey)
		})
	}
}

// TestVerifyPR402_F3_TruncatedDocRowsAreIndistinguishable_Contract
//
// POLARITY: contract
//
// The sharper half of F3: the three router env rows the doc prints collapse to the
// same string, whereas the implementation yields three distinct addresses. Asserting
// distinctness is what a reader needs from that table.
func TestVerifyPR402_F3_TruncatedDocRowsAreIndistinguishable_Contract(t *testing.T) {
	const (
		instanceName = "cd-demo-prefill-0"
		ns           = "pr402-verify"
		svcName      = "s-cd-demo-prefill"
	)
	// What the doc prints for LEADER_ADDR / WORKER_0_ADDR / WORKER_1_ADDR:
	docRows := map[string]string{
		"LEADER_ADDR":   "pd-server-prefill-0-...s-pd-server-prefill.default.svc.cluster.local",
		"WORKER_0_ADDR": "pd-server-prefill-0-...s-pd-server-prefill.default.svc.cluster.local",
		"WORKER_1_ADDR": "pd-server-prefill-0-...s-pd-server-prefill.default.svc.cluster.local",
	}
	distinctDocValues := map[string]bool{}
	for _, v := range docRows {
		distinctDocValues[v] = true
	}
	assert.Len(t, distinctDocValues, 1,
		"F3: the doc's three router address rows are byte-identical after truncation")

	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: instanceName, Namespace: ns},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			Components: []workloadsv1alpha2.RoleInstanceComponent{
				{Name: "leader", ServiceName: svcName},
				{Name: "worker", ServiceName: svcName},
			},
		},
	}
	cfg := `{"addressRefs":[` +
		`{"env":"LEADER_ADDR","component":"leader","index":0},` +
		`{"env":"WORKER_0_ADDR","component":"worker","index":0},` +
		`{"env":"WORKER_1_ADDR","component":"worker","index":1}]}`
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        instanceName + "-router-0",
			Namespace:   ns,
			Annotations: map[string]string{ComponentDiscoveryAnnotationKey: cfg},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main"}}},
	}
	require.NoError(t, InjectComponentDiscovery(pod, instance))

	impl := map[string]string{}
	for _, e := range pod.Spec.Containers[0].Env {
		impl[e.Name] = e.Value
	}
	t.Logf("impl values: %v", impl)
	require.Len(t, impl, 3)

	distinctImpl := map[string]bool{}
	for _, v := range impl {
		distinctImpl[v] = true
		assert.Regexp(t, fqdnShape, v)
	}
	assert.Len(t, distinctImpl, 3,
		"the implementation produces three DISTINCT addresses; the doc must print them in full")
}

// TestVerifyPR402_F3_PortRefResolvesBothScopes_Contract
//
// POLARITY: contract
//
// Cross-reference for F1: component-discovery's resolvePortRef DOES try both the
// PodScoped and the RoleScoped annotation key, and its error text names both. This is
// the behaviour pkg/port-allocator's `references` path lacks (finding F1).
func TestVerifyPR402_F3_PortRefResolvesBothScopes_Contract(t *testing.T) {
	const (
		instanceName = "cd-demo-prefill-0"
		ns           = "pr402-verify"
	)
	mk := func(ann map[string]string) *workloadsv1alpha2.RoleInstance {
		return &workloadsv1alpha2.RoleInstance{
			ObjectMeta: metav1.ObjectMeta{Name: instanceName, Namespace: ns, Annotations: ann},
			Spec: workloadsv1alpha2.RoleInstanceSpec{
				Components: []workloadsv1alpha2.RoleInstanceComponent{
					{Name: "leader", ServiceName: "s-cd-demo-prefill"},
				},
			},
		}
	}
	ref := ComponentPortRef{Env: "LEADER_GRPC_PORT", Component: "leader", PortName: "leader-grpc", Index: 0}

	cases := []struct {
		name    string
		ann     map[string]string
		want    string
		wantErr bool
	}{
		{
			name: "PodScoped key resolves",
			ann:  map[string]string{"cd-demo-prefill-0-leader-0.leader-grpc": "30111"},
			want: "30111",
		},
		{
			name: "RoleScoped key ALSO resolves (unlike pkg/port-allocator references - F1)",
			ann:  map[string]string{"leader.leader-grpc": "30222"},
			want: "30222",
		},
		{
			name:    "neither key present -> error naming BOTH tried keys",
			ann:     map[string]string{"unrelated": "1"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePortRef(ref, mk(tc.ann))
			t.Logf("annotations=%v -> value=%q err=%v", tc.ann, got, err)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "tried keys")
				assert.Contains(t, err.Error(), "cd-demo-prefill-0-leader-0.leader-grpc")
				assert.Contains(t, err.Error(), "leader.leader-grpc")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
