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

package reconciler

// Review harness for PR #548 (KEP-531, docs-only). These tests assert, over
// the repository source itself, the code facts that the KEP states about
// main. They intentionally read the source tree (relative to this package)
// so that grafting the harness onto any future ref re-checks the claims
// against THAT code. Additive only: no production code is changed.
//
// CONTRACT polarity throughout: each test asserts the code state the KEP
// documents (or, for S3/S4, the observed reality that contradicts the KEP
// text — see docs/verification/kep-531-profile-injection-codex/README.md).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// S1: KEP "Effective Component Set" — under leaderWorkerPattern the absent-
// component default injects into the LEADER only; both reconcilers configure
// the worker pod reconciler WITHOUT the sidecar injector.
func TestKEP531_S1_WorkerTemplatesSkipSidecarInjection(t *testing.T) {
	ris := readRepoFile(t, "pkg/reconciler/roleinstanceset_reconciler.go")
	if !strings.Contains(ris, "leaderPodReconciler.SetInjectors([]string{configInjector, sidecarInjector, commonEnvInjector, lwpEnvInjector})") {
		t.Error("RIS leader injector chain no longer includes sidecarInjector")
	}
	if !strings.Contains(ris, "workerPodReconciler.SetInjectors([]string{configInjector, commonEnvInjector, lwpEnvInjector})") {
		t.Error("RIS worker injector chain changed: expected no sidecarInjector")
	}
	lws := readRepoFile(t, "pkg/reconciler/lws_reconciler.go")
	if !strings.Contains(lws, "workerPodReconciler.SetInjectors([]string{configInjector, commonEnvInjector})") {
		t.Error("LWS worker injector chain changed: expected [config, common_env] only")
	}
	// LWS leader uses the default injector set (no SetInjectors call) which
	// is [config, sidecar, common_env] per pod_reconciler.go.
	pr := readRepoFile(t, "pkg/reconciler/pod_reconciler.go")
	if !strings.Contains(pr, "r.injectObjects = []string{configInjector, sidecarInjector, commonEnvInjector}") {
		t.Error("default injector set changed")
	}
}

// S2: KEP D3 / Compatibility #4 — injectContainers is optional and inert:
// no consumer outside the API types, generated code, and conversion.
func TestKEP531_S2_InjectContainersInert(t *testing.T) {
	var offenders []string
	for _, root := range []string{"pkg", "internal", "cmd"} {
		_ = filepath.Walk(filepath.Join("..", "..", root), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, _ := os.ReadFile(path)
			if strings.Contains(string(b), "InjectContainers") {
				offenders = append(offenders, path)
			}
			return nil
		})
	}
	if len(offenders) != 0 {
		t.Errorf("injectContainers is consumed by %v — KEP inertness premise broken", offenders)
	}
}

// S3: REALITY CHECK (contradicts KEP Compatibility #6) — neither the
// v1alpha1 nor the v1alpha2 RoleBasedGroup schema caps `roles` or
// `engineRuntimes` with MaxItems today, so the KEP's "matching the
// v1alpha2 cap" premise is false on main; the implementation must tighten
// BOTH published versions.
func TestKEP531_S3_NoMaxItemsCapsExistOnMain(t *testing.T) {
	for _, f := range []string{
		"api/workloads/v1alpha1/rolebasedgroup_types.go",
		"api/workloads/v1alpha2/rolebasedgroup_types.go",
	} {
		if strings.Contains(readRepoFile(t, f), "MaxItems") {
			t.Errorf("%s gained a MaxItems marker — re-check KEP Compatibility #6 wording", f)
		}
	}
	crd := readRepoFile(t, "config/crd/bases/workloads.x-k8s.io_rolebasedgroups.yaml")
	if strings.Contains(crd, "maxItems") {
		t.Error("rolebasedgroups CRD gained maxItems — re-check KEP Compatibility #6 wording")
	}
}

