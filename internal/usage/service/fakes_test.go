package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Synthetic identifiers shared by the tests. The session and agent IDs are
// the ones of internal/usage/testdata.
const (
	testSession  = "11111111-1111-4111-8111-111111111111"
	testAgent    = "a1b2c3"
	testProject  = "alpha"
	otherProject = "beta"
	testLabel    = "~/transcripts"
	fixtureDir   = "../testdata"
	rootFixture  = "session.jsonl"
	agentFixture = "agent-a1b2c3.jsonl"
	rootRel      = testProject + "/" + testSession + ".jsonl"
	agentRel     = testProject + "/" + testSession + "/subagents/agent-" + testAgent + ".jsonl"
)

// testNow is the fixed clock of the package tests.
var testNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

var errInjected = errors.New("injected failure")

// fakeClock is a clock the test moves by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type appliedBatch struct {
	file  usage.FileRef
	batch usage.Batch
}

type pruneCall struct{ usageBefore, extBefore time.Time }

type extensionSetting struct {
	enabled bool
	port    int
}

type streamKey struct{ sessionID, agentID string }

// fakeStore is an in-memory Store. It keeps the stale-batch rules of the real
// one, records every call and returns canned aggregates whose lists are nil,
// the way a careless store would.
type fakeStore struct {
	mu       sync.Mutex
	settings usage.Settings
	version  int64
	cursors  map[string]usage.FileCursor
	// log names the calls that change the store, in order.
	log           []string
	applied       []appliedBatch
	marked        []usage.FileRef
	cursorLookups []string
	pruned        []pruneCall
	extensionSets []extensionSetting
	settingsCalls int

	// beforeApply runs with the lock held and may change the fake's fields.
	// A non-nil error is returned from Apply as is.
	beforeApply func(f *fakeStore, file usage.FileRef, batch usage.Batch) error
	// fail maps a method name to the error it returns.
	fail map[string]error
	// settingsSeen, when set, receives one value per Settings call that
	// finds room in the buffer.
	settingsSeen chan struct{}

	coverage        usage.Coverage
	coverageQueries []usage.Query
	overview        usage.Overview
	overviewQueries []usage.Query
	overviewLoc     *time.Location
	tools           usage.Tools
	toolQueries     []usage.Query
	projects        usage.Projects
	projectQueries  []usage.Query
	sessionPage     usage.SessionPage
	sessionQueries  []usage.SessionQuery
	details         map[string]usage.SessionDetail
	streams         map[streamKey]string
	extSources      []usage.ExtSourceRow
	extRejected     int64
	extSourceQuery  []usage.Query
	events          []usage.ExtEvent
	extQueries      []usage.ExtQuery
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		settings: usage.Settings{Consent: true, WindowDays: usage.DefaultWindowDays},
		cursors:  make(map[string]usage.FileCursor),
		fail:     make(map[string]error),
		details:  make(map[string]usage.SessionDetail),
		streams:  make(map[streamKey]string),
		coverage: usage.Coverage{State: usage.CoverageComplete},
	}
}

// with runs fn with the lock held, for tests that read or change the fake.
func (f *fakeStore) with(fn func(f *fakeStore)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeStore) AppendExt(context.Context, []usage.ExtEvent) error { return nil }

func (f *fakeStore) CountRejected(context.Context, string, int) error { return nil }

func (f *fakeStore) Settings(context.Context) (usage.Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settingsCalls++
	if f.settingsSeen != nil {
		select {
		case f.settingsSeen <- struct{}{}:
		default:
		}
	}
	settings := f.settings
	settings.Excluded = slices.Clone(f.settings.Excluded)
	return settings, f.fail["Settings"]
}

func (f *fakeStore) SetConsent(_ context.Context, granted bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["SetConsent"]; err != nil {
		return err
	}
	f.log = append(f.log, "SetConsent:"+strconv.FormatBool(granted))
	f.settings.Consent = granted
	return nil
}

func (f *fakeStore) SetWindowDays(_ context.Context, days int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["SetWindowDays"]; err != nil {
		return err
	}
	f.log = append(f.log, "SetWindowDays:"+strconv.Itoa(days))
	f.settings.WindowDays = days
	return nil
}

