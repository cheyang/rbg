/*
Round-2 addition to the PR #571 verification harness (reviewer: codex).
Additive test file — no production code changed.

R2-F1 (contract), render-path half: F-LWS-DROP — a LeaderWorkerSet-typed role
whose pattern is standalonePattern (a legal, grandfathered shape: conversion
produces it from a v1alpha1 object whose Workload field disagrees with its
structure) must keep main's published LWS behavior for a profileName-only
engineRuntimes reference: the profile's containers/volumes land in the leader
template, the worker template is skipped, no warning, no error. RED on the
round-1 head 1810f5c6 (all content silently dropped); GREEN on f3d4849a.
*/

package reconciler

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	coreapplyv1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerifyPR571R2_LWSConstructDeliversLeader(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = workloadsv1alpha2.AddToScheme(scheme)

	profile := &workloadsv1alpha2.ClusterEngineRuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "ik"},
		Spec: workloadsv1alpha2.ClusterEngineRuntimeProfileSpec{
			Containers: []corev1.Container{{Name: "ik-sidecar", Image: "ik:latest"}},
			Volumes: []corev1.Volume{{
				Name:         "ik-vol",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			}},
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()
	reconciler := NewLeaderWorkerSetReconciler(scheme, fakeClient)
	recorder := record.NewFakeRecorder(16)
	reconciler.SetRecorder(recorder)

	tmpl := corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		Containers: []corev1.Container{{Name: "engine", Image: "engine:latest"}},
	}}
	role := &workloadsv1alpha2.RoleSpec{
		Name:     "sgl",
		Replicas: ptr.To(int32(2)),
		Pattern: workloadsv1alpha2.Pattern{
			StandalonePattern: &workloadsv1alpha2.StandalonePattern{
				TemplateSource: workloadsv1alpha2.TemplateSource{Template: &tmpl},
			},
		},
		EngineRuntimes: []workloadsv1alpha2.EngineRuntime{{ProfileName: "ik"}},
		Annotations: map[string]string{
			constants.RoleWorkloadTypeAnnotationKey: constants.LeaderWorkerSetWorkloadType,
		},
	}
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "lws-verify-rbg", Namespace: "default"},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{*role}},
	}

	result, err := reconciler.constructLWSApplyConfiguration(context.Background(), rbg, role, nil, "verify-revision")
	if err != nil {
		t.Fatalf("constructLWSApplyConfiguration: %v", err)
	}
	lwt := result.Spec.LeaderWorkerTemplate
	if lwt == nil || lwt.LeaderTemplate == nil || lwt.WorkerTemplate == nil {
		t.Fatal("leader/worker templates must both be built")
	}
	hasC := func(spec *coreapplyv1.PodSpecApplyConfiguration, name string) bool {
		if spec == nil {
			return false
		}
		for _, c := range spec.Containers {
			if c.Name != nil && *c.Name == name {
				return true
			}
		}
		return false
	}
	hasV := func(spec *coreapplyv1.PodSpecApplyConfiguration, name string) bool {
		if spec == nil {
			return false
		}
		for _, v := range spec.Volumes {
			if v.Name != nil && *v.Name == name {
				return true
			}
		}
		return false
	}
	if !hasC(lwt.LeaderTemplate.Spec, "ik-sidecar") || !hasV(lwt.LeaderTemplate.Spec, "ik-vol") {
		t.Fatalf("F-LWS-DROP: profile content missing from the leader template (main injected it unconditionally)")
	}
	if hasC(lwt.WorkerTemplate.Spec, "ik-sidecar") || hasV(lwt.WorkerTemplate.Spec, "ik-vol") {
		t.Fatalf("main skipped the worker template on the LWS path; worker must stay as the role wrote it")
	}
	select {
	case ev := <-recorder.Events:
		t.Fatalf("expected no warning events — the render is main's behavior — got: %s", ev)
	default:
	}
}
