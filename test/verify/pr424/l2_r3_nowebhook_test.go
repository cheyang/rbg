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

// Round 3. 04ecd11c deleted RoleBasedGroupDefaulter and the whole
// MutatingWebhookConfiguration, which is what made the deprecated field inert.
// The round-1/2 webhook harness no longer compiles, so it is replaced here: same
// claims, but asserted against a plain API server with no defaulting webhook.
package pr424

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// startPlainEnv boots an API server with the project CRDs and no webhooks, which
// is what this release now ships for RoleBasedGroup defaulting.
func startPlainEnv(t *testing.T) (client.Client, func()) {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	return c, func() { _ = env.Stop() }
}

func r3RBG(ns, name string, legacy workloadsv1alpha2.RestartPolicyType) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{
				Name:     "role-1",
				Replicas: ptr.To(int32(1)),
				Pattern: workloadsv1alpha2.Pattern{
					LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{
						Size:          ptr.To(int32(2)),
						RestartPolicy: legacy, //nolint:staticcheck // the field under test
						TemplateSource: workloadsv1alpha2.TemplateSource{
							Template: &corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{{Name: "main", Image: "nginx:latest"}},
								},
							},
						},
					},
				},
			}},
		},
	}
}

// F2 / L2 — CONTRACT, round 3. The round-1 repro: create with only the deprecated
// field, then flip it the way `kubectl edit` does. With the defaulter gone there is
// no stored restartPolicyConfig to shadow it, so the edit must take effect.
func TestL2_R3_KubectlEditOfDeprecatedFieldTakesEffect(t *testing.T) {
	c, stop := startPlainEnv(t)
	defer stop()

	ctx := context.Background()
	key := client.ObjectKey{Namespace: "default", Name: "r3-edit"}

	require.NoError(t, c.Create(ctx, r3RBG("default", "r3-edit",
		workloadsv1alpha2.RecreateRoleInstanceOnPodRestart)))

	stored := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx, key, stored))
	assert.Nil(t, stored.Spec.Roles[0].LeaderWorkerPattern.RestartPolicyConfig,
		"nothing should materialize restartPolicyConfig now that the defaulter is gone")
	require.Equal(t, workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
		stored.Spec.Roles[0].GetRestartPolicy())

	stored.Spec.Roles[0].LeaderWorkerPattern.RestartPolicy = workloadsv1alpha2.RestartPolicyNone //nolint:staticcheck
	require.NoError(t, c.Update(ctx, stored))

	after := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx, key, after))
	assert.Equal(t, workloadsv1alpha2.RestartPolicyNone, after.Spec.Roles[0].GetRestartPolicy(),
		"editing the deprecated field must now take effect")
}

// F2 / L2 — CONTRACT, round 3. Same claim through a targeted JSON patch.
func TestL2_R3_TargetedJSONPatchOfDeprecatedFieldTakesEffect(t *testing.T) {
	c, stop := startPlainEnv(t)
	defer stop()

	ctx := context.Background()
	key := client.ObjectKey{Namespace: "default", Name: "r3-jsonpatch"}

	require.NoError(t, c.Create(ctx, r3RBG("default", "r3-jsonpatch",
		workloadsv1alpha2.RecreateRoleInstanceOnPodRestart)))

	stored := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx, key, stored))

	patch := []byte(`[{"op":"replace","path":"/spec/roles/0/leaderWorkerPattern/restartPolicy","value":"None"}]`)
	require.NoError(t, c.Patch(ctx, stored, client.RawPatch(types.JSONPatchType, patch)))

	after := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx, key, after))
	assert.Equal(t, workloadsv1alpha2.RestartPolicyNone, after.Spec.Roles[0].GetRestartPolicy(),
		"patching the deprecated field must now take effect")
}

// F2 / L2 — CONTRACT, round 3. An explicit restartPolicyConfig.type must still win,
// which is the precedence the docs and the e2e matrix promise. Removing the
// defaulter must not have removed the precedence itself.
func TestL2_R3_ExplicitConfigStillWinsOverDeprecatedField(t *testing.T) {
	c, stop := startPlainEnv(t)
	defer stop()

	ctx := context.Background()
	rbg := r3RBG("default", "r3-precedence", workloadsv1alpha2.RecreateRoleInstanceOnPodRestart)
	rbg.Spec.Roles[0].LeaderWorkerPattern.RestartPolicyConfig =
		&workloadsv1alpha2.RestartPolicyConfig{Type: workloadsv1alpha2.RestartPolicyNone}
	require.NoError(t, c.Create(ctx, rbg))

	after := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx,
		client.ObjectKey{Namespace: "default", Name: "r3-precedence"}, after))
	assert.Equal(t, workloadsv1alpha2.RestartPolicyNone, after.Spec.Roles[0].GetRestartPolicy(),
		"restartPolicyConfig.type must still outrank the deprecated field when set explicitly")
}
