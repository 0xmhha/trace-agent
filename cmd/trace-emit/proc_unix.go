//go:build unix

package main

import (
	"os"
	"syscall"
)

// signalExitBase is added to a signal number to form the exit status of a
// command that a signal ended, as shells do.
const signalExitBase = 128

// forwardedSignals lists the signals handed on to the wrapped command: the
// ones that ask a process to stop or that tools use for their own purposes.
// Listing them, rather than subscribing to everything, keeps the signals the
// Go runtime raises for itself away from the command.
func forwardedSignals() []os.Signal {
	return []os.Signal{
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGHUP,
		syscall.SIGQUIT,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
	}
}

// sameEnvName reports whether two environment variable names are the same
// variable. Names are case-sensitive on Unix.
func sameEnvName(a, b string) bool { return a == b }

// exitStatus converts the state of a finished command into an exit status.
func exitStatus(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return signalExitBase + int(status.Signal())
	}
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	return exitFailure
}
