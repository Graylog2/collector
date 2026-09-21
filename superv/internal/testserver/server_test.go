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

package testserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Graylog2/collector/superv/auth"
)

func TestServer_JWKS(t *testing.T) {
	server, err := New()
	require.NoError(t, err)

	url := server.Start()
	defer server.Stop()

	// Fetch JWKS
	client := server.Client()
	keys, err := auth.FetchJWKS(t.Context(), client, url)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, server.KeyID, keys[0].KeyID)
}

func TestServer_EnrollmentJWT(t *testing.T) {
	server, err := New()
	require.NoError(t, err)

	url := server.Start()
	defer server.Stop()

	// Create enrollment JWT
	jwt, err := server.CreateEnrollmentJWT("test", time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, jwt)

	// Validate it using our auth package
	client := server.Client()
	keys, err := auth.FetchJWKS(t.Context(), client, url)
	require.NoError(t, err)

	claims, err := auth.ValidateEnrollmentJWT(jwt, keys)
	require.NoError(t, err)
	require.Equal(t, "test", claims.Issuer)
}

// TestServer_HandlerServesAllRoutes guards against route drift between
// Start() and consumers that mount the handler themselves (cmd/testserver):
// every server endpoint must be reachable through Handler().
func TestServer_HandlerServesAllRoutes(t *testing.T) {
	server, err := New()
	require.NoError(t, err)

	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	for _, path := range []string{"/.well-known/jwks.json", "/v1/opamp", auth.EnrollmentAuthCheckPath} {
		resp, err := http.Get(httpServer.URL + path)
		require.NoError(t, err, path)
		require.NoError(t, resp.Body.Close())
		require.NotEqual(t, http.StatusNotFound, resp.StatusCode, path)
	}
}

func TestServer_EnrollAuthCheck(t *testing.T) {
	server, err := New()
	require.NoError(t, err)

	url := server.Start()
	defer server.Stop()

	jwt, err := server.CreateEnrollmentJWT("test", time.Hour)
	require.NoError(t, err)

	get := func(authHeader string) int {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url+auth.EnrollmentAuthCheckPath, nil)
		require.NoError(t, err)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		resp, err := server.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode
	}

	require.Equal(t, http.StatusOK, get(auth.BearerToken(jwt)))
	require.Equal(t, http.StatusUnauthorized, get("Bearer not-a-valid-token"))
	require.Equal(t, http.StatusUnauthorized, get(""))
}

// Like HandleOpAMP, the auth check must accept anything when RequireAuth is
// disabled, so tests that enroll with dummy tokens keep working.
func TestServer_EnrollAuthCheck_RequireAuthDisabled(t *testing.T) {
	server, err := New()
	require.NoError(t, err)
	server.RequireAuth = false

	url := server.Start()
	defer server.Stop()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url+auth.EnrollmentAuthCheckPath, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", auth.BearerToken("dummy-token"))

	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
