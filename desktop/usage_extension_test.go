package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/usage"
)

func TestUsageEventsToken(t *testing.T) {
	key := testInstallationKey()
	token, err := usageEventsToken(key)
	if err != nil {
		t.Fatal(err)
	}
	if raw, decodeErr := hex.DecodeString(token); decodeErr != nil || len(raw) != 32 || len(token) != 64 {
		t.Fatalf("token is not 64 hex characters: %d", len(token))
	}
	if again, _ := usageEventsToken(slices.Clone(key)); again != token {
		t.Fatal("token is not deterministic")
	}
	other, err := usageEventsToken(bytes.Repeat([]byte{8}, installationKeySize))
	if err != nil || other == token {
		t.Fatal("token does not depend on the key", err)
	}
	// The trace receivers derive their credentials from the same key; no
	// source ID may reproduce the extension credential.
	for _, source := range []string{"", "v1", usageEventsTokenLabel, receiverInstance, codexReceiverInstance} {
		if receiverToken(key, source) == token {
			t.Fatalf("receiver token of source %q equals the extension token", source)
		}
	}
	for _, size := range []int{0, 1, 16, 31, 33, 64} {
		if got, sizeErr := usageEventsToken(make([]byte, size)); !errors.Is(sizeErr, errInstallationKeySize) || got != "" {
			t.Errorf("key of %d bytes: token %q, error %v", size, got, sizeErr)
		}
	}
}

// countingVault records what the collector saves.
type countingVault struct {
	saved   []registry
	saveErr error
}

func (v *countingVault) Load() (registry, error) { return registry{}, nil }
func (v *countingVault) Save(r registry) error {
	if v.saveErr != nil {
		return v.saveErr
	}
	v.saved = append(v.saved, r)
	return nil
}

func TestEnsureInstallationKey(t *testing.T) {
	existing := bytes.Repeat([]byte{3}, installationKeySize)
	generated := bytes.Repeat([]byte{5}, installationKeySize)
	sources := []selectedSource{{Kind: collect.Transcript, Path: "/synthetic/session.jsonl"}}
	for _, tc := range []struct {
		name      string
		config    registry
		random    io.Reader
		saveErr   error
		wantKey   []byte
		wantSaves int
		wantErr   bool
	}{
		{"existing key is returned without saving", registry{Key: existing}, strings.NewReader(""), nil, existing, 0, false},
		{"missing key is generated and saved", registry{Sources: sources}, bytes.NewReader(generated), nil, generated, 1, false},
		{"vault failure leaves no key in memory", registry{}, bytes.NewReader(generated), errTest, nil, 0, true},
		{"short random source is refused", registry{}, bytes.NewReader(generated[:8]), nil, nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &countingVault{saveErr: tc.saveErr}
			c := &collector{vault: v, config: tc.config}
			key, err := c.ensureInstallationKey(tc.random)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error %t", err, tc.wantErr)
			}
			if !bytes.Equal(key, tc.wantKey) {
				t.Fatalf("key = %x, want %x", key, tc.wantKey)
			}
			if len(v.saved) != tc.wantSaves {
				t.Fatalf("saves = %d, want %d", len(v.saved), tc.wantSaves)
			}
			if tc.wantErr {
				if len(c.config.Key) != 0 {
					t.Fatal("unsaved key stayed in memory")
				}
				return
			}
			if !bytes.Equal(c.config.Key, tc.wantKey) {
				t.Fatal("collector does not hold the returned key")
			}
			for _, saved := range v.saved {
				if !bytes.Equal(saved.Key, tc.wantKey) || !slices.Equal(saved.Sources, tc.config.Sources) {
					t.Fatal("saved registry lost the key or the sources")
				}
			}
			// The caller must not be able to change the stored key.
			key[0] ^= 0xff
			again, err := c.ensureInstallationKey(strings.NewReader(""))
			if err != nil || !bytes.Equal(again, tc.wantKey) || len(v.saved) != tc.wantSaves {
				t.Fatal("second call changed the key or saved again", err)
			}
		})
	}
}