func (f *fakeStore) SetExtension(_ context.Context, enabled bool, port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["SetExtension"]; err != nil {
		return err
	}
	f.log = append(f.log, "SetExtension")
	f.extensionSets = append(f.extensionSets, extensionSetting{enabled, port})
	f.settings.ExtEnabled, f.settings.ExtPort = enabled, port
	return nil
}

// SetExcluded drops the project's cursors on exclusion, like the real store.
func (f *fakeStore) SetExcluded(_ context.Context, project string, excluded bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["SetExcluded"]; err != nil {
		return err
	}
	f.log = append(f.log, "SetExcluded:"+project+":"+strconv.FormatBool(excluded))
	f.settings.Excluded = slices.DeleteFunc(f.settings.Excluded, func(p string) bool { return p == project })
	if excluded {
		f.settings.Excluded = append(f.settings.Excluded, project)
		for name := range f.cursors {
			if strings.HasPrefix(name, project+"/") {
				delete(f.cursors, name)
			}
		}
	}
	return nil
}

func (f *fakeStore) DataVersion(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version, f.fail["DataVersion"]
}

func (f *fakeStore) Cursor(_ context.Context, name string) (usage.FileCursor, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursorLookups = append(f.cursorLookups, name)
	if err := f.fail["Cursor:"+name]; err != nil {
		return usage.FileCursor{}, false, err
	}
	cursor, ok := f.cursors[name]
	return cursor, ok, nil
}

func (f *fakeStore) Apply(_ context.Context, file usage.FileRef, batch usage.Batch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.beforeApply != nil {
		if err := f.beforeApply(f, file, batch); err != nil {
			return err
		}
	}
	if !f.settings.Consent || slices.Contains(f.settings.Excluded, file.ProjectKey) {
		return usage.ErrStale
	}
	if batch.BaseOffset != 0 && batch.BaseOffset != f.cursors[file.Path].Offset {
		return usage.ErrStale
	}
	f.cursors[file.Path] = usage.FileCursor{Offset: batch.NextOffset, Size: file.Size, ModTime: file.ModTime}
	f.applied = append(f.applied, appliedBatch{file, batch})
	f.version++
	return nil
}

func (f *fakeStore) MarkUnreadable(_ context.Context, file usage.FileRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["MarkUnreadable"]; err != nil {
		return err
	}
	f.marked = append(f.marked, file)
	return nil
}

func (f *fakeStore) Clear(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["Clear"]; err != nil {
		return err
	}
	f.log = append(f.log, "Clear")
	clear(f.cursors)
	return nil
}

func (f *fakeStore) ResetCursors(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["ResetCursors"]; err != nil {
		return err
	}
	f.log = append(f.log, "ResetCursors")
	for name, cursor := range f.cursors {
		cursor.Offset = 0
		f.cursors[name] = cursor
	}
	return nil
}

func (f *fakeStore) Prune(_ context.Context, usageBefore, extBefore time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruned = append(f.pruned, pruneCall{usageBefore, extBefore})
	return f.fail["Prune"]
}

func (f *fakeStore) Coverage(_ context.Context, q usage.Query) (usage.Coverage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.coverageQueries = append(f.coverageQueries, q)
	return f.coverage, f.fail["Coverage"]
}

func (f *fakeStore) Overview(_ context.Context, q usage.Query, loc *time.Location) (usage.Overview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overviewQueries = append(f.overviewQueries, q)
	f.overviewLoc = loc
	return f.overview, f.fail["Overview"]
}

func (f *fakeStore) Tools(_ context.Context, q usage.Query) (usage.Tools, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolQueries = append(f.toolQueries, q)
	return f.tools, f.fail["Tools"]
}

func (f *fakeStore) Projects(_ context.Context, q usage.Query) (usage.Projects, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projectQueries = append(f.projectQueries, q)
	return f.projects, f.fail["Projects"]
}

func (f *fakeStore) Sessions(_ context.Context, q usage.SessionQuery) (usage.SessionPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionQueries = append(f.sessionQueries, q)
	return f.sessionPage, f.fail["Sessions"]
}

func (f *fakeStore) Session(_ context.Context, sessionID string) (usage.SessionDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["Session"]; err != nil {
		return usage.SessionDetail{}, err
	}
	detail, ok := f.details[sessionID]
	if !ok {
		return usage.SessionDetail{}, usage.ErrNotFound
	}
	return detail, nil
}

