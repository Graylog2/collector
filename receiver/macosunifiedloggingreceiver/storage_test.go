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
	mu     sync.Mutex
	m      map[string][]byte
	closes int
	// sets counts Set calls; canceledSets counts those whose context was already done on
	// arrival. Recorded as a bool at call time rather than by retaining the ctx, which would
	// outlive the call it belongs to.
	sets         int
	canceledSets int
}

func newMemStorage() *memStorage { return &memStorage{m: map[string][]byte{}} }

// setStats reports how many Set calls arrived in total and how many arrived with an
// already-canceled context.
func (s *memStorage) setStats() (total, canceled int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sets, s.canceledSets
}

// closeCount reports how many times Close was called, so a test can assert Shutdown
// releases the storage client instead of leaking it.
func (s *memStorage) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closes
}

func (s *memStorage) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[key], nil
}

func (s *memStorage) Set(ctx context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sets++
	if ctx.Err() != nil {
		s.canceledSets++
	}
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

func (s *memStorage) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closes++
	return nil
}

// errGetStorage behaves like memStorage except that Get always fails, standing in for a
// corrupt or unreadable backing store. Everything else (notably Close) still works, so a test
// can assert Shutdown releases the client even when Start bailed out.
type errGetStorage struct {
	*memStorage
	err error
}

func (s *errGetStorage) Get(context.Context, string) ([]byte, error) { return nil, s.err }

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
