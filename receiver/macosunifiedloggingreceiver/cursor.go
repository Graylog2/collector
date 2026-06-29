// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import "encoding/json"

type identity struct {
	Mach   int64 `json:"m"`
	Thread int64 `json:"t"`
}

// cursor tracks forward progress through the unified log. wallSecond is the floored
// second used as the next --start (whole seconds only — fractional is rejected by `log`).
// seen holds the (machTimestamp, threadID) identities at wallSecond so the re-fetched
// boundary second is deduped. batch* accumulate the in-progress poll's new boundary.
type cursor struct {
	bootUUID   string
	wallSecond string
	seen       map[identity]struct{}

	batchSecond string
	batchSeen   map[identity]struct{}
}

func newCursor() *cursor {
	return &cursor{seen: map[identity]struct{}{}, batchSeen: map[identity]struct{}{}}
}

// startArg is the --start value for the next poll ("" until the first commit).
func (c *cursor) startArg() string { return c.wallSecond }

func (c *cursor) beginPoll() {
	c.batchSecond = ""
	c.batchSeen = map[identity]struct{}{}
}

// consider records an event for the in-progress batch and reports whether to emit it.
// A bootUUID change resets all state (machTimestamp resets across reboots).
func (c *cursor) consider(e *logEvent) bool {
	if e.BootUUID != c.bootUUID {
		c.bootUUID = e.BootUUID
		c.wallSecond = ""
		c.seen = map[identity]struct{}{}
		c.batchSecond = ""
		c.batchSeen = map[identity]struct{}{}
	}

	sec := e.wallSecond()
	id := identity{Mach: e.MachTimestamp, Thread: e.ThreadID}

	emit := true
	if sec == c.wallSecond {
		if _, dup := c.seen[id]; dup {
			emit = false
		}
	}

	// Track the batch's max second and the identities within it. Because --start floors
	// to wallSecond, every returned event has sec >= wallSecond, so batchSecond is
	// monotonic and never moves the cursor backward.
	switch {
	case sec > c.batchSecond:
		c.batchSecond = sec
		c.batchSeen = map[identity]struct{}{id: {}}
	case sec == c.batchSecond:
		c.batchSeen[id] = struct{}{}
	}
	return emit
}

// commit folds the in-progress batch into the committed cursor. An empty batch (idle
// poll, no events) leaves the cursor unchanged.
func (c *cursor) commit() {
	if c.batchSecond != "" {
		c.wallSecond = c.batchSecond
		c.seen = c.batchSeen
	}
}

type cursorState struct {
	BootUUID   string     `json:"bootUUID"`
	WallSecond string     `json:"wallSecond"`
	Seen       []identity `json:"seen"`
}

func (c *cursor) marshal() ([]byte, error) {
	s := cursorState{BootUUID: c.bootUUID, WallSecond: c.wallSecond}
	for id := range c.seen {
		s.Seen = append(s.Seen, id)
	}
	return json.Marshal(s)
}

func loadCursor(data []byte) (*cursor, error) {
	c := newCursor()
	if len(data) == 0 {
		return c, nil
	}
	var s cursorState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	c.bootUUID = s.BootUUID
	c.wallSecond = s.WallSecond
	for _, id := range s.Seen {
		c.seen[id] = struct{}{}
	}
	return c, nil
}
