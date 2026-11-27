package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func TestRunPrintsSectionsInOrder(t *testing.T) {
	status := statusJSON(false, 2, 2)
	svc := newFakeService(status)
	h := newHarness(t, svc)
	cfg := testConfig()
	cfg.project = "synthetic-project"
	cfg.sessions = 7

	if code := run(t.Context(), cfg, h.deps()); code != exitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, h.stderr.String())
	}

	want := `{"status":` + status + `,"overview":{"sessions":3},"tools":{"tools":[]},"insights":{"items":[]},"sessions":{"items":[],"total":0}}` + "\n"
	if got := h.stdout.String(); got != want {
		t.Errorf("stdout =\n%s\nwant\n%s", got, want)
	}

	wantCalls := []string{
		callConsent, callScan, usage.MethodStatus,
		usage.MethodStatus, usage.MethodOverview, usage.MethodTools, usage.MethodInsights, usage.MethodSessions,
	}
	if got := svc.recorded(); !slices.Equal(got, wantCalls) {
		t.Errorf("calls = %v, want %v", got, wantCalls)
	}
	if !slices.Equal(svc.consent, []bool{true}) {
		t.Errorf("consent = %v, want one grant", svc.consent)
	}
	for _, params := range svc.params[usage.MethodStatus] {
		if params != emptyParams {
			t.Errorf("status params = %s, want %s", params, emptyParams)
		}
	}

	wantRange := reportRange(h.now, h.location, cfg.days)
	for _, method := range []string{usage.MethodOverview, usage.MethodTools, usage.MethodInsights} {
		var got usage.Query
		decodeOnly(t, svc.params[method], &got)
		if !got.Range.From.Equal(wantRange.From) || !got.Range.Until.Equal(wantRange.Until) || got.Project != cfg.project || got.BinHours != 0 {
			t.Errorf("%s query = %+v, want range %+v and project %q", method, got, wantRange, cfg.project)
		}
	}
	var sessions usage.SessionQuery
	decodeOnly(t, svc.params[usage.MethodSessions], &sessions)
	if !sessions.Range.From.Equal(wantRange.From) || !sessions.Range.Until.Equal(wantRange.Until) ||
		sessions.Project != cfg.project || sessions.Limit != cfg.sessions || sessions.Sort != usage.SortLast || sessions.Cursor != "" {
		t.Errorf("sessions query = %+v", sessions)
	}

	if len(h.opened) != 1 {
		t.Fatalf("open calls = %d, want 1", len(h.opened))
	}
	opened := h.opened[0]
	if opened.root != cfg.root || opened.rootLabel != cfg.rootLabel || opened.days != cfg.days || opened.location != h.location {
		t.Errorf("open params = %+v", opened)
	}
	if got := opened.now(); !got.Equal(h.now) {
		t.Errorf("open clock = %v, want the injected clock %v", got, h.now)
	}
	if h.closed != 1 {
		t.Errorf("store closed %d times, want 1", h.closed)
	}
}

// decodeOnly decodes the single recorded request body of a method.
func decodeOnly(t *testing.T, recorded []string, into any) {
	t.Helper()
	if len(recorded) != 1 {
		t.Fatalf("recorded %d requests, want 1: %v", len(recorded), recorded)
	}
	if err := json.Unmarshal([]byte(recorded[0]), into); err != nil {
		t.Fatalf("decode request %s: %v", recorded[0], err)
	}
}

func TestRunPretty(t *testing.T) {
	status := statusJSON(false, 1, 1)
	h := newHarness(t, newFakeService(status))
	cfg := testConfig()
	cfg.pretty = true

	if code := run(t.Context(), cfg, h.deps()); code != exitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, h.stderr.String())
	}
	got := h.stdout.String()
	if !strings.HasPrefix(got, "{\n"+prettyIndent+`"status": {`) || !strings.HasSuffix(got, "\n}\n") {
		t.Errorf("stdout is not indented:\n%s", got)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(got)); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	want := `{"status":` + status + `,"overview":{"sessions":3},"tools":{"tools":[]},"insights":{"items":[]},"sessions":{"items":[],"total":0}}`
	if compact.String() != want {
		t.Errorf("compacted stdout =\n%s\nwant\n%s", compact.String(), want)
	}
}

