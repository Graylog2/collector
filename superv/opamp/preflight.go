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
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/open-telemetry/opamp-go/protobufs"
	"google.golang.org/protobuf/proto"
)

const preflightTimeout = 10 * time.Second

// AuthCheckCustomCapability identifies the auth-check message exchange. It is
// announced via CustomCapabilities so that sending the auth-check
// CustomMessage is spec-conformant; per the OpAMP spec, servers ignore custom
// capabilities and messages they don't support.
const AuthCheckCustomCapability = "org.graylog.collector.enrollment.auth-check"

// AuthCheckMessageType is the CustomMessage type marking an auth-check
// request, so the server can recognize it explicitly instead of treating the
// message as a malformed enrollment attempt.
const AuthCheckMessageType = "request"

// PreflightAuthCheck sends a single minimal AgentToServer message to the
// configured endpoint to detect a definitive authentication rejection before
// entering the OpAMP client's retry loop, where opamp-go only logs it.
// Only 401/403 responses fail the check; every other outcome, including
// network errors, defers to the normal client flow so the check can never
// make a connection less reliable. ws(s) endpoints are skipped because the
// check is HTTP-specific.
func PreflightAuthCheck(ctx context.Context, cfg ClientConfig) error {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || strings.HasPrefix(u.Scheme, "ws") {
		return nil
	}

	uid, err := parseInstanceUID(cfg.InstanceUID)
	if err != nil {
		return nil
	}
	body, err := proto.Marshal(&protobufs.AgentToServer{
		InstanceUid: uid[:],
		CustomCapabilities: &protobufs.CustomCapabilities{
			Capabilities: []string{AuthCheckCustomCapability},
		},
		CustomMessage: &protobufs.CustomMessage{
			Capability: AuthCheckCustomCapability,
			Type:       AuthCheckMessageType,
		},
	})
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil
	}
	if cfg.Headers != nil {
		req.Header = cfg.Headers.Clone()
	}
	req.Header.Set("Content-Type", "application/x-protobuf")

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg.TLSConfig}}
	defer client.CloseIdleConnections()

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("enrollment rejected by server (HTTP %d %s): check the enrollment token",
			resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return nil
}
