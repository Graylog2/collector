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

package supervisor

import (
	"encoding/json"
	"errors"
	"path"
	"testing"

	"github.com/Graylog2/collector/superv/internal/testfixtures"
	"github.com/Graylog2/collector/superv/internal/testsysinfo"
	"github.com/Graylog2/collector/superv/sysinfo"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSupervisor_NonIdentifyingAttributes_WithCollectorVersion(t *testing.T) {
	s := &Supervisor{
		collectorVersion: "2.0.0-alpha.0",
		logger:           zap.NewNop(),
	}

	attrs := s.nonIdentifyingAttributes("test-host")

	attrMap := make(map[string]string)
	for _, kv := range attrs {
		attrMap[kv.GetKey()] = kv.GetValue().GetStringValue()
	}

	require.Equal(t, "test-host", attrMap["host.name"])
	require.NotEmpty(t, attrMap["service.version"])
	require.NotEmpty(t, attrMap["os.type"])
	require.NotEmpty(t, attrMap["host.arch"])
	require.Equal(t, "2.0.0-alpha.0", attrMap["collector.version"])
}

func TestSupervisor_NonIdentifyingAttributes_WithoutCollectorVersion(t *testing.T) {
	s := &Supervisor{logger: zap.NewNop()}

	attrs := s.nonIdentifyingAttributes("test-host")

	attrMap := make(map[string]string)
	for _, kv := range attrs {
		attrMap[kv.GetKey()] = kv.GetValue().GetStringValue()
	}

	require.Equal(t, "test-host", attrMap["host.name"])
	require.NotEmpty(t, attrMap["service.version"])
	_, hasCollectorVersion := attrMap["collector.version"]
	require.False(t, hasCollectorVersion, "collector.version should not be present when empty")
}

func loadHostInfo(t *testing.T, name string) *host.InfoStat {
	t.Helper()

	// Using path.Join to avoid backslashes on windows. The embed.FS always uses slashes.
	data, err := testfixtures.HostInfoFS.ReadFile(path.Join("testdata", "hostinfo", "hostinfo-"+name+".json"))
	require.NoError(t, err)

	info := &host.InfoStat{}
	require.NoError(t, json.Unmarshal(data, info))

	return info
}

func TestGetOSDescription(t *testing.T) {
	tests := []struct {
		os      string
		fixture string
		want    string
	}{
		{"linux", "almalinux-8", "AlmaLinux 8.10"},
		{"linux", "almalinux-9", "AlmaLinux 9.7"},
		{"linux", "almalinux-10", "AlmaLinux 10.2"},
		{"linux", "alpine-3", "Alpine Linux 3.23.3"},
		{"linux", "amazonlinux-2023", "Amazon Linux 2023"},
		{"linux", "arch-2026-08", "Arch Linux 20260726.0.562117"},
		{"linux", "debian-13", "Debian GNU/Linux 13"},
		{"linux", "opensuse-tumbleweed-2026-08", "openSUSE Tumbleweed 20260802"},
		{"linux", "ubuntu-24.04", "Ubuntu 24.04"},
		{"linux", "ubuntu-26.04", "Ubuntu 26.04"},
		{"darwin", "macos-26", "macOS 26.5.2"},
		{"windows", "windows-2019", "Microsoft Windows Server 2019 Datacenter"},
		{"windows", "windows-2022", "Microsoft Windows Server 2022 Datacenter 21H2"},
		{"windows", "windows-2025", "Microsoft Windows Server 2025 Datacenter 24H2"},
	}

	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			description, err := getOSDescription(tc.os, func() (*host.InfoStat, error) {
				info := loadHostInfo(t, tc.fixture)
				return info, nil
			}, testsysinfo.GetOSReleaseSupplier(t, tc.fixture))
			require.NoError(t, err)
			require.Equal(t, tc.want, description)
		})
	}

	t.Run("all hostinfo testdata files are covered", func(t *testing.T) {
		// Using path.Join to avoid backslashes on windows. The embed.FS always uses slashes.
		files, err := testfixtures.HostInfoFS.ReadDir(path.Join("testdata", "hostinfo"))
		require.NoError(t, err)
		require.NotEmpty(t, files)

		covered := make(map[string]bool, len(tests))
		for _, tc := range tests {
			covered["hostinfo-"+tc.fixture+".json"] = true
		}
		for _, file := range files {
			assert.True(t, covered[file.Name()], "missing test case for %s", file.Name())
		}
	})
}

func TestGetOSDescription_UnknownOS(t *testing.T) {
	description, err := getOSDescription("freebsd", func() (*host.InfoStat, error) {
		return &host.InfoStat{
			OS:              "freebsd",
			Platform:        "freebsd",
			PlatformVersion: "14.1",
		}, nil
	}, testsysinfo.GetOSReleaseSupplier(t, ""))
	require.NoError(t, err)
	require.Equal(t, "freebsd 14.1", description)
}

func TestGetOSDescription_UnknownOSWithoutPlatform(t *testing.T) {
	description, err := getOSDescription("freebsd", func() (*host.InfoStat, error) {
		return &host.InfoStat{OS: "freebsd"}, nil
	}, testsysinfo.GetOSReleaseSupplier(t, ""))
	require.NoError(t, err)
	require.Equal(t, "Unknown freebsd", description)
}

func TestGetOSDescription_LinuxPrettyNameFallback(t *testing.T) {
	description, err := getOSDescription("linux", nil, func() (sysinfo.OSRelease, error) {
		return sysinfo.OSRelease{PrettyName: "Foo Linux 1.0 (Bar)"}, nil
	})
	require.NoError(t, err)
	require.Equal(t, "Foo Linux 1.0 (Bar)", description)
}

func TestGetOSDescription_LinuxEmptyOSReleaseFallsBackToHostInfo(t *testing.T) {
	description, err := getOSDescription("linux", func() (*host.InfoStat, error) {
		return &host.InfoStat{OS: "linux", Platform: "ubuntu", PlatformVersion: "24.04"}, nil
	}, func() (sysinfo.OSRelease, error) {
		return sysinfo.OSRelease{}, nil
	})
	require.NoError(t, err)
	require.Equal(t, "ubuntu 24.04", description)
}

func TestGetOSDescription_LinuxOSReleaseErrorFallsBackToHostInfo(t *testing.T) {
	description, err := getOSDescription("linux", func() (*host.InfoStat, error) {
		return &host.InfoStat{OS: "linux", Platform: "ubuntu", PlatformVersion: "24.04"}, nil
	}, func() (sysinfo.OSRelease, error) {
		return sysinfo.OSRelease{}, errors.New("no os-release file")
	})
	require.NoError(t, err)
	require.Equal(t, "ubuntu 24.04", description)
}

func TestGetOSDescription_LinuxAllSourcesFailing(t *testing.T) {
	_, err := getOSDescription("linux", func() (*host.InfoStat, error) {
		return nil, errors.New("no host info")
	}, func() (sysinfo.OSRelease, error) {
		return sysinfo.OSRelease{}, errors.New("no os-release file")
	})
	require.Error(t, err)
}