func TestRunScansUntilStatusReportsDone(t *testing.T) {
	scanning := statusJSON(true, 1, 4)
	done := statusJSON(false, 4, 4)
	tests := []struct {
		name      string
		statuses  []string
		wantScans int
	}{
		{name: "done after the first pass", statuses: []string{done}, wantScans: 1},
		{name: "done after the second pass", statuses: []string{scanning, done}, wantScans: 2},
		{name: "done after the fourth pass", statuses: []string{scanning, scanning, scanning, done}, wantScans: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newFakeService(tt.statuses...)
			h := newHarness(t, svc)
			if code := run(t.Context(), testConfig(), h.deps()); code != exitOK {
				t.Fatalf("exit code = %d, stderr: %s", code, h.stderr.String())
			}
			if got := svc.scanCount(); got != tt.wantScans {
				t.Errorf("scan passes = %d, want %d", got, tt.wantScans)
			}
			if !strings.HasPrefix(h.stdout.String(), `{"status":`+done) {
				t.Errorf("stdout does not carry the final status: %s", h.stdout.String())
			}
		})
	}
}

func TestRunFailsWhenScanNeverFinishes(t *testing.T) {
	svc := newFakeService(statusJSON(true, 0, 1))
	h := newHarness(t, svc)

	if code := run(t.Context(), testConfig(), h.deps()); code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if got := svc.scanCount(); got != maxScanPasses {
		t.Errorf("scan passes = %d, want the cap %d", got, maxScanPasses)
	}
	if h.stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), errScanUnfinished.Error()) {
		t.Errorf("stderr = %q, want %q", h.stderr.String(), errScanUnfinished)
	}
	if h.closed != 1 {
		t.Errorf("store closed %d times, want 1", h.closed)
	}
}

func TestRunFailures(t *testing.T) {
	tests := []struct {
		name        string
		arrange     func(h *harness)
		wantMessage string
		wantClosed  int
	}{
		{
			name:        "pipeline does not open",
			arrange:     func(h *harness) { h.openErr = errInjected },
			wantMessage: "open usage pipeline: " + errInjected.Error(),
		},
		{
			name:        "consent is not recorded",
			arrange:     func(h *harness) { h.svc.errs[callConsent] = errInjected },
			wantMessage: "record consent: " + errInjected.Error(),
			wantClosed:  1,
		},
		{
			name:        "scan pass fails",
			arrange:     func(h *harness) { h.svc.errs[callScan] = errInjected },
			wantMessage: "scan transcripts: " + errInjected.Error(),
			wantClosed:  1,
		},
		{
			name:        "status request fails",
			arrange:     func(h *harness) { h.svc.errs[usage.MethodStatus] = errInjected },
			wantMessage: "request status: " + errInjected.Error(),
			wantClosed:  1,
		},
		{
			name:        "status is not JSON",
			arrange:     func(h *harness) { h.svc.statuses = []string{"not json"} },
			wantMessage: "decode status",
			wantClosed:  1,
		},
		{
			name:        "overview request fails",
			arrange:     func(h *harness) { h.svc.errs[usage.MethodOverview] = usage.ErrBadRequest },
			wantMessage: "request overview: " + usage.ErrBadRequest.Error(),
			wantClosed:  1,
		},
		{
			name:        "tools request fails",
			arrange:     func(h *harness) { h.svc.errs[usage.MethodTools] = errInjected },
			wantMessage: "request tools: " + errInjected.Error(),
			wantClosed:  1,
		},
		{
			name:        "insights request fails",
			arrange:     func(h *harness) { h.svc.errs[usage.MethodInsights] = errInjected },
			wantMessage: "request insights: " + errInjected.Error(),
			wantClosed:  1,
		},
		{
			name:        "sessions request fails",
			arrange:     func(h *harness) { h.svc.errs[usage.MethodSessions] = errInjected },
			wantMessage: "request sessions: " + errInjected.Error(),
			wantClosed:  1,
		},
		{
			name:        "response is empty",
			arrange:     func(h *harness) { h.svc.responses[usage.MethodTools] = "" },
			wantMessage: "request tools: " + errInvalidResponse.Error(),
			wantClosed:  1,
		},
		{
			name:        "response would add keys",
			arrange:     func(h *harness) { h.svc.responses[usage.MethodOverview] = `1,"extra":2` },
			wantMessage: "request overview: " + errInvalidResponse.Error(),
			wantClosed:  1,
		},
		{
			name:        "store does not close",
			arrange:     func(h *harness) { h.closeErr = errInjected },
			wantMessage: "close usage database: " + errInjected.Error(),
			wantClosed:  1,
		},
		{
			name:        "temporary directory cannot be created",
			arrange:     func(h *harness) { h.tempBase = filepath.Join(h.tempBase, "missing") },
			wantMessage: "create temporary database directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, newFakeService())
			tt.arrange(h)

			if code := run(t.Context(), testConfig(), h.deps()); code != exitFailure {
				t.Fatalf("exit code = %d, want %d", code, exitFailure)
			}
			if h.stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", h.stdout.String())
			}
			stderr := h.stderr.String()
			if !strings.Contains(stderr, program+": ") || !strings.Contains(stderr, tt.wantMessage) {
				t.Errorf("stderr = %q, want a %q line with %q", stderr, program, tt.wantMessage)
			}
			if h.closed != tt.wantClosed {
				t.Errorf("store closed %d times, want %d", h.closed, tt.wantClosed)
			}
		})
	}
}

