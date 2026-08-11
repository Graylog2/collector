// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package macosunifiedloggingreceiver implements a receiver that uses the native
// macOS `log` command to retrieve and parse unified logging data from the live system log.
//
// Reading archived log files (.logarchive) is not supported. A storage extension is
// required so the read cursor survives restarts; see Config.StorageID.
package macosunifiedloggingreceiver // import "github.com/Graylog2/collector/receiver/macosunifiedloggingreceiver"
