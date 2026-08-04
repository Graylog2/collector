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
