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
	"fmt"
	"time"

	"github.com/Graylog2/collector/superv/config"
	"github.com/Graylog2/collector/superv/supervisor/connection"
)

// defaultConnectionSettings returns the baseline connection settings used when
// no persisted settings exist yet.
func defaultConnectionSettings(cfg config.Config) connection.Settings {
	return connection.Settings{
		HeartbeatInterval: 30 * time.Second,
		TLS: connection.TLSSettings{
			Insecure:   cfg.IsInsecure(),
			MinVersion: "TLSv1.3",
			MaxVersion: "TLSv1.3",
		},
		UpdatedAt: time.Now().UTC(),
	}
}

// DefaultEnrollmentSettings returns the connection settings used to reach the
// OpAMP server for enrollment: the baseline defaults with the endpoint derived
// from the configured enrollment endpoint and the enrollment headers applied.
func DefaultEnrollmentSettings(cfg config.Config) (connection.Settings, error) {
	endpoint, err := config.DeriveEnrollmentEndpoint(cfg.Server.Auth.EnrollmentEndpoint)
	if err != nil {
		return connection.Settings{}, fmt.Errorf("failed to derive enrollment endpoint: %w", err)
	}

	settings := defaultConnectionSettings(cfg)
	settings.Endpoint = endpoint
	settings.Headers = cfg.Server.Auth.EnrollmentHeaders
	return settings, nil
}
