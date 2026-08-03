// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

// flakyConsumer rejects any batch containing a record whose body equals failBody, up to
// failLeft times, then delegates to sink. It lets a test fail delivery of one specific
// second while others succeed, exercising per-second commit granularity.
type flakyConsumer struct {
	mu       sync.Mutex
	sink     *consumertest.LogsSink
	failBody string
	failLeft int
}

func (c *flakyConsumer) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func (c *flakyConsumer) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failLeft > 0 && logsContainBody(ld, c.failBody) {
		c.failLeft--
		return errors.New("rejected batch containing " + c.failBody)
	}
	return c.sink.ConsumeLogs(ctx, ld)
}

func logsContainBody(ld plog.Logs, body string) bool {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		sl := ld.ResourceLogs().At(i).ScopeLogs()
		for j := 0; j < sl.Len(); j++ {
			recs := sl.At(j).LogRecords()
			for k := 0; k < recs.Len(); k++ {
				if recs.At(k).Body().Str() == body {
					return true
				}
			}
		}
	}
	return false
}

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
	oldEv := ev(200, 2, "A", "2026-06-29 10:00:02.000000+0000")
	old.shouldEmit(oldEv)
	old.recordDelivered([]*logEvent{oldEv})
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

// TestPollOnce_ConsumeErrorDoesNotAdvanceCursor proves the exactly-once invariant for a
// durable source: when ConsumeLogs rejects a batch (downstream backpressure), pollOnce must
// surface the error AND leave the cursor unadvanced, so the next poll re-reads that second
// rather than skipping past records nothing downstream accepted.
func TestPollOnce_ConsumeErrorDoesNotAdvanceCursor(t *testing.T) {
	footer := "\n" + `{"count":1,"finished":1}`
	e1 := `{"timestamp":"2026-06-29 10:00:02.200000+0000","machTimestamp":200,"threadID":2,"bootUUID":"A","eventMessage":"one","messageType":"Default","eventType":"logEvent"}`
	runner := &fakeRunner{polls: []string{e1 + footer}}

	cfg := createDefaultConfig().(*Config)
	cons := consumertest.NewErr(errors.New("downstream refused"))
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), cons, runner)

	_, err := r.pollOnce(context.Background())
	if err == nil {
		t.Fatal("pollOnce returned nil error; a rejected ConsumeLogs must surface as an error")
	}
	if got := r.cursor.startArg(); got != "" {
		t.Errorf("cursor advanced to %q after a rejected delivery; want unadvanced (empty) so the next poll re-reads that second", got)
	}
}

// TestPollOnce_PartialFailureCommitsDeliveredSecondsOnly proves per-second commit
// granularity: in a poll spanning seconds S1<S2<S3, if S2's delivery is rejected, S1 must be
// committed (delivered, cursor advanced to S1) and S2+ must re-read on the next poll and
// deliver, with every record emitted exactly once.
func TestPollOnce_PartialFailureCommitsDeliveredSecondsOnly(t *testing.T) {
	footer := "\n" + `{"count":3,"finished":1}`
	e1 := `{"timestamp":"2026-06-29 10:00:01.100000+0000","machTimestamp":100,"threadID":1,"bootUUID":"A","eventMessage":"one","messageType":"Default","eventType":"logEvent"}`
	e2 := `{"timestamp":"2026-06-29 10:00:02.100000+0000","machTimestamp":200,"threadID":2,"bootUUID":"A","eventMessage":"two","messageType":"Default","eventType":"logEvent"}`
	e3 := `{"timestamp":"2026-06-29 10:00:03.100000+0000","machTimestamp":300,"threadID":3,"bootUUID":"A","eventMessage":"three","messageType":"Default","eventType":"logEvent"}`
	body := e1 + "\n" + e2 + "\n" + e3 + footer
	runner := &fakeRunner{polls: []string{body, body, body}}

	cfg := createDefaultConfig().(*Config)
	sink := new(consumertest.LogsSink)
	cons := &flakyConsumer{sink: sink, failBody: "two", failLeft: 1}
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), cons, runner)

	// Poll 1: S1 delivers, S2 is rejected -> stop with an error, cursor committed only to S1.
	if _, err := r.pollOnce(context.Background()); err == nil {
		t.Fatal("poll 1: expected an error from the rejected S2 delivery")
	}
	if got := r.cursor.startArg(); got != "2026-06-29 10:00:01" {
		t.Fatalf("poll 1: cursor = %q, want 10:00:01 (only the delivered second S1 committed)", got)
	}

	// Poll 2: re-reads from S1 (deduped), retries S2, delivers S3.
	if _, err := r.pollOnce(context.Background()); err != nil {
		t.Fatalf("poll 2: unexpected error: %v", err)
	}
	if got := r.cursor.startArg(); got != "2026-06-29 10:00:03" {
		t.Fatalf("poll 2: cursor = %q, want 10:00:03 (all seconds now delivered)", got)
	}

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
	for _, want := range []string{"one", "two", "three"} {
		if bodies[want] != 1 {
			t.Errorf("%q delivered %d times, want exactly 1", want, bodies[want])
		}
	}
}

