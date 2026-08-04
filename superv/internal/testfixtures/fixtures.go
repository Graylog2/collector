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

// Package testfixtures provides fixtures as embed.FS instances for reusability from different packages.
package testfixtures

import (
	"embed"
)

// OsReleaseFS provides /etc/os-release file fixtures.
//
//go:embed testdata/os-release/*.txt
var OsReleaseFS embed.FS

//go:embed testdata/hostinfo/*.json
var HostInfoFS embed.FS
