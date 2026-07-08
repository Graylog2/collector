// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// predicateHash is the identity a persisted cursor is valid for. If the configured
// predicate changes, wallSecond/seen no longer describe the same event stream (a broader
// predicate would match past events before wallSecond that we'd otherwise skip forever),
// so a hash mismatch forces a fresh start. This is conservative: narrowing the predicate
// is actually safe to resume, but a hash only reports "changed", not "broader vs narrower",
// so every change resets. Distinguishing the two would need semantic predicate comparison.
func predicateHash(predicate string) string {
	sum := sha256.Sum256([]byte(predicate))
	return hex.EncodeToString(sum[:])
}

type identity struct {
	Mach   int64 `json:"m"`
	Thread int64 `json:"t"`
}

// cursor tracks forward progress through the unified log. wallSecond is the floored
// second used as the next --start (whole seconds only — fractional is rejected by `log`).
// seen holds the (machTimestamp, threadID) identities at wallSecond so the re-fetched
// boundary second is deduped. batch* accumulate the in-progress poll's new boundary.
type cursor struct {
	bootUUID      string
	wallSecond    string
	seen          map[identity]struct{}
	predicateHash string

	batchSecond string
	batchSeen   map[identity]struct{}
}

func newCursor(predicateHash string) *cursor {
	return &cursor{seen: map[identity]struct{}{}, batchSeen: map[identity]struct{}{}, predicateHash: predicateHash}
}

// startArg is the --start value for the next poll ("" until the first commit).
func (c *cursor) startArg() string { return c.wallSecond }

func (c *cursor) beginPoll() {
	c.batchSecond = ""
	c.batchSeen = map[identity]struct{}{}
}

// shouldEmit reports whether e should be emitted, without mutating any cursor state. It
// returns false only when e duplicates an already-committed event at the committed boundary
// second. Dedup applies within a single boot: a bootUUID change means machTimestamp reset,
// so the committed identities no longer describe the same events. Batch progress is recorded
// separately by recordDelivered — only after the event is actually delivered — so the cursor
// never advances past records a consumer rejected.
func (c *cursor) shouldEmit(e *logEvent) bool {
	if e.BootUUID != c.bootUUID {
		return true
	}
	if e.wallSecond() != c.wallSecond {
		return true
	}
	_, dup := c.seen[identity{Mach: e.MachTimestamp, Thread: e.ThreadID}]
	return !dup
}

// recordDelivered folds a batch of successfully-delivered events into the in-progress batch
// cursor. Called only after ConsumeLogs accepted the events, so batchSecond never advances
// past data that was not delivered. A bootUUID change resets state (machTimestamp resets
// across reboots). Because --start floors to wallSecond, every returned event has
// sec >= wallSecond, so batchSecond is monotonic and never moves the cursor backward.
func (c *cursor) recordDelivered(events []*logEvent) {
	for _, e := range events {
		if e.BootUUID != c.bootUUID {
			c.bootUUID = e.BootUUID
			c.wallSecond = ""
			c.seen = map[identity]struct{}{}
			c.batchSecond = ""
			c.batchSeen = map[identity]struct{}{}
		}
		sec := e.wallSecond()
		id := identity{Mach: e.MachTimestamp, Thread: e.ThreadID}
		switch {
		case sec > c.batchSecond:
			c.batchSecond = sec
			c.batchSeen = map[identity]struct{}{id: {}}
		case sec == c.batchSecond:
			c.batchSeen[id] = struct{}{}
		}
	}
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
	BootUUID      string     `json:"bootUUID"`
	WallSecond    string     `json:"wallSecond"`
	Seen          []identity `json:"seen"`
	PredicateHash string     `json:"predicateHash"`
}

func (c *cursor) marshal() ([]byte, error) {
	s := cursorState{BootUUID: c.bootUUID, WallSecond: c.wallSecond, PredicateHash: c.predicateHash}
	for id := range c.seen {
		s.Seen = append(s.Seen, id)
	}
	return json.Marshal(s)
}

// loadCursor deserializes a persisted cursor, carrying whatever predicateHash was
// written. It does not judge validity: the caller compares the returned predicateHash
// against the current config's hash and decides whether to adopt or discard it.
func loadCursor(data []byte) (*cursor, error) {
	var s cursorState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	c := newCursor(s.PredicateHash)
	c.bootUUID = s.BootUUID
	c.wallSecond = s.WallSecond
	for _, id := range s.Seen {
		c.seen[id] = struct{}{}
	}
	return c, nil
}
