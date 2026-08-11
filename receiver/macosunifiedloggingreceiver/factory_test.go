// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"

	"github.com/Graylog2/collector/receiver/macosunifiedloggingreceiver/internal/metadata"
)

// TestFactoryType pins the configuration key users write in their collector YAML. Changing it
// is a breaking change for every existing pipeline, so it should require editing this test.
func TestFactoryType(t *testing.T) {
	require.Equal(t, metadata.Type, NewFactory().Type())
	require.Equal(t, "macos_unified_logging", NewFactory().Type().String())
}

// TestConfigStructIsUnmarshalable asserts every Config field carries a mapstructure tag.
// Without one, confmap silently ignores the field: the option appears to work in YAML but
// never reaches the receiver. CheckConfigStruct walks the struct and catches that at test
// time rather than in a support ticket.
func TestConfigStructIsUnmarshalable(t *testing.T) {
	require.NoError(t, componenttest.CheckConfigStruct(createDefaultConfig()))
}
