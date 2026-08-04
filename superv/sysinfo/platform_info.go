// Copyright (C)  2026 Graylog, Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the Server Side Public License, version 1,
// as published by MongoDB, Inc.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// Server Side Public License for more details.
//
// You should have received a copy of the Server Side Public License
// along with this program. If not, see
// <http://www.mongodb.com/licensing/server-side-public-license>.
//
// SPDX-License-Identifier: SSPL-1.0

package sysinfo

import (
	"runtime"

	"github.com/shirou/gopsutil/v4/host"
)

type PlatformInfo struct {
	OS      string
	Name    string
	Family  string
	Version string
}

// GetPlatformInfo returns host platform data using the narrow
// host.PlatformInformation probe. host.Info would fail if any of its
// unrelated probes (host ID, boot time, uptime, ...) fails, and would
// query the host ID on a code path that only needs non-identifying data.
func GetPlatformInfo() (PlatformInfo, error) {
	platform, family, version, err := host.PlatformInformation()
	if err != nil {
		return PlatformInfo{}, err
	}
	return PlatformInfo{
		OS:      runtime.GOOS,
		Name:    platform,
		Family:  family,
		Version: version,
	}, nil
}
