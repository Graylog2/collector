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

// Add OpenTelemetry Collector customizations to this file.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Graylog2/collector/superv"
	"github.com/Graylog2/collector/superv/ownlogs"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/collector/otelcol"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// ownLogsCore is the active own-logs OTLP zap core, set when own-logs
	// export is configured. Used by the RunE wrapper in customizeCommand to
	// export the collector's fatal error (e.g. config validation failures),
	// which otherwise never passes through the zap logger.
	ownLogsCore     zapcore.Core
	ownLogsShutdown func(context.Context)
)

func customizeSettings(params *otelcol.CollectorSettings) {
	// Disable caller information in logs to reduce log chatter and avoid exposing source code file names.
	params.LoggingOptions = append(params.LoggingOptions, zap.WithCaller(false))

	persistDir := os.Getenv("GLC_INTERNAL_PERSISTENCE_DIR")
	if persistDir == "" {
		return
	}

	res := ownlogs.BuildResource("collector", params.BuildInfo.Version,
		os.Getenv("GLC_INTERNAL_INSTANCE_UID"))

	core, shutdown, err := ownlogs.NewCoreFromFile(
		persistDir,
		os.Getenv("GLC_INTERNAL_TLS_CLIENT_CERT_PATH"),
		os.Getenv("GLC_INTERNAL_TLS_CLIENT_KEY_PATH"),
		res,
	)
	if err != nil {
		// Collector availability first: log warning, continue without own-logs.
		// A broken own-logs config must never prevent the collector from starting.
		fmt.Fprintf(os.Stderr, "WARNING: own-logs setup failed, continuing without OTLP log export: %v\n", err)
		return
	}
	if core == nil {
		return // no own-logs.yaml, skip
	}

	ownLogsShutdown = shutdown
	ownLogsCore = core
	// The OTel Collector's service layer attaches its own telemetry resource
	// (service.name, service.instance.id, etc.) as a zap field named "resource"
	// on component loggers. The otelzap bridge converts that field into an OTLP
	// log record attribute, which is redundant with the top-level OTLP resource
	// we set via BuildResource and confusing because the two carry different
	// values. Strip it before it reaches the bridge.
	params.LoggingOptions = append(params.LoggingOptions,
		zap.WrapCore(func(original zapcore.Core) zapcore.Core {
			return zapcore.NewTee(original, &ownlogs.FieldFilterCore{
				Core:       core,
				DropFields: []string{"resource"},
			})
		}),
	)
}

// noopFatalHook lets us emit a Fatal-level entry without zap terminating the
// process, so the flush and the error return after it still happen. It cannot
// be zapcore.WriteThenNoop: zap deliberately maps that value (and nil) back to
// WriteThenFatal in terminalHookOverride (zap/logger.go:424), so only a
// distinct hook type gets through.
type noopFatalHook struct{}

func (noopFatalHook) OnWrite(*zapcore.CheckedEntry, []zapcore.Field) {}

// ownLogsFlushTimeout keep this low to not delay shutdown too much
const ownLogsFlushTimeout = 1 * time.Second

func customizeCommand(params *otelcol.CollectorSettings, cmd *cobra.Command) {
	cmd.AddCommand(superv.GetEnrollCommand())

	if ownLogsShutdown != nil {
		// Best-effort flush: PersistentPostRun only fires when RunE succeeds.
		// Cobra skips all post-run hooks on error (command.go:1009), so on
		// error exits the batch processor's periodic export (~1s) is the only
		// flush mechanism. This is accepted — see the "Shutdown — Best-Effort
		// Flush" section in the design spec.
		existing := cmd.PersistentPostRun
		cmd.PersistentPostRun = func(cmd *cobra.Command, args []string) {
			if existing != nil {
				existing(cmd, args)
	supervCmd := superv.GetCommand()
	cmd.AddCommand(supervCmd)

	if ownLogsShutdown == nil {
		return
	}

	// flush logs only once, so we don't wait for the timeout twice
	var flushOnce sync.Once
	flushOwnLogs := func(ctx context.Context) {
		flushOnce.Do(func() {
			// context.WithoutCancel: on SIGTERM the command context is already
			// canceled, which would make the export fail instantly.
			flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ownLogsFlushTimeout)
			defer cancel()
			ownLogsShutdown(flushCtx)
		})
	}

	// Wrapping RunE here to ensure we can also intercept error returns.
	// This can happen because by design the otel-collector is crashing on bad config early and in that case
	// we have no chance to send out these messages to the configured OTLP endpoint before the process exits
	wrapRunE := func(c *cobra.Command) {
		origRunE := c.RunE
		if origRunE == nil {
			return
		}
		c.RunE = func(c *cobra.Command, args []string) error {
			err := origRunE(c, args)
			if err != nil && ownLogsCore != nil {
				// Fatal, not Error: the process is dying, and own-logs applies a
				// server-provided minimum level (see ownlogs.NewCoreFromFile). At
				// log_level: fatal an Error entry would be filtered out, dropping
				// the crash report in exactly the case where it matters most.
				// noopFatalHook suppresses zap's default os.Exit(1) on Fatal so the
				// flush below still runs and the error still reaches Cobra's stderr.
				logger := zap.New(ownLogsCore, zap.WithFatalHook(noopFatalHook{}))
				logger.Fatal(err.Error())
			}
			flushOwnLogs(c.Context())
			return err
		}
	}
	wrapRunE(cmd)
	wrapRunE(supervCmd)

	// Catch-all for the subcommands we don't wrap above: otelcol adds
	// components, validate, config-print and featuregate. PersistentPostRun is
	// inherited by every command in the tree, but Cobra skips it when RunE
	// returns an error -- which is why the error paths need the wrappers.
	existingPostRun := cmd.PersistentPostRun
	cmd.PersistentPostRun = func(c *cobra.Command, args []string) {
		if existingPostRun != nil {
			existingPostRun(c, args)
		}
		flushOwnLogs(c.Context())
	}
}
