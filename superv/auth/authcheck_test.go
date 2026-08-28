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

package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Graylog2/collector/superv/auth"
)

func authCheckManager(t *testing.T, client *http.Client) *auth.Manager {
	t.Helper()
	return auth.NewManager(zap.NewNop(), auth.ManagerConfig{
		KeysDir:    t.TempDir(),
		HTTPClient: client,
	})
}

func TestCheckEnrollmentToken_SendsAuthenticatedGetRequest(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
	}))
	defer server.Close()

	mgr := authCheckManager(t, server.Client())
	require.NoError(t, mgr.CheckEnrollmentToken(context.Background(), server.URL, "test-token", nil))

	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, auth.EnrollmentAuthCheckPath, gotPath)
	assert.Equal(t, "Bearer test-token", gotAuth)
}

func TestCheckEnrollmentToken_SendsConfiguredEnrollmentHeaders(t *testing.T) {
	var gotAPIKey, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-Api-Key")
		gotAuth = r.Header.Get("Authorization")
	}))
	defer server.Close()

	mgr := authCheckManager(t, server.Client())
	headers := map[string]string{"X-Api-Key": "gateway-key", "Authorization": "must-not-override"}
	require.NoError(t, mgr.CheckEnrollmentToken(context.Background(), server.URL, "test-token", headers))

	assert.Equal(t, "gateway-key", gotAPIKey)
	assert.Equal(t, "Bearer test-token", gotAuth)
}

func TestCheckEnrollmentToken_FailsOnNon200(t *testing.T) {
	tests := []struct {
		status  int
		wantErr string
	}{
		{http.StatusUnauthorized, "enrollment token rejected"},
		{http.StatusForbidden, "enrollment token rejected"},
		{http.StatusNotFound, "auth check failed"},
		{http.StatusInternalServerError, "auth check failed"},
		{http.StatusServiceUnavailable, "auth check failed"},
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			mgr := authCheckManager(t, server.Client())
			err := mgr.CheckEnrollmentToken(context.Background(), server.URL, "test-token", nil)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// The server's response body may carry the only diagnostic for a rejection
// (e.g. that the token expired or was revoked), so it must reach the user.
func TestCheckEnrollmentToken_ErrorIncludesResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "enrollment token was revoked by an administrator", http.StatusUnauthorized)
	}))
	defer server.Close()

	mgr := authCheckManager(t, server.Client())
	err := mgr.CheckEnrollmentToken(context.Background(), server.URL, "test-token", nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "revoked by an administrator")
}

func TestCheckEnrollmentToken_FailsOnNetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close() // now nothing is listening on the URL

	mgr := authCheckManager(t, http.DefaultClient)
	require.Error(t, mgr.CheckEnrollmentToken(context.Background(), server.URL, "test-token", nil))
}

func TestCheckEnrollmentToken_FailsOnInvalidEndpoint(t *testing.T) {
	mgr := authCheckManager(t, http.DefaultClient)
	require.Error(t, mgr.CheckEnrollmentToken(context.Background(), "", "test-token", nil))
}
