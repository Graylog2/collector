// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

const sampleEvent = `{"timestamp":"2026-06-29 15:54:42.063082+0200","machTimestamp":12868010147176,"threadID":11537025,"bootUUID":"BOOT-A","eventMessage":"hello world","messageType":"Error","eventType":"logEvent","subsystem":"com.apple.bluetooth","category":"Server.LE.Scan","processID":401,"userID":205,"processImagePath":"/usr/sbin/bluetoothd","processImageUUID":"PUUID","senderImagePath":"/usr/sbin/bluetoothd","senderImageUUID":"SUUID","senderProgramCounter":7787736,"activityIdentifier":0,"parentActivityIdentifier":0,"creatorActivityID":0,"traceID":45473881108119556,"formatString":"fmt %@"}`

func TestParseLogEvent_SkipsNonEvents(t *testing.T) {
	for _, line := range []string{
		`{"count":3335,"finished":1}`, // footer
		``,                            // blank
		`   `,                         // whitespace
		`not json`,                    // non-JSON prefix
	} {
		e, err := parseLogEvent([]byte(line))
		if err != nil || e != nil {
			t.Errorf("parseLogEvent(%q) = (%v,%v), want (nil,nil)", line, e, err)
		}
	}
}

func TestParseLogEvent_MalformedJSON(t *testing.T) {
	if _, err := parseLogEvent([]byte(`{"machTimestamp":1,`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// TestParseLogEvent_UnparseableTimestamp pins the contract: a well-formed JSON event whose
// timestamp does not parse is a hard error (skipped + logged by the caller), never emitted
// with a fallback. This keeps un-timeable events out of the cursor.
func TestParseLogEvent_UnparseableTimestamp(t *testing.T) {
	line := `{"machTimestamp":1,"threadID":2,"timestamp":"not-a-timestamp"}`
	if e, err := parseLogEvent([]byte(line)); err == nil {
		t.Fatalf("expected error for unparseable timestamp, got event %+v", e)
	}
}

func TestParseLogEvent_Fields(t *testing.T) {
	e, err := parseLogEvent([]byte(sampleEvent))
	if err != nil || e == nil {
		t.Fatalf("parseLogEvent error=%v event=%v", err, e)
	}
	if e.MachTimestamp != 12868010147176 || e.ThreadID != 11537025 || e.BootUUID != "BOOT-A" {
		t.Errorf("dedup fields wrong: %+v", e)
	}
	// The sample event is in +0200; wallSecond floors it to the second in UTC.
	if got := e.utcSecondClamped; got != "2026-06-29 13:54:42" {
		t.Errorf("wallSecond = %q, want the UTC-normalized second", got)
	}
}

func TestSetLogRecord(t *testing.T) {
	e, _ := parseLogEvent([]byte(sampleEvent))
	lr := plog.NewLogs().ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	e.setLogRecord(lr, time.Unix(0, 0))

	if lr.Body().Str() != "hello world" {
		t.Errorf("body = %q, want eventMessage", lr.Body().Str())
	}
	if lr.SeverityNumber() != plog.SeverityNumberError || lr.SeverityText() != "Error" {
		t.Errorf("severity = %v/%q", lr.SeverityNumber(), lr.SeverityText())
	}
	attrs := lr.Attributes()
	if v, ok := attrs.Get("macos.subsystem"); !ok || v.Str() != "com.apple.bluetooth" {
		t.Errorf("macos.subsystem missing/wrong")
	}
	if v, ok := attrs.Get("macos.processID"); !ok || v.Int() != 401 {
		t.Errorf("macos.processID missing/wrong")
	}
	if v, ok := attrs.Get("macos.userID"); !ok || v.Int() != 205 {
		t.Errorf("macos.userID missing/wrong")
	}
	if v, ok := attrs.Get("macos.machTimestamp"); !ok || v.Int() != 12868010147176 {
		t.Errorf("macos.machTimestamp missing/wrong")
	}
	wantTS := time.Date(2026, 6, 29, 15, 54, 42, 63082000, time.FixedZone("", 2*3600))
	if !lr.Timestamp().AsTime().Equal(wantTS) {
		t.Errorf("timestamp = %v, want %v", lr.Timestamp().AsTime(), wantTS)
	}
}

func TestWallSecond_TimezoneStable(t *testing.T) {
	// 2026-06-29 15:54:42+0200 and 2026-06-29 13:54:42+0000 are the same instant.
	plus2 := `{"machTimestamp":1,"threadID":2,"timestamp":"2026-06-29 15:54:42.500000+0200"}`
	utc := `{"machTimestamp":1,"threadID":2,"timestamp":"2026-06-29 13:54:42.500000+0000"}`

	a, err := parseLogEvent([]byte(plus2))
	if err != nil || a == nil {
		t.Fatalf("parseLogEvent(+0200) = (%v, %v)", a, err)
	}
	b, err := parseLogEvent([]byte(utc))
	if err != nil || b == nil {
		t.Fatalf("parseLogEvent(+0000) = (%v, %v)", b, err)
	}
	if a.utcSecondClamped != "2026-06-29 13:54:42" {
		t.Errorf("wallSecond(+0200) = %q, want UTC-normalized 2026-06-29 13:54:42", a.utcSecondClamped)
	}
	if a.utcSecondClamped != b.utcSecondClamped {
		t.Errorf("wallSecond differs across offsets: %q vs %q", a.utcSecondClamped, b.utcSecondClamped)
	}
}
