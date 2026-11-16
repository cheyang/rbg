/*
Round-3 addition to the PR #571 verification harness (reviewer: codex).
Additive test file — no production code changed.

R3-F5 (contract): F-CONVERSION-RAW-COPY fix — both ContainerJSONPatch
conversion directions deep-copy the complete RawExtension, so an
Object-backed patch value (not only Raw) survives conversion without
aliasing the source. RED on the previous head f3d4849a (the Object was
dropped), GREEN on c87a2e1e.
*/

package v1alpha1

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestVerifyPR571R3_ConversionDeepCopiesObjectBackedValue(t *testing.T) {
	obj := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "engine"}}}}

	v1Patch := &ContainerJSONPatch{
		Op:    "add",
		Path:  "/env/0",
		Value: runtime.RawExtension{Object: obj},
	}
	converted := convertContainerJSONPatchV1alpha1ToV2(v1Patch)
	if converted.Value.Object == nil {
		t.Fatalf("ConvertTo must preserve an Object-backed patch value, got nil Object")
	}
	if converted.Value.Object == runtime.Object(obj) {
		t.Fatalf("ConvertTo must not alias the source Object")
	}
	back := convertContainerJSONPatchV2ToV1alpha1(converted)
	if back.Value.Object == nil {
		t.Fatalf("ConvertFrom must preserve an Object-backed patch value, got nil Object")
	}
	if back.Value.Object == runtime.Object(obj) {
		t.Fatalf("ConvertFrom must not alias the source Object")
	}

	// Raw-backed values keep working (the wire path): no Object involved.
	rawPatch := &ContainerJSONPatch{
		Op:    "add",
		Path:  "/env/0",
		Value: runtime.RawExtension{Raw: []byte(`{"name":"A","value":"b"}`)},
	}
	out := convertContainerJSONPatchV1alpha1ToV2(rawPatch)
	if string(out.Value.Raw) != string(rawPatch.Value.Raw) {
		t.Fatalf("Raw must survive conversion, got %q", out.Value.Raw)
	}
	// The copy must not alias the source Raw backing array.
	rawPatch.Value.Raw[0] = '['
	if string(out.Value.Raw) != `{"name":"A","value":"b"}` {
		t.Fatalf("converted Raw must be a copy, got %q", out.Value.Raw)
	}
}
