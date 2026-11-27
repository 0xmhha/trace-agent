package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/extension"
)

// childEnv turns the test binary into the wrapped command of run mode, so
// that the tests need no external program.
const childEnv = "TRACE_EMIT_TEST_CHILD"

// Behaviours of the test binary when it runs as the wrapped command.
const (
	childExit  = "exit"  // exit with the status given as the next argument
	childEcho  = "echo"  // copy stdin to stdout
	childSleep = "sleep" // stay alive until a signal ends the process
)

const (
	testEndpoint = endpointPrefix + "8123" + extension.EventsPath
	// Not secret-shaped on purpose; the secret scanner reads test files too.
	testToken = "local" + "-" + "test"
)

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) != "" {
		os.Exit(childMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func childMain(args []string) int {
	if len(args) == 0 {
		return exitUsage
	}
	switch args[0] {
	case childExit:
		code, err := strconv.Atoi(args[1])
		if err != nil {
			return exitUsage
		}
		return code
	case childEcho:
		if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
			return exitFailure
		}
		return exitOK
	case childSleep:
		// The work of a long-running command; the test ends it by signal.
		time.Sleep(time.Minute)
		return exitOK
	}
	return exitUsage
}

// harness is an environment whose every input and output is under test
// control.
type harness struct {
	t         *testing.T
	env       environment
	vars      map[string]string
	stdout    *bytes.Buffer
	stderr    *bytes.Buffer
	transport *fakeTransport
}

var testStart = time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)

// testID is the identifier the fake randomness yields.
var testID = strings.Repeat("ab", eventIDBytes)

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:         t,
		vars:      map[string]string{envURL: testEndpoint, envToken: testToken},
		stdout:    &bytes.Buffer{},
		stderr:    &bytes.Buffer{},
		transport: &fakeTransport{},
	}
	h.env = environment{
		getenv:    func(name string) string { return h.vars[name] },
		stdout:    h.stdout,
		stderr:    h.stderr,
		now:       fakeClock(testStart),
		random:    bytes.NewReader(bytes.Repeat([]byte{0xab}, eventIDBytes)),
		transport: h.transport,
		signals:   func() (<-chan os.Signal, func()) { return nil, func() {} },
	}
	return h
}

func (h *harness) dispatch(args ...string) int {
	h.t.Helper()
	return h.env.dispatch(h.t.Context(), args)
}

// sent returns the only event the command sent.
func (h *harness) sent() extension.WireEvent {
	h.t.Helper()
	requests := h.transport.captured()
	if len(requests) != 1 {
		h.t.Fatalf("requests = %d, want 1 (stderr: %s)", len(requests), h.stderr)
	}
	decoder := json.NewDecoder(bytes.NewReader(requests[0].body))
	decoder.DisallowUnknownFields()
	var ev extension.WireEvent
	if err := decoder.Decode(&ev); err != nil {
		h.t.Fatalf("body is not a WireEvent: %v", err)
	}
	return ev
}

func (h *harness) wantNothingSent() {
	h.t.Helper()
	if n := len(h.transport.captured()); n != 0 {
		h.t.Fatalf("requests = %d, want 0", n)
	}
}

// wantNoCredential fails when the credential reached stdout or stderr.
func (h *harness) wantNoCredential() {
	h.t.Helper()
	token := h.vars[envToken]
	if token == "" {
		return
	}
	if strings.Contains(h.stderr.String(), token) || strings.Contains(h.stdout.String(), token) {
		h.t.Fatalf("credential printed: stdout %q stderr %q", h.stdout, h.stderr)
	}
}

// fakeClock returns each of times in turn and then keeps returning the last.
func fakeClock(times ...time.Time) func() time.Time {
	var mu sync.Mutex
	next := 0
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t := times[next]
		if next < len(times)-1 {
			next++
		}
		return t
	}
}

type capturedRequest struct {
	method      string
	url         string
	header      http.Header
	body        []byte
	hasDeadline bool
	deadline    time.Time
}

// fakeTransport records requests and answers them without a socket.
type fakeTransport struct {
	mu       sync.Mutex
	requests []capturedRequest
	// respond overrides the default 202 answer.
	respond func(*http.Request) (*http.Response, error)
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	deadline, hasDeadline := req.Context().Deadline()
	f.mu.Lock()
	f.requests = append(f.requests, capturedRequest{
		method:      req.Method,
		url:         req.URL.String(),
		header:      req.Header.Clone(),
		body:        body,
		hasDeadline: hasDeadline,
		deadline:    deadline,
	})
	respond := f.respond
	f.mu.Unlock()
	if respond != nil {
		return respond(req)
	}
	return answer(req, http.StatusAccepted, `{"accepted":1,"rejected":[]}`), nil
}

func (f *fakeTransport) captured() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]capturedRequest(nil), f.requests...)
}

