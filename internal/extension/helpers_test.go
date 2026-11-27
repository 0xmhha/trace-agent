package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Fixed parts of every synthetic request.
const (
	testHost      = "127.0.0.1:4318"
	testURL       = "http://" + testHost + EventsPath
	testSource    = "deploy-script"
	testBearerLen = 40
)

// testBearer is built at run time so that no credential-shaped literal is
// committed.
func testBearer() string { return strings.Repeat("k", testBearerLen) }

// testEpoch is the receive time of the fake clock.
func testEpoch() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

// fakeClock is a clock tests move by hand.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{at: testEpoch()} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// rejectCall is one CountRejected call.
type rejectCall struct {
	source string
	n      int
}

// fakeSink records what the handler hands to the store.
type fakeSink struct {
	mu          sync.Mutex
	appends     [][]usage.ExtEvent
	rejects     []rejectCall
	appendErr   error
	rejectErr   error
	noDeadline  bool
	entered     chan struct{}
	release     chan struct{}
	blockAppend bool
}

func (s *fakeSink) AppendExt(ctx context.Context, events []usage.ExtEvent) error {
	if s.blockAppend {
		s.entered <- struct{}{}
		<-s.release
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		s.noDeadline = true
	}
	s.appends = append(s.appends, append([]usage.ExtEvent(nil), events...))
	return s.appendErr
}

func (s *fakeSink) CountRejected(ctx context.Context, source string, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		s.noDeadline = true
	}
	s.rejects = append(s.rejects, rejectCall{source: source, n: n})
	return s.rejectErr
}

// stored returns every event handed over, in order.
func (s *fakeSink) stored() []usage.ExtEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []usage.ExtEvent
	for _, batch := range s.appends {
		all = append(all, batch...)
	}
	return all
}

func (s *fakeSink) appendCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.appends)
}

func (s *fakeSink) rejectCalls() []rejectCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rejectCall(nil), s.rejects...)
}

// rejectedBySource sums the CountRejected calls per source.
func (s *fakeSink) rejectedBySource() map[string]int {
	total := map[string]int{}
	for _, call := range s.rejectCalls() {
		total[call.source] += call.n
	}
	return total
}

// untouched reports whether the handler made no sink call at all.
func (s *fakeSink) untouched() bool {
	return s.appendCalls() == 0 && len(s.rejectCalls()) == 0
}

// fixture is a handler with its fake dependencies.
type fixture struct {
	handler http.Handler
	sink    *fakeSink
	clock   *fakeClock
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	sink := &fakeSink{}
	clock := newFakeClock()
	h, err := NewHandler(sink, testHost, testBearer(), clock.Now)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return &fixture{handler: h, sink: sink, clock: clock}
}

// newRequest builds a request that passes every check; tests break one
// thing at a time.
func newRequest(body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, testURL, bytes.NewReader(body))
	req.Header.Set(headerAuthorization, bearerPrefix+testBearer())
	req.Header.Set(headerContentType, jsonMediaType)
	return req
}

func (f *fixture) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) post(body []byte) *httptest.ResponseRecorder {
	return f.do(newRequest(body))
}

// postJSON encodes v and posts it.
func (f *fixture) postJSON(t *testing.T, v any) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(encode(t, v))
}

// accept posts v and requires a 202, returning the decoded response.
func (f *fixture) accept(t *testing.T, v any) WireResponse {
	t.Helper()
	return decodeResponse(t, f.postJSON(t, v))
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	return raw
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) WireResponse {
	t.Helper()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %q; want %d", rec.Code, rec.Body.String(), http.StatusAccepted)
	}
	var out WireResponse
	dec := json.NewDecoder(rec.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("response has trailing data")
	}
	return out
}

// obj is a JSON object a test can bend into shapes WireEvent cannot hold.
type obj = map[string]any

// minimalEvent is the smallest valid event.
func minimalEvent() obj {
	return obj{"version": WireVersion, "source": testSource, "kind": "span", "name": "upload"}
}

// with returns a copy of base with the given members set.
func with(base obj, members obj) obj {
	out := maps.Clone(base)
	maps.Copy(out, members)
	return out
}

// without returns a copy of base lacking the given members.
func without(base obj, names ...string) obj {
	out := maps.Clone(base)
	for _, name := range names {
		delete(out, name)
	}
	return out
}

// batch wraps events into the multi-event body.
func batch(events ...any) obj { return obj{"events": events} }

// hostingToken returns a string the masking rules recognise as a credential.
// It is assembled here so that the source holds no such literal.
func hostingToken() string { return "gh" + "p_" + strings.Repeat("A", 24) }
