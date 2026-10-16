//go:build windows

package api

import "os"

// dirAccessible on Windows only checks that path is an existing directory (and
// not marked read only when write is true). There is no side effect free
// writability probe there, and a false alarm is worse than a missed one.
func dirAccessible(path string, write bool) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return false
	}
	if write && fi.Mode().Perm()&0o200 == 0 {
		return false
	}
	return true
}
