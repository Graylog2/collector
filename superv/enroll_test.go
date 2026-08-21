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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"github.com/Graylog2/collector/superv"
	"github.com/Graylog2/collector/superv/auth"
	"github.com/Graylog2/collector/superv/config"
	"github.com/Graylog2/collector/superv/internal/testserver"
	"github.com/Graylog2/collector/superv/supervisor/connection"
)

// enrollTestConfig returns a config pointing at the given enrollment endpoint
// with temp persistence and keys directories.
func enrollTestConfig(t *testing.T, enrollmentEndpoint, enrollmentToken string) config.Config {
	t.Helper()

	dir := t.TempDir()
	cfg, err := config.Load("")
	require.NoError(t, err)

	cfg.Persistence.Dir = filepath.Join(dir, "data")
	cfg.Keys.Dir = filepath.Join(dir, "keys")
	cfg.Server.Auth.EnrollmentEndpoint = enrollmentEndpoint
	cfg.Server.Auth.EnrollmentToken = enrollmentToken
	cfg.SetInsecure() // test server uses a self-signed TLS certificate

	return cfg
}

func newAuthManager(cfg config.Config) *auth.Manager {
	return auth.NewManager(zap.NewNop(), auth.ManagerConfig{
		KeysDir:     cfg.Keys.Dir,
		InsecureTLS: true,
	})
}

func TestEnroll_FreshEnrollment(t *testing.T) {
	server, err := testserver.New()
	require.NoError(t, err)
	server.Start()
	defer server.Stop()

	token, err := server.CreateEnrollmentJWT("test", time.Hour)
	require.NoError(t, err)

	cfg := enrollTestConfig(t, server.URL(), token)
	logger := zaptest.NewLogger(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	require.NoError(t, superv.Enroll(ctx, logger, cfg))

	// Credentials must be persisted so the supervisor can start enrolled.
	require.True(t, newAuthManager(cfg).IsEnrolled())

	// Connection settings must be persisted with the derived OpAMP endpoint.
	settingsMgr := connection.NewSettingsManager(logger, cfg.Persistence.Dir)
	settings, exists, err := settingsMgr.TryLoadPersisted()
	require.NoError(t, err)
	require.True(t, exists)

	expectedEndpoint, err := config.DeriveEnrollmentEndpoint(server.URL())
	require.NoError(t, err)
	require.Equal(t, expectedEndpoint, settings.Endpoint)
}

func TestEnroll_AlreadyEnrolledIsNoOp(t *testing.T) {
	server, err := testserver.New()
	require.NoError(t, err)
	server.Start()
	defer server.Stop()

	token, err := server.CreateEnrollmentJWT("test", time.Hour)
	require.NoError(t, err)

	cfg := enrollTestConfig(t, server.URL(), token)
	logger := zaptest.NewLogger(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	require.NoError(t, superv.Enroll(ctx, logger, cfg))

	keyBefore, err := os.ReadFile(filepath.Join(cfg.Keys.Dir, "signing.key"))
	require.NoError(t, err)

	// Second run must succeed as a no-op without touching the credentials,
	// even without a valid token.
	cfg.Server.Auth.EnrollmentToken = ""
	require.NoError(t, superv.Enroll(ctx, logger, cfg))

	keyAfter, err := os.ReadFile(filepath.Join(cfg.Keys.Dir, "signing.key"))
	require.NoError(t, err)
	require.Equal(t, keyBefore, keyAfter)
}

func TestEnroll_InvalidToken(t *testing.T) {
	server, err := testserver.New()
	require.NoError(t, err)
	server.Start()
	defer server.Stop()

	cfg := enrollTestConfig(t, server.URL(), "not-a-valid-jwt")
	logger := zaptest.NewLogger(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = superv.Enroll(ctx, logger, cfg)
	require.Error(t, err)
	require.False(t, newAuthManager(cfg).IsEnrolled())
}

func TestEnroll_MissingEnrollmentSettings(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("missing endpoint", func(t *testing.T) {
		cfg := enrollTestConfig(t, "", "some-token")
		err := superv.Enroll(context.Background(), logger, cfg)
		require.ErrorContains(t, err, "enrollment endpoint")
	})

	t.Run("missing token", func(t *testing.T) {
		cfg := enrollTestConfig(t, "https://example.com", "")
		err := superv.Enroll(context.Background(), logger, cfg)
		require.ErrorContains(t, err, "enrollment token")
	})
}

func TestEnroll_FailsFastWhenServerRejectsToken(t *testing.T) {
	// Real JWKS (so local token validation succeeds), but the OpAMP endpoint
	// rejects the connection as unauthorized, e.g. for a token that was
	// revoked server-side.
	server, err := testserver.New()
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", server.HandleJWKS)
	mux.HandleFunc("/v1/opamp", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	httpServer := httptest.NewTLSServer(mux)
	defer httpServer.Close()

	token, err := server.CreateEnrollmentJWT("test", time.Hour)
	require.NoError(t, err)

	cfg := enrollTestConfig(t, httpServer.URL, token)
	logger := zaptest.NewLogger(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	err = superv.Enroll(ctx, logger, cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "401")
	require.NotErrorIs(t, err, context.DeadlineExceeded, "rejection must fail fast, not wait for the timeout")
	require.Less(t, time.Since(start), 10*time.Second)
	require.False(t, newAuthManager(cfg).IsEnrolled())
}

func TestEnroll_TimeoutIncludesLastConnectionError(t *testing.T) {
	// Real JWKS, but the OpAMP endpoint drops every connection at the TCP
	// level, so the client retries until the context expires. The final error
	// must include the underlying connection error, not just the timeout.
	server, err := testserver.New()
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", server.HandleJWKS)
	mux.HandleFunc("/v1/opamp", func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	})
	httpServer := httptest.NewTLSServer(mux)
	defer httpServer.Close()

	token, err := server.CreateEnrollmentJWT("test", time.Hour)
	require.NoError(t, err)

	cfg := enrollTestConfig(t, httpServer.URL, token)
	logger := zaptest.NewLogger(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err = superv.Enroll(ctx, logger, cfg)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "last connection error")
	require.False(t, newAuthManager(cfg).IsEnrolled())
}