func TestRunReportsStdoutWriteFailure(t *testing.T) {
	h := newHarness(t, newFakeService())
	d := h.deps()
	d.stdout = failingWriter{}

	if code := run(t.Context(), testConfig(), d); code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(h.stderr.String(), "write report: "+errInjected.Error()) {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}

func TestRunTimesOut(t *testing.T) {
	svc := newFakeService()
	svc.scan = func(ctx context.Context, _ int) error {
		<-ctx.Done()
		return ctx.Err()
	}
	h := newHarness(t, svc)
	cfg := testConfig()
	cfg.timeout = time.Millisecond

	if code := run(t.Context(), cfg, h.deps()); code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if h.stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", h.stdout.String())
	}
	if want := "timed out after " + cfg.timeout.String(); !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr = %q, want %q", h.stderr.String(), want)
	}
	if h.closed != 1 {
		t.Errorf("store closed %d times, want 1", h.closed)
	}
	if left := entries(t, h.tempBase); len(left) != 0 {
		t.Errorf("temporary database left behind: %v", left)
	}
}

func TestRunStopsWhenInterrupted(t *testing.T) {
	tests := []struct {
		name      string
		cancelIn  bool
		wantScans int
	}{
		{name: "cancelled during a pass", cancelIn: true, wantScans: 1},
		{name: "cancelled before the first pass", cancelIn: false, wantScans: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			svc := newFakeService()
			svc.scan = func(ctx context.Context, _ int) error {
				cancel()
				<-ctx.Done()
				return ctx.Err()
			}
			if !tt.cancelIn {
				cancel()
			}
			h := newHarness(t, svc)

			if code := run(ctx, testConfig(), h.deps()); code != exitFailure {
				t.Fatalf("exit code = %d, want %d", code, exitFailure)
			}
			if h.stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", h.stdout.String())
			}
			if !strings.Contains(h.stderr.String(), program+": interrupted: ") {
				t.Errorf("stderr = %q, want an interrupted line", h.stderr.String())
			}
			if got := svc.scanCount(); got != tt.wantScans {
				t.Errorf("scan passes = %d, want %d", got, tt.wantScans)
			}
			if h.closed != 1 {
				t.Errorf("store closed %d times, want 1", h.closed)
			}
			if left := entries(t, h.tempBase); len(left) != 0 {
				t.Errorf("temporary database left behind: %v", left)
			}
		})
	}
}

func TestRunRemovesTemporaryDatabase(t *testing.T) {
	const privateDir = 0o700
	tests := []struct {
		name     string
		arrange  func(h *harness)
		wantCode int
	}{
		{name: "after success", arrange: func(*harness) {}, wantCode: exitOK},
		{name: "after a failed scan", arrange: func(h *harness) { h.svc.errs[callScan] = errInjected }, wantCode: exitFailure},
		{name: "after a failed open", arrange: func(h *harness) { h.openErr = errInjected }, wantCode: exitFailure},
		{name: "after a failed close", arrange: func(h *harness) { h.closeErr = errInjected }, wantCode: exitFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, newFakeService())
			tt.arrange(h)
			var dbPath string
			h.onOpen = func(p openParams) {
				dbPath = p.dbPath
				dir := filepath.Dir(p.dbPath)
				if filepath.Dir(dir) != h.tempBase || filepath.Base(p.dbPath) != tempDBName {
					t.Errorf("database path %s is not a fresh directory under %s", p.dbPath, h.tempBase)
				}
				info, err := os.Stat(dir)
				if err != nil {
					t.Fatalf("stat temporary directory: %v", err)
				}
				if info.Mode().Perm() != privateDir {
					t.Errorf("temporary directory mode = %v, want %v", info.Mode().Perm(), os.FileMode(privateDir))
				}
				// The database and a side file, as SQLite would leave them.
				for _, name := range []string{p.dbPath, p.dbPath + "-wal"} {
					if err = os.WriteFile(name, []byte("synthetic"), 0o600); err != nil {
						t.Fatalf("write %s: %v", name, err)
					}
				}
			}
			h.onClose = func() {
				if _, err := os.Stat(dbPath); err != nil {
					t.Errorf("database removed before the store was closed: %v", err)
				}
			}

			if code := run(t.Context(), testConfig(), h.deps()); code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d, stderr: %s", code, tt.wantCode, h.stderr.String())
			}
			if dbPath == "" {
				t.Fatal("pipeline was not opened")
			}
			if left := entries(t, h.tempBase); len(left) != 0 {
				t.Errorf("temporary database left behind: %v", left)
			}
		})
	}
}

