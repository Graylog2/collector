// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package if any test leaks a goroutine. The receiver spawns a polling
// goroutine in Start and is expected to reap it in Shutdown, so a leak here means Shutdown
// stopped waiting on something it launched.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
