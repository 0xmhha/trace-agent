//go:build unix

package main

import (
	"os"
	"syscall"
)

// linkCount returns how many directory entries name the file of info. ok is
// false when the file system does not say.
func linkCount(info os.FileInfo) (count uint64, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	// The conversion is needed where the field is narrower, as on darwin.
	return uint64(st.Nlink), true //nolint:unconvert // uint64 already on linux.
}