// TestPollOnce_BoundarySecondDedupSurvivesNewEvents guards the steady-state case: when the
// newest second stays the boundary across polls and keeps gaining new events, the records
// already delivered at that second must stay deduped. If the cursor only remembered the
// most-recently-delivered identities, an earlier event at the same second would be re-emitted
// on every subsequent poll.
func TestPollOnce_BoundarySecondDedupSurvivesNewEvents(t *testing.T) {
	footer := "\n" + `{"count":2,"finished":1}`
	// Two events in the SAME wall-clock second, distinct identities.
	a := `{"timestamp":"2026-06-29 10:00:05.100000+0000","machTimestamp":500,"threadID":1,"bootUUID":"A","eventMessage":"a","messageType":"Default","eventType":"logEvent"}`
	b := `{"timestamp":"2026-06-29 10:00:05.600000+0000","machTimestamp":550,"threadID":2,"bootUUID":"A","eventMessage":"b","messageType":"Default","eventType":"logEvent"}`
	runner := &fakeRunner{polls: []string{
		a + footer,            // poll 1: "a"
		a + "\n" + b + footer, // poll 2: re-reads "a" (dup) + new "b" at the same second
		a + "\n" + b + footer, // poll 3: re-reads both — BOTH must dedup
	}}

	cfg := createDefaultConfig().(*Config)
	sink := new(consumertest.LogsSink)
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), sink, runner)

	for i := range 3 {
		if _, err := r.pollOnce(context.Background()); err != nil {
			t.Fatalf("poll %d: unexpected error: %v", i+1, err)
		}
	}

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
	if bodies["a"] != 1 {
		t.Errorf(`"a" delivered %d times, want exactly 1 (boundary-second dedup must survive new same-second events)`, bodies["a"])
	}
	if bodies["b"] != 1 {
		t.Errorf(`"b" delivered %d times, want exactly 1`, bodies["b"])
	}
}

// TestStartArgValue_ResumeIgnoresMaxLogAge proves #4: a persisted cursor is honored on
// resume even when it predates now−max_log_age, so an outage longer than max_log_age does
// not silently skip the gap. max_log_age bounds only a cold start.
func TestStartArgValue_ResumeIgnoresMaxLogAge(t *testing.T) {
	cfg := createDefaultConfig().(*Config) // MaxLogAge = 24h
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), &fakeRunner{})
	r.now = func() time.Time { return time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC) }

	// Seed a committed cursor 3 days back — well older than now−24h.
	old := ev(100, 1, "A", "2026-06-27 09:00:00.000000+0000")
	r.cursor.shouldEmit(old)
	r.cursor.recordDelivered([]*logEvent{old})
	r.cursor.commit()

	if got, want := r.startArgValue(), "2026-06-27 09:00:00"; got != want {
		t.Errorf("startArgValue = %q, want the persisted cursor %q (resume must ignore max_log_age)", got, want)
	}
}

