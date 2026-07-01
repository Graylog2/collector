// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"testing"
	"time"
)

func TestCadence_BackoffAndReset(t *testing.T) {
	c := newCadence(time.Second, 8*time.Second)

	// Idle polls double from the floor up to max.
	want := []time.Duration{1, 2, 4, 8, 8}
	for i, w := range want {
		if got := c.next(0); got != w*time.Second {
			t.Errorf("idle step %d: got %v want %v", i, got, w*time.Second)
		}
	}
	// A poll that found logs resets to the floor.
	if got := c.next(5); got != time.Second {
		t.Errorf("reset on count>0: got %v want 1s", got)
	}
}
