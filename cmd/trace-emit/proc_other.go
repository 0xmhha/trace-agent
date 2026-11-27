//go:build !unix

package main

import "os"

// forwardedSignals lists the signals handed on to the wrapped command.
func forwardedSignals() []os.Signal { return []os.Signal{os.Interrupt} }

// exitStatus converts the state of a finished command into an exit status.
func exitStatus(state *os.ProcessState) int {
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	return exitFailure
}
