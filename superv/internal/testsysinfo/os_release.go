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
