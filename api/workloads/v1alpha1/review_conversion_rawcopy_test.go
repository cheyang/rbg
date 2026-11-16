/*
Copyright 2026.

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

// Round-3 harness (reviewer A): contract test for F-CONVERSION-RAW-COPY.
//
// Claim: both ContainerJSONPatch conversion directions must deep-copy the
// complete RawExtension, so an Object-backed patch value survives the
// round-trip instead of reading null. Polarity: contract — RED on f3d4849a
// (only Value.Raw copied), GREEN on c87a2e1e (3dd34d72 DeepCopy).
package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	v2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestReview_ConversionRawCopyPreserved(t *testing.T) {
	obj := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "engine"}}}}

	t.Run("v1alpha1 -> v2 preserves an Object-backed value", func(t *testing.T) {
		patch := &ContainerJSONPatch{
			Op:    "add",
			Path:  "/env/0",
			Value: k8sruntime.RawExtension{Object: obj},
		}
		converted := convertContainerJSONPatchV1alpha1ToV2(patch)
		require.NotNil(t, converted)
		require.NotNil(t, converted.Value.Object,
			"ConvertTo must copy Object alongside Raw (F-CONVERSION-RAW-COPY)")
		assert.Equal(t, obj, converted.Value.Object)
		assert.NotSame(t, obj, converted.Value.Object, "the copy must not alias the source")
	})

	t.Run("v2 -> v1alpha1 preserves an Object-backed value", func(t *testing.T) {
		patch := &v2.ContainerJSONPatch{
			Op:    "add",
			Path:  "/env/0",
			Value: k8sruntime.RawExtension{Object: obj},
		}
		converted := convertContainerJSONPatchV2ToV1alpha1(patch)
		require.NotNil(t, converted)
		require.NotNil(t, converted.Value.Object,
			"ConvertFrom must copy Object alongside Raw (F-CONVERSION-RAW-COPY)")
		assert.Equal(t, obj, converted.Value.Object)
		assert.NotSame(t, obj, converted.Value.Object, "the copy must not alias the source")
	})
}
