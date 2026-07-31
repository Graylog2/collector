// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package metadata contains the component identity and telemetry constants for the
// macOS unified logging receiver.
//
// This package was originally produced by OpenTelemetry's mdatagen from a metadata.yaml
// descriptor. mdatagen is not wired into this repository (it is not a dependency and there
// is no generate target), so these values are now maintained by hand. Edit them directly.
package metadata

import (
	"go.opentelemetry.io/collector/component"
)

var (
	// Type is the component type used in collector configuration.
	Type = component.MustNewType("macos_unified_logging")

	// DeprecatedType is the previous component type, still accepted as an alias.
	DeprecatedType = component.MustNewType("macosunifiedlogging")

	// ScopeName identifies this receiver as the instrumentation scope on emitted logs.
	ScopeName = "github.com/Graylog2/collector/receiver/macosunifiedloggingreceiver"
)

// LogsStability is the stability level of this receiver's logs pipeline.
const LogsStability = component.StabilityLevelAlpha
