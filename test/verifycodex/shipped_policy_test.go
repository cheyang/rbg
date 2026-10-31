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

package verifycodex

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// TestShippedWarningOnlyPolicy (contract, ledger F2, fixed in 22acc570):
// the SHIPPED webhook configuration bytes must register the four warning-only
// v1alpha1 deprecation webhooks (instance, instanceset, scalingadapter,
// clusterengineruntimeprofile) with failurePolicy=Ignore -- their handlers can
// never reject, so failing closed would block still-supported v1alpha1 writes
// during any webhook outage for zero enforcement value -- while the two
// enforcing RBG/RBGSet endpoints stay failurePolicy=Fail, and none of the six
// registers DELETE (the delete path stays unblocked; CRD-level
// deprecationWarning covers deletes).
//
// Polarity: contract -- green only while the shipped bytes keep this shape.
// On the pre-fix head 993dcf10 this test FAILS (all six shipped Fail); the
// mechanism demo lives in TestWarningOnlyWebhookOutage.
func TestShippedWarningOnlyPolicy(t *testing.T) {
	_, thisFile, _, _ := goruntime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))

	enforcing := map[string]bool{
		"vrolebasedgroup-v1alpha1-deprecation.kb.io":    true,
		"vrolebasedgroupset-v1alpha1-deprecation.kb.io": true,
	}

	for _, rel := range []string{
		filepath.Join("config", "webhook", "manifests.yaml"),
		filepath.Join("deploy", "kubectl", "manifests.yaml"),
	} {
		hooks := shippedDeprecationWebhooks(t, filepath.Join(repoRoot, rel))
		if len(hooks) != 6 {
			t.Fatalf("%s: expected 6 v1alpha1 deprecation webhooks, found %d (%v)", rel, len(hooks), keys(hooks))
		}
		for name, wh := range hooks {
			if wh.FailurePolicy == nil {
				t.Fatalf("%s: %s has no failurePolicy", rel, name)
			}
			want := admissionregistrationv1.Ignore
			if enforcing[name] {
				want = admissionregistrationv1.Fail
			}
			if *wh.FailurePolicy != want {
				t.Errorf("%s: %s failurePolicy=%s, want %s", rel, name, *wh.FailurePolicy, want)
			}
			for _, rule := range wh.Rules {
				for _, op := range rule.Operations {
					if op == admissionregistrationv1.Delete {
						t.Errorf("%s: %s intercepts DELETE (delete path must stay unblocked)", rel, name)
					}
				}
			}
		}
	}
}

func shippedDeprecationWebhooks(t *testing.T, path string) map[string]admissionregistrationv1.ValidatingWebhook {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := map[string]admissionregistrationv1.ValidatingWebhook{}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		var raw map[string]interface{}
		err := decoder.Decode(&raw)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if raw == nil || raw["kind"] != "ValidatingWebhookConfiguration" {
			continue
		}
		config := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, config); err != nil {
			t.Fatalf("convert %s: %v", path, err)
		}
		for _, wh := range config.Webhooks {
			if strings.HasSuffix(wh.Name, "-v1alpha1-deprecation.kb.io") {
				out[wh.Name] = wh
			}
		}
	}
	return out
}

func keys(m map[string]admissionregistrationv1.ValidatingWebhook) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
