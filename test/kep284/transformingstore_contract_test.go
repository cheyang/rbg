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

package kep284

import (
	"testing"

	toolscache "k8s.io/client-go/tools/cache"
)

// TestFIFOsDoNotImplementTransformingStore pins the client-go mechanism the
// KEP's watch-stream wrapper relies on (KEP "Cache Implementation" item 6):
// reflector.watchList hands its transform to the streaming-list temporary
// store only when the informer's store implements TransformingStore — which
// embeds the FULL Store interface (List/Get/GetByKey/...), and neither
// DeltaFIFO nor RealFIFO satisfies it (they implement only ReflectorStore).
//
// Verified at f8d67fa against vendored client-go v0.34.2 by direct type
// assertion and end-to-end (store-only streaming transform runs at Replace,
// peak HeapAlloc 71.3 MiB vs 18.3 MiB with the wrapper; probes in
// docs/verification/pod-cache-filter-kep/results/).
//
// CONTRACT polarity, expected GREEN: if a future client-go makes the informer
// FIFOs implement the full Store interface, this test flips RED — that is the
// signal that the wrapper workaround (listed in the KEP's Drawbacks) can be
// dropped, not a regression.
func TestFIFOsDoNotImplementTransformingStore(t *testing.T) {
	noop := func(obj interface{}) (interface{}, error) { return obj, nil }
	delta := toolscache.NewDeltaFIFOWithOptions(toolscache.DeltaFIFOOptions{
		KeyFunction:  toolscache.MetaNamespaceKeyFunc,
		KnownObjects: toolscache.NewStore(toolscache.MetaNamespaceKeyFunc),
		Transformer:  noop,
	})
	if _, ok := interface{}(delta).(toolscache.TransformingStore); ok {
		t.Fatal("DeltaFIFO now implements TransformingStore: the KEP's watch-stream wrapper is obsolete, drop it")
	}
	real := toolscache.NewRealFIFO(toolscache.MetaNamespaceKeyFunc, toolscache.NewStore(toolscache.MetaNamespaceKeyFunc), noop)
	if _, ok := interface{}(real).(toolscache.TransformingStore); ok {
		t.Fatal("RealFIFO now implements TransformingStore: the KEP's watch-stream wrapper is obsolete, drop it")
	}
}
