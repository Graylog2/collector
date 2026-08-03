// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package macosunifiedloggingreceiver // import "github.com/Graylog2/collector/receiver/macosunifiedloggingreceiver"

import (
	"errors"
	"fmt"
	"time"
)

// minPollFloor is the lowest accepted min_poll_interval. `log show` writes a record for its
// own invocation, so a sub-100ms floor lets the receiver's own reads sustain a self-feeding
// hot loop: each poll observes the previous poll and immediately schedules another.
const minPollFloor = 100 * time.Millisecond

// Validate checks the Config is valid.
//
// The predicate is deliberately not validated here. `log` has a real NSPredicate parser and is
// the only authority on what it accepts; an in-process approximation was both too strict
// (rejecting `messageType == 'Error'`, and any filter whose string literal contained `$`, `;`,
// `|` or a backtick) and too permissive (accepting `processIDX == 'garbage'`, because it matched
// field names as substrings of the whole expression). A bad predicate now surfaces on the first
// poll with `log`'s own diagnostic. See the predicate notes in README.md.
func (cfg *Config) Validate() error {
	// Checked here as well as in Start so `otelcol validate` rejects the config offline,
	// instead of the collector failing on the first startup attempt.
	if cfg.StorageID == nil {
		return errors.New("storage is required: set 'storage:' to the ID of a configured storage extension (for example file_storage/default)")
	}

	// Set default format if not specified
	if cfg.Format == "" {
		cfg.Format = "default"
	}

	// Validate format
	validFormats := map[string]bool{
		"default": true,
		"ndjson":  true,
		"json":    true,
		"syslog":  true,
		"compact": true,
	}
	if !validFormats[cfg.Format] {
		return fmt.Errorf("invalid format: %s (valid options: default, ndjson, json, syslog, compact)", cfg.Format)
	}

	// Validate time format if specified
	if cfg.StartTime != "" {
		if _, err := time.Parse("2006-01-02 15:04:05", cfg.StartTime); err != nil {
			return fmt.Errorf("invalid start_time format (expected: 2006-01-02 15:04:05): %w", err)
		}
	}

	// Zero means "unset": newUnifiedLoggingReceiver substitutes the 1s default, so it stays
	// legal. Any explicit value must clear the floor.
	if cfg.MinPollInterval < 0 {
		return errors.New("min_poll_interval must not be negative")
	}
	if cfg.MinPollInterval != 0 && cfg.MinPollInterval < minPollFloor {
		return fmt.Errorf("min_poll_interval (%s) must be at least %s: `log show` logs its own invocation, so a shorter interval can sustain a self-feeding poll loop", cfg.MinPollInterval, minPollFloor)
	}
	if cfg.MaxPollInterval > 0 && cfg.MinPollInterval > cfg.MaxPollInterval {
		return fmt.Errorf("min_poll_interval (%s) must not exceed max_poll_interval (%s)", cfg.MinPollInterval, cfg.MaxPollInterval)
	}

	return nil
}