// TestStartArgValue_ColdStartUsesMaxLogAge guards the other half: with no cursor, the initial
// read is still bounded by now−max_log_age.
func TestStartArgValue_ColdStartUsesMaxLogAge(t *testing.T) {
	cfg := createDefaultConfig().(*Config) // MaxLogAge = 24h
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), &fakeRunner{})
	r.now = func() time.Time { return time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC) }

	if got, want := r.startArgValue(), "2026-06-29 12:00:00"; got != want {
		t.Errorf("startArgValue = %q, want the max_log_age floor %q on cold start", got, want)
	}
}

// TestPollOnce_OversizedLineIsSkipped proves #3: a single line exceeding the byte cap is
// skipped (not fatal), so both surrounding seconds are delivered and the cursor makes
// forward progress rather than wedging on the oversized line forever.
func TestPollOnce_OversizedLineIsSkipped(t *testing.T) {
	footer := "\n" + `{"count":3,"finished":1}`
	hugeBody := strings.Repeat("x", 2000)
	e1 := `{"timestamp":"2026-06-29 10:00:01.100000+0000","machTimestamp":100,"threadID":1,"bootUUID":"A","eventMessage":"one","messageType":"Default","eventType":"logEvent"}`
	huge := `{"timestamp":"2026-06-29 10:00:01.500000+0000","machTimestamp":150,"threadID":9,"bootUUID":"A","eventMessage":"` + hugeBody + `"}`
	e2 := `{"timestamp":"2026-06-29 10:00:02.100000+0000","machTimestamp":200,"threadID":2,"bootUUID":"A","eventMessage":"two","messageType":"Default","eventType":"logEvent"}`
	runner := &fakeRunner{polls: []string{e1 + "\n" + huge + "\n" + e2 + footer}}

	cfg := createDefaultConfig().(*Config)
	sink := new(consumertest.LogsSink)
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), sink, runner)
	r.maxLineBytes = 1024 // e1/e2 (~150B) fit; the ~2KB line must be skipped, not delivered

	if _, err := r.pollOnce(context.Background()); err != nil {
		t.Fatalf("oversized line must not fail the poll, got: %v", err)
	}
	if got := r.cursor.startArg(); got != "2026-06-29 10:00:02" {
		t.Errorf("cursor = %q, want 10:00:02 (must progress past the oversized line)", got)
	}

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
	if bodies["one"] != 1 || bodies["two"] != 1 {
		t.Errorf("want one=1,two=1 around the skipped line; got one=%d two=%d", bodies["one"], bodies["two"])
	}
	if bodies[hugeBody] != 0 {
		t.Errorf("oversized line (%d bytes) must be skipped, but it was delivered", len(hugeBody))
	}
}

// TestPollOnce_FinalCursorWriteSurvivesCancellation pins the shutdown-ordering fix: Shutdown
// cancels the polling context while a poll is in flight, so the last poll reaches its cursor
// write with an already-canceled ctx. That write must not inherit the cancellation — a backend
// that honors ctx (bbolt ignores it today, a networked store would not) would drop the last
// poll's progress, and every event it delivered would be re-emitted as a duplicate on restart.
func TestPollOnce_FinalCursorWriteSurvivesCancellation(t *testing.T) {
	mem := newMemStorage()
	cfg := createDefaultConfig().(*Config)
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), &fakeRunner{})
	r.storage = mem

	// Exactly the state Shutdown creates: the poll's parent context is already done.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := r.pollOnce(ctx); err != nil {
		t.Fatalf("a poll interrupted by shutdown should not report an error, got: %v", err)
	}

	total, canceled := mem.setStats()
	if total == 0 {
		t.Fatal("the interrupted poll never persisted its cursor at all")
	}
	if canceled != 0 {
		t.Errorf("%d of %d cursor writes arrived with an already-canceled context; wrap it in context.WithoutCancel so the final write survives shutdown", canceled, total)
	}
}

