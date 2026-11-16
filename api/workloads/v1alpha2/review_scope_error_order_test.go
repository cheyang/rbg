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

// Round-3 harness (reviewer A): contract test for F-SCOPE-ERROR-ORDER.
//
// Claim: the scope-ownership aggregate's error order — including which
// overlaps survive the 20-error cap — must be deterministic across runs.
// Polarity: CONTRACT — RED on f3d4849a for shapes with several distinct
// (profile, component) keys (Go map iteration order is randomized; the
// red is probabilistic but overwhelming with 6+ keys over 30 runs), GREEN
// on c87a2e1e (7fe890a6 sorted walk).
package v1alpha2

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func TestReview_ScopeOwnershipErrorOrderStable(t *testing.T) {
	role := &RoleSpec{
		Name:    "review-r",
		Pattern: Pattern{LeaderWorkerPattern: &LeaderWorkerPattern{}},
	}
	runtimes := make([]EngineRuntime, 0, 16)
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("review-p-%d", i)
		runtimes = append(runtimes,
			EngineRuntime{ProfileName: name, Component: "leader", TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}}},
			EngineRuntime{ProfileName: name, Component: "leader", TargetContainers: []EngineRuntimeTargetContainer{{Name: "engine"}}},
		)
	}
	role.EngineRuntimes = runtimes

	var first []string
	for run := 0; run < 30; run++ {
		err := ValidateEngineRuntimes(role)
		agg, ok := err.(utilerrors.Aggregate)
		require.True(t, ok, "expected an aggregate error")
		msgs := make([]string, len(agg.Errors()))
		for i, e := range agg.Errors() {
			msgs[i] = e.Error()
		}
		if first == nil {
			first = msgs
			continue
		}
		assert.Equal(t, first, msgs,
			"scope-ownership error order must be deterministic across runs (F-SCOPE-ERROR-ORDER)")
	}
}
