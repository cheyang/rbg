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

// zz_verify_pr402_doc_claims_test.go is a REVIEW VERIFICATION HARNESS, not part of the
// product test suite. It pins the real behaviour of pkg/port-allocator against the
// assertions made by the documentation added in PR #402
// (doc/best-practice/{zh,en}/05-port-allocation-and-service-discovery.md).
//
// Topic: pr402-port-alloc-doc   Code under review: b412ed6c
// See docs/verification/pr402-port-alloc-doc/README.md for the polarity table.
package port_allocator

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// ---------------------------------------------------------------------------
// shared fixtures
// ---------------------------------------------------------------------------

// vfxInstance builds the RoleInstance shape used by the doc's worked example:
// role "prefill", CustomComponentsPattern with components leader + worker.
// instanceName mirrors "<rbg>-<role>-<idx>" e.g. "pd-server-prefill-0".
func vfxInstance(instanceName, ns string, annotations map[string]string) *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:        instanceName,
			Namespace:   ns,
			Annotations: annotations,
			Labels: map[string]string{
				constants.RoleTypeLabelKey: string(constants.ComponentsTemplateType),
			},
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			Components: []workloadsv1alpha2.RoleInstanceComponent{
				{Name: "leader", ServiceName: "s-" + instanceName},
				{Name: "worker", ServiceName: "s-" + instanceName},
			},
		},
	}
}

func vfxPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: map[string]string{}},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "main", Image: "busybox"}},
		},
	}
}

func vfxEnvOf(pod *corev1.Pod, name string) (string, bool) {
	for _, e := range pod.Spec.Containers[0].Env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// F1 - references can only resolve PodScoped ports
// ---------------------------------------------------------------------------

// TestVerifyPR402_F1_ReferencesOnlyResolvePodScoped_Canary
//
// POLARITY: canary  (asserts CURRENT behaviour, which the PR #402 doc contradicts)
//
// CLAIM (F1, major): When component B declares
//
//	references: [{env: LEADER_GRPC_PORT, from: "prefill.leader.leader-grpc"}]
//
// and the referenced port "leader-grpc" on component "leader" was allocated with
// scope RoleScoped, the doc (zh:313-344 / en:316-347 - "references can obtain
// already-allocated ports of other components in the same role", with no scope
// constraint in the parameter table, while the same doc's full example allocates
// leader-grpc as RoleScoped) implies the reference resolves. ACTUALLY
// InjectPortsIntoPod only builds the PodScoped key
// FormatPodScopedPortKey(refPodName, portName) (manager.go:125-143) with no RoleScoped
// fallback, so it returns
//
//	referenced port not found: prefill.leader.leader-grpc (key: <pod>.leader-grpc)
//
// which bubbles out of instance_scale.go:275-277 and blocks Pod creation for the
// whole component.
//
// Contrast: pkg/component-discovery/component_discovery.go resolvePortRef() DOES try
// both key shapes - the two features disagree.
//
// AFTER A FIX (adding a RoleScoped fallback in manager.go) THIS CANARY FLIPS TO RED.
// That is the expected signal; at that point invert the assertion (the roleScoped case
// should then succeed and inject the port) or promote it to a contract test. If the
// maintainers instead fix the DOC (documenting that references are PodScoped-only),
// this canary stays green and becomes the contract.
func TestVerifyPR402_F1_ReferencesOnlyResolvePodScoped_Canary(t *testing.T) {
	const (
		instanceName = "pd-server-prefill-0"
		ns           = "default"
		refFrom      = "prefill.leader.leader-grpc"
		portName     = "leader-grpc"
		refEnv       = "LEADER_GRPC_PORT"
		portValue    = "30111"
	)
	// The pod name the reference resolves to: component "leader", index 0.
	leaderPod := fmt.Sprintf("%s-leader-0", instanceName)

	cases := []struct {
		name string
		// how the target port was stored on the RoleInstance annotation
		annotationKey string
		scopeUsed     PortScope
		// what the PR #402 doc leads a reader to expect
		docSaysResolves bool
		// what the implementation actually does
		wantErr         bool
		wantErrContains string
		wantEnv         string
	}{
		{
			name:            "target port allocated PodScoped -> resolves (doc and impl agree)",
			annotationKey:   FormatPodScopedPortKey(leaderPod, portName),
			scopeUsed:       PodScoped,
			docSaysResolves: true,
			wantErr:         false,
			wantEnv:         portValue,
		},
		{
			// This is the case the doc's own worked example produces.
			name:            "target port allocated RoleScoped -> hard failure (doc implies it resolves)",
			annotationKey:   FormatRoleScopedPortKey("leader", portName),
			scopeUsed:       RoleScoped,
			docSaysResolves: true,
			wantErr:         true,
			wantErrContains: "referenced port not found: " + refFrom,
			wantEnv:         "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instance := vfxInstance(instanceName, ns, map[string]string{
				tc.annotationKey: portValue,
			})
			// The referencing component ("worker") declares only a reference.
			cfg := &PortAllocatorConfig{
				References: []PortReference{{Env: refEnv, From: refFrom}},
			}
			pod := vfxPod(fmt.Sprintf("%s-worker-0", instanceName))

			err := InjectPortsIntoPod(pod, instance, cfg, "worker")

			got, present := vfxEnvOf(pod, refEnv)
			t.Logf("scope=%s annotationKey=%q err=%v injected %s=%q",
				tc.scopeUsed, tc.annotationKey, err, refEnv, got)

			if tc.wantErr {
				require.Error(t, err,
					"doc (zh:313-344) implies references resolve regardless of scope; "+
						"expected the CURRENT implementation to fail for %s", tc.scopeUsed)
				assert.Contains(t, err.Error(), tc.wantErrContains)
				// Also pin the exact key the implementation looked for: PodScoped only.
				assert.Contains(t, err.Error(),
					"key: "+FormatPodScopedPortKey(leaderPod, portName),
					"implementation must have tried ONLY the PodScoped key shape")
				// The error mentions exactly ONE lookup key. A RoleScoped fallback would
				// have to report a second key (as component-discovery's resolvePortRef
				// does: `tried keys: %q, %q`). Exactly one "key:" occurrence == no fallback.
				assert.Equal(t, 1, strings.Count(err.Error(), "key: "),
					"a RoleScoped fallback would report a second lookup key; "+
						"only the PodScoped key is reported - that is finding F1")
				assert.NotContains(t, err.Error(), "tried keys",
					"component-discovery reports both key shapes; port-allocator does not")
				assert.False(t, present, "no env var should be injected on failure")
			} else {
				require.NoError(t, err)
				assert.True(t, present)
				assert.Equal(t, tc.wantEnv, got)
			}
		})
	}
}

