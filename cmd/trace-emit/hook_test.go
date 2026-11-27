package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// unreadable fails the test when the command reads stdin although it must
// not.
type unreadable struct{ t *testing.T }

func (u unreadable) Read([]byte) (int, error) {
	u.t.Error("stdin was read")
	return 0, io.EOF
}

type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

// endsOnce yields its text with end of file and fails the test when it is
// read again: an input that has ended must not be read twice, as a terminal
// would block there.
type endsOnce struct {
	t    *testing.T
	text string
	done bool
}

func (e *endsOnce) Read(p []byte) (int, error) {
	if e.done {
		e.t.Error("stdin was read after end of file")
		return 0, io.EOF
	}
	e.done = true
	return copy(p, e.text), io.EOF
}

func cancelledContext(t *testing.T) context.Context {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

// Bytes that arrive after the wait gave up still reach the wrapped command.
func TestHookReadDeliversLateInputThroughRest(t *testing.T) {
	reader, writer := io.Pipe()
	read := startHookRead(reader)
	defer read.stop()
	raw, rest, err := read.wait(cancelledContext(t))
	if len(raw) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("wait = %q, %v", raw, err)
	}
	go func() {
		_, _ = io.WriteString(writer, "late input")
		_ = writer.Close()
	}()
	got, err := io.ReadAll(rest)
	if err != nil || string(got) != "late input" {
		t.Fatalf("rest = %q, %v", got, err)
	}
}

func TestHookReadRestEndsAfterStop(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	read := startHookRead(reader)
	_, rest, _ := read.wait(cancelledContext(t))
	read.stop()
	read.stop() // a second stop is harmless
	if n, err := rest.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("Read after stop = %d, %v", n, err)
	}
}

func TestHookReadRest(t *testing.T) {
	const beyond = 9
	oversized := strings.Repeat("x", hookInputLimit+1+beyond)
	for _, tc := range []struct {
		name     string
		stdin    func(t *testing.T) io.Reader
		wantRaw  int
		wantRest int
		wantErr  error
	}{
		{"ended input", func(t *testing.T) io.Reader { return &endsOnce{t: t, text: "{}"} }, 2, 0, nil},
		{"larger than the limit", func(*testing.T) io.Reader { return strings.NewReader(oversized) }, hookInputLimit + 1, beyond, nil},
		{"no stdin", func(*testing.T) io.Reader { return nil }, 0, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := startHookRead(tc.stdin(t))
			defer read.stop()
			raw, rest, err := read.wait(t.Context())
			if len(raw) != tc.wantRaw || !errors.Is(err, tc.wantErr) {
				t.Fatalf("wait = %d bytes, %v; want %d bytes", len(raw), err, tc.wantRaw)
			}
			got, err := io.ReadAll(rest)
			if err != nil || len(got) != tc.wantRest {
				t.Fatalf("rest = %d bytes, %v; want %d", len(got), err, tc.wantRest)
			}
		})
	}
}

