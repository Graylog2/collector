// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

func TestMapMessageTypeToSeverity(t *testing.T) {
	cases := map[string]plog.SeverityNumber{
		"Error":   plog.SeverityNumberError,
		"Fault":   plog.SeverityNumberFatal,
		"Default": plog.SeverityNumberInfo,
		"Info":    plog.SeverityNumberInfo,
		"Debug":   plog.SeverityNumberDebug,
		"":        plog.SeverityNumberUnspecified,
		"weird":   plog.SeverityNumberUnspecified,
	}
	for in, want := range cases {
		if got := mapMessageTypeToSeverity(in); got != want {
			t.Errorf("mapMessageTypeToSeverity(%q) = %v, want %v", in, got, want)
		}
	}
}