func fixedKey(key []byte, err error) func() ([]byte, error) {
	return func() ([]byte, error) { return key, err }
}

func TestExtensionControlLifecycle(t *testing.T) {
	ctx := t.Context()
	l := newFakeUsageListener()
	c := newExtensionControl(nil, l.listen, fixedKey(testInstallationKey(), nil), 0)
	if got := c.Endpoint(); got != (usage.ExtEndpoint{}) {
		t.Fatalf("initial endpoint = %+v", got)
	}
	if port, err := c.SetEnabled(ctx, false); err != nil || port != 0 {
		t.Fatalf("stopping a stopped endpoint: %d %v", port, err)
	}
	if _, _, ok := c.credential(); ok {
		t.Fatal("stopped endpoint exposes a credential")
	}

	port, err := c.SetEnabled(ctx, true)
	if err != nil || port != testFreePort {
		t.Fatalf("start: %d %v", port, err)
	}
	want := usage.ExtEndpoint{Enabled: true, URL: "http://127.0.0.1:41000/v1/events"}
	if got := c.Endpoint(); got != want {
		t.Fatalf("endpoint = %+v, want %+v", got, want)
	}
	token, _ := usageEventsToken(testInstallationKey())
	if gotPort, bearer, ok := c.credential(); !ok || gotPort != port || bearer != token {
		t.Fatal("credential does not match the running endpoint")
	}
	if l.calls[0].bearer != token {
		t.Fatal("endpoint listens with another credential than the derived one")
	}
	if strings.Contains(c.Endpoint().URL, token) {
		t.Fatal("endpoint URL carries the credential")
	}

	if again, err := c.SetEnabled(ctx, true); err != nil || again != port || len(l.ports()) != 1 {
		t.Fatalf("second start bound again: %d %v %v", again, err, l.ports())
	}

	if stopped, err := c.SetEnabled(ctx, false); err != nil || stopped != port {
		t.Fatalf("stop: %d %v", stopped, err)
	}
	if l.receivers[0].closed != 1 || c.Endpoint().Enabled {
		t.Fatal("endpoint still open after stop")
	}
	if stopped, err := c.SetEnabled(ctx, false); err != nil || stopped != port || l.receivers[0].closed != 1 {
		t.Fatalf("second stop: %d %v", stopped, err)
	}

	if restarted, err := c.SetEnabled(ctx, true); err != nil || restarted != port {
		t.Fatalf("restart: %d %v", restarted, err)
	}
	if got, want := l.ports(), []int{0, port}; !slices.Equal(got, want) {
		t.Fatalf("listen ports = %v, want %v", got, want)
	}

	c.shutdown()
	if l.receivers[1].closed != 1 || c.Endpoint().Enabled {
		t.Fatal("shutdown left the endpoint open")
	}
	if _, err := c.SetEnabled(ctx, true); !errors.Is(err, errExtensionShutDown) {
		t.Fatalf("start after shutdown: %v", err)
	}
	if stopped, err := c.SetEnabled(ctx, false); err != nil || stopped != port {
		t.Fatalf("stop after shutdown: %d %v", stopped, err)
	}
	c.shutdown()
	if len(l.ports()) != 2 {
		t.Fatalf("unexpected listen calls %v", l.ports())
	}
}