// TestReceiver_StartFailsOnStorageReadError proves a cursor read failure is surfaced instead of
// being swallowed into a silent fresh start. Starting fresh would re-read up to max_log_age
// (24h by default) and re-ingest all of it — expensive and invisible, where a failed Start is
// neither.
func TestReceiver_StartFailsOnStorageReadError(t *testing.T) {
	sid := component.MustNewID("file_storage")
	client := &errGetStorage{memStorage: newMemStorage(), err: errors.New("bbolt: database file is corrupt")}
	host := fakeHost{exts: map[component.ID]component.Component{
		sid: &fakeStorageExt{client: client},
	}}

	cfg := createDefaultConfig().(*Config)
	cfg.StorageID = &sid
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), &fakeRunner{})

	err := r.Start(context.Background(), host)
	if err == nil {
		t.Fatal("Start must fail when the persisted cursor cannot be read, not silently start fresh")
	}
	if !strings.Contains(err.Error(), "database file is corrupt") {
		t.Errorf("Start error should wrap the underlying storage failure so it is diagnosable, got: %v", err)
	}

	// The collector still calls Shutdown on a component whose Start failed; the client we
	// already acquired must be released rather than leaked.
	if err := r.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown after failed start: %v", err)
	}
	if got := client.closeCount(); got != 1 {
		t.Errorf("storage client closed %d times after a failed Start, want 1", got)
	}
}

// TestPollOnce_ReusesLineBuffer pins both halves of the line-cap fix. The cap is back to
// upstream's 10MB, but bufio.NewReaderSize allocates its buffer eagerly (unlike bufio.Scanner,
// which grows lazily from 64KB), so a reader per poll would churn 10MB on every tick — once a
// second at the default cadence. The reader must be built once and Reset onto each poll's
// stdout. That Reset is exercised end-to-end by the multi-poll dedup tests above, which would
// read stale bytes if it were missing.
func TestPollOnce_ReusesLineBuffer(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), &fakeRunner{polls: []string{"", ""}})

	if _, err := r.pollOnce(context.Background()); err != nil {
		t.Fatalf("poll 1: %v", err)
	}
	first := r.br
	if first == nil {
		t.Fatal("no line reader retained after the first poll")
	}
	// Deliberately a literal, not maxLineBytes: comparing the constant to itself would pass for
	// any value and would not have caught the 10MB -> 1MB regression this pins.
	if got, want := first.Size(), 10*1024*1024; got != want {
		t.Errorf("line buffer = %d bytes, want %d (upstream's cap; a lower value silently drops large events)", got, want)
	}

	if _, err := r.pollOnce(context.Background()); err != nil {
		t.Fatalf("poll 2: %v", err)
	}
	if r.br != first {
		t.Errorf("poll 2 allocated a new %d-byte line reader instead of resetting the existing one", maxLineBytes)
	}
}

// TestReceiver_StartPropagatesItsContext proves Start hands the collector's context to the
// storage extension rather than substituting context.Background(), so a startup deadline or
// cancellation reaches the backend. Cancelling before Start is the cheapest way to make the
// propagated context observably distinct from a fresh Background one.
//
// The polling goroutine is a deliberate exception and must keep using Background: the collector
// cancels the Start context as soon as Start returns, which would stop the receiver instantly.
// TestReceiver_RestartOnSameHost covers that, since a poll has to survive Start returning for
// its storage client to be closed exactly once per lifecycle.
func TestReceiver_StartPropagatesItsContext(t *testing.T) {
	sid := component.MustNewID("file_storage")
	mem := newMemStorage()
	host := fakeHost{exts: map[component.ID]component.Component{
		sid: &fakeStorageExt{client: mem},
	}}
	cfg := createDefaultConfig().(*Config)
	cfg.StorageID = &sid
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), &fakeRunner{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Start(ctx, host); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })

	total, canceled := mem.getStats()
	if total == 0 {
		t.Fatal("Start never read the cursor")
	}
	if canceled != total {
		t.Errorf("%d of %d cursor reads saw a live context; Start must pass its own ctx through, not context.Background()", total-canceled, total)
	}
}