// TestVerifyPR402_F1_PortAllocatorHasNoRoleScopedFallback_Contract
//
// POLARITY: contract
//
// Supporting evidence for F1: the two scopes use structurally different annotation
// keys, so looking up one can never find the other. This is why the missing fallback
// in manager.go is a real defect rather than a naming coincidence. Green now and
// after any fix.
func TestVerifyPR402_F1_PortAllocatorHasNoRoleScopedFallback_Contract(t *testing.T) {
	assert.Equal(t, "pod-0.grpc", FormatPodScopedPortKey("pod-0", "grpc"))
	assert.Equal(t, "leader.grpc", FormatRoleScopedPortKey("leader", "grpc"))
	assert.NotEqual(t,
		FormatPodScopedPortKey("inst-leader-0", "grpc"),
		FormatRoleScopedPortKey("leader", "grpc"),
		"the two scopes use different annotation keys, so resolving one does not find the other")
}

// ---------------------------------------------------------------------------
// F2 - random allocation is not unique across calls
// ---------------------------------------------------------------------------

// TestVerifyPR402_F2_RandomAllocatorNotUniqueAcrossCalls_Canary
//
// POLARITY: canary  (asserts CURRENT behaviour)
//
// CLAIM (F2, major): PR #402's doc (zh:222-245 / en:225-248 ASCII diagram
// "solution: RBG dynamically allocates a different port for each Pod", and zh:278
// "PodScoped = each Pod gets its own port value") presents dynamic allocation as THE
// fix for hostNetwork port collisions. ACTUALLY RandomAllocator's own doc comment
// (random.go:28-31) states uniqueness is guaranteed "within a single AllocateBatch
// call, but NOT across multiple calls or multiple instances" - and PodScoped
// allocation calls AllocateBatch once PER POD (manager.go:243-253), i.e. exactly the
// unguaranteed case. Release() is a no-op (random.go:81-83) so nothing is book-kept
// either. zh:249 says random is the only supported strategy.
//
// DETERMINISM: this test does NOT rely on a birthday-paradox collision. It uses the
// pigeonhole principle - the port range is deliberately SMALLER than the number of
// pods, so a duplicate is mathematically unavoidable for any implementation that
// lacks cross-call bookkeeping. Zero flake.
//
// AFTER A FIX (a globally-book-kept allocator, or one batch call covering all pods)
// THIS CANARY FLIPS TO RED.
func TestVerifyPR402_F2_RandomAllocatorNotUniqueAcrossCalls_Canary(t *testing.T) {
	cases := []struct {
		name      string
		startPort int32
		portRange int32
		numPods   int
		// pigeonhole: numPods > portRange => a duplicate is CERTAIN unless the
		// allocator keeps cross-call state (which random.go does not).
		wantDuplicate bool
	}{
		{
			name:          "9 PodScoped ports out of a range of 8 (pigeonhole: duplicate certain)",
			startPort:     30000,
			portRange:     8,
			numPods:       9,
			wantDuplicate: true,
		},
		{
			name:          "33 PodScoped ports out of a range of 32 (pigeonhole: duplicate certain)",
			startPort:     31000,
			portRange:     32,
			numPods:       33,
			wantDuplicate: true,
		},
		{
			name:          "2 PodScoped ports out of a range of 1 (degenerate pigeonhole)",
			startPort:     30500,
			portRange:     1,
			numPods:       2,
			wantDuplicate: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ra, err := newRandomAllocator(tc.startPort, tc.portRange)
			require.NoError(t, err)

			// Mirror manager.go:243-253 - one AllocateBatch(1) call per pod.
			seen := map[int32][]int{}
			ports := make([]int32, 0, tc.numPods)
			for pod := 0; pod < tc.numPods; pod++ {
				got, err := ra.AllocateBatch(1)
				require.NoError(t, err, "per-pod AllocateBatch must succeed")
				require.Len(t, got, 1)
				seen[got[0]] = append(seen[got[0]], pod)
				ports = append(ports, got[0])
			}

			dups := map[int32][]int{}
			for p, pods := range seen {
				if len(pods) > 1 {
					dups[p] = pods
				}
			}
			t.Logf("range=[%d,%d) pods=%d allocated=%v duplicates=%v",
				tc.startPort, tc.startPort+tc.portRange, tc.numPods, ports, dups)

			if tc.wantDuplicate {
				require.NotEmpty(t, dups,
					"doc presents dynamic allocation as collision-free, but %d per-pod "+
						"AllocateBatch(1) calls over a range of %d MUST collide unless the "+
						"allocator keeps cross-call state", tc.numPods, tc.portRange)
			} else {
				assert.Empty(t, dups)
			}
		})
	}
}

