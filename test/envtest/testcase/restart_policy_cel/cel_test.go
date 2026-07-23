/*
Copyright 2026 The RBG Authors.
Licensed under the Apache License, Version 2.0 (the "License");
*/

// Verification harness (L2 / integration) for sgl-project/rbg PR #409, Finding #4.
// ADDITIVE ONLY — spins up an isolated envtest API server loaded with the PR's
// CRDs (config/crd/bases) and exercises the new CEL rule + kubebuilder defaults on
// RestartPolicyConfig. No production code and no shared cluster are touched.
package restart_policy_cel

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

var (
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
)

func TestMain(m *testing.M) {
	_, thisFile, _, _ := runtime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "config", "crd", "bases")

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}
	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		panic("failed to start envtest: " + err.Error())
	}
	if err := workloadsv1alpha2.AddToScheme(scheme.Scheme); err != nil {
		panic(err)
	}
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		panic(err)
	}
	code := m.Run()
	_ = testEnv.Stop()
	os.Exit(code)
}

func newRoleInstance(name string, rp workloadsv1alpha2.RestartPolicyConfig) *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicy: rp,
			Components: []workloadsv1alpha2.RoleInstanceComponent{
				{
					Name: "main",
					Size: ptr.To(int32(1)),
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "c", Image: "busybox"}},
						},
					},
				},
			},
		},
	}
}

// Finding #4 (CONTRACT): with maxDelaySeconds now defaulting to 600, a config that sets
// baseDelaySeconds > 600 and OMITS maxDelaySeconds is rejected by the new CEL rule
// (defaulting fills max=600, then 600 >= 700 is false). This documents a behavior change:
// such a config was silently accepted (and capped at 600 at runtime) before this PR.
func TestVerifyPR409_F4_CEL_And_Defaulting(t *testing.T) {
	ctx := context.Background()

	t.Run("base>600 with max omitted is REJECTED (default max=600 < base)", func(t *testing.T) {
		ri := newRoleInstance("vpr409-f4-reject", workloadsv1alpha2.RestartPolicyConfig{
			Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			BaseDelaySeconds: ptr.To(int32(700)),
			// MaxDelaySeconds intentionally omitted → defaults to 600
		})
		err := k8sClient.Create(ctx, ri)
		if err == nil {
			_ = k8sClient.Delete(ctx, ri)
			t.Fatalf("expected CEL rejection, but Create succeeded")
		}
		if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "maxDelaySeconds must be greater than or equal") {
			t.Fatalf("expected CEL invalid error, got: %v", err)
		}
		t.Logf("REJECTED as expected: %v", err)
	})

	t.Run("base=30 (default) with max omitted is ACCEPTED, and max defaults to 600", func(t *testing.T) {
		ri := newRoleInstance("vpr409-f4-defaults", workloadsv1alpha2.RestartPolicyConfig{
			Type: workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			// both delays omitted → base defaults 30, max defaults 600
		})
		if err := k8sClient.Create(ctx, ri); err != nil {
			t.Fatalf("expected accept, got: %v", err)
		}
		defer k8sClient.Delete(ctx, ri)
		got := &workloadsv1alpha2.RoleInstance{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(ri), got); err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Spec.RestartPolicy.BaseDelaySeconds == nil || *got.Spec.RestartPolicy.BaseDelaySeconds != 30 {
			t.Fatalf("expected baseDelaySeconds defaulted to 30, got %v", got.Spec.RestartPolicy.BaseDelaySeconds)
		}
		if got.Spec.RestartPolicy.MaxDelaySeconds == nil || *got.Spec.RestartPolicy.MaxDelaySeconds != 600 {
			t.Fatalf("expected maxDelaySeconds defaulted to 600, got %v", got.Spec.RestartPolicy.MaxDelaySeconds)
		}
		t.Logf("ACCEPTED; server-side defaults applied: base=%d max=%d",
			*got.Spec.RestartPolicy.BaseDelaySeconds, *got.Spec.RestartPolicy.MaxDelaySeconds)
	})

	t.Run("base=700 with explicit max=800 is ACCEPTED (control)", func(t *testing.T) {
		ri := newRoleInstance("vpr409-f4-ok", workloadsv1alpha2.RestartPolicyConfig{
			Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			BaseDelaySeconds: ptr.To(int32(700)),
			MaxDelaySeconds:  ptr.To(int32(800)),
		})
		if err := k8sClient.Create(ctx, ri); err != nil {
			t.Fatalf("expected accept for max>=base, got: %v", err)
		}
		_ = k8sClient.Delete(ctx, ri)
	})

	t.Run("explicit max<base is REJECTED (CEL sanity)", func(t *testing.T) {
		ri := newRoleInstance("vpr409-f4-explicit-bad", workloadsv1alpha2.RestartPolicyConfig{
			Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			BaseDelaySeconds: ptr.To(int32(100)),
			MaxDelaySeconds:  ptr.To(int32(50)),
		})
		err := k8sClient.Create(ctx, ri)
		if err == nil {
			_ = k8sClient.Delete(ctx, ri)
			t.Fatalf("expected CEL rejection for max<base")
		}
		if !strings.Contains(err.Error(), "maxDelaySeconds must be greater than or equal") {
			t.Fatalf("expected CEL message, got: %v", err)
		}
	})
}
