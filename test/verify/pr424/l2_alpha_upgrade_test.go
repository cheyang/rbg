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

package pr424

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

const roleInstanceCRDName = "roleinstances.workloads.x-k8s.io"

// envCfg is the rest.Config of the most recently started harness API server.
var envCfg *rest.Config

// startEnvWithOldCRD boots an API server carrying the v0.8.0-alpha.x RoleInstance
// CRD, i.e. the shape where spec.restartPolicy is an object. testdata/ holds that
// CRD as generated at the PR's merge base.
func startEnvWithOldCRD(t *testing.T) (client.Client, *runtime.Scheme, func()) {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, apiextv1.AddToScheme(scheme))

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("testdata", "crd-alpha")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)

	envCfg = cfg

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	return c, scheme, func() { envCfg = nil; _ = env.Stop() }
}

// upgradeCRD replaces the installed RoleInstance CRD with the PR 424 version,
// which is what `helm upgrade` does. Existing stored objects are untouched: the
// API server does not re-validate them on a CRD change.
func upgradeCRD(t *testing.T, c client.Client) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "crd", "bases",
		"workloads.x-k8s.io_roleinstances.yaml"))
	require.NoError(t, err)

	want := &apiextv1.CustomResourceDefinition{}
	require.NoError(t, yaml.Unmarshal(raw, want))

	live := &apiextv1.CustomResourceDefinition{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: roleInstanceCRDName}, live))
	live.Spec = want.Spec
	require.NoError(t, c.Update(context.Background(), live))

	waitForNewSchema(t, c)
}

// waitForNewSchema blocks until the API server actually serves the upgraded schema.
// A CRD update is picked up asynchronously; a write validated against the stale
// schema silently prunes the (still unknown) restartPolicyConfig field, which would
// make the migration test fail for the wrong reason.
func waitForNewSchema(t *testing.T, c client.Client) {
	t.Helper()
	ctx := context.Background()
	var lastErr error
	deadline := time.Now().Add(60 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		probe := &unstructured.Unstructured{}
		probe.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
		probe.SetKind("RoleInstance")
		probe.SetNamespace("default")
		probe.SetName(fmt.Sprintf("schema-probe-%d", attempt))
		require.NoError(t, unstructured.SetNestedField(probe.Object, validComponents(), "spec", "components"))
		require.NoError(t, unstructured.SetNestedMap(probe.Object, map[string]interface{}{
			"type": "None",
		}, "spec", "restartPolicyConfig"))

		err := c.Create(ctx, probe)
		if err == nil {
			got, found, _ := unstructured.NestedString(probe.Object, "spec", "restartPolicyConfig", "type")
			_ = c.Delete(ctx, probe)
			if found && got == "None" {
				return
			}
			lastErr = fmt.Errorf("restartPolicyConfig pruned (found=%v value=%q): stale schema still served", found, got)
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("API server never started serving the upgraded RoleInstance schema; last probe result: %v", lastErr)
}

// validComponents is the minimum spec.components content both CRD versions accept.
func validComponents() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"name": "main",
			"size": int64(1),
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{"name": "main", "image": "nginx:latest"},
					},
				},
			},
		},
	}
}

func alphaShapedRoleInstance(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	u.SetKind("RoleInstance")
	u.SetNamespace("default")
	u.SetName(name)
	require_ := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	require_(unstructured.SetNestedField(u.Object, validComponents(), "spec", "components"))
	// The v0.8.0-alpha.x wire shape: restartPolicy is an OBJECT.
	require_(unstructured.SetNestedMap(u.Object, map[string]interface{}{
		"type":             "RecreateRoleInstanceOnPodRestart",
		"baseDelaySeconds": int64(30),
	}, "spec", "restartPolicy"))
	return u
}

