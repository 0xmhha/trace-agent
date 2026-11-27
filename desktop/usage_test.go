package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
	"github.com/0xmhha/trace-agent/internal/usage/service"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	testHome       = "/home/tester"
	testRootLabel  = "~/.claude/projects"
	eventReceiver  = "receiver.close"
	eventScan      = "scan.exit"
	eventStore     = "store.close"
	testFreePort   = 41000
	testStoredPort = 40123
)

var errTest = errors.New("synthetic failure with /private/path")

// usageEvents records the order of shutdown steps across fakes.
type usageEvents struct {
	mu  sync.Mutex
	log []string
}

func (e *usageEvents) add(name string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.log = append(e.log, name)
}

func (e *usageEvents) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.log)
}

// fakeUsageStore answers only what the runtime itself asks of the store. The
// embedded interface is nil, so any other call fails the test loudly.
type fakeUsageStore struct {
	service.Store
	events      *usageEvents
	settings    usage.Settings
	settingsErr error
	setExtErr   error
	closeErr    error

	mu     sync.Mutex
	saved  []savedExtension
	closed int
}

// savedExtension is one SetExtension call.
type savedExtension struct {
	enabled bool
	port    int
}

func (s *fakeUsageStore) Settings(context.Context) (usage.Settings, error) {
	return s.settings, s.settingsErr
}

func (s *fakeUsageStore) SetExtension(_ context.Context, enabled bool, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = append(s.saved, savedExtension{enabled, port})
	return s.setExtErr
}

func (s *fakeUsageStore) Close() error {
	s.mu.Lock()
	s.closed++
	s.mu.Unlock()
	s.events.add(eventStore)
	return s.closeErr
}

type usageCall struct {
	method string
	params string
}

type fakeUsageService struct {
	events     *usageEvents
	status     usage.Status
	handleErr  map[string]error
	consentErr error
	runErr     error
	started    chan struct{}

	mu      sync.Mutex
	calls   []usageCall
	consent []bool
}

func newFakeUsageService() *fakeUsageService {
	return &fakeUsageService{
		status:    usage.Status{Root: testRootLabel, WindowDays: usage.DefaultWindowDays, Excluded: []string{}},
		handleErr: map[string]error{},
		started:   make(chan struct{}),
	}
}

func (s *fakeUsageService) Run(ctx context.Context) error {
	close(s.started)
	if s.runErr != nil {
		return s.runErr
	}
	<-ctx.Done()
	s.events.add(eventScan)
	return ctx.Err()
}

func (s *fakeUsageService) SetConsent(_ context.Context, granted bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consentErr != nil {
		return s.consentErr
	}
	s.consent = append(s.consent, granted)
	return nil
}

func (s *fakeUsageService) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("service call without a deadline")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, usageCall{method, string(params)})
	if err := s.handleErr[method]; err != nil {
		return nil, err
	}
	if method == usage.MethodStatus {
		return json.Marshal(s.status)
	}
	return json.Marshal(map[string]string{"handled": method})
}

// forwarded returns the calls other than status reads.
func (s *fakeUsageService) forwarded() []usageCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []usageCall
	for _, c := range s.calls {
		if c.method != usage.MethodStatus {
			out = append(out, c)
		}
	}
	return out
}

func (s *fakeUsageService) consents() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.consent)
}

type fakeUsageReceiver struct {
	port     int
	events   *usageEvents
	closeErr error

	mu     sync.Mutex
	closed int
}

func (r *fakeUsageReceiver) URL() string {
	return fmt.Sprintf("http://%s:%d/v1/events", usageLoopbackHost, r.port)
}
func (r *fakeUsageReceiver) Port() int { return r.port }
func (r *fakeUsageReceiver) Close(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("close without a deadline")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	r.closed++
	r.mu.Unlock()
	r.events.add(eventReceiver)
	return r.closeErr
}

