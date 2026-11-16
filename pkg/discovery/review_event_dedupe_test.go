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

package discovery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestReview_SuccessfulRenderWarningDedupe is the review-harness contract
// test for F-EVENTS-REEMIT.
//
// Claim: a healthy, successfully rendering object re-reconciled without any
// spec change must not re-emit the same successful-render warning event
// (unaimed patch); a spec write (generation bump) re-arms it. Polarity:
// CONTRACT — RED on f3d4849a (every reconcile re-emits), GREEN on c87a2e1e
// (44e2e531 renderWarningLedger).
func TestReview_SuccessfulRenderWarningDedupe(t *testing.T) {
	// The ledger is a package-level global keyed by UID: use a dedicated
	// UID so this test cannot collide with (or be poisoned by) other
	// tests in the package.
	profile := makeProfile("review-dedupe", []workloadsv1alpha2.ContainerPatch{
		envPatch("REVIEW_DEDUPE_VAR", "/opt/review"),
	})
	role := leaderWorkerRole([]workloadsv1alpha2.EngineRuntime{
		{ProfileName: "review-dedupe", Component: "leader"},
	})
	rbg := basicRBG()
	rbg.UID = "review-event-dedupe-uid"
	rbg.Generation = 41

	resolve := func() int {
		recorder := record.NewFakeRecorder(16)
		cli := fake.NewClientBuilder().WithScheme(resolveScheme()).WithObjects(profile).Build()
		_, err := ResolveEngineRuntimes(context.Background(), cli, recorder, rbg, role)
		require.NoError(t, err)
		return countEventsContaining(t, recorder, ReasonUnaimedContainerPatch)
	}

	assert.Equal(t, 1, resolve(), "the first resolve emits the unaimed warning")
	assert.Equal(t, 0, resolve(),
		"a re-reconcile of the unchanged object must not re-emit the warning (F-EVENTS-REEMIT)")

	rbg.Generation = 42
	assert.Equal(t, 1, resolve(), "a spec write (generation bump) re-arms the warning")
}

// TestReview_RenderFailureNotDeduped pins the polarity boundary: a render
// failure is live state, so it must keep firing on every reconcile instead of
// being silenced by the successful-render ledger.
func TestReview_RenderFailureNotDeduped(t *testing.T) {
	role := leaderWorkerRole([]workloadsv1alpha2.EngineRuntime{{ProfileName: "review-ghost"}})
	rbg := basicRBG()
	rbg.UID = "review-render-fail-uid"
	rbg.Generation = 1

	for i := 0; i < 2; i++ {
		recorder := record.NewFakeRecorder(16)
		cli := fake.NewClientBuilder().WithScheme(resolveScheme()).Build()
		_, err := ResolveEngineRuntimes(context.Background(), cli, recorder, rbg, role)
		require.Error(t, err)
		assert.Equal(t, 1, countEventsContaining(t, recorder, ReasonEngineRuntimeRenderFailed),
			"render failure %d is live state and must keep firing per reconcile", i+1)
	}
}
