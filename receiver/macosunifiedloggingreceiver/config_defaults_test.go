// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"testing"
	"time"
)

func TestCreateDefaultConfig_Defaults(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	if cfg.MinPollInterval != time.Second {
		t.Errorf("MinPollInterval default = %v, want 1s", cfg.MinPollInterval)
	}
	if cfg.MaxPollInterval != 30*time.Second {
		t.Errorf("MaxPollInterval default = %v, want 30s", cfg.MaxPollInterval)
	}
	if cfg.StorageID != nil {
		t.Errorf("StorageID default should be nil")
	}
}