type listenCall struct {
	port   int
	bearer string
}

// fakeUsageListener binds the requested port unless it is marked taken; port
// zero yields the next free port.
type fakeUsageListener struct {
	events *usageEvents
	taken  map[int]bool
	err    error

	mu        sync.Mutex
	nextFree  int
	calls     []listenCall
	receivers []*fakeUsageReceiver
}

func newFakeUsageListener() *fakeUsageListener {
	return &fakeUsageListener{taken: map[int]bool{}, nextFree: testFreePort}
}

func (l *fakeUsageListener) listen(_ usage.ExtSink, port int, bearer string) (usageReceiver, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, listenCall{port, bearer})
	if l.err != nil {
		return nil, l.err
	}
	if l.taken[port] {
		return nil, errors.New("address already in use")
	}
	if port == 0 {
		port = l.nextFree
		l.nextFree++
	}
	r := &fakeUsageReceiver{port: port, events: l.events}
	l.receivers = append(l.receivers, r)
	return r, nil
}

func (l *fakeUsageListener) ports() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]int, len(l.calls))
	for i, c := range l.calls {
		out[i] = c.port
	}
	return out
}

func testInstallationKey() []byte { return bytes.Repeat([]byte{7}, installationKeySize) }

// usageHarness assembles start-up dependencies out of fakes.
type usageHarness struct {
	events   *usageEvents
	store    *fakeUsageStore
	service  *fakeUsageService
	listener *fakeUsageListener
	config   service.Config
	deps     usageDeps
	openErr  error
	newErr   error
	paths    []string
}

func newUsageHarness() *usageHarness {
	events := &usageEvents{}
	h := &usageHarness{events: events, store: &fakeUsageStore{events: events}, service: newFakeUsageService(), listener: newFakeUsageListener()}
	h.service.events = events
	h.listener.events = events
	h.deps = usageDeps{
		openStore: func(_ context.Context, path string) (usageStore, error) {
			h.paths = append(h.paths, path)
			if h.openErr != nil {
				return nil, h.openErr
			}
			return h.store, nil
		},
		newService: func(cfg service.Config) (usageService, error) {
			h.config = cfg
			if h.newErr != nil {
				return nil, h.newErr
			}
			return h.service, nil
		},
		listen:          h.listener.listen,
		installationKey: func() ([]byte, error) { return testInstallationKey(), nil },
		getenv:          func(string) string { return "" },
		homeDir:         func() (string, error) { return testHome, nil },
		now:             func() time.Time { return time.Unix(1700000000, 0) },
		location:        time.UTC,
		dialog: func(context.Context, wruntime.MessageDialogOptions) (string, error) {
			return "", errors.New("unexpected dialog")
		},
		clipboard:      func(context.Context, string) error { return errors.New("unexpected clipboard") },
		saveCredential: func(string, string) error { return errors.New("unexpected credential") },
	}
	return h
}

func TestStartUsageWiresService(t *testing.T) {
	h := newUsageHarness()
	dir := t.TempDir()
	u := startUsage(t.Context(), dir, h.deps)
	if u.startErr != nil || u.receiverErr != nil {
		t.Fatal("start failed", u.startErr, u.receiverErr)
	}
	<-h.service.started
	if want := []string{filepath.Join(dir, usageDatabaseFile)}; !slices.Equal(h.paths, want) {
		t.Fatalf("store path = %v, want %v", h.paths, want)
	}
	cfg := h.config
	if cfg.Store != h.store || cfg.Root != filepath.Join(testHome, ".claude", "projects") || cfg.RootLabel != testRootLabel {
		t.Fatalf("config root = %q label = %q", cfg.Root, cfg.RootLabel)
	}
	if cfg.Scan == nil || cfg.Timeline == nil || cfg.Evaluate == nil || cfg.Now == nil || cfg.Location != time.UTC {
		t.Fatal("config misses a required function")
	}
	if cfg.Extension != service.ExtensionControl(u.extension) || u.extension == nil {
		t.Fatal("service does not share the runtime's extension control")
	}
	if len(h.listener.ports()) != 0 || cfg.Extension.Endpoint().Enabled {
		t.Fatal("endpoint started although it was not enabled")
	}
	var notice bytes.Buffer
	u.report(&notice)
	if notice.Len() != 0 {
		t.Fatalf("unexpected notice %q", notice.String())
	}
	if err := u.close(); err != nil {
		t.Fatal(err)
	}
}