func answer(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func floatPtr(v float64) *float64 { return &v }

func TestEmitSendsOneWireEvent(t *testing.T) {
	h := newHarness(t)
	code := h.dispatch(
		"--name", "upload", "--kind", "span", "--status", "error", "--level", "warn",
		"--duration-ms", "812.5", "--value", "1", "--unit", "count",
		"--message", "exit status 1", "--attr", "step=3", "--attr", "note=a=b", "--attr", "empty=",
		"--source", "deploy-script", "--session-id", "session-1", "--tool-use-id", "toolu_1",
		"--trace-id", "trace:1", "--span-id", "span.1", "--parent-span-id", "span.0",
	)
	if code != exitOK || h.stdout.Len() != 0 || h.stderr.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, h.stdout, h.stderr)
	}
	want := extension.WireEvent{
		Version:      extension.WireVersion,
		ID:           testID,
		Source:       "deploy-script",
		Kind:         "span",
		Name:         "upload",
		Timestamp:    "2026-01-01T01:00:00.000Z",
		DurationMs:   floatPtr(812.5),
		Status:       "error",
		Level:        "warn",
		SessionID:    "session-1",
		ToolUseID:    "toolu_1",
		TraceID:      "trace:1",
		SpanID:       "span.1",
		ParentSpanID: "span.0",
		Message:      "exit status 1",
		Attributes:   map[string]any{"step": "3", "note": "a=b", "empty": ""},
		Value:        floatPtr(1),
		Unit:         "count",
	}
	wantBody, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	requests := h.transport.captured()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	req := requests[0]
	if !bytes.Equal(req.body, wantBody) {
		t.Fatalf("body\n got %s\nwant %s", req.body, wantBody)
	}
	if req.method != http.MethodPost || req.url != testEndpoint {
		t.Fatalf("request %s %s", req.method, req.url)
	}
	if got := req.header.Values(headerAuthorization); len(got) != 1 || got[0] != bearerPrefix+testToken {
		t.Fatalf("authorization header count %d or value differs", len(got))
	}
	if got := req.header.Get(headerContentType); got != contentTypeJSON {
		t.Fatalf("content type %q", got)
	}
	// The endpoint refuses any request that carries an Origin header.
	if _, present := req.header["Origin"]; present {
		t.Fatal("origin header sent")
	}
	if !req.hasDeadline || req.deadline.After(time.Now().Add(sendTimeout)) {
		t.Fatalf("send is not bounded by %v", sendTimeout)
	}
}

func TestEmitDefaults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		envSource  string
		wantSource string
	}{
		{"built-in source", []string{"--name", "build"}, "", defaultSource},
		{"source from the environment", []string{"--name", "build"}, "my-plugin", "my-plugin"},
		{"flag wins over the environment", []string{"--name", "build", "--source", "cli"}, "my-plugin", "cli"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.vars[envSource] = tc.envSource
			if code := h.dispatch(tc.args...); code != exitOK {
				t.Fatalf("exit %d", code)
			}
			want := extension.WireEvent{
				Version:   extension.WireVersion,
				ID:        testID,
				Source:    tc.wantSource,
				Kind:      string(kindLog),
				Name:      "build",
				Timestamp: "2026-01-01T01:00:00.000Z",
			}
			if got := h.sent(); !reflect.DeepEqual(got, want) {
				t.Fatalf("event\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestEmitLongTextIsCutOnCharacterBoundary(t *testing.T) {
	h := newHarness(t)
	long := strings.Repeat("가", maxSentMessage+10)
	if code := h.dispatch("--name", "build", "--message", long, "--attr", "detail="+long); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	ev := h.sent()
	if want := strings.Repeat("가", maxSentMessage); ev.Message != want {
		t.Fatalf("message has %d bytes, want %d", len(ev.Message), len(want))
	}
	if want := strings.Repeat("가", maxSentAttribute); ev.Attributes["detail"] != want {
		t.Fatalf("attribute value was not cut to %d characters", maxSentAttribute)
	}
}

// Tracing that is switched off or misconfigured must not fail the caller.
func TestEmitNotConfiguredExitsZeroAndSendsNothing(t *testing.T) {
	userInfo := "http://user:" + "hunter2222" + "@127.0.0.1:8123" + extension.EventsPath
	for _, tc := range []struct {
		name   string
		vars   map[string]string
		reason error
	}{
		{"nothing set", map[string]string{}, errNoEndpoint},
		{"address missing", map[string]string{envToken: testToken}, errNoEndpoint},
		{"credential missing", map[string]string{envURL: testEndpoint}, errNoToken},
		{"address is not loopback", map[string]string{envURL: "http://localhost:8123" + extension.EventsPath, envToken: testToken}, errEndpointRefused},
		{"address carries user info", map[string]string{envURL: userInfo, envToken: testToken}, errEndpointRefused},
		{"credential would split the header", map[string]string{envURL: testEndpoint, envToken: "line\nbreak"}, errTokenRefused},
		{"source in the environment is invalid", map[string]string{envURL: testEndpoint, envToken: testToken, envSource: "not a name"}, errEnvSource},
	} {
		for _, verbose := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/verbose=%t", tc.name, verbose), func(t *testing.T) {
				h := newHarness(t)
				h.vars = tc.vars
				args := []string{"--name", "build"}
				if verbose {
					args = append(args, "--verbose")
				}
				if code := h.dispatch(args...); code != exitOK {
					t.Fatalf("exit %d, want 0", code)
				}
				h.wantNothingSent()
				h.wantNoCredential()
				if h.stdout.Len() != 0 {
					t.Fatalf("stdout %q", h.stdout)
				}
				switch {
				case !verbose && h.stderr.Len() != 0:
					t.Fatalf("stderr without --verbose: %q", h.stderr)
				case verbose && !strings.Contains(h.stderr.String(), tc.reason.Error()):
					t.Fatalf("stderr %q does not explain %q", h.stderr, tc.reason)
				case strings.Contains(h.stderr.String(), "hunter2222"):
					t.Fatalf("stderr echoes the address: %q", h.stderr)
				}
			})
		}
	}
}

