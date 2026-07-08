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

// TestReceiver_PredicateChangeDiscardsCursor proves the invalidation end-to-end: a cursor
// persisted under one predicate is adopted on restart with the same predicate, but discarded
// (start fresh) when the predicate — hence its hash — has changed.
func TestReceiver_PredicateChangeDiscardsCursor(t *testing.T) {
	// A cursor persisted under the OLD predicate, parked at a known second.
	old := newCursor(predicateHash(`process == "old"`))
	old.beginPoll()
	old.shouldEmit(ev(200, 2, "A", "2026-06-29 10:00:02.000000+0000"))
	old.commit()
	seeded, err := old.marshal()
	if err != nil {
		t.Fatal(err)
	}
	if old.startArg() != "2026-06-29 10:00:02" {
		t.Fatalf("precondition: seeded cursor should sit at 10:00:02, got %q", old.startArg())
	}

	// settle starts a receiver whose storage already holds the OLD cursor, configured with
	// the given predicate, then shuts it down so the poll goroutine can't touch r.cursor
	// while we assert. r.cursor reflects only Start's adopt/discard decision.
	settle := func(predicate string) *unifiedLoggingReceiver {
		mem := newMemStorage()
		if serr := mem.Set(context.Background(), cursorStorageKey, seeded); serr != nil {
			t.Fatal(serr)
		}
		cfg := createDefaultConfig().(*Config)
		cfg.MinPollInterval = time.Hour // one idle poll, then block until shutdown cancels
		cfg.MaxPollInterval = time.Hour
		cfg.Predicate = predicate
		sid := component.MustNewID("file_storage")
		cfg.StorageID = &sid
		r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), &fakeRunner{})
		host := fakeHost{exts: map[component.ID]component.Component{sid: &fakeStorageExt{client: mem}}}
		if serr := r.Start(context.Background(), host); serr != nil {
			t.Fatal(serr)
		}
		_ = r.Shutdown(context.Background())
		return r
	}

	t.Run("same predicate resumes", func(t *testing.T) {
		r := settle(`process == "old"`)
		// Hash matches -> persisted cursor adopted; the receiver resumes at the seeded second.
		if got := r.cursor.startArg(); got != "2026-06-29 10:00:02" {
			t.Errorf("startArg = %q, want the seeded second (cursor should have been adopted)", got)
		}
	})

	t.Run("changed predicate discards", func(t *testing.T) {
		r := settle(`process == "new"`)
		// Hash differs -> persisted cursor discarded; the fresh start carries the NEW hash so
		// the next persist self-heals. No resume point should carry over.
		if got := r.cursor.startArg(); got != "" {
			t.Errorf("startArg = %q, want empty (stale cursor should have been discarded)", got)
		}
		if r.cursor.predicateHash != predicateHash(`process == "new"`) {
			t.Error("fresh cursor must carry the new predicate hash so the next persist self-heals")
		}
	})
}

func TestReceiver_StartRequiresStorage(t *testing.T) {
	cfg := createDefaultConfig().(*Config) // StorageID nil, live mode
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), &fakeRunner{})
	if err := r.Start(context.Background(), fakeHost{exts: map[component.ID]component.Component{}}); err == nil {
		t.Fatal("expected Start to fail without storage in live mode")
	}
}
