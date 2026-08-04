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
	"path/filepath"
	"strings"
	"testing"

	"github.com/Graylog2/collector/superv/internal/testfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOSRelease(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  OSRelease
	}{
		{
			name: "unquoted values",
			input: strings.Join([]string{
				`ID=ubuntu`,
				`ID_LIKE=debian`,
				`VERSION_ID=24.04`,
			}, "\n"),
			want: OSRelease{ID: "ubuntu", IDLike: "debian", VersionID: "24.04"},
		},
		{
			name: "quoted values",
			input: strings.Join([]string{
				`NAME="Red Hat Enterprise Linux"`,
				`VERSION="9.4 (Plow)"`,
				`ID="rhel"`,
			}, "\n"),
			want: OSRelease{Name: "Red Hat Enterprise Linux", Version: "9.4 (Plow)", ID: "rhel"},
		},
		{
			name:  "single-quoted values",
			input: `NAME='Test Linux'`,
			want:  OSRelease{Name: "Test Linux"},
		},
		{
			name: "comments and blank lines are skipped",
			input: strings.Join([]string{
				`# VERSION="20260802"`,
				``,
				`ID="opensuse-tumbleweed"`,
			}, "\n"),
			want: OSRelease{ID: "opensuse-tumbleweed"},
		},
		{
			name:  "value containing equals sign",
			input: `PRETTY_NAME="Test a=b"`,
			want:  OSRelease{PrettyName: "Test a=b"},
		},
		{
			name:  "unknown fields are ignored",
			input: "HOME_URL=\"https://example.com/\"\nID=test",
			want:  OSRelease{ID: "test"},
		},
		{
			name:  "malformed lines are skipped",
			input: "GARBAGE\nID=test",
			want:  OSRelease{ID: "test"},
		},
		{
			name:  "surrounding whitespace is trimmed",
			input: "  ID=test  \n",
			want:  OSRelease{ID: "test"},
		},
		{
			name:  "empty input",
			input: "",
			want:  OSRelease{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseOSRelease(strings.NewReader(tt.input))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseOSReleaseFromFixture(t *testing.T) {
	tests := []struct {
		file string
		want OSRelease
	}{
		{
			file: "os-release-almalinux-8.txt",
			want: OSRelease{
				Name:       "AlmaLinux",
				Version:    "8.10 (Cerulean Leopard)",
				ID:         "almalinux",
				IDLike:     "rhel centos fedora",
				VersionID:  "8.10",
				PrettyName: "AlmaLinux 8.10 (Cerulean Leopard)",
			},
		},
		{
			file: "os-release-almalinux-9.txt",
			want: OSRelease{
				Name:       "AlmaLinux",
				Version:    "9.7 (Moss Jungle Cat)",
				ID:         "almalinux",
				IDLike:     "rhel centos fedora",
				VersionID:  "9.7",
				PrettyName: "AlmaLinux 9.7 (Moss Jungle Cat)",
			},
		},
		{
			file: "os-release-almalinux-10.txt",
			want: OSRelease{
				Name:       "AlmaLinux",
				Version:    "10.2 (Lavender Lion)",
				ID:         "almalinux",
				IDLike:     "rhel centos fedora",
				VersionID:  "10.2",
				PrettyName: "AlmaLinux 10.2 (Lavender Lion)",
			},
		},
		{
			file: "os-release-alpine-3.txt",
			want: OSRelease{
				Name:       "Alpine Linux",
				ID:         "alpine",
				VersionID:  "3.23.3",
				PrettyName: "Alpine Linux v3.23",
			},
		},
		{
			file: "os-release-amazonlinux-2.txt",
			want: OSRelease{
				Name:       "Amazon Linux",
				Version:    "2",
				ID:         "amzn",
				IDLike:     "centos rhel fedora",
				VersionID:  "2",
				PrettyName: "Amazon Linux 2",
			},
		},
		{
			file: "os-release-amazonlinux-2023.txt",
			want: OSRelease{
				Name:       "Amazon Linux",
				Version:    "2023",
				ID:         "amzn",
				IDLike:     "fedora",
				VersionID:  "2023",
				PrettyName: "Amazon Linux 2023.12.20260724",
			},
		},
		{
			file: "os-release-arch-2026-08.txt",
			want: OSRelease{
				Name:       "Arch Linux",
				ID:         "arch",
				VersionID:  "20260726.0.562117",
				PrettyName: "Arch Linux",
			},
		},
		{
			file: "os-release-debian-12.txt",
			want: OSRelease{
				Name:       "Debian GNU/Linux",
				Version:    "12 (bookworm)",
				ID:         "debian",
				VersionID:  "12",
				PrettyName: "Debian GNU/Linux 12 (bookworm)",
			},
		},
		{
			file: "os-release-debian-13.txt",
			want: OSRelease{
				Name:       "Debian GNU/Linux",
				Version:    "13 (trixie)",
				ID:         "debian",
				VersionID:  "13",
				PrettyName: "Debian GNU/Linux 13 (trixie)",
			},
		},
		{
			file: "os-release-fedora-44.txt",
			want: OSRelease{
				Name:       "Fedora Linux",
				Version:    "44 (Server Edition)",
				ID:         "fedora",
				VersionID:  "44",
				PrettyName: "Fedora Linux 44 (Server Edition)",
			},
		},
		{
			file: "os-release-fedora-container-44.txt",
			want: OSRelease{
				Name:       "Fedora Linux",
				Version:    "44 (Container Image)",
				ID:         "fedora",
				VersionID:  "44",
				PrettyName: "Fedora Linux 44 (Container Image)",
			},
		},
		{
			file: "os-release-opensuse-tumbleweed-2026-08.txt",
			want: OSRelease{
				Name:       "openSUSE Tumbleweed",
				ID:         "opensuse-tumbleweed",
				IDLike:     "opensuse suse",
				VersionID:  "20260802",
				PrettyName: "openSUSE Tumbleweed",
			},
		},
		{
			file: "os-release-oraclelinux-8.txt",
			want: OSRelease{
				Name:       "Oracle Linux Server",
				Version:    "8.10",
				ID:         "ol",
				IDLike:     "fedora",
				VersionID:  "8.10",
				PrettyName: "Oracle Linux Server 8.10",
			},
		},
		{
			file: "os-release-oraclelinux-9.txt",
			want: OSRelease{
				Name:       "Oracle Linux Server",
				Version:    "9.8",
				ID:         "ol",
				IDLike:     "fedora",
				VersionID:  "9.8",
				PrettyName: "Oracle Linux Server 9.8",
			},
		},
		{
			file: "os-release-oraclelinux-10.txt",
			want: OSRelease{
				Name:       "Oracle Linux Server",
				Version:    "10.2",
				ID:         "ol",
				IDLike:     "fedora",
				VersionID:  "10.2",
				PrettyName: "Oracle Linux Server 10.2",
			},
		},
		{
			file: "os-release-redhat-8.txt",
			want: OSRelease{
				Name:       "Red Hat Enterprise Linux",
				Version:    "8.5 (Ootpa)",
				ID:         "rhel",
				IDLike:     "fedora",
				VersionID:  "8.5",
				PrettyName: "Red Hat Enterprise Linux 8.5 (Ootpa)",
			},
		},
		{
			file: "os-release-redhat-9.txt",
			want: OSRelease{
				Name:       "Red Hat Enterprise Linux",
				Version:    "9.4 (Plow)",
				ID:         "rhel",
				IDLike:     "fedora",
				VersionID:  "9.4",
				PrettyName: "Red Hat Enterprise Linux 9.4 (Plow)",
			},
		},
		{
			file: "os-release-rockylinux-8.txt",
			want: OSRelease{
				Name:       "Rocky Linux",
				Version:    "8.9 (Green Obsidian)",
				ID:         "rocky",
				IDLike:     "rhel centos fedora",
				VersionID:  "8.9",
				PrettyName: "Rocky Linux 8.9 (Green Obsidian)",
			},
		},
		{
			file: "os-release-rockylinux-9.txt",
			want: OSRelease{
				Name:       "Rocky Linux",
				Version:    "9.3 (Blue Onyx)",
				ID:         "rocky",
				IDLike:     "rhel centos fedora",
				VersionID:  "9.3",
				PrettyName: "Rocky Linux 9.3 (Blue Onyx)",
			},
		},
		{
			file: "os-release-ubuntu-24.04.txt",
			want: OSRelease{
				Name:       "Ubuntu",
				Version:    "24.04.1 LTS (Noble Numbat)",
				ID:         "ubuntu",
				IDLike:     "debian",
				VersionID:  "24.04",
				PrettyName: "Ubuntu 24.04.1 LTS",
			},
		},
		{
			file: "os-release-ubuntu-26.04.txt",
			want: OSRelease{
				Name:       "Ubuntu",
				Version:    "26.04 LTS (Resolute Raccoon)",
				ID:         "ubuntu",
				IDLike:     "debian",
				VersionID:  "26.04",
				PrettyName: "Ubuntu 26.04 LTS",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			file, err := testfixtures.OsReleaseFS.Open(filepath.Join("testdata", "os-release", tt.file))
			require.NoError(t, err)
			got, err := ParseOSRelease(file)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			require.NoError(t, file.Close())
		})
	}

	t.Run("all testdata files are covered", func(t *testing.T) {
		files, err := testfixtures.OsReleaseFS.ReadDir(filepath.Join("testdata", "os-release"))
		require.NoError(t, err)
		require.NotEmpty(t, files)

		covered := make(map[string]bool, len(tests))
		for _, tt := range tests {
			covered[tt.file] = true
		}
		for _, file := range files {
			assert.True(t, covered[filepath.Base(file.Name())], "missing test case for %s", file)
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		_, err := ReadOSRelease(filepath.Join(t.TempDir(), "does-not-exist"))
		require.Error(t, err)
	})
}