func TestRunKeepsNamedDatabase(t *testing.T) {
	h := newHarness(t, newFakeService())
	cfg := testConfig()
	cfg.dbPath = filepath.Join(t.TempDir(), "kept.sqlite")
	h.onOpen = func(p openParams) {
		if p.dbPath != cfg.dbPath {
			t.Errorf("database path = %s, want %s", p.dbPath, cfg.dbPath)
		}
		if err := os.WriteFile(p.dbPath, []byte("synthetic"), 0o600); err != nil {
			t.Fatalf("write database: %v", err)
		}
	}

	if code := run(t.Context(), cfg, h.deps()); code != exitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, h.stderr.String())
	}
	if _, err := os.Stat(cfg.dbPath); err != nil {
		t.Errorf("named database was not kept: %v", err)
	}
	if left := entries(t, h.tempBase); len(left) != 0 {
		t.Errorf("temporary directory created for a named database: %v", left)
	}
}

func TestRunReportsProgress(t *testing.T) {
	ticks := make(chan time.Time)
	// The reporter reads one status per tick; the scan loop reads the last.
	svc := newFakeService(
		statusJSON(true, 1, 3),
		statusJSON(true, 1, 3),
		statusJSON(true, 2, 3),
		statusJSON(false, 3, 3),
	)
	svc.scan = func(context.Context, int) error {
		// An unbuffered send returns only when the reporter has taken the
		// tick, and it takes the next one only after handling the previous.
		for range 3 {
			ticks <- time.Time{}
		}
		return nil
	}
	h := newHarness(t, svc)
	h.ticks = ticks
	cfg := testConfig()

	if code := run(t.Context(), cfg, h.deps()); code != exitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, h.stderr.String())
	}
	want := strings.Join([]string{
		program + ": scanning " + cfg.rootLabel + ", last 30 days",
		program + ": 1 of 3 files read",
		program + ": 2 of 3 files read",
		program + ": pass 1: 3 of 3 files read",
		"",
	}, "\n")
	if got := h.stderr.String(); got != want {
		t.Errorf("stderr =\n%s\nwant\n%s", got, want)
	}
	if !slices.Equal(h.intervals, []time.Duration{progressInterval}) {
		t.Errorf("ticker intervals = %v, want one of %v", h.intervals, progressInterval)
	}
	if h.stops != 1 {
		t.Errorf("ticker stopped %d times, want 1", h.stops)
	}
}

func TestReportRange(t *testing.T) {
	now, loc := testClock()
	day := func(month time.Month, d int) time.Time { return time.Date(2026, month, d, 0, 0, 0, 0, loc) }
	tests := []struct {
		name      string
		now       time.Time
		days      int
		wantFrom  time.Time
		wantUntil time.Time
	}{
		{name: "one day is today", now: now, days: 1, wantFrom: day(time.March, 10), wantUntil: day(time.March, 11)},
		{name: "thirty days cross a month", now: now, days: 30, wantFrom: day(time.February, 9), wantUntil: day(time.March, 11)},
		{name: "exactly midnight", now: day(time.March, 10), days: 2, wantFrom: day(time.March, 9), wantUntil: day(time.March, 11)},
		{name: "clock in another zone", now: now.UTC(), days: 1, wantFrom: day(time.March, 10), wantUntil: day(time.March, 11)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reportRange(tt.now, loc, tt.days)
			if !got.From.Equal(tt.wantFrom) || !got.Until.Equal(tt.wantUntil) {
				t.Errorf("range = [%v, %v), want [%v, %v)", got.From, got.Until, tt.wantFrom, tt.wantUntil)
			}
			// The store measures its window back from the current instant;
			// the range must not start before it.
			if windowFrom := tt.now.In(loc).AddDate(0, 0, -tt.days); got.From.Before(windowFrom) {
				t.Errorf("range starts at %v, before the window start %v", got.From, windowFrom)
			}
		})
	}
}

func TestReportKeysFollowSections(t *testing.T) {
	want := []string{keyStatus, keyOverview, keyTools, keyInsights, keySessions}
	if got := reportKeys(); !slices.Equal(got, want) {
		t.Errorf("report keys = %v, want %v", got, want)
	}
}