func TestEmitFromHook(t *testing.T) {
	const private = "do-not-forward"
	oversized := `{"session_id":"session-1","padding":"` + strings.Repeat("p", hookInputLimit) + `"}`
	for _, tc := range []struct {
		name        string
		stdin       string
		args        []string
		wantSession string
		wantToolUse string
		wantNote    string
	}{
		{
			name:        "identifiers are taken and the rest is dropped",
			stdin:       `{"hook_event_name":"PostToolUse","session_id":"session-1","tool_use_id":"toolu_1","tool_input":{"command":"` + private + `"}}`,
			wantSession: "session-1",
			wantToolUse: "toolu_1",
		},
		{
			name:        "session only",
			stdin:       `{"hook_event_name":"Stop","session_id":"session-1"}`,
			wantSession: "session-1",
		},
		{
			name:        "flags win over the payload",
			stdin:       `{"session_id":"session-1","tool_use_id":"toolu_1"}`,
			args:        []string{"--session-id", "session-2"},
			wantSession: "session-2",
			wantToolUse: "toolu_1",
		},
		{
			name:        "identifier the endpoint would refuse is left out",
			stdin:       `{"session_id":"session-1","tool_use_id":"` + private + ` with space"}`,
			wantSession: "session-1",
			wantNote:    "tool_use_id does not match",
		},
		{
			name:     "not JSON",
			stdin:    private + " plain text",
			wantNote: "not the expected JSON",
		},
		{
			name:     "identifier of the wrong type",
			stdin:    `{"session_id":17,"tool_use_id":"toolu_1"}`,
			wantNote: "not the expected JSON",
		},
		{
			name:     "empty stdin",
			stdin:    "",
			wantNote: "not the expected JSON",
		},
		{
			name:     "larger than the read limit",
			stdin:    oversized,
			wantNote: errHookTooLarge.Error(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.env.stdin = strings.NewReader(tc.stdin)
			args := append([]string{"--name", "lint", "--from-hook", "--verbose"}, tc.args...)
			if code := h.dispatch(args...); code != exitOK {
				t.Fatalf("exit %d", code)
			}
			// A payload that cannot be used never stops the event.
			ev := h.sent()
			if ev.SessionID != tc.wantSession || ev.ToolUseID != tc.wantToolUse {
				t.Fatalf("linked to session %q, tool use %q; want %q, %q", ev.SessionID, ev.ToolUseID, tc.wantSession, tc.wantToolUse)
			}
			if body := string(h.transport.captured()[0].body); strings.Contains(body, private) {
				t.Fatalf("hook content forwarded: %s", body)
			}
			if strings.Contains(h.stderr.String(), private) {
				t.Fatalf("hook content printed: %q", h.stderr)
			}
			if tc.wantNote == "" && h.stderr.Len() != 0 {
				t.Fatalf("stderr %q", h.stderr)
			}
			if !strings.Contains(h.stderr.String(), tc.wantNote) {
				t.Fatalf("stderr %q does not mention %q", h.stderr, tc.wantNote)
			}
		})
	}
}

func TestEmitReadsStdinOnlyWhenAskedAndConfigured(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		vars map[string]string
	}{
		{"without --from-hook", []string{"--name", "lint"}, map[string]string{envURL: testEndpoint, envToken: testToken}},
		{"tracing is off", []string{"--name", "lint", "--from-hook"}, map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.vars = tc.vars
			h.env.stdin = unreadable{t}
			if code := h.dispatch(tc.args...); code != exitOK {
				t.Fatalf("exit %d", code)
			}
		})
	}
}

// A terminal never reaches end of file; the event must still go out.
func TestEmitFromHookGivesUpOnSilentStdin(t *testing.T) {
	h := newHarness(t)
	reader, writer := io.Pipe()
	// Closing the writer ends the read that the command left behind.
	t.Cleanup(func() { _ = writer.Close() })
	h.env.stdin = reader

	started := time.Now()
	if code := h.dispatch("--name", "lint", "--from-hook", "--verbose"); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if waited := time.Since(started); waited < hookReadTimeout {
		t.Fatalf("gave up after %v, before %v", waited, hookReadTimeout)
	}
	if ev := h.sent(); ev.SessionID != "" || ev.ToolUseID != "" {
		t.Fatalf("event linked without hook input: %+v", ev)
	}
	if !strings.Contains(h.stderr.String(), "deadline exceeded") {
		t.Fatalf("stderr %q", h.stderr)
	}
}

func TestReadHookInputReturnsWhatItReadOnFailure(t *testing.T) {
	failure := errors.New("device gone")
	raw, err := readHookInput(io.MultiReader(strings.NewReader("partial"), failingReader{failure}))
	if string(raw) != "partial" || !errors.Is(err, failure) {
		t.Fatalf("readHookInput = %q, %v", raw, err)
	}
	if raw, err := readHookInput(nil); raw != nil || err != nil {
		t.Fatalf("readHookInput(nil) = %q, %v", raw, err)
	}
}
