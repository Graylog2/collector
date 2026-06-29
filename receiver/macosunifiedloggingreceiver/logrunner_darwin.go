// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package macosunifiedloggingreceiver

import (
	"bytes"
	"context"
	"io"
	"os/exec"

	"go.uber.org/zap"
)

// logBinaryPath is the fixed, non-configurable path to the macOS `log` binary
// that is execed. It is integrity-verified before use (see verifyLogBinary).
const logBinaryPath = "/usr/bin/log"

// execLogRunner runs the real, integrity-verified /usr/bin/log binary.
type execLogRunner struct {
	logger *zap.Logger
}

// newExecLogRunner verifies the integrity of /usr/bin/log and returns a runner.
// It returns an error (failing receiver startup) if the required integrity checks fail.
func newExecLogRunner(logger *zap.Logger) (*execLogRunner, error) {
	if err := verifyLogBinary(logger); err != nil {
		return nil, err
	}
	return &execLogRunner{logger: logger}, nil
}

func (r *execLogRunner) Run(ctx context.Context, args []string) (io.ReadCloser, func() (string, error), error) {
	cmd := exec.CommandContext(ctx, logBinaryPath, args...) // #nosec G204 - logBinaryPath is the fixed, verified /usr/bin/log; args are config-controlled
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	wait := func() (string, error) {
		err := cmd.Wait()
		return stderr.String(), err
	}
	return stdout, wait, nil
}
