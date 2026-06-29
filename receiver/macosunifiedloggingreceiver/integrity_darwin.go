// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package macosunifiedloggingreceiver

import "go.uber.org/zap"

// verifyLogBinary checks the integrity of the log binary at the given path.
// This is a temporary stub; Task 11 replaces it with real integrity checks.
func verifyLogBinary(_ *zap.Logger, _ string) error {
	return nil
}
