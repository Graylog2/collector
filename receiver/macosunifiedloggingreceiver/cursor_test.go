// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"testing"
	"time"
)

// ev builds a logEvent the way parseLogEvent would, deriving parsedTime/utcSecond from ts so
// the cursor sees the same UTC-normalized second production code does. Test timestamps are
// always valid, so a parse error here is a bug in the test's input.
func ev(mach, thread int64, boot, ts string) *logEvent {
	t, err := time.Parse(timestampLayout, ts)
	if err != nil {
		panic("ev: invalid test timestamp " + ts + ": " + err.Error())
	}
	return &logEvent{
		MachTimestamp:    mach,
		ThreadID:         thread,
		BootUUID:         boot,
		Timestamp:        ts,
		parsedTime:       t,
		utcSecondClamped: t.UTC().Format(startLayout),
	}
}

// One poll covering two seconds; commit; second poll re-fetches the boundary second
// (whole-second --start overlap) and must NOT re-emit those events.
func TestCursor_DedupAcrossPolls(t *testing.T) {
	c := newCursor("")

	c.beginPoll()
	p1 := []*logEvent{
		ev(100, 1, "A", "2026-06-29 10:00:01.100000+0000"),
		ev(200, 2, "A", "2026-06-29 10:00:02.200000+0000"),
		ev(300, 3, "A", "2026-06-29 10:00:02.900000+0000"),
	}
	got1 := []bool{c.shouldEmit(p1[0]), c.shouldEmit(p1[1]), c.shouldEmit(p1[2])}
	c.recordDelivered(p1)
	c.commit()
	for i, e := range got1 {
		if !e {
			t.Fatalf("first poll event %d should emit", i)
		}
	}
	if c.startArg() != "2026-06-29 10:00:02" {
		t.Fatalf("startArg = %q, want boundary second", c.startArg())
	}

	// Second poll: --start floored to 10:00:02 re-delivers the two :02 events + a new one.
	c.beginPoll()
	reDup1 := c.shouldEmit(ev(200, 2, "A", "2026-06-29 10:00:02.200000+0000")) // dup
	reDup2 := c.shouldEmit(ev(300, 3, "A", "2026-06-29 10:00:02.900000+0000")) // dup
	e3 := ev(400, 4, "A", "2026-06-29 10:00:03.000000+0000")
	fresh := c.shouldEmit(e3) // new
	c.recordDelivered([]*logEvent{e3})
	c.commit()
	if reDup1 || reDup2 {
		t.Errorf("boundary-second events must be deduped, got %v/%v", reDup1, reDup2)
	}
	if !fresh {
		t.Errorf("new event must emit")
	}
	if c.startArg() != "2026-06-29 10:00:03" {
		t.Errorf("startArg should advance to %q", c.startArg())
	}
}

func TestCursor_RebootResets(t *testing.T) {
	c := newCursor("")
	c.beginPoll()
	e1 := ev(900, 1, "A", "2026-06-29 10:00:09.000000+0000")
	c.shouldEmit(e1)
	c.recordDelivered([]*logEvent{e1})
	c.commit()
	// New boot: machTimestamp resets low; must NOT be treated as an old dup.
	c.beginPoll()
	e2 := ev(5, 1, "B", "2026-06-29 11:00:00.000000+0000")
	emit := c.shouldEmit(e2)
	c.recordDelivered([]*logEvent{e2})
	c.commit()
	if !emit {
		t.Errorf("post-reboot event must emit")
	}
	if c.startArg() != "2026-06-29 11:00:00" {
		t.Errorf("startArg should follow new boot, got %q", c.startArg())
	}
}

func TestCursor_IdlePollKeepsCursor(t *testing.T) {
	c := newCursor("")
	c.beginPoll()
	e1 := ev(100, 1, "A", "2026-06-29 10:00:01.000000+0000")
	c.shouldEmit(e1)
	c.recordDelivered([]*logEvent{e1})
	c.commit()
	before := c.startArg()
	c.beginPoll() // no events
	c.commit()
	if c.startArg() != before {
		t.Errorf("idle poll must not move cursor: %q -> %q", before, c.startArg())
	}
}

func TestCursor_RoundTrip(t *testing.T) {
	c := newCursor(predicateHash(`process == "corecaptured"`))
	c.beginPoll()
	p := []*logEvent{
		ev(100, 1, "A", "2026-06-29 10:00:01.000000+0000"),
		ev(150, 2, "A", "2026-06-29 10:00:01.500000+0000"),
	}
	c.shouldEmit(p[0])
	c.shouldEmit(p[1])
	c.recordDelivered(p)
	c.commit()
	data, err := c.marshal()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadCursor(data)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.predicateHash != c.predicateHash {
		t.Error("predicateHash mismatch after round-trip")
	}
	if loaded.startArg() != c.startArg() {
		t.Error("startArg mismatch after round-trip")
	}
	// The restored boundary-second identities still dedupe.
	loaded.beginPoll()
	if loaded.shouldEmit(ev(150, 2, "A", "2026-06-29 10:00:01.500000+0000")) {
		t.Errorf("restored cursor must still dedupe boundary identities")
	}
}
