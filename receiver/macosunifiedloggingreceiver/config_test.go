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
				config := createDefaultConfig().(*Config)
				return config
			},
			omitStorage: true,
			expectedErr: "storage is required",
		},
		{
			desc: "min_poll_interval below the floor is rejected",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				config.MinPollInterval = time.Millisecond
				return config
			},
			expectedErr: "must be at least 100ms",
		},
		{
			desc: "min_poll_interval at the floor is accepted",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				config.MinPollInterval = 100 * time.Millisecond
				return config
			},
		},
		{
			desc: "min_poll_interval unset means default, not below the floor",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				return config
			},
		},
		{
			desc: "min_poll_interval must be positive",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				config.MinPollInterval = -time.Second
				return config
			},
			expectedErr: "must be positive",
		},
		{
			desc: "min_poll_interval must not exceed max_poll_interval",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				config.MinPollInterval = time.Minute
				config.MaxPollInterval = time.Second
				return config
			},
			expectedErr: "must not exceed max_poll_interval",
		},
		{
			desc: "valid config - live mode",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				config.MaxPollInterval = 50 * time.Second
				config.MaxLogAge = 12 * time.Hour
				return config
			},
		},
		{
			desc: "invalid format is rejected",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				config.Format = "yaml"
				return config
			},
			expectedErr: "invalid format",
		},
		{
			desc: "invalid start_time is rejected",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				config.StartTime = "yesterday"
				return config
			},
			expectedErr: "invalid start_time format",
		},
		// The predicate is not validated in-process; `log` is the authority on its grammar. These
		// cases pin that: shapes the old lexical validator wrongly rejected must now pass
		// Validate untouched, and reach `log` byte-for-byte as configured.
		{
			desc: "predicate with a shell metacharacter inside a string literal is accepted",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				config.Predicate = "eventMessage CONTAINS 'exit code 1; retry $HOME `x` a|b'"
				return config
			},
		},
		{
			desc: "predicate using a field the log help text omits is accepted",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				config.Predicate = "messageType == 'Error'"
				return config
			},
		},
		{
			desc: "negative max_log_age is rejected",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				config.MaxLogAge = -defaultMaxLogAge
				return config
			},
			expectedErr: "max_log_age must not be negative",
		},
		{
			desc: "zero max_log_age is valid",
			makeCfg: func(_ *testing.T) *Config {
				config := createDefaultConfig().(*Config)
				config.MaxLogAge = 0
				return config
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
			cfg := createDefaultConfig().(*Config)
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