func TestEmitSendFailureExitsZero(t *testing.T) {
	for _, tc := range []struct {
		name    string
		respond func(*http.Request) (*http.Response, error)
		reason  string
	}{
		{
			"transport error",
			func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") },
			"connection refused",
		},
		{
			"error text that quotes the credential",
			func(*http.Request) (*http.Response, error) { return nil, errors.New("bad header " + testToken) },
			redacted,
		},
		{
			"timeout",
			func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, req.Context().Err()
			},
			"deadline exceeded",
		},
		{
			"unauthorized",
			func(req *http.Request) (*http.Response, error) { return answer(req, http.StatusUnauthorized, ""), nil },
			"status 401",
		},
		{
			"rate limited",
			func(req *http.Request) (*http.Response, error) {
				return answer(req, http.StatusTooManyRequests, ""), nil
			},
			"status 429",
		},
		{
			"redirect is not followed",
			func(req *http.Request) (*http.Response, error) {
				resp := answer(req, http.StatusTemporaryRedirect, "")
				resp.Header.Set("Location", "http://127.0.0.1:9"+extension.EventsPath)
				return resp, nil
			},
			"status 307",
		},
		{
			"event rejected",
			func(req *http.Request) (*http.Response, error) {
				return answer(req, http.StatusAccepted, `{"accepted":0,"rejected":[{"index":0,"reason":"name too long"}]}`), nil
			},
			`"name too long"`,
		},
		{
			"unreadable answer",
			func(req *http.Request) (*http.Response, error) {
				return answer(req, http.StatusAccepted, "<html>"), nil
			},
			"not the expected JSON",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.transport.respond = tc.respond
			if code := h.dispatch("--name", "build", "--verbose"); code != exitOK {
				t.Fatalf("exit %d, want 0", code)
			}
			if n := len(h.transport.captured()); n != 1 {
				t.Fatalf("requests = %d, want exactly 1", n)
			}
			if !strings.Contains(h.stderr.String(), tc.reason) {
				t.Fatalf("stderr %q does not mention %q", h.stderr, tc.reason)
			}
			h.wantNoCredential()

			quiet := newHarness(t)
			quiet.transport.respond = tc.respond
			if code := quiet.dispatch("--name", "build"); code != exitOK || quiet.stderr.Len() != 0 {
				t.Fatalf("without --verbose: exit %d, stderr %q", code, quiet.stderr)
			}
		})
	}
}

func TestEmitWithoutRandomnessSendsNothing(t *testing.T) {
	h := newHarness(t)
	h.env.random = bytes.NewReader(nil)
	if code := h.dispatch("--name", "build", "--verbose"); code != exitOK {
		t.Fatalf("exit %d, want 0", code)
	}
	h.wantNothingSent()
	if !strings.Contains(h.stderr.String(), "generate event id") {
		t.Fatalf("stderr %q", h.stderr)
	}
}

func TestHelpPrintsUsage(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {runWord, "-h"}} {
		h := newHarness(t)
		if code := h.dispatch(args...); code != exitOK {
			t.Fatalf("%v: exit %d", args, code)
		}
		if h.stdout.String() != usageText || h.stderr.Len() != 0 {
			t.Fatalf("%v: stdout %q stderr %q", args, h.stdout, h.stderr)
		}
		h.wantNothingSent()
	}
}