func (f *fakeStore) StreamFile(_ context.Context, sessionID, agentID string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name, ok := f.streams[streamKey{sessionID, agentID}]
	return name, ok, f.fail["StreamFile"]
}

func (f *fakeStore) ExtSources(_ context.Context, q usage.Query) ([]usage.ExtSourceRow, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.extSourceQuery = append(f.extSourceQuery, q)
	return f.extSources, f.extRejected, f.fail["ExtSources"]
}

// ExtEvents pages through the events of the query's session. The cursor is
// the decimal index of the next event.
func (f *fakeStore) ExtEvents(_ context.Context, q usage.ExtQuery) (usage.ExtPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.extQueries = append(f.extQueries, q)
	if err := f.fail["ExtEvents"]; err != nil {
		return usage.ExtPage{}, err
	}
	var matching []usage.ExtEvent
	for _, event := range f.events {
		if q.SessionID == "" || event.SessionID == q.SessionID {
			matching = append(matching, event)
		}
	}
	start := 0
	if q.Cursor != "" {
		start, _ = strconv.Atoi(q.Cursor)
	}
	start = min(start, len(matching))
	end := min(start+q.Limit, len(matching))
	page := usage.ExtPage{Items: matching[start:end], Total: len(matching)}
	if end < len(matching) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

// lineScan is a ScanFunc that honours the scanner contract without parsing:
// it consumes complete lines until EOF or the first line boundary at or
// after MaxBytes, and leaves a trailing partial line unread.
func lineScan(r io.Reader, in usage.ScanInput) (usage.Batch, error) {
	batch := usage.Batch{
		BaseOffset: in.Base,
		NextOffset: in.Base,
		Session:    usage.SessionPatch{SessionID: in.SessionID, AgentID: in.AgentID},
	}
	reader := bufio.NewReader(r)
	var read int64
	for in.MaxBytes <= 0 || read < in.MaxBytes {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return batch, err
		}
		read += int64(len(line))
		batch.Lines++
	}
	batch.NextOffset = in.Base + read
	return batch, nil
}

// scanLog is a scripted ScanFunc: it records every input and lets a test
// step in before the lines are read.
type scanLog struct {
	mu     sync.Mutex
	inputs []usage.ScanInput
	// hook runs without the lock; n is the zero-based index of the call. A
	// non-nil error is returned as a read failure.
	hook func(n int, in usage.ScanInput) error
	// result, when set, replaces the batch lineScan produced.
	result func(batch usage.Batch) usage.Batch
}

func (l *scanLog) scan(r io.Reader, in usage.ScanInput) (usage.Batch, error) {
	l.mu.Lock()
	n := len(l.inputs)
	l.inputs = append(l.inputs, in)
	hook, result := l.hook, l.result
	l.mu.Unlock()
	if hook != nil {
		if err := hook(n, in); err != nil {
			return usage.Batch{BaseOffset: in.Base, NextOffset: in.Base}, err
		}
	}
	batch, err := lineScan(r, in)
	if err == nil && result != nil {
		batch = result(batch)
	}
	return batch, err
}

func (l *scanLog) calls() []usage.ScanInput {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.inputs)
}

func (l *scanLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inputs = nil
}

func (l *scanLog) setHook(hook func(n int, in usage.ScanInput) error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hook = hook
}

// timelineLog is a scripted TimelineFunc.
type timelineLog struct {
	mu       sync.Mutex
	requests []usage.TimelineRequest
	// sizes holds the number of bytes of the transcript handed to each call.
	sizes []int64
	page  usage.TimelinePage
	err   error
	// hook runs without the lock before the page is returned.
	hook func()
}

func (l *timelineLog) read(r io.ReadSeeker, request usage.TimelineRequest) (usage.TimelinePage, error) {
	// Reading proves that the service handed over an open transcript.
	size, err := io.Copy(io.Discard, r)
	if err != nil {
		return usage.TimelinePage{}, err
	}
	l.mu.Lock()
	l.requests = append(l.requests, request)
	l.sizes = append(l.sizes, size)
	page, scripted, hook := l.page, l.err, l.hook
	l.mu.Unlock()
	if hook != nil {
		hook()
	}
	page.Items = slices.Clone(page.Items)
	return page, scripted
}

