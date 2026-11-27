package extension

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// clientTimeout bounds every request and shutdown of the socket test, so a
// regression fails instead of hanging.
const clientTimeout = 5 * time.Second

// These refusals happen before anything is bound.
func TestListenArguments(t *testing.T) {
	sink := &fakeSink{}
	now := newFakeClock().Now
	for _, tc := range []struct {
		name   string
		sink   usage.ExtSink
		port   int
		bearer string
		now    func() time.Time
		want   error
	}{
		{"nil sink", nil, 0, testBearer(), now, errNilSink},
		{"nil clock", sink, 0, testBearer(), nil, errNilClock},
		{"negative port", sink, -1, testBearer(), now, errBadPort},
		{"port out of range", sink, maxPort + 1, testBearer(), now, errBadPort},
		{"bearer too short", sink, 0, strings.Repeat("k", minBearerLen-1), now, errBadBearer},
		{"bearer too long", sink, 0, strings.Repeat("k", maxBearerLen+1), now, errBadBearer},
		{"bearer with space", sink, 0, strings.Repeat("k", 20) + " " + strings.Repeat("k", 20), now, errBadBearer},
		{"bearer with tab", sink, 0, strings.Repeat("k", 40) + "\t", now, errBadBearer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Listen(tc.sink, tc.port, tc.bearer, tc.now)
			if !errors.Is(err, tc.want) || r != nil {
				t.Fatalf("Listen = %v, %v; want nil, %v", r, err, tc.want)
			}
		})
	}
}

func TestReceiverNotListening(t *testing.T) {
	for name, r := range map[string]*Receiver{"nil": nil, "zero": {}} {
		if r.URL() != "" || r.Port() != 0 {
			t.Errorf("%s receiver reports an address: %q, %d", name, r.URL(), r.Port())
		}
		if err := r.Close(t.Context()); !errors.Is(err, errNilReceiver) {
			t.Errorf("%s receiver Close = %v; want %v", name, err, errNilReceiver)
		}
	}
}

// TestCloseCutsStalledRequest binds a real loopback port, like
// TestListenServesEvents. A request stuck in the store must not keep Close
// from returning once its context has ended.
func TestCloseCutsStalledRequest(t *testing.T) {
	sink := &fakeSink{blockAppend: true, entered: make(chan struct{}), release: make(chan struct{})}
	r, err := Listen(sink, 0, testBearer(), newFakeClock().Now)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: clientTimeout}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, r.URL(), bytes.NewReader(encode(t, minimalEvent())))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(headerAuthorization, bearerPrefix+testBearer())
	req.Header.Set(headerContentType, jsonMediaType)
	answered := make(chan error, 1)
	go func() {
		res, err := client.Do(req)
		if err == nil {
			err = res.Body.Close()
		}
		answered <- err
	}()
	<-sink.entered

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Close(expired); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close = %v; want %v", err, context.Canceled)
	}
	// The connection was cut, so the sender sees a failure, not an answer.
	if err := <-answered; err == nil {
		t.Fatal("the stalled request was answered after its connection was cut")
	}
	close(sink.release)
}

// TestListenServesEvents binds a real loopback port. A sandbox that forbids
// binding makes it fail at Listen.
func TestListenServesEvents(t *testing.T) {
	sink := &fakeSink{}
	r, err := Listen(sink, 0, testBearer(), newFakeClock().Now)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = r.Close(context.Background())
		}
	}()
	if r.Port() <= 0 || r.Port() > maxPort {
		t.Fatalf("port = %d", r.Port())
	}
	if want := "http://127.0.0.1:" + strconv.Itoa(r.Port()) + EventsPath; r.URL() != want {
		t.Fatalf("URL = %q; want %q", r.URL(), want)
	}
	// The proxy is switched off so that the request cannot leave the machine
	// whatever the environment says.
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: clientTimeout}
	post := func(url, credential string, body []byte, change func(*http.Request)) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(headerAuthorization, bearerPrefix+credential)
		req.Header.Set(headerContentType, jsonMediaType)
		if change != nil {
			change(req)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer func() { _ = res.Body.Close() }()
		text, err := io.ReadAll(io.LimitReader(res.Body, MaxBodyBytes))
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		return res.StatusCode, string(text)
	}

	body := encode(t, batch(minimalEvent(), without(minimalEvent(), "name")))
	code, text := post(r.URL(), testBearer(), body, nil)
	if code != http.StatusAccepted || text != `{"accepted":1,"rejected":[{"index":1,"reason":"missing_name"}]}` {
		t.Fatalf("response = %d %s", code, text)
	}
	if stored := sink.stored(); len(stored) != 1 || stored[0].Source != testSource || !stored[0].ReceivedAt.Equal(testEpoch()) {
		t.Fatalf("stored = %+v", stored)
	}
	if got, want := sink.rejectCalls(), []rejectCall{{testSource, 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rejections = %v; want %v", got, want)
	}
	if code, _ := post(r.URL(), strings.Repeat("x", testBearerLen), body, nil); code != http.StatusUnauthorized {
		t.Fatalf("wrong credential: status = %d; want %d", code, http.StatusUnauthorized)
	}
	// The same listener reached under another name is a different Host.
	otherName := func(req *http.Request) { req.Host = "localhost:" + strconv.Itoa(r.Port()) }
	if code, _ := post(r.URL(), testBearer(), body, otherName); code != http.StatusForbidden {
		t.Fatalf("other host name: status = %d; want %d", code, http.StatusForbidden)
	}
	if code, _ := post(r.URL()+"/more", testBearer(), body, nil); code != http.StatusForbidden {
		t.Fatalf("other path: status = %d; want %d", code, http.StatusForbidden)
	}
	if again, err := Listen(sink, r.Port(), testBearer(), newFakeClock().Now); err == nil {
		_ = again.Close(context.Background())
		t.Fatal("a second listener bound the same port")
	}

	ctx, cancel := context.WithTimeout(context.Background(), clientTimeout)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed = true
	if err := r.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := client.Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatal("the endpoint still answers after Close")
	}
}
