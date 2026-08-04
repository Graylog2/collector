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

package testsysinfo

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Graylog2/collector/superv/internal/testfixtures"
	"github.com/Graylog2/collector/superv/sysinfo"
	"github.com/stretchr/testify/require"
)

func GetOSReleaseSupplier(t *testing.T, name string) func() (sysinfo.OSRelease, error) {
	t.Helper()

	// os-release files only exist on Linux
	if name == "" || strings.Contains(name, "macos") || strings.Contains(name, "windows") {
		return func() (sysinfo.OSRelease, error) {
			return sysinfo.OSRelease{}, nil
		}
	}

	file, err := testfixtures.OsReleaseFS.Open(filepath.Join("testdata", "os-release", "os-release-"+name+".txt"))
	require.NoError(t, err)

	osRelease, err := sysinfo.ParseOSRelease(file)
	require.NoError(t, err)

	return func() (sysinfo.OSRelease, error) {
		return osRelease, nil
	}
}
