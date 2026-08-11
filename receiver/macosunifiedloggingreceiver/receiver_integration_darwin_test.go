// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build darwin && integration

package macosunifiedloggingreceiver

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

// Injects a unique marker via `logger`, then asserts the receiver emits it exactly once
// across multiple polls (real cursor + dedup against /usr/bin/log).
func TestIntegration_LiveMarkerEmittedOnce(t *testing.T) {
	marker := fmt.Sprintf("graylog-itest-%d", time.Now().UnixNano())

	runner, err := newExecLogRunner(receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")).Logger)
	if err != nil {
		t.Fatalf("integrity check failed: %v", err)
	}

	cfg := createDefaultConfig().(*Config)
	cfg.MinPollInterval = 200 * time.Millisecond
	cfg.MaxPollInterval = 200 * time.Millisecond
	cfg.MaxLogAge = time.Minute
	// Narrow to the logger(1) process only: each log-show invocation records itself in
	// the unified log (senderProcessName is null; processImagePath identifies the binary),
	// so without the path filter the predicate would also match the `log show` command
	// that carries the marker string in its own event message (one new self-log per poll).
	cfg.Predicate = fmt.Sprintf("processImagePath == \"/usr/bin/logger\" AND eventMessage CONTAINS %q", marker)
	sid := component.MustNewID("file_storage")
	cfg.StorageID = &sid

	sink := new(consumertest.LogsSink)
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), sink, runner)
	host := fakeHost{exts: map[component.ID]component.Component{sid: &fakeStorageExt{client: storage.NewNopClient()}}}

	if err := r.Start(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Shutdown(context.Background()) }()

	if err := exec.Command("/usr/bin/logger", marker).Run(); err != nil {
		t.Fatalf("logger failed: %v", err)
	}

	// Let several polls run so any duplicate re-fetch would surface.
	time.Sleep(3 * time.Second)

	count := 0
	for _, ld := range sink.AllLogs() {
		for i := 0; i < ld.ResourceLogs().Len(); i++ {
			sl := ld.ResourceLogs().At(i).ScopeLogs()
			for j := 0; j < sl.Len(); j++ {
				recs := sl.At(j).LogRecords()
				for k := 0; k < recs.Len(); k++ {
					if strings.Contains(recs.At(k).Body().Str(), marker) {
						count++
					}
				}
			}
		}
	}
	if count != 1 {
		t.Fatalf("marker emitted %d times, want exactly 1", count)
	}
}
