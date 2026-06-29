// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"bytes"
	"encoding/json"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const timestampLayout = "2006-01-02 15:04:05.000000-0700"

// logEvent is one macOS unified-logging ndjson record (subset of fields we use).
type logEvent struct {
	Timestamp                string `json:"timestamp"`
	MachTimestamp            int64  `json:"machTimestamp"`
	ThreadID                 int64  `json:"threadID"`
	BootUUID                 string `json:"bootUUID"`
	EventMessage             string `json:"eventMessage"`
	MessageType              string `json:"messageType"`
	EventType                string `json:"eventType"`
	Subsystem                string `json:"subsystem"`
	Category                 string `json:"category"`
	ProcessID                int64  `json:"processID"`
	ProcessImagePath         string `json:"processImagePath"`
	ProcessImageUUID         string `json:"processImageUUID"`
	SenderImagePath          string `json:"senderImagePath"`
	SenderImageUUID          string `json:"senderImageUUID"`
	SenderProgramCounter     int64  `json:"senderProgramCounter"`
	ActivityIdentifier       int64  `json:"activityIdentifier"`
	ParentActivityIdentifier int64  `json:"parentActivityIdentifier"`
	CreatorActivityID        int64  `json:"creatorActivityID"`
	TraceID                  int64  `json:"traceID"`
	FormatString             string `json:"formatString"`

	raw string // original line, used as a body fallback when eventMessage is empty
}

// parseLogEvent parses one ndjson line. It returns (nil, nil) for non-events: the
// trailing {"count":N,"finished":1} footer, blank/whitespace lines, and any object
// lacking machTimestamp or timestamp. It returns (nil, err) only on malformed JSON.
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
	e.raw = string(trimmed)
	return &e, nil
}

// wallSecond returns the event's wall-clock floored to the second in the layout the
// `log --start` flag accepts. The first 19 chars of the timestamp are exactly that.
func (e *logEvent) wallSecond() string {
	if len(e.Timestamp) >= 19 {
		return e.Timestamp[:19]
	}
	return ""
}

func (e *logEvent) setLogRecord(lr plog.LogRecord, now time.Time) {
	if e.EventMessage != "" {
		lr.Body().SetStr(e.EventMessage)
	} else {
		lr.Body().SetStr(e.raw)
	}
	lr.SetObservedTimestamp(pcommon.NewTimestampFromTime(now))
	if t, err := time.Parse(timestampLayout, e.Timestamp); err == nil {
		lr.SetTimestamp(pcommon.NewTimestampFromTime(t))
	}
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
	a.PutInt("macos.processID", e.ProcessID)
	a.PutInt("macos.threadID", e.ThreadID)
	a.PutInt("macos.machTimestamp", e.MachTimestamp)
	a.PutInt("macos.activityIdentifier", e.ActivityIdentifier)
	a.PutInt("macos.parentActivityIdentifier", e.ParentActivityIdentifier)
	a.PutInt("macos.creatorActivityID", e.CreatorActivityID)
	a.PutInt("macos.traceID", e.TraceID)
	a.PutInt("macos.senderProgramCounter", e.SenderProgramCounter)
}

func putStr(a pcommon.Map, key, val string) {
	if val != "" {
		a.PutStr(key, val)
	}
}
