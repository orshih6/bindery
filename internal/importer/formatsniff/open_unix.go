//go:build !windows

package formatsniff

import (
	"os"
	"syscall"
)

// openNonBlocking opens path read only without waiting on a writer, so a FIFO
// returns at once and the caller's fstat can refuse it.
func openNonBlocking(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0) // #nosec G304 -- path comes from the importer's own scan of the download directory
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil // #nosec G115 -- fd is a non negative descriptor from a successful open
}
