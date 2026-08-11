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

// Run execs the verified `log` binary with args and returns its stdout plus a wait func.
//
// args carry untrusted input: the predicate comes from collector config, which is pushed from a
// central fleet manager by operators who do not necessarily have shell access to this host. Two
// properties keep that safe and must be preserved:
//
//   - exec passes args directly to the process, never through a shell, so shell metacharacters
//     in a predicate are inert bytes. Do not reintroduce a shell here.
//   - each arg is one argv element (see liveArgs), so a predicate cannot smuggle extra flags and
//     change what is read. Never build the command as one string and split it.
//
// The predicate's grammar is `log`'s to enforce; a bad one exits non-zero and the error, with
// stderr, is returned to pollOnce.
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
