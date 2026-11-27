package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/extension"
)

// commandElapsed is how long the fake clock says the wrapped command ran.
const commandElapsed = 1500 * time.Millisecond

// newRunHarness prepares run mode: the wrapped command is this test binary
// and the clock advances by commandElapsed between start and end.
func newRunHarness(t *testing.T) (*harness, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The wrapped command inherits the test's real environment.
	t.Setenv(childEnv, "1")
	h := newHarness(t)
	h.env.now = fakeClock(testStart, testStart.Add(commandElapsed))
	return h, self
}

func TestRunReportsTheCommandAsASpan(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-installed")
	for _, tc := range []struct {
		name       string
		command    func(self string) []string
		wantCode   int
		wantStatus eventStatus
		wantStderr string
	}{
		{"success", func(self string) []string { return []string{self, childExit, "0"} }, 0, statusOK, ""},
		{"failure", func(self string) []string { return []string{self, childExit, "3"} }, 3, statusError, ""},
		{"cannot be started", func(string) []string { return []string{missing} }, exitNotStarted, statusError, "cannot run command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, self := newRunHarness(t)
			command := tc.command(self)
			args := append([]string{runWord, "--attr", "step=3", "--message", "nightly", "--"}, command...)
			if code := h.dispatch(args...); code != tc.wantCode {
				t.Fatalf("exit %d, want %d (stderr %q)", code, tc.wantCode, h.stderr)
			}
			if !strings.Contains(h.stderr.String(), tc.wantStderr) || (tc.wantStderr == "" && h.stderr.Len() != 0) {
				t.Fatalf("stderr %q, want it to mention %q", h.stderr, tc.wantStderr)
			}
			ev := h.sent()
			// Numbers decode as float64.
			wantAttributes := map[string]any{"step": "3", attrExitCode: float64(tc.wantCode)}
			switch {
			case ev.Kind != string(kindSpan):
				t.Fatalf("kind %q", ev.Kind)
			case ev.Name != filepath.Base(command[0]):
				t.Fatalf("name %q, want the command's base name", ev.Name)
			case ev.Status != string(tc.wantStatus):
				t.Fatalf("status %q, want %q", ev.Status, tc.wantStatus)
			case ev.Timestamp != formatTimestamp(testStart):
				t.Fatalf("timestamp %q, want the start time", ev.Timestamp)
			case ev.DurationMs == nil || *ev.DurationMs != milliseconds(commandElapsed):
				t.Fatalf("duration %v, want %v ms", ev.DurationMs, milliseconds(commandElapsed))
			case len(ev.Attributes) != len(wantAttributes) || ev.Attributes["step"] != "3" || ev.Attributes[attrExitCode] != wantAttributes[attrExitCode]:
				t.Fatalf("attributes %v, want %v", ev.Attributes, wantAttributes)
			case ev.Message != "nightly" || ev.ID != testID || ev.Source != defaultSource:
				t.Fatalf("event %+v", ev)
			}
		})
	}
}

func TestRunHonoursNameAndKind(t *testing.T) {
	h, self := newRunHarness(t)
	if code := h.dispatch(runWord, "--name", "build", "--kind", "log", "--level", "warn", "--", self, childExit, "0"); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if ev := h.sent(); ev.Name != "build" || ev.Kind != string(kindLog) || ev.Level != string(levelWarn) {
		t.Fatalf("event %+v", ev)
	}
}

// Whatever happens to the event, the caller gets the command's exit status.
func TestRunExitStatusDoesNotDependOnTracing(t *testing.T) {
	// A status no part of trace-emit produces by itself.
	const commandStatus = 3
	for _, tc := range []struct {
		name    string
		prepare func(*harness)
		sends   int
	}{
		{"tracing is off", func(h *harness) { h.vars = map[string]string{} }, 0},
		{"address refused", func(h *harness) { h.vars[envURL] = "https://127.0.0.1:8123" + extension.EventsPath }, 0},
		{"send fails", func(h *harness) {
			h.transport.respond = func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") }
		}, 1},
		{"endpoint answers with an error", func(h *harness) {
			h.transport.respond = func(req *http.Request) (*http.Response, error) {
				return answer(req, http.StatusInternalServerError, ""), nil
			}
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, self := newRunHarness(t)
			tc.prepare(h)
			if got := h.dispatch(runWord, "--", self, childExit, strconv.Itoa(commandStatus)); got != commandStatus {
				t.Fatalf("exit %d, want %d", got, commandStatus)
			}
			if n := len(h.transport.captured()); n != tc.sends {
				t.Fatalf("requests = %d, want %d", n, tc.sends)
			}
			if h.stderr.Len() != 0 {
				t.Fatalf("stderr without --verbose: %q", h.stderr)
			}
		})
	}
}

func TestRunPassesStdioThrough(t *testing.T) {
	h, self := newRunHarness(t)
	h.env.stdin = strings.NewReader("line one\nline two\n")
	if code := h.dispatch(runWord, "--", self, childEcho); code != exitOK {
		t.Fatalf("exit %d (stderr %q)", code, h.stderr)
	}
	if got := h.stdout.String(); got != "line one\nline two\n" {
		t.Fatalf("stdout %q", got)
	}
	if ev := h.sent(); ev.SessionID != "" {
		t.Fatalf("stdin was parsed without --from-hook: %+v", ev)
	}
}

// With --from-hook the command still receives the stdin that trace-emit read.
func TestRunFromHookReplaysStdin(t *testing.T) {
	oversized := `{"session_id":"session-1","padding":"` + strings.Repeat("p", hookInputLimit) + `"}`
	for _, tc := range []struct {
		name        string
		stdin       string
		wantSession string
		wantToolUse string
	}{
		{"hook payload", `{"session_id":"session-1","tool_use_id":"toolu_1","tool_input":{"file_path":"a.txt"}}`, "session-1", "toolu_1"},
		{"not a hook payload", "plain text\n", "", ""},
		{"larger than the read limit", oversized, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, self := newRunHarness(t)
			h.env.stdin = strings.NewReader(tc.stdin)
			if code := h.dispatch(runWord, "--from-hook", "--name", "lint", "--", self, childEcho); code != exitOK {
				t.Fatalf("exit %d (stderr %q)", code, h.stderr)
			}
			if got := h.stdout.String(); got != tc.stdin {
				t.Fatalf("command received %d bytes of stdin, want %d", len(got), len(tc.stdin))
			}
			if ev := h.sent(); ev.SessionID != tc.wantSession || ev.ToolUseID != tc.wantToolUse {
				t.Fatalf("linked to session %q, tool use %q; want %q, %q", ev.SessionID, ev.ToolUseID, tc.wantSession, tc.wantToolUse)
			}
		})
	}
}

func TestMillisecondsNeverNegative(t *testing.T) {
	for _, tc := range []struct {
		elapsed time.Duration
		want    float64
	}{
		{0, 0},
		{1500 * time.Microsecond, 1.5},
		{2 * time.Second, 2000},
		{-time.Second, 0}, // a clock that stepped backwards
	} {
		if got := milliseconds(tc.elapsed); got != tc.want {
			t.Errorf("milliseconds(%v) = %v, want %v", tc.elapsed, got, tc.want)
		}
	}
}