// S4: REALITY CHECK — InstanceComponent.Name (customComponentsPattern) has no
// DNS-label validation today, while the KEP's new `component` field requires
// the DNS-label pattern: existing components with non-DNS-label names could
// not be named in `component`.
func TestKEP531_S4_CustomComponentNamesUnconstrained(t *testing.T) {
	types := readRepoFile(t, "api/workloads/v1alpha2/rolebasedgroup_types.go")
	idx := strings.Index(types, "type InstanceComponent struct")
	if idx < 0 {
		t.Fatal("InstanceComponent struct not found — harness update needed")
	}
	block := types[idx:]
	if end := strings.Index(block, "\n}"); end > 0 {
		block = block[:end]
	}
	if strings.Contains(block, "kubebuilder:validation:Pattern") {
		t.Error("InstanceComponent.Name gained a Pattern — the component-field DNS-label mismatch noted in the KEP review may be resolved")
	}
}

// S5: KEP "Consent and Trust" premise — ClusterEngineRuntimeProfile is
// cluster-scoped.
func TestKEP531_S5_CERPClusterScoped(t *testing.T) {
	cerp := readRepoFile(t, "api/workloads/v1alpha2/clusterengineruntimeprofile_types.go")
	if !strings.Contains(cerp, "+kubebuilder:resource:scope=Cluster") {
		t.Error("CERP is no longer cluster-scoped — the KEP trust premise changes")
	}
}

// S6: KEP gap evidence (issue #531 item 4) — the RBG controller does not
// watch ClusterEngineRuntimeProfile, so profile edits do not trigger
// re-render; CERP spec.updateStrategy is unconsumed.
func TestKEP531_S6_ProfileEditsDoNotPropagate(t *testing.T) {
	ctrl := readRepoFile(t, "internal/controller/workloads/rolebasedgroup_controller.go")
	if strings.Contains(ctrl, "ClusterEngineRuntimeProfile") {
		t.Error("RBG controller now references CERP — re-check the KEP's missing updateStrategy/propagation discussion")
	}
	// The only non-generated production consumer of CERP spec content remains the
	// sidecar builder. Base-move note (fd160cc9, PR #524): cmd/rbgs/main.go gained
	// a second reference — the v1alpha1 CERP *deprecation* webhook registration
	// (warning-only admission, failurePolicy: Ignore, v1alpha1 CREATE/UPDATE only,
	// config/webhook/manifests.yaml) — which is neither a controller watch nor a
	// spec consumer, so the pinned premises (no CERP-driven re-render,
	// updateStrategy unconsumed) still hold. Any OTHER new consumer is a real
	// premise change and fails here.
	var consumers []string
	for _, root := range []string{"pkg", "internal", "cmd"} {
		_ = filepath.Walk(filepath.Join("..", "..", root), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, _ := os.ReadFile(path)
			if strings.Contains(string(b), "ClusterEngineRuntimeProfile") {
				consumers = append(consumers, path)
			}
			return nil
		})
	}
	allowed := map[string]bool{
		"pkg/discovery/sidecar_builder.go": true, // the render-path consumer
		"cmd/rbgs/main.go":                 true, // v1alpha1 deprecation webhook registration (warning-only)
	}
	for _, c := range consumers {
		normalized := strings.TrimPrefix(filepath.ToSlash(c), "../../")
		if !allowed[normalized] {
			t.Errorf("new CERP consumer %s — re-check KEP premises (updateStrategy, render path, no CERP watch)", c)
		}
	}
	// updateStrategy stays unconsumed: nothing reads CERP's Spec.UpdateStrategy.
	for _, root := range []string{"pkg", "internal", "cmd"} {
		_ = filepath.Walk(filepath.Join("..", "..", root), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, _ := os.ReadFile(path)
			if strings.Contains(string(b), ".Spec.UpdateStrategy") && strings.Contains(string(b), "ClusterEngineRuntimeProfile") {
				t.Errorf("CERP updateStrategy now consumed in %s — the KEP's deprecation premise is stale", path)
			}
			return nil
		})
	}
}

