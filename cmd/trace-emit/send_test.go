package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/0xmhha/trace-agent/internal/extension"
)

func TestLoopbackEndpoint(t *testing.T) {
	path := extension.EventsPath
	for _, tc := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{"canonical", "http://127.0.0.1:8123" + path, true},
		{"lowest port", "http://127.0.0.1:1" + path, true},
		{"highest port", "http://127.0.0.1:65535" + path, true},
		{"empty", "", false},
		{"localhost", "http://localhost:8123" + path, false},
		{"other loopback address", "http://127.0.0.2:8123" + path, false},
		{"ipv6 loopback", "http://[::1]:8123" + path, false},
		{"all interfaces", "http://0.0.0.0:8123" + path, false},
		{"documentation address", "http://192.0.2.10:8123" + path, false},
		{"host that only starts like loopback", "http://127.0.0.1.example.com:8123" + path, false},
		{"https", "https://127.0.0.1:8123" + path, false},
		{"upper case scheme", "HTTP://127.0.0.1:8123" + path, false},
		{"no scheme", "127.0.0.1:8123" + path, false},
		{"missing port", "http://127.0.0.1" + path, false},
		{"empty port", "http://127.0.0.1:" + path, false},
		{"port zero", "http://127.0.0.1:0" + path, false},
		{"port out of range", "http://127.0.0.1:65536" + path, false},
		{"port with leading zero", "http://127.0.0.1:08123" + path, false},
		{"port with sign", "http://127.0.0.1:+8123" + path, false},
		{"port is a name", "http://127.0.0.1:http" + path, false},
		{"extra path segment", "http://127.0.0.1:8123" + path + "/extra", false},
		{"trailing slash", "http://127.0.0.1:8123" + path + "/", false},
		{"path prefix", "http://127.0.0.1:8123/proxy" + path, false},
		{"other path", "http://127.0.0.1:8123/v1/hooks", false},
		{"no path", "http://127.0.0.1:8123", false},
		{"encoded path", "http://127.0.0.1:8123/v1/%65vents", false},
		{"user info", "http://user@127.0.0.1:8123" + path, false},
		{"user info with password", "http://user:" + "hunter2222" + "@127.0.0.1:8123" + path, false},
		{"loopback as user info of another host", "http://127.0.0.1:8123@example.com" + path, false},
		{"query", "http://127.0.0.1:8123" + path + "?a=1", false},
		{"empty query", "http://127.0.0.1:8123" + path + "?", false},
		{"fragment", "http://127.0.0.1:8123" + path + "#x", false},
		{"surrounding space", " http://127.0.0.1:8123" + path + " ", false},
		{"line break", "http://127.0.0.1:8123" + path + "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := loopbackEndpoint(tc.raw)
			switch {
			case tc.ok && (err != nil || got != tc.raw):
				t.Fatalf("loopbackEndpoint = %q, %v; want the address unchanged", got, err)
			case !tc.ok && !errors.Is(err, errEndpointRefused):
				t.Fatalf("loopbackEndpoint = %q, %v; want %v", got, err, errEndpointRefused)
			case !tc.ok && got != "":
				t.Fatalf("refused address returned as %q", got)
			}
		})
	}
}

func TestHeaderSafe(t *testing.T) {
	for _, tc := range []struct {
		token string
		want  bool
	}{
		{"abc-DEF_123.~+/=", true},
		{"", true}, // emptiness is reported separately
		{"two words", false},
		{"line\nbreak", false},
		{"carriage\rreturn", false},
		{"tab\there", false},
		{"nul\x00", false},
		{"non-ascii-é", false},
	} {
		if got := headerSafe(tc.token); got != tc.want {
			t.Errorf("headerSafe(%q) = %t, want %t", tc.token, got, tc.want)
		}
	}
}

// buildEvent keeps every event below the request limit, so the guard in post
// is exercised directly.
func TestPostRefusesOversizedBody(t *testing.T) {
	h := newHarness(t)
	ev := extension.WireEvent{
		Version: extension.WireVersion,
		Source:  defaultSource,
		Kind:    string(kindLog),
		Name:    "build",
		Message: strings.Repeat("m", extension.MaxBodyBytes),
	}
	err := h.env.post(t.Context(), destination{url: testEndpoint, token: testToken}, ev)
	if !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("post = %v, want %v", err, errBodyTooLarge)
	}
	h.wantNothingSent()
}

func TestNewEventID(t *testing.T) {
	h := newHarness(t)
	id, err := newEventID(h.env.random)
	if err != nil || id != testID {
		t.Fatalf("newEventID = %q, %v; want %q", id, err, testID)
	}
	if _, err := newEventID(h.env.random); err == nil {
		t.Fatal("exhausted randomness yielded an identifier")
	}
}
