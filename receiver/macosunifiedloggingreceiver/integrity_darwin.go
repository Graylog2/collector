// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package macosunifiedloggingreceiver

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"go.uber.org/zap"
)

const (
	codesignPath = "/usr/bin/codesign"
	// SF_RESTRICTED: file is protected by System Integrity Protection (on the sealed
	// system volume). A planted copy outside the system volume can never carry this flag.
	sfRestricted = 0x80000
)

// verifyLogBinary checks that path is the genuine, SIP-protected /usr/bin/log.
// Layer 1 (filesystem/SIP) is REQUIRED and pure-Go. Layer 2 (codesign) is best-effort:
// it runs only if codesign itself self-verifies, and a missing/untrusted codesign is
// logged and skipped rather than failing startup.
func verifyLogBinary(logger *zap.Logger, path string) error {
	if path != logBinaryPath {
		return fmt.Errorf("refusing to run unexpected log binary path %q", path)
	}
	if err := verifyRestrictedRootFile(path); err != nil {
		return fmt.Errorf("filesystem integrity check failed: %w", err)
	}
	if err := verifyRestrictedRootFile(codesignPath); err != nil {
		logger.Warn("codesign not available for signature verification; relying on SIP/filesystem checks", zap.Error(err))
		return nil
	}
	if err := runCodesignVerify(path); err != nil {
		return fmt.Errorf("code signature verification failed: %w", err)
	}
	return nil
}

// verifyRestrictedRootFile asserts path is a regular, root-owned, non-world-writable,
// SIP-restricted file (not a symlink).
func verifyRestrictedRootFile(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is group/other-writable", path)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read stat for %s", path)
	}
	if st.Uid != 0 || st.Gid != 0 {
		return fmt.Errorf("%s is not owned by root:wheel (uid=%d gid=%d)", path, st.Uid, st.Gid)
	}
	if st.Flags&sfRestricted == 0 {
		return fmt.Errorf("%s is not SIP-restricted", path)
	}
	return nil
}

func runCodesignVerify(path string) error {
	// -R='...' uses codesign's inline requirement syntax (a bare argument is treated as a file).
	cmd := exec.Command(codesignPath, "--verify", "--strict",
		`-R=anchor apple and identifier "com.apple.log"`, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}
