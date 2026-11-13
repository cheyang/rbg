package statefulmode

// KEP-543 review verification harness (PR #569, reviewer Claude).
// These tests prove the two "existing behavior" claims that the round-2
// KEP revision (commit d8d53364) newly relies on:
//
//  1. B-F1 fix: "RIS floors a resolved maxUnavailable to 1 only when
//     maxSurge is zero; ... With positive maxSurge, the literal 0 written
//     by RBG survives existing RIS logic unchanged"
//     (keps/543-segment/README.md, "Policy Overrides and RIS Execution
//     Windows"). Contract tests: the floor is surge-conditional.
//
//  2. A-F1 fix: "Stateful surge instances live at ordinals at or above
//     RIS.spec.replicas and never change that field"
//     (keps/543-segment/README.md, "Complete Segment Invariants").
//     Contract test: materializeSurge only allocates ordinals >= replicas
//     and extends endOrdinal, never the desired replica count.
//
// Additive review harness only; no production code is changed.

import (
	"testing"

	intstrutil "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/ordinals"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestVerifyKEP543_ExplicitZeroFloorsToOneOnlyWithoutSurge pins the floor
// behavior the zero-budget rule must avoid tripping. With maxSurge == 0 an
// explicit 0 resolves to 1 (the KEP now forbids Segment from ever writing a
// zero budget into this case); with positive maxSurge the literal 0 survives.
func TestVerifyKEP543_ExplicitZeroFloorsToOneOnlyWithoutSurge(t *testing.T) {
	cases := []struct {
		name         string
		maxSurge     int
		maxUnavail   *intstrutil.IntOrString
		replicas     int32
		wantResolved int
	}{
		{
			name:         "explicit 0 with zero surge floors to 1 (case Segment admission must reject)",
			maxSurge:     0,
			maxUnavail:   ptr.To(intstrutil.FromInt32(0)),
			replicas:     3,
			wantResolved: 1,
		},
		{
			name:         "explicit 0 with positive surge survives as literal 0 (KEP claim)",
			maxSurge:     2,
			maxUnavail:   ptr.To(intstrutil.FromInt32(0)),
			replicas:     3,
			wantResolved: 0,
		},
		{
			name:         "explicit 1 with zero surge stays 1",
			maxSurge:     0,
			maxUnavail:   ptr.To(intstrutil.FromInt32(1)),
			replicas:     3,
			wantResolved: 1,
		},
		{
			name:         "explicit 1 with positive surge stays 1",
			maxSurge:     2,
			maxUnavail:   ptr.To(intstrutil.FromInt32(1)),
			replicas:     3,
			wantResolved: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := buildSet("kep543-s", tc.replicas, nil, tc.maxUnavail)
			got, err := computeMaxUnavailable(set, tc.maxSurge)
			if err != nil {
				t.Fatalf("computeMaxUnavailable() error = %v", err)
			}
			if got != tc.wantResolved {
				t.Fatalf("computeMaxUnavailable(set, maxSurge=%d) = %d, want %d",
					tc.maxSurge, got, tc.wantResolved)
			}
		})
	}
}

// TestVerifyKEP543_SurgeOrdinalsAtOrAboveReplicas proves the A-F1 fix's
// exclusion-by-construction claim: surge slots only get ordinals >= replicas
// (dense Segment membership means base ordinals are [0, replicas)), the
// in-memory window extends endOrdinal, and no spec field grows.
func TestVerifyKEP543_SurgeOrdinalsAtOrAboveReplicas(t *testing.T) {
	const replicas = 3
	const activeSurge = 2

	// Dense base membership [0, replicas), as required for Segment members.
	base := make([]int, 0, replicas)
	for ord := 0; ord < replicas; ord++ {
		base = append(base, ord)
	}
	tp := &topology{
		ordinals:     base,
		startOrdinal: 0,
		endOrdinal:   replicas,
		replicas:     replicas,
		activeSurge:  activeSurge,
	}

	err := tp.materializeSurge(ordinals.Ranges{}, ordinals.Ranges{}, nil)
	if err != nil {
		t.Fatalf("materializeSurge() error = %v", err)
	}

	if got := tp.endOrdinal; got != replicas+activeSurge {
		t.Fatalf("endOrdinal = %d, want %d", got, replicas+activeSurge)
	}
	if got := len(tp.ordinals); got != replicas+activeSurge {
		t.Fatalf("len(ordinals) = %d, want %d", got, replicas+activeSurge)
	}
	for i, ord := range tp.ordinals[replicas:] {
		if ord < replicas {
			t.Fatalf("surge ordinal at position %d is %d, want >= %d (claim: surge instances live at ordinals at or above RIS.spec.replicas)", replicas+i, ord, replicas)
		}
	}

	// Re-run on a larger surge request to also cover ordinal allocation
	// walking forward from the last base ordinal.
	tp2 := &topology{
		ordinals:     append([]int(nil), base...),
		startOrdinal: 0,
		endOrdinal:   replicas,
		replicas:     replicas,
		activeSurge:  4,
	}
	if err := tp2.materializeSurge(ordinals.Ranges{}, ordinals.Ranges{}, nil); err != nil {
		t.Fatalf("materializeSurge(larger surge) error = %v", err)
	}
	for i, ord := range tp2.ordinals[replicas:] {
		if ord < replicas {
			t.Fatalf("larger-surge ordinal at position %d is %d, want >= %d", replicas+i, ord, replicas)
		}
	}

	// The claim "never change that field": materializeSurge is pure
	// topology bookkeeping; verify it never touched a spec-shaped value by
	// construction (no RoleInstanceSet is involved at all). Guard the
	// type contract: surge lives in endOrdinal, not in replicas.
	if tp2.replicas != replicas {
		t.Fatalf("topology.replicas mutated: %d, want %d", tp2.replicas, replicas)
	}
}

// TestVerifyKEP543_CommittedOrdinalsAreSpecBased pins the discovery claim in
// the A-F1 fix: "Discovery derives the address table from the committed base
// target ... the child RIS committed ordinals ... never from live Pods or
// RoleInstances." CommittedOrdinals() must be a pure function of
// spec.replicas and the committed-skip annotation, independent of live state.
func TestVerifyKEP543_CommittedOrdinalsAreSpecBased(t *testing.T) {
	set := &workloadsv1alpha2.RoleInstanceSet{
		Spec: workloadsv1alpha2.RoleInstanceSetSpec{Replicas: ptr.To(int32(4))},
	}
	got, err := set.CommittedOrdinals()
	if err != nil {
		t.Fatalf("CommittedOrdinals() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("CommittedOrdinals() = %v, want dense [0..3] from spec.replicas alone (no live instances involved)", got)
	}
	for i, ord := range got {
		if ord != i {
			t.Fatalf("CommittedOrdinals()[%d] = %d, want %d", i, ord, i)
		}
	}
}