// raise lifts peak to v when v is higher.
func raise(peak *atomic.Int32, v int32) {
	for {
		current := peak.Load()
		if v <= current || peak.CompareAndSwap(current, v) {
			return
		}
	}
}

// evaluateLog is a scripted EvaluateFunc.
type evaluateLog struct {
	mu     sync.Mutex
	inputs []usage.InsightInput
	at     []time.Time
}

func (l *evaluateLog) evaluate(in usage.InsightInput, now time.Time) usage.InsightReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inputs = append(l.inputs, in)
	l.at = append(l.at, now)
	return usage.InsightReport{GeneratedAt: now, RulesPassed: 1}
}

// fakeExtension is a scripted ExtensionControl.
type fakeExtension struct {
	mu       sync.Mutex
	endpoint usage.ExtEndpoint
	port     int
	err      error
	calls    []bool
}

func (e *fakeExtension) SetEnabled(_ context.Context, enabled bool) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, enabled)
	if e.err != nil {
		return 0, e.err
	}
	e.endpoint.Enabled = enabled
	return e.port, nil
}

func (e *fakeExtension) Endpoint() usage.ExtEndpoint {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.endpoint
}

// harness is a service wired to fakes over a temporary transcript root.
type harness struct {
	t        *testing.T
	root     string
	store    *fakeStore
	scans    *scanLog
	timeline *timelineLog
	insights *evaluateLog
	clock    *fakeClock
	svc      *Service
}

func newHarness(t *testing.T, options ...func(*Config)) *harness {
	t.Helper()
	h := &harness{
		t:        t,
		root:     filepath.Join(t.TempDir(), "projects"),
		store:    newFakeStore(),
		scans:    &scanLog{},
		timeline: &timelineLog{},
		insights: &evaluateLog{},
		clock:    &fakeClock{now: testNow},
	}
	if err := os.Mkdir(h.root, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Store:     h.store,
		Root:      h.root,
		RootLabel: testLabel,
		Scan:      h.scans.scan,
		Timeline:  h.timeline.read,
		Evaluate:  h.insights.evaluate,
		Now:       h.clock.Now,
	}
	for _, option := range options {
		option(&cfg)
	}
	svc, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	return h
}

// write creates a transcript of n synthetic lines under the root and gives it
// a modification time inside the default window. It returns the content.
func (h *harness) write(rel string, lines int) []byte {
	h.t.Helper()
	content := []byte(strings.Repeat(`{"type":"sample","note":"synthetic line"}`+"\n", lines))
	h.put(rel, content)
	return content
}

func (h *harness) put(rel string, content []byte) {
	h.t.Helper()
	name := filepath.Join(h.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(name, content, 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.touch(rel, testNow.Add(-time.Hour))
}

func (h *harness) touch(rel string, at time.Time) {
	h.t.Helper()
	if err := os.Chtimes(filepath.Join(h.root, filepath.FromSlash(rel)), at, at); err != nil {
		h.t.Fatal(err)
	}
}

// fixtures lays out the shared synthetic transcripts the way the client
// does: a root transcript and one sub-agent transcript of the same session.
func (h *harness) fixtures() (rootSize, agentSize int64) {
	h.t.Helper()
	for rel, fixture := range map[string]string{rootRel: rootFixture, agentRel: agentFixture} {
		content, err := os.ReadFile(filepath.Join(fixtureDir, fixture))
		if err != nil {
			h.t.Fatal(err)
		}
		h.put(rel, content)
		if rel == rootRel {
			rootSize = int64(len(content))
		} else {
			agentSize = int64(len(content))
		}
	}
	return rootSize, agentSize
}

func (h *harness) scanOnce() {
	h.t.Helper()
	if err := h.svc.ScanOnce(context.Background()); err != nil {
		h.t.Fatalf("ScanOnce: %v", err)
	}
}

func (h *harness) status() usage.Status {
	h.t.Helper()
	return call[usage.Status](h.t, h.svc, usage.MethodStatus, "")
}

// scanned lists the root-relative streams of the recorded scan calls as
// "session/agent@base".
func (h *harness) scanned() []string {
	var out []string
	for _, in := range h.scans.calls() {
		out = append(out, in.SessionID+"/"+in.AgentID+"@"+strconv.FormatInt(in.Base, 10))
	}
	return out
}
