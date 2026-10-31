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

// Minimal envtest suite for PR #532 verification (reviewer: codex). Boots only
// etcd + kube-apiserver (no CRDs, no controllers): the goal is to prove that
// the pod specs produced by the real InjectPortsIntoPod are accepted by a real
// API server and round-trip intact (schema/validation integration layer).

package verifypr532

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	testEnv    *envtest.Environment
	testClient client.Client
	testCtx    = context.Background()
)

func TestVerifyPR532Envtest(t *testing.T) {
	testEnv = &envtest.Environment{}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("failed to start envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("failed to stop envtest: %v", err)
		}
	})

	c, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	testClient = c

	t.Run("InjectedPodSpecsAcceptedByAPIServer", testInjectedPodSpecsAccepted)
	t.Run("APIServerDefaultsHostNetworkHostPort", testAPIServerDefaultsHostNetworkHostPort)
}

func createNamespace(t *testing.T, name string) {
	t.Helper()
	ns := &corev1.Namespace{}
	ns.Name = name
	if err := testClient.Create(testCtx, ns); err != nil {
		t.Fatalf("failed to create namespace: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(testCtx, ns) })
}
