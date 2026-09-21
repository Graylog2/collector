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

package auth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Graylog2/collector/superv/config"
)

// EnrollmentAuthCheckPath is the server endpoint that validates an enrollment
// token: it returns 200 for a valid token and 401 otherwise.
const EnrollmentAuthCheckPath = "/v1/opamp-enroll-auth-check"

const authCheckTimeout = 10 * time.Second

// CheckEnrollmentToken asks the server whether the enrollment token is valid,
// so a bad token fails fast with a clear error instead of being retried by the
// OpAMP client until a timeout expires. Any outcome other than HTTP 200 is an
// error: 401/403 report a rejected token, everything else (including network
// errors) reports the endpoint as unreachable. The headers are sent with the
// request so deployments behind header-gated proxies see the same headers as
// the OpAMP enrollment connection; Authorization always carries the token.
func (m *Manager) CheckEnrollmentToken(ctx context.Context, enrollmentEndpoint, enrollmentToken string, headers map[string]string) error {
	baseURL, err := config.ServerBaseURL(enrollmentEndpoint)
	if err != nil {
		return fmt.Errorf("failed to get server base URL: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, authCheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+EnrollmentAuthCheckPath, nil)
	if err != nil {
		return fmt.Errorf("failed to create auth check request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", BearerToken(enrollmentToken))

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("enrollment auth check failed: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("enrollment token rejected by server (HTTP %d %s%s): check the enrollment token",
			resp.StatusCode, http.StatusText(resp.StatusCode), bodySnippet(resp.Body))
	default:
		return fmt.Errorf("enrollment auth check failed (HTTP %d %s%s)",
			resp.StatusCode, http.StatusText(resp.StatusCode), bodySnippet(resp.Body))
	}
}

// bodySnippet returns the beginning of an error response body formatted for
// inclusion in an error message, or "" when the body is empty or unreadable.
// The body may carry the only diagnostic the server gives for a rejection,
// e.g. that the token expired or was revoked.
func bodySnippet(body io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(body, 512))
	snippet := strings.TrimSpace(string(b))
	if err != nil || snippet == "" {
		return ""
	}
	return fmt.Sprintf("; server says: %q", snippet)
}
