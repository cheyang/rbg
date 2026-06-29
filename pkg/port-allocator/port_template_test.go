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

package port_allocator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatPortTemplateKeys(t *testing.T) {
	assert.Equal(t, "portTemplate.leader.grpc.base", FormatPortTemplateBaseKey("leader", "grpc"))
	assert.Equal(t, "portTemplate.leader.grpc.stride", FormatPortTemplateStrideKey("leader", "grpc"))
	assert.Equal(t, "portTemplate.worker.nccl.base", FormatPortTemplateBaseKey("worker", "nccl"))
}

func TestIsPortTemplateKey(t *testing.T) {
	assert.True(t, IsPortTemplateKey("portTemplate.leader.grpc.base"))
	assert.True(t, IsPortTemplateKey("portTemplate.worker.nccl.stride"))
	assert.False(t, IsPortTemplateKey("portTemplate."))
	assert.False(t, IsPortTemplateKey("leader.grpc"))
	assert.False(t, IsPortTemplateKey(""))
}

func TestHasPortTemplate(t *testing.T) {
	assert.True(t, HasPortTemplate(map[string]string{
		"portTemplate.leader.grpc.base":   "30000",
		"portTemplate.leader.grpc.stride": "2",
	}))
	assert.False(t, HasPortTemplate(map[string]string{
		"leader.grpc": "30000",
	}))
	assert.False(t, HasPortTemplate(nil))
}

func TestCollectPortTemplates(t *testing.T) {
	annotations := map[string]string{
		"portTemplate.leader.grpc.base":   "30100",
		"portTemplate.leader.grpc.stride": "2",
		"portTemplate.leader.nccl.base":   "31200",
		"portTemplate.leader.nccl.stride": "2",
		"leader.some-port":                "9999",
	}

	templates := CollectPortTemplates(annotations)
	assert.Len(t, templates, 2)

	grpc, ok := templates["leader.grpc"]
	assert.True(t, ok)
	assert.Equal(t, int32(30100), grpc.Base)
	assert.Equal(t, int32(2), grpc.Stride)

	nccl, ok := templates["leader.nccl"]
	assert.True(t, ok)
	assert.Equal(t, int32(31200), nccl.Base)
	assert.Equal(t, int32(2), nccl.Stride)
}

func TestCollectPortTemplates_empty(t *testing.T) {
	templates := CollectPortTemplates(map[string]string{"foo": "bar"})
	assert.Empty(t, templates)
}

func TestDerivePortsForInstance(t *testing.T) {
	annotations := map[string]string{
		"portTemplate.worker.grpc.base":   "30000",
		"portTemplate.worker.grpc.stride": "2",
	}
	config := &PortAllocatorConfig{
		Allocations: []PortAllocation{
			{Name: "grpc", Env: "GRPC_PORT", Scope: PodScoped},
		},
	}

	t.Run("instance 0 with 2 pods", func(t *testing.T) {
		result, err := DerivePortsForInstance(0, annotations, config, "worker", []string{"rbg-prefill-0-worker-0", "rbg-prefill-0-worker-1"})
		require.NoError(t, err)
		assert.Equal(t, "30000", result["rbg-prefill-0-worker-0.grpc"])
		assert.Equal(t, "30001", result["rbg-prefill-0-worker-1.grpc"])
	})

	t.Run("instance 1 with 2 pods", func(t *testing.T) {
		result, err := DerivePortsForInstance(1, annotations, config, "worker", []string{"rbg-prefill-1-worker-0", "rbg-prefill-1-worker-1"})
		require.NoError(t, err)
		assert.Equal(t, "30002", result["rbg-prefill-1-worker-0.grpc"])
		assert.Equal(t, "30003", result["rbg-prefill-1-worker-1.grpc"])
	})

	t.Run("instance 3 with 2 pods", func(t *testing.T) {
		result, err := DerivePortsForInstance(3, annotations, config, "worker", []string{"rbg-prefill-3-worker-0", "rbg-prefill-3-worker-1"})
		require.NoError(t, err)
		assert.Equal(t, "30006", result["rbg-prefill-3-worker-0.grpc"])
		assert.Equal(t, "30007", result["rbg-prefill-3-worker-1.grpc"])
	})

	t.Run("missing base returns error", func(t *testing.T) {
		_, err := DerivePortsForInstance(0, map[string]string{}, config, "worker", []string{"pod-0"})
		assert.Error(t, err)
	})
}

func TestDerivePortsForInstance_multiplePortNames(t *testing.T) {
	annotations := map[string]string{
		"portTemplate.worker.grpc.base":   "30000",
		"portTemplate.worker.grpc.stride": "1",
		"portTemplate.worker.nccl.base":   "31000",
		"portTemplate.worker.nccl.stride": "1",
	}
	config := &PortAllocatorConfig{
		Allocations: []PortAllocation{
			{Name: "grpc", Env: "GRPC_PORT", Scope: PodScoped},
			{Name: "nccl", Env: "NCCL_PORT", Scope: PodScoped},
		},
	}

	result, err := DerivePortsForInstance(2, annotations, config, "worker", []string{"pod-2"})
	require.NoError(t, err)
	assert.Equal(t, "30002", result["pod-2.grpc"])
	assert.Equal(t, "31002", result["pod-2.nccl"])
}