func TestStartUsageIsNotFatal(t *testing.T) {
	for _, tc := range []struct {
		name        string
		arrange     func(*usageHarness)
		storeClosed int
	}{
		{"home directory unknown", func(h *usageHarness) {
			h.deps.homeDir = func() (string, error) { return "", errTest }
		}, 0},
		{"store does not open", func(h *usageHarness) { h.openErr = errTest }, 0},
		{"settings unreadable", func(h *usageHarness) { h.store.settingsErr = errTest }, 1},
		{"service rejects config", func(h *usageHarness) {
			h.newErr = errTest
			h.store.settings = usage.Settings{ExtEnabled: true, ExtPort: testStoredPort}
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newUsageHarness()
			tc.arrange(h)
			u := startUsage(t.Context(), t.TempDir(), h.deps)
			if u.startErr == nil {
				t.Fatal("start-up failure was lost")
			}
			if h.store.closed != tc.storeClosed {
				t.Fatalf("store closed %d times, want %d", h.store.closed, tc.storeClosed)
			}
			if len(h.listener.ports()) != 0 {
				t.Fatal("endpoint started without a service")
			}
			b := &Bridge{ctx: t.Context(), usage: u}
			if _, err := b.Usage(usage.MethodStatus, "{}"); !errors.Is(err, errUsageUnavailable) {
				t.Fatalf("Usage error = %v", err)
			}
			if _, err := b.EnableUsageWatch(); !errors.Is(err, errUsageUnavailable) {
				t.Fatalf("EnableUsageWatch error = %v", err)
			}
			if err := b.DisableUsageWatch(); !errors.Is(err, errUsageUnavailable) {
				t.Fatalf("DisableUsageWatch error = %v", err)
			}
			if err := b.CopyExtensionSettings(); !errors.Is(err, errUsageUnavailable) {
				t.Fatalf("CopyExtensionSettings error = %v", err)
			}
			var notice bytes.Buffer
			u.report(&notice)
			if notice.String() != usageStartNotice || strings.Contains(notice.String(), "private") {
				t.Fatalf("notice = %q", notice.String())
			}
			if err := u.close(); err != nil {
				t.Fatal("closing a runtime that never started failed", err)
			}
			if h.store.closed != tc.storeClosed {
				t.Fatal("close touched the store again")
			}
		})
	}
}

func TestBridgeWithoutUsageRuntime(t *testing.T) {
	b := &Bridge{ctx: t.Context()}
	if _, err := b.Usage(usage.MethodStatus, ""); !errors.Is(err, errUsageUnavailable) {
		t.Fatalf("Usage error = %v", err)
	}
	if err := b.usage.close(); err != nil {
		t.Fatal(err)
	}
}

