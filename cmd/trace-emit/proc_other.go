//go:build !unix

package main

import (
	"os"
	"strings"
)

// sameEnvName reports whether two environment variable names are the same
// variable. Windows compares names without regard to case.
func sameEnvName(a, b string) bool { return strings.EqualFold(a, b) }

// forwardedSignals lists the signals handed on to the wrapped command.
func forwardedSignals() []os.Signal { return []os.Signal{os.Interrupt} }

// exitStatus converts the state of a finished command into an exit status.
func exitStatus(state *os.ProcessState) int {
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	return exitFailure
}