// TestPollOnce_SkipsUnchangedCursorWrite proves an idle poll does not rewrite an identical
// cursor. At the 1s floor the receiver polls forever, so persisting unconditionally means a
// storage write every second for the lifetime of the collector even when nothing has happened.
func TestPollOnce_SkipsUnchangedCursorWrite(t *testing.T) {
	footer := "\n" + `{"count":1,"finished":1}`
	// Two events in the same second, so the committed seen set holds more than one identity --
	// exactly the case where an unsorted marshal would produce different bytes each call and
	// defeat the comparison.
	a := `{"timestamp":"2026-06-29 10:00:05.100000+0000","machTimestamp":500,"threadID":1,"bootUUID":"A","eventMessage":"a","messageType":"Default","eventType":"logEvent"}`
	b := `{"timestamp":"2026-06-29 10:00:05.600000+0000","machTimestamp":550,"threadID":2,"bootUUID":"A","eventMessage":"b","messageType":"Default","eventType":"logEvent"}`
	runner := &fakeRunner{polls: []string{
		a + "\n" + b + footer, // poll 1: cursor advances -> must write
		a + "\n" + b + footer, // poll 2: both dedup, cursor unchanged -> must skip
		"",                    // poll 3: nothing at all, still unchanged -> must skip
	}}

	mem := newMemStorage()
	cfg := createDefaultConfig().(*Config)
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), runner)
	r.storage = mem

	for i := 1; i <= 3; i++ {
		if _, err := r.pollOnce(context.Background()); err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
		writes, _ := mem.setStats()
		if writes != 1 {
			t.Fatalf("after poll %d: %d cursor writes, want 1 (only the poll that advanced the cursor should write)", i, writes)
		}
	}

	// A genuine advance must still be persisted, or the skip logic would strand the cursor.
	runner.mu.Lock()
	runner.polls = append(runner.polls, `{"timestamp":"2026-06-29 10:00:09.000000+0000","machTimestamp":900,"threadID":3,"bootUUID":"A","eventMessage":"c","messageType":"Default","eventType":"logEvent"}`+footer)
	runner.mu.Unlock()
	if _, err := r.pollOnce(context.Background()); err != nil {
		t.Fatalf("poll 4: %v", err)
	}
	if writes, _ := mem.setStats(); writes != 2 {
		t.Errorf("after an advancing poll: %d cursor writes, want 2", writes)
	}
}

func TestReceiver_StartRequiresStorage(t *testing.T) {
	cfg := createDefaultConfig().(*Config) // StorageID nil, live mode
	r := newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), &fakeRunner{})
	if err := r.Start(context.Background(), fakeHost{exts: map[component.ID]component.Component{}}); err == nil {
		t.Fatal("expected Start to fail without storage in live mode")
	}
}

// TestReceiver_RestartOnSameHost exercises the collector's real lifecycle contract: two
// successive receiver instances started and stopped against one host and one storage
// extension, as happens on a config reload. Combined with goleak in TestMain this proves
// Shutdown reaps the polling goroutine, and the close count proves it releases the storage
// client rather than leaking a handle per reload.
func TestReceiver_RestartOnSameHost(t *testing.T) {
	sid := component.MustNewID("file_storage")
	mem := newMemStorage()
	host := fakeHost{exts: map[component.ID]component.Component{
		sid: &fakeStorageExt{client: mem},
	}}

	newReceiver := func() *unifiedLoggingReceiver {
		cfg := createDefaultConfig().(*Config)
		cfg.MinPollInterval = 10 * time.Millisecond
		cfg.MaxPollInterval = 10 * time.Millisecond
		cfg.StorageID = &sid
		return newUnifiedLoggingReceiver(cfg, receivertest.NewNopSettings(component.MustNewType("macos_unified_logging")), new(consumertest.LogsSink), &fakeRunner{})
	}

	for i := 1; i <= 2; i++ {
		r := newReceiver()
		if err := r.Start(context.Background(), host); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		if err := r.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown %d: %v", i, err)
		}
		if got := mem.closeCount(); got != i {
			t.Errorf("after %d lifecycles: storage closed %d times, want %d", i, got, i)
		}
	}
}

