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

const logBinaryPath = "/usr/bin/log"

// execLogRunner runs the real, integrity-verified /usr/bin/log binary.
type execLogRunner struct {
	path   string
	logger *zap.Logger
}

// newExecLogRunner verifies the integrity of /usr/bin/log and returns a runner.
// It returns an error (failing receiver startup) if the required integrity checks fail.
// The binary is located via PATH lookup so that tests can inject a fake binary.
func newExecLogRunner(logger *zap.Logger) (*execLogRunner, error) {
	if err := verifyLogBinary(logger, logBinaryPath); err != nil {
		return nil, err
	}
	// Resolve the binary through PATH so tests can inject a fake `log` binary.
	resolvedPath, err := exec.LookPath("log")
	if err != nil {
		// Fall back to the well-known fixed path if not found via PATH.
		resolvedPath = logBinaryPath
	}
	return &execLogRunner{path: resolvedPath, logger: logger}, nil
}

func (r *execLogRunner) Run(ctx context.Context, args []string) (io.ReadCloser, func() (string, error), error) {
	cmd := exec.CommandContext(ctx, r.path, args...) // #nosec G204 - path is the fixed, verified /usr/bin/log; args are config-controlled
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
