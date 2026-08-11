// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver // import "github.com/Graylog2/collector/receiver/macosunifiedloggingreceiver"

import (
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/receiver"
)

// NewFactory creates a factory for the macOS unified logging receiver
func NewFactory() receiver.Factory {
	return newFactoryAdapter()
}

// createDefaultConfig creates a config with default values
func createDefaultConfig() component.Config {
	return &Config{
		MaxPollInterval: defaultMaxPollInterval,
		MinPollInterval: defaultMinPollInterval,
		MaxLogAge:       defaultMaxLogAge,
		Format:          "default",
	}
}
