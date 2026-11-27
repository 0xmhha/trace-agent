package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Call names recorded by fakeService for the calls that are not Handle
// methods.
const (
	callConsent = "consent"
	callScan    = "scan"
)

// Canned section responses. The stray whitespace proves that the command
// re-encodes what it receives.
const (
	rawOverview = `{ "sessions": 3 }`
	rawTools    = `{ "tools": [] }`
	rawInsights = `{ "items": [] }`
	rawSessions = `{ "items": [], "total": 0 }`
)

var errInjected = errors.New("injected failure")

// statusJSON encodes the status fields the command reads.
func statusJSON(scanning bool, done, total int) string {
	raw, err := json.Marshal(usage.Status{Consent: true, Scanning: scanning, FilesDone: done, FilesTotal: total})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// fakeService is a scripted usageService. Status responses are consumed in
// order and the last one repeats.
type fakeService struct {
	mu        sync.Mutex
	calls     []string
	params    map[string][]string
	consent   []bool
	statuses  []string
	responses map[string]string
	errs      map[string]error
	scans     int
	scan      func(ctx context.Context, pass int) error
}

func newFakeService(statuses ...string) *fakeService {
	if len(statuses) == 0 {
		statuses = []string{statusJSON(false, 2, 2)}
	}
	return &fakeService{
		params:   map[string][]string{},
		statuses: statuses,
		responses: map[string]string{
			usage.MethodOverview: rawOverview,
			usage.MethodTools:    rawTools,
			usage.MethodInsights: rawInsights,
			usage.MethodSessions: rawSessions,
		},
		errs: map[string]error{},
	}
}

func (f *fakeService) SetConsent(_ context.Context, granted bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, callConsent)
	f.consent = append(f.consent, granted)
	return f.errs[callConsent]
}

func (f *fakeService) ScanOnce(ctx context.Context) error {
	f.mu.Lock()
	f.calls = append(f.calls, callScan)
	f.scans++
	pass, scan, err := f.scans, f.scan, f.errs[callScan]
	f.mu.Unlock()
	if scan != nil {
		return scan(ctx, pass)
	}
	return err
}

func (f *fakeService) Handle(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method)
	f.params[method] = append(f.params[method], string(params))
	if err := f.errs[method]; err != nil {
		return nil, err
	}
	if method != usage.MethodStatus {
		return json.RawMessage(f.responses[method]), nil
	}
	raw := f.statuses[0]
	if len(f.statuses) > 1 {
		f.statuses = f.statuses[1:]
	}
	return json.RawMessage(raw), nil
}

func (f *fakeService) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeService) scanCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scans
}

// harness wires run to a fake service and captures everything it does.
type harness struct {
	svc       *fakeService
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	opened    []openParams
	openErr   error
	closeErr  error
	closed    int
	tempBase  string
	onOpen    func(p openParams)
	onClose   func()
	ticks     <-chan time.Time
	intervals []time.Duration
	stops     int
	now       time.Time
	location  *time.Location
}

// Fixed clock of the tests: a mid-afternoon instant in a zone that is not
// UTC, so that day boundaries are visibly local.
func testClock() (time.Time, *time.Location) {
	loc := time.FixedZone("test", 9*60*60)
	return time.Date(2026, time.March, 10, 15, 30, 0, 0, loc), loc
}

func newHarness(t *testing.T, svc *fakeService) *harness {
	t.Helper()
	now, loc := testClock()
	return &harness{svc: svc, tempBase: t.TempDir(), now: now, location: loc}
}

func (h *harness) deps() deps {
	return deps{
		open: func(_ context.Context, p openParams) (usageService, func() error, error) {
			h.opened = append(h.opened, p)
			if h.onOpen != nil {
				h.onOpen(p)
			}
			if h.openErr != nil {
				return nil, nil, h.openErr
			}
			return h.svc, func() error {
				h.closed++
				if h.onClose != nil {
					h.onClose()
				}
				return h.closeErr
			}, nil
		},
		now:      func() time.Time { return h.now },
		location: h.location,
		ticks: func(interval time.Duration) (<-chan time.Time, func()) {
			h.intervals = append(h.intervals, interval)
			return h.ticks, func() { h.stops++ }
		},
		tempBase: h.tempBase,
		stdout:   &h.stdout,
		stderr:   &h.stderr,
	}
}

// testConfig is a valid configuration over a synthetic root.
func testConfig() config {
	return config{
		root:      filepath.Join(string(filepath.Separator), "synthetic", "projects"),
		rootLabel: filepath.Join(homeLabel, "projects"),
		days:      usage.DefaultWindowDays,
		sessions:  defaultSessions,
		timeout:   time.Minute,
	}
}

// entries lists the names left in dir.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	names := make([]string, 0, len(list))
	for _, entry := range list {
		names = append(names, entry.Name())
	}
	return names
}

// failingWriter refuses every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errInjected }