func TestExtensionControlStart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lastPort  int
		key       []byte
		keyErr    error
		taken     []int
		listenErr error
		wantPorts []int
		wantPort  int
		wantErr   error
	}{
		{"free port on first start", 0, testInstallationKey(), nil, nil, nil, []int{0}, testFreePort, nil},
		{"stored port reused", testStoredPort, testInstallationKey(), nil, nil, nil, []int{testStoredPort}, testStoredPort, nil},
		{"taken port falls back to a free one", testStoredPort, testInstallationKey(), nil, []int{testStoredPort}, nil, []int{testStoredPort, 0}, testFreePort, nil},
		{"no fallback without a stored port", 0, testInstallationKey(), nil, nil, errTest, []int{0}, 0, errTest},
		{"fallback failure is reported", testStoredPort, testInstallationKey(), nil, nil, errTest, []int{testStoredPort, 0}, 0, errTest},
		{"negative stored port is ignored", -1, testInstallationKey(), nil, nil, nil, []int{0}, testFreePort, nil},
		{"out of range stored port is ignored", maxTCPPort + 1, testInstallationKey(), nil, nil, nil, []int{0}, testFreePort, nil},
		{"missing key", testStoredPort, nil, errTest, nil, nil, nil, 0, errTest},
		{"empty key", testStoredPort, nil, nil, nil, nil, nil, 0, errInstallationKeySize},
		{"short key", testStoredPort, make([]byte, 16), nil, nil, nil, nil, 0, errInstallationKeySize},
		{"long key", testStoredPort, make([]byte, 33), nil, nil, nil, nil, 0, errInstallationKeySize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newFakeUsageListener()
			l.err = tc.listenErr
			for _, port := range tc.taken {
				l.taken[port] = true
			}
			c := newExtensionControl(nil, l.listen, fixedKey(tc.key, tc.keyErr), tc.lastPort)
			port, err := c.SetEnabled(t.Context(), true)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if port != tc.wantPort {
				t.Fatalf("port = %d, want %d", port, tc.wantPort)
			}
			if got := l.ports(); !slices.Equal(got, tc.wantPorts) {
				t.Fatalf("listen ports = %v, want %v", got, tc.wantPorts)
			}
			if got := c.Endpoint().Enabled; got != (tc.wantErr == nil) {
				t.Fatalf("endpoint enabled = %t", got)
			}
			if _, _, ok := c.credential(); ok != (tc.wantErr == nil) {
				t.Fatalf("credential available = %t", ok)
			}
		})
	}
}

func TestExtensionControlCancelledContext(t *testing.T) {
	l := newFakeUsageListener()
	c := newExtensionControl(nil, l.listen, fixedKey(testInstallationKey(), nil), 0)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.SetEnabled(cancelled, true); !errors.Is(err, context.Canceled) || len(l.ports()) != 0 {
		t.Fatalf("start with a cancelled context: %v", err)
	}
	port, err := c.SetEnabled(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	// Stopping narrows what the process accepts and must not be skipped
	// because the request that asked for it was abandoned.
	if stopped, err := c.SetEnabled(cancelled, false); err != nil || stopped != port || l.receivers[0].closed != 1 {
		t.Fatalf("stop with a cancelled context: %d %v", stopped, err)
	}
}

func TestExtensionControlStopSurvivesCloseError(t *testing.T) {
	l := newFakeUsageListener()
	c := newExtensionControl(nil, l.listen, fixedKey(testInstallationKey(), nil), 0)
	port, err := c.SetEnabled(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	l.receivers[0].closeErr = errTest
	if stopped, err := c.SetEnabled(t.Context(), false); err != nil || stopped != port {
		t.Fatalf("stop: %d %v", stopped, err)
	}
	if c.Endpoint().Enabled {
		t.Fatal("endpoint reported as running after stop")
	}
}

func TestExtensionControlConcurrentToggles(t *testing.T) {
	l := newFakeUsageListener()
	c := newExtensionControl(nil, l.listen, fixedKey(testInstallationKey(), nil), 0)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			_, _ = c.SetEnabled(t.Context(), i%2 == 0)
			_ = c.Endpoint()
			_, _, _ = c.credential()
		})
	}
	wg.Wait()
	c.shutdown()
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, r := range l.receivers {
		if r.closed != 1 {
			t.Fatalf("receiver %d closed %d times", i, r.closed)
		}
	}
}
