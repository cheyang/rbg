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

package portallocator

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	portallocator "sigs.k8s.io/rbgs/pkg/port-allocator"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

func TestPortAllocatorHostPortInjection(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Port Allocator Host Port Injection Suite")
}

var _ = BeforeSuite(func() {
	testutil.SetupTestEnv()

	// The production binary enables the port allocator via
	// --enable-port-allocator (cmd/rbgs/main.go). The shared envtest setup does
	// not, so enable it here with a deterministic range before the first
	// reconcile that needs it.
	Expect(portallocator.SetupPortAllocator(30000, 200, "random", true, testutil.K8sClient)).To(Succeed())
})

var _ = AfterSuite(func() {
	testutil.TeardownTestEnv()
})
