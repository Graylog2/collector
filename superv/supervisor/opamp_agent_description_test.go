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
	"os"
	"path/filepath"
	"testing"

	"github.com/shirou/gopsutil/v4/host"
	"github.com/stretchr/testify/require"
)

func TestSupervisor_NonIdentifyingAttributes_WithCollectorVersion(t *testing.T) {
	s := &Supervisor{
		collectorVersion: "2.0.0-alpha.0",
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
	s := &Supervisor{}

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

	data, err := os.ReadFile(filepath.Join("testdata", "hostinfo", "hostinfo-"+name+".json"))
	require.NoError(t, err)

	info := &host.InfoStat{}
	require.NoError(t, json.Unmarshal(data, info))

	return info
}

func TestGetOSDescription(t *testing.T) {
	tests := []struct {
		fixture string
		want    string
	}{
		{"almalinux-8", "AlmaLinux 8.10"},
		{"almalinux-9", "AlmaLinux 9.7"},
		{"almalinux-10", "AlmaLinux 10.1"},
		{"alpine-3", "Alpine 3.23.3"},
		{"amazon-2023.12.20260724", "Amazon Linux 2023.12.20260724"},
		{"arch-20260726", "Arch 20260726.0.562117"},
		{"debian-13", "Debian 13.3"},
		{"opensuse-tumbleweed-20260802", "openSUSE Tumbleweed 20260802"},
		{"linux-ubuntu-2404", "Ubuntu 24.04"},
		{"linux-ubuntu-2604", "Ubuntu 26.04"},
		{"ubuntu-2404", "Ubuntu 24.04"},
		{"ubuntu-2604", "Ubuntu 26.04"},
		{"macos-26", "macOS 26.5.2"},
		{"windows-2019", "Microsoft Windows Server 2019 Datacenter"},
		{"windows-2022", "Microsoft Windows Server 2022 Datacenter 21H2"},
		{"windows-2025", "Microsoft Windows Server 2025 Datacenter 24H2"},
	}

	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			info := loadHostInfo(t, tc.fixture)
			require.Equal(t, tc.want, getOSDescription(info))
		})
	}
}

// TestGetOSDescription_LinuxPlatformNames covers platform identifiers that
// gopsutil can report but for which we have no captured host info, ensuring
// their product names are spelled properly.
func TestGetOSDescription_LinuxPlatformNames(t *testing.T) {
	tests := []struct {
		platform string
		version  string
		want     string
	}{
		{"redhat", "9.4", "Red Hat Enterprise Linux 9.4"},
		{"rhel", "9.4", "Red Hat Enterprise Linux 9.4"},
		{"centos", "9", "CentOS 9"},
		{"rocky", "9.4", "Rocky Linux 9.4"},
		{"oracle", "9.4", "Oracle Linux 9.4"},
		{"ol", "9.4", "Oracle Linux 9.4"},
		{"amazon", "2023", "Amazon Linux 2023"},
		{"amzn", "2023", "Amazon Linux 2023"},
		{"sles", "15.6", "SUSE Linux Enterprise Server 15.6"},
		{"sled", "15.6", "SUSE Linux Enterprise Desktop 15.6"},
		{"opensuse-leap", "15.6", "openSUSE Leap 15.6"},
		{"opensuse-tumbleweed", "20260802", "openSUSE Tumbleweed 20260802"},
		{"opensuse", "13.2", "openSUSE 13.2"},
		{"suse", "11.4", "SUSE 11.4"},
		{"linuxmint", "22", "Linux Mint 22"},
		{"nixos", "24.11", "NixOS 24.11"},
		{"cloudlinux", "9.4", "CloudLinux 9.4"},
		{"scientific", "7.9", "Scientific Linux 7.9"},
	}

	for _, tc := range tests {
		t.Run(tc.platform, func(t *testing.T) {
			info := &host.InfoStat{OS: "linux", Platform: tc.platform, PlatformVersion: tc.version}
			require.Equal(t, tc.want, getOSDescription(info))
		})
	}
}

func TestGetOSDescription_UnknownOS(t *testing.T) {
	info := &host.InfoStat{
		OS:              "freebsd",
		Platform:        "freebsd",
		PlatformVersion: "14.1",
	}

	require.Equal(t, "Unknown freebsd", getOSDescription(info))
}
