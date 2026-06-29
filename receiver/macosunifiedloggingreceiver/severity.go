// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import "go.opentelemetry.io/collector/pdata/plog"

// mapMessageTypeToSeverity maps a macOS unified-logging messageType to an OTel severity.
func mapMessageTypeToSeverity(msgType string) plog.SeverityNumber {
	switch msgType {
	case "Error":
		return plog.SeverityNumberError
	case "Fault":
		return plog.SeverityNumberFatal
	case "Default", "Info":
		return plog.SeverityNumberInfo
	case "Debug":
		return plog.SeverityNumberDebug
	default:
		return plog.SeverityNumberUnspecified
	}
}
