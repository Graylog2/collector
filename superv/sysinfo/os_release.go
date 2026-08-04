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
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// OSRelease holds a subset of the fields defined in the os-release.
// specification: https://www.freedesktop.org/software/systemd/man/latest/os-release.html
type OSRelease struct {
	Name       string // NAME
	Version    string // VERSION
	ID         string // ID
	IDLike     string // ID_LIKE (space-separated list)
	VersionID  string // VERSION_ID
	PrettyName string // PRETTY_NAME
}

// GetOSRelease reads the os-release information of the current host,
// preferring /etc/os-release over the /usr/lib/os-release fallback.
func GetOSRelease() (OSRelease, error) {
	release, err := ReadOSRelease("/etc/os-release")
	if os.IsNotExist(err) {
		return ReadOSRelease("/usr/lib/os-release")
	}
	return release, err
}

// ReadOSRelease reads and parses the os-release file at the given path.
func ReadOSRelease(path string) (OSRelease, error) {
	f, err := os.Open(path)
	if err != nil {
		return OSRelease{}, fmt.Errorf("opening os-release file: %w", err)
	}
	defer f.Close()

	return ParseOSRelease(f)
}

// ParseOSRelease parses os-release formatted content. Comments, blank
// lines, malformed lines, and unknown fields are ignored.
func ParseOSRelease(r io.Reader) (OSRelease, error) {
	var release OSRelease

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		value = trimQuotes(value)

		switch key {
		case "NAME":
			release.Name = value
		case "VERSION":
			release.Version = value
		case "ID":
			release.ID = value
		case "ID_LIKE":
			release.IDLike = value
		case "VERSION_ID":
			release.VersionID = value
		case "PRETTY_NAME":
			release.PrettyName = value
		}
	}
	if err := scanner.Err(); err != nil {
		return OSRelease{}, fmt.Errorf("reading os-release content: %w", err)
	}

	return release, nil
}

// trimQuotes removes matching surrounding double or single quotes.
func trimQuotes(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