// TestVerifyPR402_F2_SingleBatchIsUnique_Contract
//
// POLARITY: contract
//
// The complement of F2: WITHIN one AllocateBatch call uniqueness IS guaranteed
// (random.go's used map). Green now and after any fix. Pins the boundary of the
// guarantee so the F2 canary cannot be dismissed as "the allocator is just broken".
func TestVerifyPR402_F2_SingleBatchIsUnique_Contract(t *testing.T) {
	ra, err := newRandomAllocator(30000, 8)
	require.NoError(t, err)

	ports, err := ra.AllocateBatch(8) // exactly fills the range
	require.NoError(t, err)
	require.Len(t, ports, 8)

	seen := map[int32]bool{}
	for _, p := range ports {
		assert.False(t, seen[p], "AllocateBatch must not repeat a port within one call")
		seen[p] = true
		assert.GreaterOrEqual(t, p, int32(30000))
		assert.Less(t, p, int32(30008))
	}
	assert.Len(t, seen, 8, "a full-range batch must return every port exactly once")

	// And the batch size is capped by the range (random.go:57-59).
	_, err = ra.AllocateBatch(9)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only 8 available in range")
}

// TestVerifyPR402_F2_ReleaseIsNoOp_Canary
//
// POLARITY: canary
//
// Secondary evidence for F2: Release() is an unconditional "return nil" with no
// bookkeeping (random.go:81-83), so a released port is never reclaimed nor marked
// free - there is no state to reclaim. A fix that adds bookkeeping makes Release
// meaningful and this canary should be revisited (an accounting allocator would
// reject or at least track an out-of-range / never-allocated release).
func TestVerifyPR402_F2_ReleaseIsNoOp_Canary(t *testing.T) {
	ra, err := newRandomAllocator(30000, 4)
	require.NoError(t, err)

	ports, err := ra.AllocateBatch(4)
	require.NoError(t, err)

	// Releasing every port then re-allocating the full range still works, and would
	// work identically if we had released nothing - Release() carries no information.
	for _, p := range ports {
		require.NoError(t, ra.Release(p))
	}
	// Releasing a port that was never allocated, and an out-of-range port, are both
	// silently accepted: proof there is no accounting at all.
	require.NoError(t, ra.Release(1), "out-of-range Release is silently accepted (no-op)")
	require.NoError(t, ra.Release(65535), "out-of-range Release is silently accepted (no-op)")
	require.NoError(t, ra.Release(-1), "invalid Release is silently accepted (no-op)")
}

