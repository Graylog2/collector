// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/xextension/storage"
)

const cursorStorageKey = "cursor"

// getStorageClient resolves a storage.Client for cursor persistence. Adapted from
// pkg/stanza/adapter.GetStorageClient (Apache-2.0).
func getStorageClient(ctx context.Context, host component.Host, storageID *component.ID, id component.ID) (storage.Client, error) {
	if storageID == nil {
		return nil, errors.New("storage extension is required for live mode (set 'storage:')")
	}
	ext, ok := host.GetExtensions()[*storageID]
	if !ok {
		return nil, fmt.Errorf("storage extension %q not found", storageID)
	}
	se, ok := ext.(storage.Extension)
	if !ok {
		return nil, fmt.Errorf("non-storage extension %q configured as storage", storageID)
	}
	return se.GetClient(ctx, component.KindReceiver, id, "")
}
