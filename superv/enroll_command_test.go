// Copyright (C)  2026 Graylog, Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the Server Side Public License, version 1,
// as published by MongoDB, Inc.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// Server Side Public License for more details.
//
// You should have received a copy of the Server Side Public License
// along with this program. If not, see
// <http://www.mongodb.com/licensing/server-side-public-license>.
//
// SPDX-License-Identifier: SSPL-1.0

package superv_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Graylog2/collector/superv"
	"github.com/Graylog2/collector/superv/internal/testserver"
)

func writeEnrollConfig(t *testing.T, enrollmentEndpoint, enrollmentToken string) (configPath string, keysDir string) {
	t.Helper()

	dir := t.TempDir()
	keysDir = filepath.Join(dir, "keys")
	configPath = filepath.Join(dir, "config.yml")

	content := fmt.Sprintf(`
server:
  auth:
    enrollment_endpoint: %q
    enrollment_token: %q
keys:
  dir: %q
persistence:
  dir: %q
`, enrollmentEndpoint, enrollmentToken, keysDir, filepath.Join(dir, "data"))

	require.NoError(t, os.WriteFile(configPath, []byte(content), 0600))
	return configPath, keysDir
}

func TestEnrollCommand_FreshEnrollment(t *testing.T) {
	server, err := testserver.New()
	require.NoError(t, err)
	server.Start()
	defer server.Stop()

	token, err := server.CreateEnrollmentJWT("test", time.Hour)
	require.NoError(t, err)

	configPath, keysDir := writeEnrollConfig(t, server.URL(), token)

	cmd := superv.GetEnrollCommand()
	cmd.SetArgs([]string{"--config", configPath, "--insecure"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	require.NoError(t, cmd.ExecuteContext(ctx))

	// Credentials must exist on disk.
	require.FileExists(t, filepath.Join(keysDir, "signing.key"))
}

func TestEnrollCommand_AlreadyEnrolled(t *testing.T) {
	server, err := testserver.New()
	require.NoError(t, err)
	server.Start()
	defer server.Stop()

	token, err := server.CreateEnrollmentJWT("test", time.Hour)
	require.NoError(t, err)

	configPath, keysDir := writeEnrollConfig(t, server.URL(), token)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first := superv.GetEnrollCommand()
	first.SetArgs([]string{"--config", configPath, "--insecure"})
	require.NoError(t, first.ExecuteContext(ctx))

	keyBefore, err := os.ReadFile(filepath.Join(keysDir, "signing.key"))
	require.NoError(t, err)

	// A second run must succeed as a no-op and leave the credentials untouched.
	second := superv.GetEnrollCommand()
	second.SetArgs([]string{"--config", configPath, "--insecure"})
	require.NoError(t, second.ExecuteContext(ctx))

	keyAfter, err := os.ReadFile(filepath.Join(keysDir, "signing.key"))
	require.NoError(t, err)
	require.Equal(t, keyBefore, keyAfter)
}

func TestEnrollCommand_TimeoutFlag(t *testing.T) {
	cmd := superv.GetEnrollCommand()
	flag := cmd.Flags().Lookup("timeout")
	require.NotNil(t, flag, "enroll command must have a --timeout flag")
	require.Equal(t, "1m0s", flag.DefValue)
}
