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

package opamp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func preflightConfig(endpoint string) ClientConfig {
	return ClientConfig{
		Endpoint:    endpoint,
		InstanceUID: "0195d0d1-b48e-7000-8000-000000000000",
		Headers:     http.Header{"Authorization": []string{"Bearer test-token"}},
	}
}

func TestPreflightAuthCheck_FailsOnAuthRejection(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no", status)
		}))
		defer server.Close()

		err := PreflightAuthCheck(context.Background(), preflightConfig(server.URL))
		require.Error(t, err, "status %d", status)
		assert.ErrorContains(t, err, http.StatusText(status))
	}
}

func TestPreflightAuthCheck_PassesOnOtherResponses(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		defer server.Close()

		assert.NoError(t, PreflightAuthCheck(context.Background(), preflightConfig(server.URL)), "status %d", status)
	}
}

func TestPreflightAuthCheck_PassesOnNetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close() // now nothing is listening on the URL

	assert.NoError(t, PreflightAuthCheck(context.Background(), preflightConfig(server.URL)))
}

func TestPreflightAuthCheck_SkipsWebSocketEndpoints(t *testing.T) {
	// Nothing listens on the endpoint; a ws(s) scheme must be skipped without
	// any request, so this must succeed instantly.
	assert.NoError(t, PreflightAuthCheck(context.Background(), preflightConfig("wss://127.0.0.1:1/v1/opamp")))
}

func TestPreflightAuthCheck_SendsAuthenticatedOpAMPRequest(t *testing.T) {
	var gotAuth, gotContentType string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
	}))
	defer server.Close()

	cfg := preflightConfig(server.URL)
	require.NoError(t, PreflightAuthCheck(context.Background(), cfg))

	assert.Equal(t, "Bearer test-token", gotAuth)
	assert.Equal(t, "application/x-protobuf", gotContentType)

	var msg protobufs.AgentToServer
	require.NoError(t, proto.Unmarshal(gotBody, &msg))
	assert.Equal(t, uuid.MustParse(cfg.InstanceUID), uuid.UUID(msg.GetInstanceUid()))
	assert.Contains(t, msg.GetCustomCapabilities().GetCapabilities(), AuthCheckCustomCapability,
		"the auth-check capability must be announced so sending its custom message is spec-conformant")
	assert.Equal(t, AuthCheckCustomCapability, msg.GetCustomMessage().GetCapability(),
		"the auth-check request itself must be an explicit custom message, not just a capability announcement")
	assert.Equal(t, AuthCheckMessageType, msg.GetCustomMessage().GetType())
}