// F3 / L2 — CANARY documenting the blast radius of the v0.8.0-alpha.x -> PR 424
// upgrade: a single un-migrated object makes the typed LIST fail for the whole
// resource type, so healthy objects become invisible to the controller too.
//
// On PR 424 HEAD this PASSES (the breakage is real and total). If a compatible
// decoder is added it FLIPS TO RED and must be inverted.
func TestL2_Canary_OneAlphaObjectBreaksTheWholeList(t *testing.T) {
	c, _, stop := startEnvWithOldCRD(t)
	defer stop()
	ctx := context.Background()

	// Two objects: one legacy-shaped, one that upgrades cleanly.
	require.NoError(t, c.Create(ctx, alphaShapedRoleInstance("alpha-shaped")))

	healthy := alphaShapedRoleInstance("healthy")
	unstructured.RemoveNestedField(healthy.Object, "spec", "restartPolicy")
	require.NoError(t, c.Create(ctx, healthy))

	upgradeCRD(t, c)

	// Unstructured reads still work — the data is there, only the typed decode fails.
	rawList := &unstructured.UnstructuredList{}
	rawList.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	rawList.SetKind("RoleInstanceList")
	require.NoError(t, c.List(ctx, rawList, client.InNamespace("default")))
	require.Len(t, rawList.Items, 2, "both objects are readable as unstructured")

	// A single object read straight through the typed client.
	single := &workloadsv1alpha2.RoleInstance{}
	errSingle := c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "alpha-shaped"}, single)
	assert.Error(t, errSingle, "CANARY: the un-migrated object is undecodable")
	if errSingle != nil {
		t.Logf("typed GET of the un-migrated object: %v", errSingle)
		assert.Contains(t, errSingle.Error(), "restartPolicy",
			"CANARY: the decode error names the offending field")
	}

	// The whole-type LIST — what an informer does on startup.
	typedList := &workloadsv1alpha2.RoleInstanceList{}
	errList := c.List(ctx, typedList, client.InNamespace("default"))
	assert.Error(t, errList, "CANARY: one bad object fails the LIST for the entire resource type")
	if errList != nil {
		t.Logf("typed LIST over 1 bad + 1 healthy object: %v", errList)
	}
	// The decoder returns partially populated items alongside the error, so the
	// interesting property is not an empty slice: it is that LIST fails at all.
	// A client-go informer treats a failed LIST as a failed initial sync and
	// retries forever, which is what strands the controller.
	t.Logf("items returned alongside the LIST error: %d (a failed LIST is what strands an informer, "+
		"not an empty slice)", len(typedList.Items))
}

// F3 / L2 — CANARY on the consequence the PR description claims: a controller-runtime
// cache over the broken resource type never syncs, so the controller is stranded
// rather than merely skipping the offending object.
//
// On PR 424 HEAD this PASSES (the cache never syncs).
func TestL2_Canary_ControllerCacheNeverSyncs(t *testing.T) {
	c, scheme, stop := startEnvWithOldCRD(t)
	defer stop()
	ctx := context.Background()

	require.NoError(t, c.Create(ctx, alphaShapedRoleInstance("alpha-shaped")))
	upgradeCRD(t, c)

	cacheCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	ca, err := cache.New(envCfg, cache.Options{Scheme: scheme})
	require.NoError(t, err)
	inf, err := ca.GetInformer(cacheCtx, &workloadsv1alpha2.RoleInstance{})
	require.NoError(t, err)
	_ = inf

	go func() { _ = ca.Start(cacheCtx) }()

	syncCtx, syncCancel := context.WithTimeout(cacheCtx, 25*time.Second)
	defer syncCancel()
	synced := ca.WaitForCacheSync(syncCtx)

	assert.False(t, synced,
		"CANARY: the RoleInstance cache cannot sync while one object holds the v0.8.0-alpha.x shape")
}

// F3 / L2 — CANARY, and the most consequential detail of the upgrade break.
//
// Because the upgraded schema declares spec.restartPolicy as a string, the API
// server PRUNES every property of the stored object on the way out: the operator
// reads back `restartPolicy: {}`. The configured type and backoff values are gone
// from anything served over the API, so they cannot be recovered from the object
// being migrated — only from etcd, or from the RoleInstanceSet template that
// generated it.
//
// On PR 424 HEAD this PASSES.
func TestL2_Canary_StoredRestartPolicyIsPrunedToEmptyOnRead(t *testing.T) {
	c, _, stop := startEnvWithOldCRD(t)
	defer stop()
	ctx := context.Background()

	require.NoError(t, c.Create(ctx, alphaShapedRoleInstance("alpha-shaped")))

	// Before the upgrade the values are readable.
	pre := &unstructured.Unstructured{}
	pre.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	pre.SetKind("RoleInstance")
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "alpha-shaped"}, pre))
	preRP, _, _ := unstructured.NestedMap(pre.Object, "spec", "restartPolicy")
	require.Equal(t, "RecreateRoleInstanceOnPodRestart", preRP["type"],
		"precondition: the policy is readable under the old schema")
	t.Logf("before upgrade: spec.restartPolicy=%#v", preRP)

	upgradeCRD(t, c)

	post := &unstructured.Unstructured{}
	post.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	post.SetKind("RoleInstance")
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "alpha-shaped"}, post))
	postRP, found, _ := unstructured.NestedMap(post.Object, "spec", "restartPolicy")
	t.Logf("after upgrade: spec.restartPolicy=%#v (found=%v)", postRP, found)

	assert.True(t, found, "CANARY: the field is still present, so it still breaks the typed decode")
	assert.Empty(t, postRP,
		"CANARY: every property is pruned on read, so the configured policy is unrecoverable via the API")
}