// TestReceiver_ShutdownWithoutStartIsSafe pins the nil guards in Shutdown. The collector
// calls Shutdown on a component whose Start returned an error, so Shutdown must tolerate a
// receiver that never acquired a cancel func or a storage client.
func TestReceiver_ShutdownWithoutStartIsSafe(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	set := receivertest.NewNopSettings(component.MustNewType("macos_unified_logging"))

	// Never started at all.
	r := newUnifiedLoggingReceiver(cfg, set, new(consumertest.LogsSink), &fakeRunner{})
	if err := r.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown without start: %v", err)
	}

	// Started, but Start failed before it could install cancel or storage.
	r2 := newUnifiedLoggingReceiver(cfg, set, new(consumertest.LogsSink), &fakeRunner{})
	if err := r2.Start(context.Background(), fakeHost{exts: map[component.ID]component.Component{}}); err == nil {
		t.Fatal("expected Start to fail without storage")
	}
	if err := r2.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown after failed start: %v", err)
	}
}

// argRecorder is a logRunner that records the args of every invocation and returns an empty
// body, so a test can assert exactly which flags the receiver passed to `log`.
type argRecorder struct {
	mu   sync.Mutex
	args [][]string
}

func (r *argRecorder) Run(_ context.Context, args []string) (io.ReadCloser, func() (string, error), error) {
	r.mu.Lock()
	r.args = append(r.args, append([]string(nil), args...))
	r.mu.Unlock()
	return io.NopCloser(strings.NewReader("")), func() (string, error) { return "", nil }, nil
}

// hasFlag reports whether args contains flag immediately followed by val.
func hasFlag(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

// TestLiveArgs_StartAnchoredUTC proves the live --start value carries an explicit UTC offset,
// so `log` interprets it independently of the host's local timezone.
func TestLiveArgs_StartAnchoredUTC(t *testing.T) {
	r := &unifiedLoggingReceiver{cfg: &Config{}}
	args := r.liveArgs("2026-06-29 13:54:42")
	if !hasFlag(args, "--start", "2026-06-29 13:54:42+0000") {
		t.Errorf("liveArgs = %v, want --start anchored with +0000", args)
	}
}

// TestLiveArgs_PredicateIsASingleArg pins the property that makes the predicate safe to accept
// unvalidated. It reaches `log` as exactly one argv element, so it cannot contribute additional
// flags — a fleet-pushed predicate can filter, never change the invocation. Building the arg
// list by string concatenation and splitting (rather than appending elements) would break this
// silently, since a predicate without spaces would still work.
func TestLiveArgs_PredicateIsASingleArg(t *testing.T) {
	// Spaces, quotes, and characters that would matter to a shell but are inert in argv.
	p := "eventMessage CONTAINS 'a b; $HOME `x` --archive /tmp/evil'"
	r := &unifiedLoggingReceiver{cfg: &Config{Predicate: p}}
	args := r.liveArgs("2026-06-29 13:54:42")

	if !hasFlag(args, "--predicate", p) {
		t.Fatalf("predicate must occupy exactly one argv element; args = %#v", args)
	}
	// Nothing from the predicate may appear as its own element.
	for i, a := range args {
		if a == "--archive" || a == "/tmp/evil" {
			t.Errorf("args[%d] = %q: predicate content leaked into a separate argv element", i, a)
		}
	}
}

// TestLiveArgs_NoPredicateOmitsTheFlag guards the empty case: passing --predicate "" would make
// `log` reject the invocation rather than read unfiltered.
func TestLiveArgs_NoPredicateOmitsTheFlag(t *testing.T) {
	r := &unifiedLoggingReceiver{cfg: &Config{}}
	for _, a := range r.liveArgs("2026-06-29 13:54:42") {
		if a == "--predicate" {
			t.Error("--predicate must be omitted entirely when no predicate is configured")
		}
	}
}
