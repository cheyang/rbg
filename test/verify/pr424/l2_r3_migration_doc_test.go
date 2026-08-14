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

// Round 3. 16969e7f rewrote the Upgrading section of doc/features/failure-handling.md
// into a numbered procedure with copy-pasteable kubectl commands. Operators will run
// those verbatim, so this file checks them against a real API server rather than
// reading them.
package pr424

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

const roleInstanceSetCRDName = "roleinstancesets.workloads.x-k8s.io"

// alphaShapedRoleInstanceSet builds a RoleInstanceSet whose inlined
// roleInstanceTemplate carries the v0.8.0-alpha.x object-shaped restartPolicy.
func alphaShapedRoleInstanceSet(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	u.SetKind("RoleInstanceSet")
	u.SetNamespace("default")
	u.SetName(name)
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(unstructured.SetNestedField(u.Object, int64(1), "spec", "replicas"))
	must(unstructured.SetNestedMap(u.Object, map[string]interface{}{
		"matchLabels": map[string]interface{}{"app": name},
	}, "spec", "selector"))
	must(unstructured.SetNestedField(u.Object, validComponents(),
		"spec", "roleInstanceTemplate", "components"))
	must(unstructured.SetNestedMap(u.Object, map[string]interface{}{
		"type":             "RecreateRoleInstanceOnPodRestart",
		"baseDelaySeconds": int64(30),
	}, "spec", "roleInstanceTemplate", "restartPolicy"))
	return u
}

func upgradeCRDByFile(t *testing.T, c client.Client, crdName, file string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "crd", "bases", file))
	require.NoError(t, err)
	want := &apiextv1.CustomResourceDefinition{}
	require.NoError(t, yaml.Unmarshal(raw, want))

	live := &apiextv1.CustomResourceDefinition{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: crdName}, live))
	live.Spec = want.Spec
	require.NoError(t, c.Update(context.Background(), live))
}

// R3-1 / L2 — CONTRACT. The RoleInstanceSet command the doc gives:
//
//	kubectl patch roleinstanceset <name> -n <ns> --type=merge \
//	  -p '{"spec":{"roleInstanceTemplate":{"restartPolicy":null,"restartPolicyConfig":{"type":"<type>"}}}}'
//
// The doc's claim that the fields sit directly under roleInstanceTemplate with no
// nested "spec" key is worth pinning, since RoleInstanceTemplate inlines
// RoleInstanceSpec and getting that wrong silently patches nothing.
func TestL2_R3_DocumentedRoleInstanceSetMigrationWorks(t *testing.T) {
	c, _, stop := startEnvWithOldCRD(t)
	defer stop()
	ctx := context.Background()

	require.NoError(t, c.Create(ctx, alphaShapedRoleInstanceSet("ris-alpha")))
	upgradeCRDByFile(t, c, roleInstanceSetCRDName, "workloads.x-k8s.io_roleinstancesets.yaml")

	broken := &workloadsv1alpha2.RoleInstanceSetList{}
	errBefore := c.List(ctx, broken, client.InNamespace("default"))
	t.Logf("typed LIST before migration: %v", errBefore)
	require.Error(t, errBefore, "precondition: the object-shaped restartPolicy breaks the typed LIST")

	target := &unstructured.Unstructured{}
	target.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	target.SetKind("RoleInstanceSet")
	target.SetNamespace("default")
	target.SetName("ris-alpha")
	body := []byte(`{"spec":{"roleInstanceTemplate":{"restartPolicy":null,` +
		`"restartPolicyConfig":{"type":"RecreateRoleInstanceOnPodRestart"}}}}`)
	require.NoError(t, c.Patch(ctx, target, client.RawPatch(types.MergePatchType, body)),
		"the documented RoleInstanceSet patch must be accepted")

	migrated := &workloadsv1alpha2.RoleInstanceSetList{}
	require.NoError(t, c.List(ctx, migrated, client.InNamespace("default")),
		"the typed LIST must recover after the documented patch")
	require.Len(t, migrated.Items, 1)
	assert.Equal(t, workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
		migrated.Items[0].Spec.RoleInstanceTemplate.GetRestartPolicy(),
		"the policy must land where the controller reads it")
}

// R3-2 / L2. The doc says: "once the new CRDs are installed, the API server prunes
// the old object shape on read for every affected resource". roleInstanceTemplate is
// declared x-kubernetes-preserve-unknown-fields with no properties, so pruning should
// NOT apply there. If the values survive, operators can read the old policy off a
// RoleInstanceSet instead of digging it out of a backup, and the doc overstates.
func TestL2_R3_RoleInstanceSetIsNotPrunedOnRead(t *testing.T) {
	c, _, stop := startEnvWithOldCRD(t)
	defer stop()
	ctx := context.Background()

	require.NoError(t, c.Create(ctx, alphaShapedRoleInstanceSet("ris-alpha")))
	upgradeCRDByFile(t, c, roleInstanceSetCRDName, "workloads.x-k8s.io_roleinstancesets.yaml")

	got := &unstructured.Unstructured{}
	got.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	got.SetKind("RoleInstanceSet")
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "ris-alpha"}, got))

	rp, found, err := unstructured.NestedMap(got.Object, "spec", "roleInstanceTemplate", "restartPolicy")
	require.NoError(t, err)
	b, _ := json.Marshal(rp)
	t.Logf("RoleInstanceSet spec.roleInstanceTemplate.restartPolicy after CRD upgrade: found=%v value=%s",
		found, b)

	require.True(t, found, "the field is still present")
	assert.NotEmpty(t, rp,
		"schemaless roleInstanceTemplate is not pruned, so the old values ARE still readable here")
	assert.Equal(t, "RecreateRoleInstanceOnPodRestart", rp["type"],
		"the configured type is recoverable from a RoleInstanceSet")
}