// TestVerifyPR402_F2_PodScopedAllocatesPerPod_Contract
//
// POLARITY: contract
//
// Pins the mechanism F2 depends on: AllocatePodScopedPorts is invoked once per pod
// (AllocatePortsForInstance -> manager.go:243-253), each call an independent
// AllocateBatch. Uses the deterministic testAllocator from port_allocator_test.go
// so this stays green regardless of the random source.
func TestVerifyPR402_F2_PodScopedAllocatesPerPod_Contract(t *testing.T) {
	saved := portAllocator
	t.Cleanup(func() { portAllocator = saved })
	portAllocator = newTestAllocator(30000, 100)

	cfg := &PortAllocatorConfig{
		Allocations: []PortAllocation{
			{Name: "grpc", Env: "GRPC_PORT", Scope: PodScoped},
		},
	}

	// Two separate calls, one per pod name - the shape manager.go uses.
	a, err := AllocatePodScopedPorts(cfg, "inst-worker-0")
	require.NoError(t, err)
	b, err := AllocatePodScopedPorts(cfg, "inst-worker-1")
	require.NoError(t, err)

	require.Len(t, a, 1)
	require.Len(t, b, 1)
	assert.Contains(t, a, "inst-worker-0.grpc")
	assert.Contains(t, b, "inst-worker-1.grpc")

	// Keys are per-pod (so PodScoped values CAN differ) - the uniqueness of the
	// VALUES is what the allocator does not guarantee (see the F2 canary).
	for k := range a {
		assert.NotContains(t, b, k, "PodScoped keys must be per-pod")
	}
	// sanity: values parse as ports
	for _, v := range a {
		n, err := strconv.Atoi(v)
		require.NoError(t, err)
		assert.True(t, n >= 30000 && n < 30100)
	}
}

// TestVerifyPR402_F2_RandomIsTheOnlyRegisteredStrategy_Contract
//
// POLARITY: contract
//
// Pins zh:249 ("currently only random is supported"): "random" is registered and
// nothing else is. If a book-kept strategy is added as the fix, this test tells the
// re-verifier that the F2 canary's premise changed.
func TestVerifyPR402_F2_RandomIsTheOnlyRegisteredStrategy_Contract(t *testing.T) {
	assert.True(t, HasPolicy("random"), "doc documents --port-allocate-strategy=random")
	registered := make([]string, 0, len(paFactory))
	for k := range paFactory {
		registered = append(registered, string(k))
	}
	t.Logf("registered strategies: %v", registered)
	assert.Equal(t, []string{"random"}, registered,
		"doc zh:249 says random is the only supported strategy")
	assert.False(t, HasPolicy("deterministic"))
	assert.False(t, HasPolicy(""))
	// guard against a stray strategy name sneaking in
	for _, s := range registered {
		assert.False(t, strings.Contains(s, " "))
	}
}
