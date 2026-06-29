// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"context"
	"io"
)

// logRunner starts the macOS `log` binary and exposes its stdout stream. It is the
// platform seam: the real implementation (execLogRunner) is darwin-only, while tests
// inject a fake so the poll loop and parsing are exercised on any OS.
type logRunner interface {
	// Run starts `log` with args. It returns a reader over stdout, a wait func that
	// blocks until the process exits (returning captured stderr and the exit error),
	// and a start error. The caller must always invoke wait to reap the process.
	Run(ctx context.Context, args []string) (stdout io.ReadCloser, wait func() (string, error), err error)
}
