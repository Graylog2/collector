// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import "time"

// cadence is the live-mode poll backoff. With a correct forward cursor, count reflects
// only genuinely new events, so the interval grows while idle and snaps back to the floor
// when logs appear. (Upstream's backoff never engaged because the cursor never advanced.)
type cadence struct {
	min     time.Duration
	max     time.Duration
	current time.Duration
}

func newCadence(minInterval, maxInterval time.Duration) *cadence {
	return &cadence{min: minInterval, max: maxInterval}
}

// next returns the wait before the next poll given how many events the last poll emitted.
func (c *cadence) next(count int) time.Duration {
	switch {
	case count > 0:
		c.current = c.min
	case c.current < c.min:
		c.current = c.min
	default:
		c.current = min(c.current*2, c.max)
	}
	return c.current
}