func TestUsageErrors(t *testing.T) {
	const sensitive = "do-not-echo-this"
	many := []string{"--name", "build"}
	for i := range extension.MaxAttributes + 1 {
		many = append(many, "--attr", fmt.Sprintf("k%d=v", i))
	}
	manyForRun := []string{runWord}
	for i := range extension.MaxAttributes {
		manyForRun = append(manyForRun, "--attr", fmt.Sprintf("k%d=v", i))
	}
	manyForRun = append(manyForRun, "--", "/nonexistent/never-started")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no arguments", nil},
		{"name missing", []string{"--kind", "log"}},
		{"name empty", []string{"--name", ""}},
		{"name too long", []string{"--name", strings.Repeat("n", extension.MaxNameLen+1)}},
		{"unknown flag", []string{"--name", "build", "--bogus"}},
		{"flag without its value", []string{"--name"}},
		{"stray argument", []string{"--name", "build", sensitive}},
		{"unknown kind", []string{"--name", "build", "--kind", sensitive}},
		{"unknown status", []string{"--name", "build", "--status", "okay"}},
		{"unknown level", []string{"--name", "build", "--level", "fatal"}},
		{"source with a space", []string{"--name", "build", "--source", "my tool"}},
		{"source too long", []string{"--name", "build", "--source", strings.Repeat("s", 65)}},
		{"session id with a slash", []string{"--name", "build", "--session-id", "a/b"}},
		{"tool use id with a space", []string{"--name", "build", "--tool-use-id", "a b"}},
		{"trace id too long", []string{"--name", "build", "--trace-id", strings.Repeat("t", 129)}},
		{"span id with a quote", []string{"--name", "build", "--span-id", `a"b`}},
		{"parent span id with a space", []string{"--name", "build", "--parent-span-id", " "}},
		{"duration is not a number", []string{"--name", "build", "--duration-ms", sensitive}},
		{"duration is negative", []string{"--name", "build", "--duration-ms", "-1"}},
		{"duration is not finite", []string{"--name", "build", "--duration-ms", "Inf"}},
		{"value is not a number", []string{"--name", "build", "--value", "NaN"}},
		{"value is negative", []string{"--name", "build", "--value", "-0.5"}},
		{"unit too long", []string{"--name", "build", "--unit", strings.Repeat("u", extension.MaxUnitLen+1)}},
		{"attribute without separator", []string{"--name", "build", "--attr", sensitive}},
		{"attribute without key", []string{"--name", "build", "--attr", "=" + sensitive}},
		{"attribute key with a space", []string{"--name", "build", "--attr", "a b=" + sensitive}},
		{"attribute key too long", []string{"--name", "build", "--attr", strings.Repeat("k", 65) + "=v"}},
		{"too many attributes", many},
		{"run without separator", []string{runWord, "/nonexistent/never-started"}},
		{"run without command", []string{runWord, "--name", "build", "--"}},
		{"run with nothing", []string{runWord}},
		{"run with status", []string{runWord, "--status", "ok", "--", "/nonexistent/never-started"}},
		{"run with duration", []string{runWord, "--duration-ms", "5", "--", "/nonexistent/never-started"}},
		{"run with exit_code attribute", []string{runWord, "--attr", attrExitCode + "=0", "--", "/nonexistent/never-started"}},
		{"run with no room for exit_code", manyForRun},
		{"run with unknown kind", []string{runWord, "--kind", "trace", "--", "/nonexistent/never-started"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			// Exit 2 rather than 127 also shows that run did not start the command.
			if code := h.dispatch(tc.args...); code != exitUsage {
				t.Fatalf("exit %d, want %d (stderr %q)", code, exitUsage, h.stderr)
			}
			h.wantNothingSent()
			if h.stdout.Len() != 0 {
				t.Fatalf("stdout %q", h.stdout)
			}
			if !strings.HasPrefix(h.stderr.String(), programName+": ") {
				t.Fatalf("stderr %q does not explain the mistake", h.stderr)
			}
			if strings.Contains(h.stderr.String(), sensitive) {
				t.Fatalf("stderr echoes a flag value: %q", h.stderr)
			}
		})
	}
}

func TestAttributeLimitIsAccepted(t *testing.T) {
	h := newHarness(t)
	args := []string{"--name", "build", "--attr", "k0=first"}
	for i := range extension.MaxAttributes {
		args = append(args, "--attr", fmt.Sprintf("k%d=v", i))
	}
	if code := h.dispatch(args...); code != exitOK {
		t.Fatalf("exit %d (stderr %q)", code, h.stderr)
	}
	ev := h.sent()
	// k0 was given twice: the later value wins and the key counts once.
	if len(ev.Attributes) != extension.MaxAttributes || ev.Attributes["k0"] != "v" {
		t.Fatalf("attributes = %d, k0 = %v", len(ev.Attributes), ev.Attributes["k0"])
	}
}
