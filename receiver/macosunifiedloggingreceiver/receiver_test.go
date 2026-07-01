// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

// fakeRunner returns a scripted stdout per poll, then blocks (ctx-cancelled) afterwards.
type fakeRunner struct {
	mu    sync.Mutex
	polls []string
	calls int
}

func (f *fakeRunner) Run(ctx context.Context, _ []string) (io.ReadCloser, func() (string, error), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body string
	if f.calls < len(f.polls) {
		body = f.polls[f.calls]
	}
	f.calls++
	return io.NopCloser(strings.NewReader(body)), func() (string, error) { return "", nil }, nil
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestReceiver_EmitsEachEventOnce(t *testing.T) {
	footer := "\n" + `{"count":1,"finished":1}`
	e1 := `{"timestamp":"2026-06-29 10:00:02.200000+0000","machTimestamp":200,"threadID":2,"bootUUID":"A","eventMessage":"one","messageType":"Default","eventType":"logEvent"}`
	e2 := `{"timestamp":"2026-06-29 10:00:03.000000+0000","machTimestamp":300,"threadID":3,"bootUUID":"A","eventMessage":"two","messageType":"Default","eventType":"logEvent"}`
	runner := &fakeRunner{polls: []string{
		e1 + footer,             // poll 1: emits "one"
		e1 + "\n" + e2 + footer, // poll 2: re-fetches "one" (dup) + new "two"
	}}

	cfg := createDefaultConfig().(*Config)
	cfg.MinPollInterval = time.Millisecond
	cfg.MaxPollInterval = time.Millisecond
	sid := component.MustNewID("file_storage")
	cfg.StorageID = &sid

	sink := new(consumertest.LogsSink)
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), sink, runner)

	host := fakeHost{exts: map[component.ID]component.Component{
		sid: &fakeStorageExt{client: storage.NewNopClient()},
	}}
	if err := r.Start(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	// Wait until both polls have run, then shut down.
	deadline := time.Now().Add(2 * time.Second)
	for runner.callCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	_ = r.Shutdown(context.Background())

	bodies := map[string]int{}
	for _, ld := range sink.AllLogs() {
		for i := 0; i < ld.ResourceLogs().Len(); i++ {
			sl := ld.ResourceLogs().At(i).ScopeLogs()
			for j := 0; j < sl.Len(); j++ {
				recs := sl.At(j).LogRecords()
				for k := 0; k < recs.Len(); k++ {
					bodies[recs.At(k).Body().Str()]++
				}
			}
		}
	}
	if bodies["one"] != 1 {
		t.Errorf(`"one" emitted %d times, want exactly 1 (dedup failed)`, bodies["one"])
	}
	if bodies["two"] != 1 {
		t.Errorf(`"two" emitted %d times, want exactly 1`, bodies["two"])
	}
}

func TestReceiver_StartRequiresStorage(t *testing.T) {
	cfg := createDefaultConfig().(*Config) // StorageID nil, live mode
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), &fakeRunner{})
	if err := r.Start(context.Background(), fakeHost{exts: map[component.ID]component.Component{}}); err == nil {
		t.Fatal("expected Start to fail without storage in live mode")
	}
}
