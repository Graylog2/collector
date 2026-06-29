// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/xextension/storage"
)

type fakeStorageExt struct{ client storage.Client }

func (f *fakeStorageExt) Start(context.Context, component.Host) error { return nil }
func (f *fakeStorageExt) Shutdown(context.Context) error              { return nil }
func (f *fakeStorageExt) GetClient(context.Context, component.Kind, component.ID, string) (storage.Client, error) {
	return f.client, nil
}

type fakeHost struct{ exts map[component.ID]component.Component }

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