// F3 / L2 — CANARY: the obvious migration command silently discards the policy.
//
// `kubectl patch --type=json -p '[{"op":"move","from":"/spec/restartPolicy","path":"/spec/restartPolicyConfig"}]'`
// looks like the natural fix and does unbreak the LIST — but because the source is
// pruned to `{}` (see above), it moves nothing. The object comes back with the
// schema defaults and no type, i.e. no instance-level restart at all. A role that
// was configured for RecreateRoleInstanceOnPodRestart silently stops recreating.
//
// On PR 424 HEAD this PASSES.
func TestL2_Canary_JSONPatchMoveSilentlyDropsThePolicy(t *testing.T) {
	c, _, stop := startEnvWithOldCRD(t)
	defer stop()
	ctx := context.Background()

	require.NoError(t, c.Create(ctx, alphaShapedRoleInstance("alpha-shaped")))
	upgradeCRD(t, c)

	target := &unstructured.Unstructured{}
	target.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	target.SetKind("RoleInstance")
	target.SetNamespace("default")
	target.SetName("alpha-shaped")
	body := []byte(`[{"op":"move","from":"/spec/restartPolicy","path":"/spec/restartPolicyConfig"}]`)
	require.NoError(t, c.Patch(ctx, target, client.RawPatch(types.JSONPatchType, body)))

	migrated := &workloadsv1alpha2.RoleInstanceList{}
	require.NoError(t, c.List(ctx, migrated, client.InNamespace("default")),
		"the LIST does recover, which is what makes this trap convincing")
	require.Len(t, migrated.Items, 1)

	assert.Equal(t, workloadsv1alpha2.RestartPolicyType(""), migrated.Items[0].Spec.GetRestartPolicy(),
		"CANARY: the policy is silently lost — the role no longer recreates its instance")
}

// F3 / L2 — CONTRACT: the migration that actually preserves behaviour. The operator
// has to re-supply the intended values, because the stored ones are no longer
// readable. This is the command the upgrade note should carry instead of
// "delete and recreate the workloads".
//
//	kubectl patch roleinstance <name> -n <ns> --type=merge -p \
//	  '{"spec":{"restartPolicy":null,"restartPolicyConfig":{"type":"<type>"}}}'
func TestL2_MergePatchMigrationPreservesThePolicy(t *testing.T) {
	c, _, stop := startEnvWithOldCRD(t)
	defer stop()
	ctx := context.Background()

	require.NoError(t, c.Create(ctx, alphaShapedRoleInstance("alpha-shaped")))
	upgradeCRD(t, c)

	broken := &workloadsv1alpha2.RoleInstanceList{}
	require.Error(t, c.List(ctx, broken, client.InNamespace("default")),
		"precondition: the typed LIST is broken before migration")

	target := &unstructured.Unstructured{}
	target.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	target.SetKind("RoleInstance")
	target.SetNamespace("default")
	target.SetName("alpha-shaped")
	body := []byte(`{"spec":{"restartPolicy":null,"restartPolicyConfig":` +
		`{"type":"RecreateRoleInstanceOnPodRestart","baseDelaySeconds":30}}}`)
	require.NoError(t, c.Patch(ctx, target, client.RawPatch(types.MergePatchType, body)))

	migrated := &workloadsv1alpha2.RoleInstanceList{}
	require.NoError(t, c.List(ctx, migrated, client.InNamespace("default")),
		"the typed LIST must recover after migrating the object in place")
	require.Len(t, migrated.Items, 1)
	assert.Equal(t, workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
		migrated.Items[0].Spec.GetRestartPolicy(), "the restart policy survives this migration")
	assert.Equal(t, int32(30), migrated.Items[0].Spec.GetBaseDelaySeconds(),
		"the backoff configuration survives this migration")
}

// F3 / L2 — CONTRACT test, and a correction to a hypothesis that did NOT hold.
//
// I expected the API server to reject every write to an object still holding the
// v0.8.0-alpha.x shape, which would have meant no controller could repair them.
// It does not: CRD validation ratcheting (on by default since Kubernetes 1.30)
// skips re-validating an unchanged field that was already invalid. So unrelated
// writes go through, and an in-place migration is possible without deleting the
// workload. This test pins that down so the finding is not overstated.
func TestL2_UnrelatedWritesToAnUnmigratedObjectAreAccepted(t *testing.T) {
	c, _, stop := startEnvWithOldCRD(t)
	defer stop()
	ctx := context.Background()

	require.NoError(t, c.Create(ctx, alphaShapedRoleInstance("alpha-shaped")))
	upgradeCRD(t, c)

	target := &unstructured.Unstructured{}
	target.SetAPIVersion("workloads.x-k8s.io/v1alpha2")
	target.SetKind("RoleInstance")
	target.SetNamespace("default")
	target.SetName("alpha-shaped")
	patch := []byte(`{"metadata":{"labels":{"pr424":"touch"}}}`)
	err := c.Patch(ctx, target, client.RawPatch(types.MergePatchType, patch))

	assert.NoError(t, err,
		"validation ratcheting lets an unrelated write through while the stale shape is stored")
	if err != nil && (errors.IsInvalid(err) || strings.Contains(err.Error(), "restartPolicy")) {
		t.Log("this cluster re-validates the stale field, so in-place repair is NOT possible here")
	}
}
