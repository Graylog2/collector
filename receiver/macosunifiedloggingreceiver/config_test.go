// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package macosunifiedloggingreceiver

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap/confmaptest"
)

func TestConfigValidate(t *testing.T) {
	testCases := []struct {
		desc    string
		makeCfg func(t *testing.T) *Config
		// omitStorage keeps the loop from supplying a StorageID, for the cases that are about
		// its absence. Every other case exercises an unrelated field and would otherwise trip
		// the storage check before reaching what it means to test.
		omitStorage bool
		expectedErr string
	}{
		{
			desc: "storage is required",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{MaxPollInterval: 50 * time.Second}
			},
			omitStorage: true,
			expectedErr: "storage is required",
		},
		{
			desc: "min_poll_interval below the floor is rejected",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{MinPollInterval: time.Millisecond}
			},
			expectedErr: "must be at least 100ms",
		},
		{
			desc: "min_poll_interval at the floor is accepted",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{MinPollInterval: 100 * time.Millisecond}
			},
		},
		{
			desc: "min_poll_interval unset means default, not below the floor",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{}
			},
		},
		{
			desc: "min_poll_interval must not be negative",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{MinPollInterval: -time.Second}
			},
			expectedErr: "must not be negative",
		},
		{
			desc: "min_poll_interval must not exceed max_poll_interval",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{MinPollInterval: time.Minute, MaxPollInterval: time.Second}
			},
			expectedErr: "must not exceed max_poll_interval",
		},
		{
			desc: "valid config - live mode",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					MaxPollInterval: 50 * time.Second,
					MaxLogAge:       12 * time.Hour,
				}
			},
		},
		{
			desc: "invalid format is rejected",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{Format: "yaml"}
			},
			expectedErr: "invalid format",
		},
		{
			desc: "invalid start_time is rejected",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{StartTime: "yesterday"}
			},
			expectedErr: "invalid start_time format",
		},
		// The predicate is not validated in-process; `log` is the authority on its grammar. These
		// cases pin that: shapes the old lexical validator wrongly rejected must now pass
		// Validate untouched, and reach `log` byte-for-byte as configured.
		{
			desc: "predicate with a shell metacharacter inside a string literal is accepted",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{Predicate: "eventMessage CONTAINS 'exit code 1; retry $HOME `x` a|b'"}
			},
		},
		{
			desc: "predicate using a field the log help text omits is accepted",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{Predicate: "messageType == 'Error'"}
			},
		},
	}

	storageID := component.MustNewID("file_storage")
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := tc.makeCfg(t)
			if !tc.omitStorage {
				cfg.StorageID = &storageID
			}
			original := cfg.Predicate
			err := cfg.Validate()
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, original, cfg.Predicate,
				"Validate must not rewrite the predicate; the old &&->AND replacement corrupted string literals")
		})
	}
}

func TestLoadConfigFromYAML(t *testing.T) {

	testCases := []struct {
		name           string
		configKey      string
		expectedPred   string
		expectedStart  string
		expectedPoll   time.Duration
		expectedMaxAge time.Duration
	}{
		{
			name:           "live mode defaults",
			configKey:      "live_mode_defaults",
			expectedPoll:   30 * time.Second,
			expectedMaxAge: 24 * time.Hour,
		},
		{
			name:           "live mode with predicate",
			configKey:      "live_mode_predicate",
			expectedPred:   "process == 'kernel' AND messageType == 'Error'",
			expectedPoll:   15 * time.Second,
			expectedMaxAge: 12 * time.Hour,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Load the config from YAML file
			cm, err := confmaptest.LoadConf(filepath.Join("testdata", "test_config.yaml"))
			require.NoError(t, err)

			// Get the specific config section
			sub, err := cm.Sub(tc.configKey)
			require.NoError(t, err)

			// Unmarshal into Config struct
			cfg := &Config{}
			err = sub.Unmarshal(cfg)
			require.NoError(t, err)

			// Verify the config values were parsed correctly
			require.Equal(t, tc.expectedPred, cfg.Predicate, "predicate mismatch")
			require.Equal(t, tc.expectedStart, cfg.StartTime, "start_time mismatch")

			// storage is a *component.ID, so unlike the other fields it round-trips through
			// component.ID's TextUnmarshaler; assert the "type/name" form actually parses.
			require.NotNil(t, cfg.StorageID, "storage did not unmarshal")
			require.Equal(t, "file_storage/default", cfg.StorageID.String(), "storage mismatch")

			if tc.expectedPoll > 0 {
				require.Equal(t, tc.expectedPoll, cfg.MaxPollInterval, "max_poll_interval mismatch")
			}
			if tc.expectedMaxAge > 0 {
				require.Equal(t, tc.expectedMaxAge, cfg.MaxLogAge, "max_log_age mismatch")
			}

			// Validate the config (should pass for valid configs)
			err = cfg.Validate()
			require.NoError(t, err)
		})
	}
}
