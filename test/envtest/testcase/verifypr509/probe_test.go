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

package verifypr509

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var codec = func() serializer.CodecFactory {
	sch := runtime.NewScheme()
	if err := admissionv1.AddToScheme(sch); err != nil {
		panic(err)
	}
	return serializer.NewCodecFactory(sch)
}()

// captureProbe is a minimal always-allow admission endpoint that records the
// raw oldObject of the most recent request. It exists so the suite can check
// what the API server actually sends a validating webhook as oldObj — the
// v1alpha1 deprecation validator's persisted-object fetch is justified by a
// comment claiming oldObj "may not carry v1alpha1 schema defaults".
type captureProbe struct {
	mu        sync.Mutex
	lastOld   []byte
	lastObj   []byte
	callCount int
}

func (p *captureProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	review := admissionv1.AdmissionReview{}
	if _, _, err := codec.UniversalDeserializer().Decode(body, nil, &review); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.callCount++
	p.lastObj = append([]byte(nil), review.Request.Object.Raw...)
	p.lastOld = append([]byte(nil), review.Request.OldObject.Raw...)
	p.mu.Unlock()

	resp := admissionv1.AdmissionReview{
		TypeMeta: review.TypeMeta,
		Response: &admissionv1.AdmissionResponse{
			UID:     review.Request.UID,
			Allowed: true,
		},
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(respBytes)
}

func (p *captureProbe) snapshot() (obj, old []byte, calls int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastObj, p.lastOld, p.callCount
}
