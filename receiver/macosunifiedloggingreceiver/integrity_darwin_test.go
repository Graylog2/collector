// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package macosunifiedloggingreceiver

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestVerifyRestrictedRootFile_RejectsNonRestricted(t *testing.T) {
	// A file we create is owned by us and not SF_RESTRICTED -> must be rejected.
	p := filepath.Join(t.TempDir(), "fakelog")
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestrictedRootFile(p); err == nil {
		t.Fatal("expected rejection of a non-restricted, non-root file")
	}
}

func TestVerifyLogBinary_RealBinaryPasses(t *testing.T) {
	// The genuine /usr/bin/log on a SIP-enabled Mac must pass both layers.
	if err := verifyLogBinary(zap.NewNop(), "/usr/bin/log"); err != nil {
		t.Fatalf("genuine /usr/bin/log failed verification: %v", err)
	}
}

func TestVerifyLogBinary_RejectsWrongPath(t *testing.T) {
	if err := verifyLogBinary(zap.NewNop(), "/tmp/log"); err == nil {
		t.Fatal("expected rejection of a non-/usr/bin/log path")
	}
}
