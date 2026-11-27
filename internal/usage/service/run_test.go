package service

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

const (
	// fastInterval lets the ticker drive many passes during a test.
	fastInterval = time.Millisecond
	// idleInterval keeps the ticker out of a test that waits for a wake-up.
	idleInterval = time.Hour
	// watchdog fails a test that waits for something that never comes. No
	// test depends on it elapsing.
	watchdog = 30 * time.Second
	// settledCycles is the number of Settings calls to wait for before a full
	// cycle is known to have run since the wait began: the first may be left
	// over from before, the second starts a cycle, and the third shows that
	// cycle ended.
	settledCycles = 3
)

func await[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(watchdog):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// running starts Run and returns a function that stops it and reports what
// it returned.
func (h *harness) running() (stop func() error) {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.svc.Run(ctx) }()
	stopped := false
	stop = func() error {
		h.t.Helper()
		if stopped {
			return nil
		}
		stopped = true
		cancel()
		return await(h.t, done, "Run to return")
	}
	h.t.Cleanup(func() { _ = stop() })
	return stop
}

// cycles waits until a full cycle of Run has certainly completed.
func (h *harness) cycles(seen <-chan struct{}) {
	h.t.Helper()
	for range settledCycles {
		await(h.t, seen, "a scan pass")
	}
}

func (h *harness) watchSettings() <-chan struct{} {
	seen := make(chan struct{}, 1)
	h.store.with(func(f *fakeStore) { f.settingsSeen = seen })
	return seen
}

func TestRunScansUntilCancelled(t *testing.T) {
	h := newHarness(t, func(cfg *Config) { cfg.Interval = fastInterval })
	h.fixtures()
	seen := h.watchSettings()
	stop := h.running()
	h.cycles(seen)
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if got, want := h.scanned(), []string{testSession + "/@0", testSession + "/" + testAgent + "@0"}; !slices.Equal(got, want) {
		t.Fatalf("scans = %v; want each file read once across all passes: %v", got, want)
	}
	if h.status().Scanning {
		t.Fatal("Run must complete the pending full pass")
	}
}

func TestRunReturnsAtOnceWhenAlreadyCancelled(t *testing.T) {
	h := newHarness(t)
	h.fixtures()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.svc.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	h.store.with(func(f *fakeStore) {
		if f.settingsCalls != 0 {
			t.Fatal("a cancelled Run touched the store")
		}
	})
}

// Without consent no transcript is read, but retention still applies: the
// extension endpoint accepts events whether or not transcripts are collected.
func TestRunReadsNothingWithoutConsent(t *testing.T) {
	h := newHarness(t, func(cfg *Config) { cfg.Interval = fastInterval })
	h.fixtures()
	h.store.with(func(f *fakeStore) { f.settings.Consent = false })
	seen := h.watchSettings()
	stop := h.running()
	h.cycles(seen)
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
	if calls := h.scans.calls(); len(calls) != 0 {
		t.Fatalf("scanned without consent: %+v", calls)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.cursorLookups)+len(f.applied) != 0 {
			t.Fatalf("transcripts were read without consent: cursors %d, applied %d", len(f.cursorLookups), len(f.applied))
		}
		if len(f.pruned) != 1 {
			t.Fatalf("pruned %d times without consent; want once", len(f.pruned))
		}
	})
}

func TestRunPrunesAtMostOncePerHour(t *testing.T) {
	h := newHarness(t, func(cfg *Config) { cfg.Interval = fastInterval })
	seen := h.watchSettings()
	stop := h.running()

	pruned := func() []pruneCall {
		var calls []pruneCall
		h.store.with(func(f *fakeStore) { calls = append(calls, f.pruned...) })
		return calls
	}
	h.cycles(seen)
	h.cycles(seen)
	first := pruned()
	if len(first) != 1 {
		t.Fatalf("%d prunes after several passes; want 1", len(first))
	}
	if want := (pruneCall{testNow.AddDate(0, 0, -usage.MaxWindowDays), testNow.AddDate(0, 0, -usage.ExtRetentionDays)}); first[0] != want {
		t.Fatalf("prune = %+v\nwant %+v", first[0], want)
	}

	h.clock.Advance(pruneInterval - time.Second)
	h.cycles(seen)
	if got := len(pruned()); got != 1 {
		t.Fatalf("%d prunes before the hour was over; want 1", got)
	}

	h.clock.Advance(time.Second)
	h.cycles(seen)
	second := pruned()
	if len(second) != 2 {
		t.Fatalf("%d prunes after an hour; want 2", len(second))
	}
	later := testNow.Add(pruneInterval)
	if want := (pruneCall{later.AddDate(0, 0, -usage.MaxWindowDays), later.AddDate(0, 0, -usage.ExtRetentionDays)}); second[1] != want {
		t.Fatalf("second prune = %+v\nwant %+v", second[1], want)
	}
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
}

func TestRunReportsFailuresAndKeepsGoing(t *testing.T) {
	for _, failing := range []string{"Settings", "Prune"} {
		t.Run(failing, func(t *testing.T) {
			reported := make(chan error, 1)
			h := newHarness(t, func(cfg *Config) {
				cfg.Interval = fastInterval
				cfg.OnError = func(err error) {
					select {
					case reported <- err:
					default:
					}
				}
			})
			h.store.with(func(f *fakeStore) { f.fail[failing] = errInjected })
			stop := h.running()
			// More than one report shows that the loop survived the failure
			// and tried again.
			for range settledCycles {
				if err := await(t, reported, "a reported failure"); !errors.Is(err, errInjected) {
					t.Fatalf("reported %v; want the store failure", err)
				}
			}
			if err := stop(); !errors.Is(err, context.Canceled) {
				t.Fatalf("Run = %v; want context.Canceled", err)
			}
		})
	}
}

func TestRescanWakesRun(t *testing.T) {
	h := newHarness(t, func(cfg *Config) { cfg.Interval = idleInterval })
	h.fixtures()
	scanned := make(chan usage.ScanInput, 16)
	h.scans.setHook(func(_ int, in usage.ScanInput) error {
		scanned <- in
		return nil
	})
	h.running()
	for range 2 {
		await(t, scanned, "the first pass")
	}
	// Passes are serialized, so this returns only after the first one ended.
	h.scanOnce()

	// The ticker will not fire for an hour: only the wake-up can start this.
	if status := call[usage.Status](t, h.svc, usage.MethodRescan, ``); !status.Scanning {
		t.Fatal("rescan must make a full pass pending")
	}
	for range 2 {
		if in := await(t, scanned, "the pass after rescan"); in.Base != 0 {
			t.Fatalf("rescan read from %d; want the start of the file", in.Base)
		}
	}
}

func TestGrantingConsentWakesRun(t *testing.T) {
	h := newHarness(t, func(cfg *Config) { cfg.Interval = idleInterval })
	h.fixtures()
	h.store.with(func(f *fakeStore) { f.settings.Consent = false })
	scanned := make(chan usage.ScanInput, 16)
	h.scans.setHook(func(_ int, in usage.ScanInput) error {
		scanned <- in
		return nil
	})
	seen := h.watchSettings()
	h.running()
	// The first pass has read the settings and found collection off.
	await(t, seen, "the first pass")

	if err := h.svc.SetConsent(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		await(t, scanned, "the pass after consent")
	}
}
