//go:build unix

package main

import (
	"os"
	"syscall"
	"testing"
)

func TestRunForwardsSignals(t *testing.T) {
	h, self := newRunHarness(t)
	// The signal is already waiting when the command starts, so the test
	// needs no timing: the forwarder hands it on as soon as the command runs.
	signals := make(chan os.Signal, 1)
	signals <- syscall.SIGTERM
	stopped := false
	h.env.signals = func() (<-chan os.Signal, func()) { return signals, func() { stopped = true } }

	want := signalExitBase + int(syscall.SIGTERM)
	if code := h.dispatch(runWord, "--", self, childSleep); code != want {
		t.Fatalf("exit %d, want %d (stderr %q)", code, want, h.stderr)
	}
	if !stopped {
		t.Fatal("signal subscription was not ended")
	}
	ev := h.sent()
	if ev.Status != string(statusError) || ev.Attributes[attrExitCode] != float64(want) {
		t.Fatalf("event %+v", ev)
	}
}

func TestForwardedSignalsAreTheStopRequests(t *testing.T) {
	forwarded := forwardedSignals()
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT} {
		found := false
		for _, candidate := range forwarded {
			found = found || candidate == sig
		}
		if !found {
			t.Errorf("%v is not forwarded", sig)
		}
	}
}
