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

package supervisor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Graylog2/collector/superv/config"
)

func TestDefaultEnrollmentSettings(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Auth.EnrollmentEndpoint = "https://graylog.example.com:9000"
	cfg.Server.Auth.EnrollmentHeaders = map[string]string{"X-Custom": "yes"}

	settings, err := DefaultEnrollmentSettings(cfg)
	require.NoError(t, err)

	require.Equal(t, "https://graylog.example.com:9000/v1/opamp", settings.Endpoint)
	require.Equal(t, map[string]string{"X-Custom": "yes"}, settings.Headers)
	require.Equal(t, 30*time.Second, settings.HeartbeatInterval)
	require.Equal(t, "TLSv1.3", settings.TLS.MinVersion)
	require.Equal(t, "TLSv1.3", settings.TLS.MaxVersion)
	require.False(t, settings.TLS.Insecure)
	require.False(t, settings.UpdatedAt.IsZero())
}

func TestDefaultEnrollmentSettings_Insecure(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Auth.EnrollmentEndpoint = "https://graylog.example.com"
	cfg.SetInsecure()

	settings, err := DefaultEnrollmentSettings(cfg)
	require.NoError(t, err)
	require.True(t, settings.TLS.Insecure)
}

func TestDefaultEnrollmentSettings_InvalidEndpoint(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Auth.EnrollmentEndpoint = ""

	_, err := DefaultEnrollmentSettings(cfg)
	require.Error(t, err)
}
