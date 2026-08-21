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

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/collector/otelcol"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// The tests in this file specify the own-logs crash-export contract for
// customizeCommand (see the TODO in main_customize.go):
//
// The collector, by design, exits with an error on invalid configuration.
// That error is returned through the root command's RunE and printed by
// Cobra — it never passes through the zap logger, and Cobra skips all
// post-run hooks on error, so the own-logs OTLP batch is never flushed.
// Net effect: in exactly the failure case where remote logs matter most
// (onboarding, broken remote config), nothing reaches the Graylog input.
//
// customizeCommand must therefore wrap the root command's RunE so that:
//   - a run error is logged through ownLogsCore (making it exportable),
//   - the own-logs provider is flushed on both success and error exits,
//   - the flush uses a context with a bounded deadline, independent of the
//     command's (possibly already canceled) context,
//   - the error is returned unchanged so Cobra's stderr printing — and
//     with it the supervisor-captured agent.log — keeps working.

// resetOwnLogsState clears the package-level own-logs wiring that
// customizeSettings would normally populate.
func resetOwnLogsState() {
	ownLogsCore = nil
	ownLogsShutdown = nil
}

// newRootCommand builds a stand-in for the otelcol root command: like
// otelcol.NewCommand, it exposes the collector run as RunE on the root.
func newRootCommand(runE func(cmd *cobra.Command, args []string) error) *cobra.Command {
	return &cobra.Command{
		Use:           "graylog-collector-test",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runE,
	}
}

// shutdownRecorder records calls to the own-logs shutdown/flush function.
type shutdownRecorder struct {
	calls int
	ctx   context.Context
}

func (r *shutdownRecorder) fn(ctx context.Context) {
	r.calls++
	r.ctx = ctx
}

func TestCustomizeCommand_ExportsRunErrorThroughOwnLogs(t *testing.T) {
	defer resetOwnLogsState()

	core, observed := observer.New(zapcore.DebugLevel)
	ownLogsCore = core
	rec := &shutdownRecorder{}
	ownLogsShutdown = rec.fn

	runErr := errors.New("failed to get config: cannot unmarshal the configuration")
	cmd := newRootCommand(func(*cobra.Command, []string) error { return runErr })
	customizeCommand(&otelcol.CollectorSettings{}, cmd)

	err := cmd.Execute()

	if !errors.Is(err, runErr) {
		t.Fatalf("Execute() must return the run error unchanged, got: %v", err)
	}

	entries := observed.FilterLevelExact(zapcore.ErrorLevel).All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 error-level own-logs entry for the run error, got %d (all entries: %v)",
			len(entries), observed.All())
	}
	rendered := entries[0].Message + " " + fmt.Sprint(entries[0].ContextMap())
	if !strings.Contains(rendered, runErr.Error()) {
		t.Errorf("exported entry must carry the run error text %q, got: %s", runErr.Error(), rendered)
	}

	if rec.calls != 1 {
		t.Errorf("own-logs must be flushed exactly once on error exit, got %d calls", rec.calls)
	}
}

func TestCustomizeCommand_FlushesOwnLogsWithBoundedContextOnSuccess(t *testing.T) {
	defer resetOwnLogsState()

	core, observed := observer.New(zapcore.DebugLevel)
	ownLogsCore = core
	rec := &shutdownRecorder{}
	ownLogsShutdown = rec.fn

	runCalled := false
	cmd := newRootCommand(func(*cobra.Command, []string) error {
		runCalled = true
		return nil
	})
	customizeCommand(&otelcol.CollectorSettings{}, cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() returned unexpected error: %v", err)
	}
	if !runCalled {
		t.Fatal("the original RunE must still be invoked")
	}

	if got := observed.FilterLevelExact(zapcore.ErrorLevel).Len(); got != 0 {
		t.Errorf("a clean exit must not log error entries, got %d", got)
	}

	if rec.calls != 1 {
		t.Fatalf("own-logs must be flushed exactly once on success, got %d calls", rec.calls)
	}
	if rec.ctx == nil {
		t.Fatal("flush must receive a context")
	}
	if _, ok := rec.ctx.Deadline(); !ok {
		t.Error("flush context must have a bounded deadline so a dead OTLP endpoint cannot stall process exit")
	}
}

func TestCustomizeCommand_ReturnsRunErrorWhenOwnLogsDisabled(t *testing.T) {
	// Guard: when own-logs is not configured (no own-logs.yaml), the wrapper
	// must be inert — no panic on nil core/shutdown, error passed through.
	defer resetOwnLogsState()
	resetOwnLogsState()

	runErr := errors.New("failed to get config: boom")
	cmd := newRootCommand(func(*cobra.Command, []string) error { return runErr })
	customizeCommand(&otelcol.CollectorSettings{}, cmd)

	if err := cmd.Execute(); !errors.Is(err, runErr) {
		t.Fatalf("Execute() must return the run error unchanged, got: %v", err)
	}
}
