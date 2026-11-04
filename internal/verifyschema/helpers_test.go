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

package verifyschema

import (
	"sort"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/managedfields"
	fieldpath "sigs.k8s.io/structured-merge-diff/v6/fieldpath"
)

type managedFieldsTypeConverter = managedfields.TypeConverter

var (
	serverOnce      sync.Once
	serverModels    *ServerModels
	serverConverter managedFieldsTypeConverter
	serverErr       error
)

// serverParity loads the server-side models (from config/crd/bases) and
// builds the type converter, once per test binary.
func serverParity(t *testing.T) (managedFieldsTypeConverter, []KindModel) {
	t.Helper()
	serverOnce.Do(func() {
		serverModels, serverErr = BuildServerModels(".")
		if serverErr != nil {
			return
		}
		serverConverter, serverErr = serverModels.TypeConverter()
	})
	if serverErr != nil {
		t.Fatalf("failed to build server models/converter: %v", serverErr)
	}
	return serverConverter, serverModels.Kinds
}

// fieldSetOf converts obj to a typed value with tc and returns its field
// set, as the API server does when recording managed fields.
func fieldSetOf(tc managedFieldsTypeConverter, obj *unstructured.Unstructured) (*fieldpath.Set, error) {
	tv, err := tc.ObjectToTyped(obj)
	if err != nil {
		return nil, err
	}
	return tv.ToFieldSet()
}

// leaves returns the sorted leaf paths of a field set.
func leaves(s *fieldpath.Set) []string {
	if s == nil {
		return nil
	}
	out := []string{}
	s.Iterate(func(p fieldpath.Path) {
		out = append(out, p.String())
	})
	sort.Strings(out)
	return out
}
