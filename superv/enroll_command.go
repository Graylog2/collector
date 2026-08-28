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
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Graylog2/collector/superv/config"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/term"
)

const enrollCmdName = "enroll"

// GetEnrollCommand returns the enroll command. It performs the initial
// enrollment (key creation, CSR submission, certificate retrieval) without
// starting the collector, so it can be run standalone, e.g. by installers.
func GetEnrollCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   enrollCmdName,
		Short: "Enroll with a Graylog server",
		Long: "Perform the initial enrollment with a Graylog server without starting the collector: " +
			"create keys, submit a certificate signing request, and store the received certificate.",
		PreRunE: checkSupportedPlatform,
		RunE:    runEnroll,
	}

	addConfigFlags(cmd)
	cmd.Flags().StringP("endpoint", "e", "", "Enrollment endpoint")
	cmd.Flags().StringP("token", "t", "", "Enrollment token")
	cmd.Flags().Duration("timeout", time.Minute, "Time to wait for the enrollment to complete (using 0 disables the timeout)")

	return cmd
}

func runEnroll(cmd *cobra.Command, _ []string) error {
	cfg, events, err := buildConfig(cmd, "endpoint", "token")
	if err != nil {
		return fmt.Errorf("couldn't load config: %w", err)
	}

	logger, err := initEnrollLogger(cfg)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	for _, event := range events {
		event(logger)
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if timeout, _ := cmd.Flags().GetDuration("timeout"); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// We have a logger now so we can silence cobra errors to avoid doubling error output.
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	if err := Enroll(ctx, logger, cfg); err != nil {
		logger.Error("Error: " + err.Error())
		return err
	}

	return nil
}

func initEnrollLogger(cfg config.Config) (*zap.Logger, error) {
	level := zapcore.InfoLevel
	if cfg.Debug {
		level = zapcore.DebugLevel
	}

	loggerConfig := zap.NewDevelopmentConfig()
	loggerConfig.Level = zap.NewAtomicLevelAt(level)
	loggerConfig.DisableCaller = true
	loggerConfig.DisableStacktrace = true
	loggerConfig.EncoderConfig.TimeKey = ""
	loggerConfig.EncoderConfig.NameKey = ""

	if cfg.Logging.Color && term.IsTerminal(int(os.Stderr.Fd())) {
		enableConsoleColors()
		loggerConfig.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	return loggerConfig.Build()
}
