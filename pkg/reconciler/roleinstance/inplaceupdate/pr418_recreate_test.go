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

// pr418_recreate_test.go is a reviewer-private verification harness for
// https://github.com/sgl-project/rbg/pull/418. It is NOT part of the PR.
//
// It pins down the pod-level consequence of a component serviceName change, which is the
// second half of finding F5 and the mechanism PR#418's own API comment relies on when it says
// switching the policy "triggers a rolling replacement of the role instances".
package inplaceupdate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestVerifyPR418_F5b_ServiceNameChangeForcesRecreate is a [CANARY] recording the decision
// function that turns a serviceName change into a pod replacement.
//
// isComponentExtensionSpecChanged compares only the "serviceName" key of the component
// extension spec. Any difference makes CanUpdateInPlace return false for the whole role
// instance, so the pods are deleted and recreated rather than patched -- which is correct,
// because pod.spec.hostname/subdomain are immutable.
//
// Combined with TestVerifyPR418_F5_WorkerServiceNameUnderAll (which shows the worker
// serviceName goes from "" to the service name across this PR), this is what makes the
// controller upgrade itself replace the worker pods of every existing All role.
func TestVerifyPR418_F5b_ServiceNameChangeForcesRecreate(t *testing.T) {
	cases := []struct {
		name          string
		oldSvc        interface{}
		newSvc        interface{}
		wantRecreated bool
	}{
		{
			name:          "worker gains a serviceName across the upgrade (base -> PR#418, All)",
			oldSvc:        nil,
			newSvc:        "s-rbg-role",
			wantRecreated: true,
		},
		{
			name:          "worker loses its serviceName (All -> LeaderOnly switch)",
			oldSvc:        "s-rbg-role",
			newSvc:        nil,
			wantRecreated: true,
		},
		{
			name:          "unchanged serviceName does not force a replacement",
			oldSvc:        "s-rbg-role",
			newSvc:        "s-rbg-role",
			wantRecreated: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldSpec := map[string]interface{}{}
			if tc.oldSvc != nil {
				oldSpec["serviceName"] = tc.oldSvc
			}
			newSpec := map[string]interface{}{}
			if tc.newSvc != nil {
				newSpec["serviceName"] = tc.newSvc
			}

			rh := revisionHistory{
				oldRevision: &componentRevision{componentExtensionSpecRevision: oldSpec},
				newRevision: &componentRevision{componentExtensionSpecRevision: newSpec},
			}

			got := isComponentExtensionSpecChanged(rh)
			t.Logf("F5b old=%v new=%v -> recreate=%v", tc.oldSvc, tc.newSvc, got)

			assert.Equal(t, tc.wantRecreated, got,
				"F5b canary: a component serviceName change must fall back to ReCreate, "+
					"because pod hostname/subdomain are immutable")
		})
	}
}
