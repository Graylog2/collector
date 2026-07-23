// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/xextension/storage"
)

// memStorage is an in-memory storage.Client for tests. Unlike storage.NewNopClient it
// actually retains values, so a cursor can be pre-seeded and then read back by Start.
type memStorage struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemStorage() *memStorage { return &memStorage{m: map[string][]byte{}} }

func (s *memStorage) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[key], nil
}

func (s *memStorage) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func (s *memStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

func (s *memStorage) Batch(_ context.Context, ops ...*storage.Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, op := range ops {
		switch op.Type {
		case storage.Get:
			op.Value = s.m[op.Key]
		case storage.Set:
			s.m[op.Key] = op.Value
		case storage.Delete:
			delete(s.m, op.Key)
		}
	}
	return nil
}

func (s *memStorage) Close(context.Context) error { return nil }

type fakeStorageExt struct{ client storage.Client }

func (f *fakeStorageExt) Start(context.Context, component.Host) error { return nil }
func (f *fakeStorageExt) Shutdown(context.Context) error              { return nil }
func (f *fakeStorageExt) GetClient(context.Context, component.Kind, component.ID, string) (storage.Client, error) {
	return f.client, nil
}

type fakeHost struct {
	exts map[component.ID]component.Component
}

func (h fakeHost) GetExtensions() map[component.ID]component.Component { return h.exts }

func TestGetStorageClient(t *testing.T) {
	id := component.MustNewID("file_storage")
	host := fakeHost{exts: map[component.ID]component.Component{
		id: &fakeStorageExt{client: storage.NewNopClient()},
	}}
	// configured
	if _, err := getStorageClient(context.Background(), host, &id, component.MustNewID("macos_unified_logging")); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	// nil storageID -> error
	if _, err := getStorageClient(context.Background(), host, nil, component.MustNewID("macos_unified_logging")); err == nil {
		t.Fatal("expected error when storageID is nil")
	}
	// missing extension -> error
	missing := component.MustNewID("nope")
	if _, err := getStorageClient(context.Background(), host, &missing, component.MustNewID("macos_unified_logging")); err == nil {
		t.Fatal("expected error when extension is absent")
	}
}
