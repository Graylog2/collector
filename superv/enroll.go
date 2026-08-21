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

package superv

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/open-telemetry/opamp-go/protobufs"
	"go.uber.org/zap"

	"github.com/Graylog2/collector/superv/auth"
	"github.com/Graylog2/collector/superv/config"
	"github.com/Graylog2/collector/superv/opamp"
	"github.com/Graylog2/collector/superv/persistence"
	"github.com/Graylog2/collector/superv/supervisor"
	"github.com/Graylog2/collector/superv/supervisor/connection"
)

// Enroll performs the initial enrollment without starting the collector.
// It validates the enrollment token, generates keypairs, submits a CSR to the
// server via OpAMP, and persists the received certificate and connection
// settings. If the supervisor is already enrolled, it returns successfully
// without changing anything. Enrollment is aborted when ctx is cancelled.
func Enroll(ctx context.Context, logger *zap.Logger, cfg config.Config) error {
	authMgr := auth.NewManager(logger.Named("auth"), auth.ManagerConfig{
		KeysDir:     cfg.Keys.Dir,
		JWTLifetime: cfg.Server.Auth.JWTLifetime,
		InsecureTLS: cfg.Server.Auth.InsecureTLS,
	})

	if authMgr.IsEnrolled() {
		if err := authMgr.LoadCredentials(); err != nil {
			return fmt.Errorf("already enrolled, but failed to load credentials: %w", err)
		}
		logger.Info("Already enrolled, nothing to do",
			zap.String("cert_fingerprint", authMgr.CertFingerprint()))
		return nil
	}

	if cfg.Server.Auth.EnrollmentEndpoint == "" {
		return errors.New("no enrollment endpoint configured")
	}
	if cfg.Server.Auth.EnrollmentToken == "" {
		return errors.New("no enrollment token configured")
	}

	instanceUID, err := persistence.LoadOrCreateInstanceUID(cfg.Persistence.Dir)
	if err != nil {
		return fmt.Errorf("failed to load instance UID: %w", err)
	}

	// Validates the enrollment JWT against the server's JWKS and creates the CSR.
	prep, err := authMgr.PrepareEnrollment(ctx, cfg.Server.Auth.EnrollmentEndpoint, cfg.Server.Auth.EnrollmentToken, instanceUID)
	if err != nil {
		return fmt.Errorf("enrollment preparation failed: %w", err)
	}

	connSettings, err := supervisor.DefaultEnrollmentSettings(cfg)
	if err != nil {
		return err
	}

	settingsMgr := connection.NewSettingsManager(logger, cfg.Persistence.Dir)
	settingsMgr.SetCurrent(connSettings)

	// Retained so the final timeout error can name the underlying cause;
	// opamp-go's HTTP client otherwise only logs connection failures.
	var lastConnErr atomic.Pointer[error]

	// Buffered so the single expected completion never blocks the OpAMP callback.
	done := make(chan error, 1)
	reportDone := func(err error) {
		select {
		case done <- err:
		default:
		}
	}

	callbacks := &opamp.Callbacks{
		OnConnect: func(_ context.Context) {
			logger.Info("Connected to OpAMP server", zap.String("endpoint", connSettings.Endpoint))
		},
		OnConnectFailed: func(_ context.Context, err error) {
			logger.Warn("Failed to connect to OpAMP server, retrying", zap.Error(err))
			if err != nil {
				lastConnErr.Store(&err)
			}
		},
		OnError: func(_ context.Context, err *protobufs.ServerErrorResponse) {
			logger.Error("OpAMP server error",
				zap.String("type", err.GetType().String()),
				zap.String("message", err.GetErrorMessage()))
		},
		OnOpampConnectionSettings: func(_ context.Context, settings *protobufs.OpAMPConnectionSettings) error {
			certPEM := settings.GetCertificate().GetCert()
			if len(certPEM) == 0 {
				logger.Debug("Received connection settings without certificate, ignoring")
				return nil
			}

			// Persist the connection settings before completing enrollment:
			// CompleteEnrollment is what makes IsEnrolled() true, and a later
			// run no-ops when already enrolled. Persisting first keeps every
			// partial-failure state repairable by simply re-running enroll.
			newSettings, _ := settingsMgr.SettingsChanged(settings)
			if err := settingsMgr.Persist(newSettings); err != nil {
				err = fmt.Errorf("failed to persist connection settings: %w", err)
				reportDone(err)
				return err
			}

			if err := authMgr.CompleteEnrollment(certPEM); err != nil {
				err = fmt.Errorf("failed to complete enrollment: %w", err)
				reportDone(err)
				return err
			}

			reportDone(nil)
			return nil
		},
	}

	minVersion, maxVersion, err := connSettings.TLS.ToTLSMinMaxVersion()
	if err != nil {
		return fmt.Errorf("invalid TLS settings: %w", err)
	}

	headers := supervisor.ToHTTPHeaders(connSettings.Headers)
	if authMgr.EnrollmentJWT() != "" {
		headers.Set("Authorization", auth.BearerToken(authMgr.EnrollmentJWT()))
	} else {
		return fmt.Errorf("empty enrollment JWT")
	}

	clientCfg := opamp.ClientConfig{
		Endpoint:             connSettings.Endpoint,
		InstanceUID:          instanceUID,
		Headers:              headers,
		HeartbeatInterval:    connSettings.HeartbeatInterval,
		MaxHeartbeatInterval: cfg.Server.MaxHeartbeatInterval,
		TLSConfig: &tls.Config{
			InsecureSkipVerify: connSettings.TLS.Insecure, //nolint:gosec // Intentionally configurable
			MinVersion:         minVersion,
			MaxVersion:         maxVersion,
		},
		Capabilities: opamp.Capabilities{
			AcceptsOpAMPConnectionSettings: true,
		},
	}

	// A definitive token rejection would otherwise be retried by the client
	// until the timeout expires and be masked behind a generic timeout error.
	if err := opamp.PreflightAuthCheck(ctx, clientCfg); err != nil {
		return err
	}

	client, err := opamp.NewClient(logger.Named("opamp"), clientCfg, callbacks)
	if err != nil {
		return fmt.Errorf("failed to create OpAMP client: %w", err)
	}

	// The collector version is unknown here because enrollment doesn't start the collector.
	if err := client.SetAgentDescription(supervisor.NewAgentDescription(logger, instanceUID, "")); err != nil {
		return fmt.Errorf("failed to set agent description: %w", err)
	}

	logger.Info("Submitting CSR for enrollment via OpAMP", zap.String("endpoint", connSettings.Endpoint))
	if err := client.RequestConnectionSettings(prep.CSRPEM); err != nil {
		return fmt.Errorf("failed to request connection settings: %w", err)
	}

	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("failed to start OpAMP client: %w", err)
	}

	stopClient := func() {
		// Fresh context so the client can still shut down cleanly after ctx expired.
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Stop(stopCtx); err != nil {
			logger.Warn("Failed to stop OpAMP client", zap.Error(err))
		}
	}

	select {
	case err := <-done:
		stopClient()
		if err != nil {
			return err
		}
	case <-ctx.Done():
		stopClient()
		// The callback may have finished enrollment just as ctx expired, making
		// both channels ready and the chosen case arbitrary. The client is
		// stopped now, so one final non-blocking check is decisive.
		select {
		case err := <-done:
			if err != nil {
				return err
			}
		default:
			if connErr := lastConnErr.Load(); connErr != nil {
				return fmt.Errorf("gave up waiting for enrollment certificate: %w (last connection error: %v)", ctx.Err(), *connErr)
			}
			return fmt.Errorf("gave up waiting for enrollment certificate: %w", ctx.Err())
		}
	}

	return nil
}
