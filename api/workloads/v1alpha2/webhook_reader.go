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

package v1alpha2

import (
	"context"
	stderrors "errors"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// cacheStartupFallbackReader reads from the informer cache once it is available.
// Controller-runtime starts webhook servers before caches so conversion webhooks
// cannot deadlock during initial cache sync. A cache read before startup returns
// ErrCacheNotStarted; in that narrow window this reader falls back to the manager's
// direct API reader. This preserves cache-backed reads at steady state while avoiding
// a failed admission during startup.
type cacheStartupFallbackReader struct {
	cache  client.Reader
	direct client.Reader
}

func newCacheStartupFallbackReader(cached, direct client.Reader) client.Reader {
	if direct == nil {
		return cached
	}
	return &cacheStartupFallbackReader{cache: cached, direct: direct}
}

// Get implements client.Reader.
func (r *cacheStartupFallbackReader) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	err := r.cache.Get(ctx, key, obj, opts...)
	if isCacheNotStarted(err) {
		return r.direct.Get(ctx, key, obj, opts...)
	}
	return err
}

// List implements client.Reader.
func (r *cacheStartupFallbackReader) List(
	ctx context.Context,
	list client.ObjectList,
	opts ...client.ListOption,
) error {
	err := r.cache.List(ctx, list, opts...)
	if isCacheNotStarted(err) {
		return r.direct.List(ctx, list, opts...)
	}
	return err
}

func isCacheNotStarted(err error) bool {
	var notStarted *cache.ErrCacheNotStarted
	return stderrors.As(err, &notStarted)
}
