//go:build windows

package formatsniff

import "os"

// openNonBlocking opens path read only. Windows has no FIFOs to block on.
func openNonBlocking(path string) (*os.File, error) {
	return os.Open(path) // #nosec G304 -- path comes from the importer's own scan of the download directory
}
