// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const timestampLayout = "2006-01-02 15:04:05.000000-0700"

// logEvent is one macOS unified-logging ndjson record (subset of fields we use). Fields absent
// from the record are not emitted as attributes, so a value in the pipeline always came from
// the source.
//
// For strings that falls out of the zero value: "" carries no information, so putStr's empty
// check covers both absent and empty. Integers need explicit presence, because 0 is a real
// value — uid 0 is root, pid 0 is the kernel — and encoding/json cannot distinguish a missing
// number from a zero one. Every optional integer is therefore a pointer. Signed values are
// emitted via putInt; unsigned identifiers are emitted via putUint. Keep fields as pointers
// when adding them: a plain integer silently reports 0 for every record the source did not
// populate.
//
// MachTimestamp and ThreadID are the exception because they are not optional, not because the
// rule bends: parseLogEvent rejects any record without a machTimestamp, and both make up the
// cursor's dedup key, so a nil there would be a bug rather than a legitimate absence.
type logEvent struct {
	Timestamp                string  `json:"timestamp"`
	MachTimestamp            uint64  `json:"machTimestamp"`
	ThreadID                 uint64  `json:"threadID"`
	BootUUID                 string  `json:"bootUUID"`
	EventMessage             string  `json:"eventMessage"`
	MessageType              string  `json:"messageType"`
	EventType                string  `json:"eventType"`
	Subsystem                string  `json:"subsystem"`
	Category                 string  `json:"category"`
	ProcessID                *int64  `json:"processID"`
	UserID                   *int64  `json:"userID"`
	ProcessImagePath         string  `json:"processImagePath"`
	ProcessImageUUID         string  `json:"processImageUUID"`
	SenderImagePath          string  `json:"senderImagePath"`
	SenderImageUUID          string  `json:"senderImageUUID"`
	SenderProgramCounter     *uint64 `json:"senderProgramCounter"`
	ActivityIdentifier       *uint64 `json:"activityIdentifier"`
	ParentActivityIdentifier *uint64 `json:"parentActivityIdentifier"`
	CreatorActivityID        *uint64 `json:"creatorActivityID"`
	TraceID                  *uint64 `json:"traceID"`
	FormatString             string  `json:"formatString"`

	parsedTime       time.Time // offset-aware timestamp (from Timestamp)
	utcSecondClamped string    // seconds-resolution floor of Timestamp for use with log show --start
	raw              string    // original line, used as a body fallback when eventMessage is empty
}

// parseLogEvent parses one ndjson line. It returns (nil, nil) for non-events: the
// trailing {"count":N,"finished":1} footer, blank/whitespace lines, and any object
// lacking machTimestamp or timestamp. It returns (nil, err) on malformed JSON or an
// unparseable timestamp — `log` emits a fixed format, so either is a real anomaly rather
// than something to absorb silently.
//
// An event that cannot be timed cannot be positioned against the cursor, so pollOnce drops
// it (logging at Error) and continues. That is a deliberate trade: the alternative — refusing
// to advance past it — would wedge the receiver on the bad line forever and stop collection
// entirely. The consequence is real data loss, because once later events advance the cursor
// past that second the dropped record is unrecoverable.
func parseLogEvent(line []byte) (*logEvent, error) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, nil
	}
	var e logEvent
	if err := json.Unmarshal(trimmed, &e); err != nil {
		return nil, err
	}
	if e.MachTimestamp == 0 || e.Timestamp == "" {
		return nil, nil
	}
	t, err := time.Parse(timestampLayout, e.Timestamp)
	if err != nil {
		return nil, err
	}
	e.parsedTime = t
	e.utcSecondClamped = t.UTC().Format(startLayout)
	e.raw = string(trimmed)
	return &e, nil
}

func (e *logEvent) setLogRecord(lr plog.LogRecord, now time.Time) {
	if e.EventMessage != "" {
		lr.Body().SetStr(e.EventMessage)
	} else {
		lr.Body().SetStr(e.raw)
	}
	lr.SetObservedTimestamp(pcommon.NewTimestampFromTime(now))
	lr.SetTimestamp(pcommon.NewTimestampFromTime(e.parsedTime))
	if e.MessageType != "" {
		lr.SetSeverityText(e.MessageType)
		lr.SetSeverityNumber(mapMessageTypeToSeverity(e.MessageType))
	}
	a := lr.Attributes()
	putStr(a, "macos.subsystem", e.Subsystem)
	putStr(a, "macos.category", e.Category)
	putStr(a, "macos.eventType", e.EventType)
	putStr(a, "macos.messageType", e.MessageType)
	putStr(a, "macos.processImagePath", e.ProcessImagePath)
	putStr(a, "macos.processImageUUID", e.ProcessImageUUID)
	putStr(a, "macos.senderImagePath", e.SenderImagePath)
	putStr(a, "macos.senderImageUUID", e.SenderImageUUID)
	putStr(a, "macos.formatString", e.FormatString)
	putStr(a, "macos.bootUUID", e.BootUUID)
	// Always emitted: required, and the cursor's dedup key.
	putUintValue(a, "macos.threadID", e.ThreadID)
	putUintValue(a, "macos.machTimestamp", e.MachTimestamp)
	// Optional: emitted only when the source supplied them.
	putInt(a, "macos.processID", e.ProcessID)
	putInt(a, "macos.userID", e.UserID)
	putUint(a, "macos.activityIdentifier", e.ActivityIdentifier)
	putUint(a, "macos.parentActivityIdentifier", e.ParentActivityIdentifier)
	putUint(a, "macos.creatorActivityID", e.CreatorActivityID)
	putUint(a, "macos.traceID", e.TraceID)
	putUint(a, "macos.senderProgramCounter", e.SenderProgramCounter)
}

func putStr(a pcommon.Map, key, val string) {
	if val != "" {
		a.PutStr(key, val)
	}
}

// putInt is the integer counterpart to putStr: it distinguishes absent from zero, which putStr
// does not need to do because "" carries no information while 0 does.
func putInt(a pcommon.Map, key string, val *int64) {
	if val != nil {
		a.PutInt(key, *val)
	}
}

func putUint(a pcommon.Map, key string, val *uint64) {
	if val != nil {
		putUintValue(a, key, *val)
	}
}

// putUintValue follows the OpenTelemetry mapping for unsigned source integers: values that fit
// in an OTLP signed 64-bit integer remain integers, while larger values use their lossless
// decimal string representation.
func putUintValue(a pcommon.Map, key string, val uint64) {
	if val <= math.MaxInt64 {
		a.PutInt(key, int64(val))
		return
	}
	a.PutStr(key, strconv.FormatUint(val, 10))
}
