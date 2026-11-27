//go:build !unix

package main

import "os"

// linkCount reports that the link count is unknown: os.FileInfo carries it
// only on Unix. The identity checks of refuseDesktopDB still apply.
func linkCount(os.FileInfo) (count uint64, ok bool) { return 0, false }
