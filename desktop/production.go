//go:build !nativecheck

package main

import (
	"os"
	"path/filepath"
)

func run() error {
	home, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	const id = "org.0xmhha.trace-agent"
	dir := filepath.Join(home, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return runDesktop(dir, keychain{}, id, nil)
}