func TestStartUsageRestoresExtension(t *testing.T) {
	for _, tc := range []struct {
		name        string
		settings    usage.Settings
		taken       []int
		listenErr   error
		setExtErr   error
		wantPorts   []int
		wantEnabled bool
		wantSaved   []savedExtension
		wantProblem bool
	}{
		{"disabled stays off", usage.Settings{ExtPort: testStoredPort}, nil, nil, nil, nil, false, nil, false},
		{"stored port reused", usage.Settings{ExtEnabled: true, ExtPort: testStoredPort}, nil, nil, nil, []int{testStoredPort}, true, nil, false},
		{"taken port replaced and recorded", usage.Settings{ExtEnabled: true, ExtPort: testStoredPort}, []int{testStoredPort}, nil, nil,
			[]int{testStoredPort, 0}, true, []savedExtension{{true, testFreePort}}, false},
		{"first port recorded", usage.Settings{ExtEnabled: true}, nil, nil, nil, []int{0}, true, []savedExtension{{true, testFreePort}}, false},
		{"listen failure keeps usage available", usage.Settings{ExtEnabled: true, ExtPort: testStoredPort}, nil, errTest, nil,
			[]int{testStoredPort, 0}, false, nil, true},
		{"unrecorded port is reported", usage.Settings{ExtEnabled: true}, nil, nil, errTest, []int{0}, true,
			[]savedExtension{{true, testFreePort}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newUsageHarness()
			h.store.settings = tc.settings
			h.store.setExtErr = tc.setExtErr
			h.listener.err = tc.listenErr
			for _, port := range tc.taken {
				h.listener.taken[port] = true
			}
			u := startUsage(t.Context(), t.TempDir(), h.deps)
			if u.startErr != nil {
				t.Fatal(u.startErr)
			}
			if got := h.listener.ports(); !slices.Equal(got, tc.wantPorts) {
				t.Fatalf("listen ports = %v, want %v", got, tc.wantPorts)
			}
			if got := u.extension.Endpoint().Enabled; got != tc.wantEnabled {
				t.Fatalf("endpoint enabled = %t, want %t", got, tc.wantEnabled)
			}
			if !slices.Equal(h.store.saved, tc.wantSaved) {
				t.Fatalf("saved = %v, want %v", h.store.saved, tc.wantSaved)
			}
			if (u.receiverErr != nil) != tc.wantProblem {
				t.Fatalf("receiver problem = %v, want problem %t", u.receiverErr, tc.wantProblem)
			}
			var notice bytes.Buffer
			u.report(&notice)
			if want := map[bool]string{true: usageReceiverNotice}[tc.wantProblem]; notice.String() != want {
				t.Fatalf("notice = %q, want %q", notice.String(), want)
			}
			b := &Bridge{ctx: t.Context(), usage: u}
			if _, err := b.Usage(usage.MethodStatus, "{}"); err != nil {
				t.Fatal("usage unavailable after an endpoint problem", err)
			}
			if err := u.close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestProductionUsageDepsStart runs the real wiring against an empty,
// synthetic home directory. Whether or not the pipeline comes up, the
// runtime must be consistent and must shut down cleanly.
func TestProductionUsageDepsStart(t *testing.T) {
	home := t.TempDir()
	deps := productionUsageDeps(&collector{vault: &countingVault{}})
	deps.getenv = func(string) string { return "" }
	deps.homeDir = func() (string, error) { return home, nil }
	u := startUsage(t.Context(), t.TempDir(), deps)
	b := &Bridge{ctx: t.Context(), usage: u}
	response, err := b.Usage(usage.MethodStatus, "{}")
	if u.startErr != nil {
		if u.service != nil || !errors.Is(err, errUsageUnavailable) {
			t.Fatalf("failed start left a usable service: %v", err)
		}
	} else {
		var status usage.Status
		if err != nil || json.Unmarshal([]byte(response), &status) != nil || status.Consent {
			t.Fatalf("status = %q, %v", response, err)
		}
	}
	if err := u.close(); err != nil {
		t.Fatal(err)
	}
}

func TestUsageShutdownOrder(t *testing.T) {
	h := newUsageHarness()
	h.store.settings = usage.Settings{ExtEnabled: true, ExtPort: testStoredPort}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	u := startUsage(ctx, t.TempDir(), h.deps)
	if u.startErr != nil || u.receiverErr != nil {
		t.Fatal(u.startErr, u.receiverErr)
	}
	<-h.service.started
	if err := u.close(); err != nil {
		t.Fatal(err)
	}
	if got, want := h.events.snapshot(), []string{eventReceiver, eventScan, eventStore}; !slices.Equal(got, want) {
		t.Fatalf("shutdown order = %v, want %v", got, want)
	}
	if _, err := u.extension.SetEnabled(t.Context(), true); !errors.Is(err, errExtensionShutDown) {
		t.Fatalf("endpoint restarted after shutdown: %v", err)
	}
	if err := u.close(); err != nil {
		t.Fatal(err)
	}
	if got := h.events.snapshot(); len(got) != 3 {
		t.Fatalf("second close repeated steps: %v", got)
	}
}

func TestUsageCloseReportsFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		runErr   error
		closeErr error
		want     []error
	}{
		{"scan stopped early", errTest, nil, []error{errTest}},
		{"store close failed", nil, errTest, []error{errTest}},
		{"clean stop", nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newUsageHarness()
			h.service.runErr = tc.runErr
			h.store.closeErr = tc.closeErr
			u := startUsage(t.Context(), t.TempDir(), h.deps)
			if u.startErr != nil {
				t.Fatal(u.startErr)
			}
			err := u.close()
			if (err != nil) != (len(tc.want) != 0) {
				t.Fatalf("close error = %v", err)
			}
			for _, want := range tc.want {
				if !errors.Is(err, want) {
					t.Fatalf("close error %v does not wrap %v", err, want)
				}
			}
			if h.store.closed != 1 {
				t.Fatalf("store closed %d times", h.store.closed)
			}
		})
	}
}

func TestUsageRoot(t *testing.T) {
	relative, err := filepath.Abs(filepath.Join("config", claudeProjectsDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		env       string
		home      string
		homeErr   error
		wantRoot  string
		wantLabel string
		wantErr   bool
	}{
		{"default root", "", testHome, nil, "/home/tester/.claude/projects", "~/.claude/projects", false},
		{"home with trailing separator", "", testHome + "/", nil, "/home/tester/.claude/projects", "~/.claude/projects", false},
		{"configured under home", "/home/tester/alt", testHome, nil, "/home/tester/alt/projects", "~/alt/projects", false},
		{"configured elsewhere", "/srv/claude", testHome, nil, "/srv/claude/projects", "/srv/claude/projects", false},
		{"sibling of home keeps its path", "/home/tester2/cfg", testHome, nil, "/home/tester2/cfg/projects", "/home/tester2/cfg/projects", false},
		{"relative configuration is made absolute", "config", testHome, nil, relative, relative, false},
		{"configured without a home", "/srv/claude", "", errTest, "/srv/claude/projects", "/srv/claude/projects", false},
		{"relative home is not trusted", "/srv/claude", "tester", nil, "/srv/claude/projects", "/srv/claude/projects", false},
		{"no home and no configuration", "", "", errTest, "", "", true},
		{"relative home and no configuration", "", "tester", nil, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string {
				if key != claudeConfigDirEnv {
					t.Errorf("unexpected variable %q", key)
				}
				return tc.env
			}
			root, label, err := usageRoot(getenv, func() (string, error) { return tc.home, tc.homeErr })
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error %t", err, tc.wantErr)
			}
			if root != tc.wantRoot || label != tc.wantLabel {
				t.Fatalf("root = %q label = %q, want %q %q", root, label, tc.wantRoot, tc.wantLabel)
			}
		})
	}
}

func TestHomeRelative(t *testing.T) {
	for _, tc := range []struct{ path, home, want string }{
		{testHome, testHome, "~"},
		{testHome + "/a/b", testHome, "~/a/b"},
		{"/home/testerx/a", testHome, "/home/testerx/a"},
		{"/a", "/", "~/a"},
		{"/var/data", testHome, "/var/data"},
	} {
		if got := homeRelative(tc.path, tc.home); got != tc.want {
			t.Errorf("homeRelative(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
		}
	}
}