// S7: KEP "Render Pipeline" — templateRef is prohibited for LWS workloads.
func TestKEP531_S7_TemplateRefProhibitedForLWS(t *testing.T) {
	v := readRepoFile(t, "api/workloads/v1alpha2/roletemplate_validation.go")
	if !strings.Contains(v, "not supported for LeaderWorkerSet workloads") {
		t.Error("templateRef/LWS prohibition not found — KEP render-pipeline description stale")
	}
}

// S8: REALITY CHECK (contradicts a KEP mechanism citation) — duplicate
// mountPath inside one container is rejected by apiserver validation
// ("must be unique"), not "resolved last-wins by the kubelet" as the KEP's
// Patch Grammar section states. The KEP's render-error rule stays correct;
// only its justification is wrong.
func TestKEP531_S8_ApiserverRejectsDuplicateMountPath(t *testing.T) {
	v := readRepoFile(t, filepath.Join("vendor", "k8s.io", "kubernetes", "pkg", "apis", "core", "validation", "validation.go"))
	if !strings.Contains(v, `idxPath.Child("mountPath"), mnt.MountPath, "must be unique"`) {
		t.Error("apiserver mountPath uniqueness validation not found — re-check the KEP's 'kubelet last-wins' claim")
	}
}

// S9: REALITY CHECK (contradicts a KEP mechanism citation introduced at head
// d84004a2) — Patch Grammar's "rendered template is duplicate-free" bullet
// claims "the RoleInstanceSet reconciler — the one apply path that carries the
// new fields — applies workloads via server-side apply, whose `env` list is
// keyed by name and rejects duplicates". The applied object is a
// RoleInstanceSet CR whose spec.roleInstanceTemplate is schemaless
// (x-kubernetes-preserve-unknown-fields: true): inside that subtree no list is
// map-keyed, so SSA has no associative-list semantics for `env` there and
// never rejects duplicate entries. The duplicate-free guarantee therefore
// rests entirely on the controller-side normalization rule, not on the
// apiserver. This test pins the CRD fact so the citation stays checkable.
func TestKEP531_S9_RISTemplateSchemalessSoSSACannotRejectDuplicateEnv(t *testing.T) {
	crd := readRepoFile(t, filepath.Join("config", "crd", "bases", "workloads.x-k8s.io_roleinstancesets.yaml"))
	j, err := yaml.YAMLToJSON([]byte(crd))
	if err != nil {
		t.Fatalf("parse RIS CRD: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(j, &doc); err != nil {
		t.Fatalf("unmarshal RIS CRD: %v", err)
	}
	// locate spec.roleInstanceTemplate under the (single) served version schema
	versions, _ := doc["spec"].(map[string]interface{})["versions"].([]interface{})
	if len(versions) == 0 {
		t.Fatal("RIS CRD has no versions")
	}
	props := versions[0].(map[string]interface{})["schema"].(map[string]interface{})["openAPIV3Schema"].(map[string]interface{})["properties"].(map[string]interface{})
	tmpl, ok := props["spec"].(map[string]interface{})["properties"].(map[string]interface{})["roleInstanceTemplate"].(map[string]interface{})
	if !ok {
		t.Fatal("spec.roleInstanceTemplate not found in RIS CRD")
	}
	if tmpl["x-kubernetes-preserve-unknown-fields"] != true {
		t.Error("roleInstanceTemplate is no longer schemaless — the KEP's SSA env-keying claim may need re-checking")
	}
	// belt-and-braces: no map-keyed env list anywhere in the RIS schema
	if strings.Contains(crd, "x-kubernetes-list-map-keys") {
		t.Error("RIS CRD gained a map-keyed list — re-check the SSA duplicate-env claim")
	}
}