// R3-3 / L2 — CANARY. The doc's step 2 lists RoleBasedGroup and RoleBasedGroupSet
// among the objects to migrate but gives a command only for RoleInstance and
// RoleInstanceSet. A RoleBasedGroup's restartPolicy lives at
// spec.roles[i].leaderWorkerPattern.restartPolicy, and for a custom resource a merge
// patch REPLACES the whole roles array. So adapting the documented RoleInstance
// command to a RoleBasedGroup, which is the obvious thing to try, reports success and
// silently drops the rest of the role.
//
// This PASSES on 16969e7f, documenting the trap. It should flip to red once the doc
// carries an indexed JSON patch for these two kinds (see R3-4).
func TestL2_R3_Canary_RoleBasedGroupMergePatchDropsPodTemplate(t *testing.T) {
	c, stop := startPlainEnv(t)
	defer stop()
	ctx := context.Background()
	key := client.ObjectKey{Namespace: "default", Name: "rbg-migrate"}

	require.NoError(t, c.Create(ctx, r3RBG("default", "rbg-migrate",
		workloadsv1alpha2.RecreateRoleInstanceOnPodRestart)))

	before := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx, key, before))
	require.NotNil(t, before.Spec.Roles[0].LeaderWorkerPattern.TemplateSource.Template,
		"precondition: the role carries a pod template")

	// The shape an operator reaches for after reading the RoleInstance example.
	target := &unstructured.Unstructured{}
	target.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	target.SetKind("RoleBasedGroup")
	target.SetNamespace("default")
	target.SetName("rbg-migrate")
	body := []byte(`{"spec":{"roles":[{"name":"role-1","leaderWorkerPattern":` +
		`{"restartPolicy":null,"restartPolicyConfig":{"type":"None"}}}]}}`)
	err := c.Patch(ctx, target, client.RawPatch(types.MergePatchType, body))
	assert.NoError(t, err, "CANARY: the API server accepts it, so there is no warning to go on")

	after := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx, key, after))
	require.Len(t, after.Spec.Roles, 1)
	lwp := after.Spec.Roles[0].LeaderWorkerPattern
	require.NotNil(t, lwp)
	t.Logf("after the merge patch: template=%v size=%v restartPolicyConfig=%v",
		lwp.TemplateSource.Template != nil, lwp.Size, lwp.RestartPolicyConfig)

	assert.Nil(t, lwp.TemplateSource.Template,
		"CANARY: the pod template is gone, and the patch reported success")
}

// R3-4 / L2 — CONTRACT. The JSON patch an operator actually needs for a
// RoleBasedGroup, which the doc does not give. Indexed, so it leaves the rest of the
// role alone.
func TestL2_R3_RoleBasedGroupJSONPatchMigrationIsSafe(t *testing.T) {
	c, stop := startPlainEnv(t)
	defer stop()
	ctx := context.Background()
	key := client.ObjectKey{Namespace: "default", Name: "rbg-jsonpatch"}

	require.NoError(t, c.Create(ctx, r3RBG("default", "rbg-jsonpatch",
		workloadsv1alpha2.RecreateRoleInstanceOnPodRestart)))

	target := &unstructured.Unstructured{}
	target.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	target.SetKind("RoleBasedGroup")
	target.SetNamespace("default")
	target.SetName("rbg-jsonpatch")
	body := []byte(`[` +
		`{"op":"remove","path":"/spec/roles/0/leaderWorkerPattern/restartPolicy"},` +
		`{"op":"add","path":"/spec/roles/0/leaderWorkerPattern/restartPolicyConfig",` +
		`"value":{"type":"RecreateRoleInstanceOnPodRestart"}}]`)
	require.NoError(t, c.Patch(ctx, target, client.RawPatch(types.JSONPatchType, body)),
		"an indexed JSON patch must be accepted")

	after := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx, key, after))
	lwp := after.Spec.Roles[0].LeaderWorkerPattern
	require.NotNil(t, lwp)
	assert.NotNil(t, lwp.TemplateSource.Template, "the pod template survives an indexed JSON patch")
	assert.Equal(t, ptr.To(int32(2)), lwp.Size, "size survives an indexed JSON patch")
	assert.Equal(t, workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
		after.Spec.Roles[0].GetRestartPolicy(), "the policy lands correctly")
}
