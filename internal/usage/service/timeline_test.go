package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

const (
	sessionParams = `{"sessionId":"` + testSession + `"}`
	agentParams   = `{"sessionId":"` + testSession + `","agentId":"` + testAgent + `"}`
)

// pageStart is the time of the first item of the synthetic timeline page.
var pageStart = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

func at(seconds int) time.Time { return pageStart.Add(time.Duration(seconds) * time.Second) }

// samplePage is a page with two tool calls and one item without a time.
func samplePage() []usage.TimelineItem {
	tool := func(id, name string) *usage.TimelineTool {
		return &usage.TimelineTool{UseID: id, Name: name, Category: usage.ToolBuiltin}
	}
	return []usage.TimelineItem{
		{ID: "i0", At: at(0), Kind: usage.ItemPrompt},
		{ID: "i1", At: at(10), Kind: usage.ItemToolCall, Tool: tool("tu1", "Bash")},
		{ID: "i2", At: at(20), Kind: usage.ItemToolResult, Tool: tool("tu1", "Bash")},
		{ID: "i3", At: at(30), Kind: usage.ItemAssistant},
		{ID: "i4", At: at(40), Kind: usage.ItemToolCall, Tool: tool("tu2", "Read")},
		{ID: "i5", Kind: usage.ItemSystem},
		{ID: "i6", At: at(50), Kind: usage.ItemToolResult, Tool: tool("tu2", "Read")},
	}
}

func ids(items []usage.TimelineItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

func TestMergeExt(t *testing.T) {
	event := func(id, toolUseID string, when time.Time) usage.ExtEvent {
		return usage.ExtEvent{ID: id, Source: "deploy-script", SessionID: testSession, ToolUseID: toolUseID, At: when}
	}
	for _, tc := range []struct {
		name   string
		items  []usage.TimelineItem
		events []usage.ExtEvent
		want   []string
	}{
		{
			name:   "tool-use event follows its call",
			items:  samplePage(),
			events: []usage.ExtEvent{event("a", "tu1", at(15))},
			want:   []string{"i0", "i1", "ext:a", "i2", "i3", "i4", "i5", "i6"},
		},
		{
			name:   "tool-use events keep time order whatever their arrival order",
			items:  samplePage(),
			events: []usage.ExtEvent{event("late", "tu1", at(15)), event("early", "tu1", at(12))},
			want:   []string{"i0", "i1", "ext:early", "ext:late", "i2", "i3", "i4", "i5", "i6"},
		},
		{
			name:   "tool-use event is placed by its call even when its time is elsewhere",
			items:  samplePage(),
			events: []usage.ExtEvent{event("j", "tu2", at(-3600))},
			want:   []string{"i0", "i1", "i2", "i3", "i4", "ext:j", "i5", "i6"},
		},
		{
			name:   "tool-use event without its call on the page is left out",
			items:  samplePage(),
			events: []usage.ExtEvent{event("g", "tu-elsewhere", at(30))},
			want:   []string{"i0", "i1", "i2", "i3", "i4", "i5", "i6"},
		},
		{
			name:   "a result item does not attract events",
			items:  samplePage()[2:4],
			events: []usage.ExtEvent{event("a", "tu1", at(25))},
			want:   []string{"i2", "i3"},
		},
		{
			name:   "timed event goes before the first later item",
			items:  samplePage(),
			events: []usage.ExtEvent{event("c", "", at(25))},
			want:   []string{"i0", "i1", "i2", "ext:c", "i3", "i4", "i5", "i6"},
		},
		{
			name:   "items without a time are not compared",
			items:  samplePage(),
			events: []usage.ExtEvent{event("k", "", at(45))},
			want:   []string{"i0", "i1", "i2", "i3", "i4", "i5", "ext:k", "i6"},
		},
		{
			name:   "event at the first instant of the page",
			items:  samplePage(),
			events: []usage.ExtEvent{event("h", "", at(0))},
			want:   []string{"i0", "ext:h", "i1", "i2", "i3", "i4", "i5", "i6"},
		},
		{
			name:   "event at the last instant ends the page",
			items:  samplePage(),
			events: []usage.ExtEvent{event("d", "", at(50))},
			want:   []string{"i0", "i1", "i2", "i3", "i4", "i5", "i6", "ext:d"},
		},
		{
			name:   "events outside the time span are left out",
			items:  samplePage(),
			events: []usage.ExtEvent{event("before", "", at(-1)), event("after", "", at(51)), event("timeless", "", time.Time{})},
			want:   []string{"i0", "i1", "i2", "i3", "i4", "i5", "i6"},
		},
		{
			name:  "everything together",
			items: samplePage(),
			events: []usage.ExtEvent{
				event("d", "", at(50)), event("k", "", at(45)), event("j", "tu2", at(41)), event("c2", "", at(25)),
				event("c1", "", at(25)), event("a", "tu1", at(15)), event("b", "tu1", at(12)), event("h", "", at(0)),
			},
			want: []string{"i0", "ext:h", "i1", "ext:b", "ext:a", "i2", "ext:c1", "ext:c2", "i3", "i4", "ext:j", "i5", "ext:k", "i6", "ext:d"},
		},
		{
			name:   "a page without times takes only tool-use events",
			items:  []usage.TimelineItem{{ID: "n0", Kind: usage.ItemSystem}, {ID: "n1", Kind: usage.ItemToolCall, Tool: &usage.TimelineTool{UseID: "tu1"}}},
			events: []usage.ExtEvent{event("timed", "", at(5)), event("linked", "tu1", at(5))},
			want:   []string{"n0", "n1", "ext:linked"},
		},
		{name: "empty page", events: []usage.ExtEvent{event("a", "", at(5))}, want: []string{}},
		{name: "no events", items: samplePage()[:2], want: []string{"i0", "i1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merged := mergeExt(tc.items, tc.events)
			if got := ids(merged); !slices.Equal(got, tc.want) {
				t.Fatalf("merged = %v\nwant %v", got, tc.want)
			}
			for _, item := range merged {
				if item.Kind != usage.ItemExt {
					continue
				}
				if item.Ext == nil || extItemPrefix+item.Ext.ID != item.ID || !item.At.Equal(item.Ext.At) {
					t.Errorf("ext item %+v does not carry its own event", item)
				}
			}
		})
	}
}

func TestHandleTimelineInsertsExtensionEvents(t *testing.T) {
	h := settled(t)
	h.timeline.page = usage.TimelinePage{Items: samplePage(), NextCursor: "next", Skipped: 2}
	h.store.with(func(f *fakeStore) {
		f.events = []usage.ExtEvent{
			{ID: "other", SessionID: "another-session", At: at(25)},
			{ID: "outside", SessionID: testSession, At: at(500)},
			{ID: "timed", SessionID: testSession, At: at(25)},
			{ID: "linked", SessionID: testSession, ToolUseID: "tu1", At: at(11)},
		}
	})
	page := call[usage.TimelinePage](t, h.svc, usage.MethodTimeline, `{"sessionId":"`+testSession+`","cursor":"c0","limit":25}`)
	if page.Source != usage.SourceAvailable || page.NextCursor != "next" || page.Skipped != 2 {
		t.Fatalf("page = %+v; want the reader's page marked available", page)
	}
	want := []string{"i0", "i1", "ext:linked", "i2", "ext:timed", "i3", "i4", "i5", "i6"}
	if got := ids(page.Items); !slices.Equal(got, want) {
		t.Fatalf("items = %v\nwant %v", got, want)
	}
	if linked := page.Items[2]; linked.Kind != usage.ItemExt || linked.Ext == nil || linked.Ext.ToolUseID != "tu1" {
		t.Fatalf("inserted item = %+v", linked)
	}
	if got := h.timeline.requests; len(got) != 1 || got[0] != (usage.TimelineRequest{SessionID: testSession, Cursor: "c0", Limit: 25}) {
		t.Fatalf("reader requests = %+v", got)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.extQueries) != 1 {
			t.Fatalf("extension queries = %+v", f.extQueries)
		}
		q := f.extQueries[0]
		if q.SessionID != testSession || q.Limit != usage.MaxPageSize || !q.Range.From.IsZero() || !q.Range.Until.IsZero() {
			t.Fatalf("extension query = %+v; want all events of the session", q)
		}
	})
}

func TestHandleTimelineReadsTheRequestedStream(t *testing.T) {
	h := settled(t)
	rootSize, agentSize := sizeOf(t, h, rootRel), sizeOf(t, h, agentRel)
	for _, tc := range []struct {
		params   string
		want     usage.TimelineRequest
		wantSize int64
	}{
		{sessionParams, usage.TimelineRequest{SessionID: testSession, Limit: usage.DefaultPageSize}, rootSize},
		{agentParams, usage.TimelineRequest{SessionID: testSession, AgentID: testAgent, Limit: usage.DefaultPageSize}, agentSize},
		{
			`{"sessionId":"` + testSession + `","agentId":"` + testAgent + `","limit":100000,"cursor":"c9"}`,
			usage.TimelineRequest{SessionID: testSession, AgentID: testAgent, Limit: usage.MaxPageSize, Cursor: "c9"},
			agentSize,
		},
	} {
		h.timeline.requests, h.timeline.sizes = nil, nil
		page := call[usage.TimelinePage](t, h.svc, usage.MethodTimeline, tc.params)
		if page.Source != usage.SourceAvailable || len(page.Items) != 0 {
			t.Fatalf("page = %+v; want an empty available page", page)
		}
		if got := h.timeline.requests; len(got) != 1 || got[0] != tc.want {
			t.Fatalf("reader requests for %s = %+v\nwant %+v", tc.params, got, tc.want)
		}
		if got := h.timeline.sizes[0]; got != tc.wantSize {
			t.Fatalf("the reader was handed a file of %d bytes for %s; want %d", got, tc.params, tc.wantSize)
		}
	}
}

func TestHandleSessionAndTimelineErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		method  string
		params  string
		prepare func(f *fakeStore)
		want    error
	}{
		{"session without consent", usage.MethodSession, sessionParams, func(f *fakeStore) { f.settings.Consent = false }, usage.ErrNoConsent},
		{"timeline without consent", usage.MethodTimeline, sessionParams, func(f *fakeStore) { f.settings.Consent = false }, usage.ErrNoConsent},
		{"unknown session", usage.MethodSession, `{"sessionId":"nobody"}`, nil, usage.ErrNotFound},
		{"timeline of an unknown session", usage.MethodTimeline, `{"sessionId":"nobody"}`, nil, usage.ErrNotFound},
		{"timeline of an unknown agent", usage.MethodTimeline, `{"sessionId":"` + testSession + `","agentId":"nobody"}`, nil, usage.ErrNotFound},
		{"session store failure", usage.MethodSession, sessionParams, func(f *fakeStore) { f.fail["Session"] = errInjected }, errInjected},
		{"session stream lookup failure", usage.MethodSession, sessionParams, func(f *fakeStore) { f.fail["StreamFile"] = errInjected }, errInjected},
		{"session settings failure", usage.MethodSession, sessionParams, func(f *fakeStore) { f.fail["Settings"] = errInjected }, errInjected},
		{"timeline stream lookup failure", usage.MethodTimeline, sessionParams, func(f *fakeStore) { f.fail["StreamFile"] = errInjected }, errInjected},
		{"timeline extension lookup failure", usage.MethodTimeline, sessionParams, func(f *fakeStore) { f.fail["ExtEvents"] = errInjected }, errInjected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := settled(t)
			h.timeline.page = usage.TimelinePage{Items: samplePage()}
			if tc.prepare != nil {
				h.store.with(tc.prepare)
			}
			raw, err := h.svc.Handle(context.Background(), tc.method, json.RawMessage(tc.params))
			if !errors.Is(err, tc.want) || raw != nil {
				t.Fatalf("Handle = %s, %v; want %v", raw, err, tc.want)
			}
			if errors.Is(tc.want, usage.ErrNoConsent) && len(h.timeline.requests) != 0 {
				t.Fatal("a transcript was read without consent")
			}
		})
	}
}

func TestHandleReportsTranscriptSource(t *testing.T) {
	transcript := func(h *harness) string { return filepath.Join(h.root, filepath.FromSlash(rootRel)) }
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, h *harness)
		want    usage.SourceState
	}{
		{"available", func(*testing.T, *harness) {}, usage.SourceAvailable},
		{"missing after the file was deleted", func(t *testing.T, h *harness) {
			if err := os.Remove(transcript(h)); err != nil {
				t.Fatal(err)
			}
		}, usage.SourceMissing},
		{"missing with the whole root gone", func(t *testing.T, h *harness) {
			if err := os.RemoveAll(h.root); err != nil {
				t.Fatal(err)
			}
		}, usage.SourceMissing},
		{"unreadable without permission", func(t *testing.T, h *harness) {
			if os.Geteuid() == 0 {
				t.Skip("permission bits do not stop the superuser")
			}
			if err := os.Chmod(transcript(h), 0); err != nil {
				t.Fatal(err)
			}
		}, usage.SourceUnreadable},
		{"unreadable when replaced by a symbolic link", func(t *testing.T, h *harness) {
			target := filepath.Join(h.root, "kept.jsonl")
			if err := os.Rename(transcript(h), target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, transcript(h)); err != nil {
				t.Fatal(err)
			}
		}, usage.SourceUnreadable},
		{"unreadable when replaced by a directory", func(t *testing.T, h *harness) {
			if err := os.Remove(transcript(h)); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(transcript(h), 0o700); err != nil {
				t.Fatal(err)
			}
		}, usage.SourceUnreadable},
		{"unreadable when replaced by a fifo", func(t *testing.T, h *harness) {
			if err := os.Remove(transcript(h)); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(transcript(h), 0o600); err != nil {
				t.Fatalf("create fifo: %v", err)
			}
		}, usage.SourceUnreadable},
		{"unreadable when the stored path leaves the root", func(t *testing.T, h *harness) {
			outside := filepath.Join(filepath.Dir(h.root), "outside.jsonl")
			if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			h.store.with(func(f *fakeStore) { f.streams[streamKey{testSession, ""}] = "../outside.jsonl" })
		}, usage.SourceUnreadable},
		{"unreadable when the stored path is absolute", func(_ *testing.T, h *harness) {
			h.store.with(func(f *fakeStore) { f.streams[streamKey{testSession, ""}] = transcript(h) })
		}, usage.SourceUnreadable},
		{"unreadable when the stored path is not a transcript", func(_ *testing.T, h *harness) {
			h.write(testProject+"/notes.txt", 1)
			h.store.with(func(f *fakeStore) { f.streams[streamKey{testSession, ""}] = testProject + "/notes.txt" })
		}, usage.SourceUnreadable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := settled(t)
			h.timeline.page = usage.TimelinePage{Items: samplePage()}
			tc.prepare(t, h)

			if detail := call[usage.SessionDetail](t, h.svc, usage.MethodSession, sessionParams); detail.Source != tc.want || detail.Session.ID != testSession {
				t.Errorf("session source = %q; want %q", detail.Source, tc.want)
			}
			// A reader that opened the FIFO would block; fail fast instead.
			type result struct {
				raw json.RawMessage
				err error
			}
			done := make(chan result, 1)
			go func() {
				raw, err := h.svc.Handle(context.Background(), usage.MethodTimeline, json.RawMessage(sessionParams))
				done <- result{raw, err}
			}()
			var page usage.TimelinePage
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatalf("timeline: %v", got.err)
				}
				if err := json.Unmarshal(got.raw, &page); err != nil {
					t.Fatal(err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the timeline request hangs")
			}
			if page.Source != tc.want {
				t.Errorf("timeline source = %q; want %q", page.Source, tc.want)
			}
			wantItems, wantReads := 0, 0
			if tc.want == usage.SourceAvailable {
				wantItems, wantReads = len(samplePage()), 1
			}
			if len(page.Items) != wantItems || len(h.timeline.requests) != wantReads {
				t.Errorf("page holds %d items after %d reads; want %d items after %d reads", len(page.Items), len(h.timeline.requests), wantItems, wantReads)
			}
		})
	}
}

func TestHandleSessionWithoutRootStreamIsMissing(t *testing.T) {
	h := settled(t)
	h.store.with(func(f *fakeStore) { delete(f.streams, streamKey{testSession, ""}) })
	if detail := call[usage.SessionDetail](t, h.svc, usage.MethodSession, sessionParams); detail.Source != usage.SourceMissing {
		t.Fatalf("source = %q; a session without a stored root stream has no transcript to open", detail.Source)
	}
}

func TestHandleTimelineReaderFailures(t *testing.T) {
	h := settled(t)
	h.timeline.err = errInjected
	h.timeline.page = usage.TimelinePage{Items: samplePage(), NextCursor: "next"}
	page := call[usage.TimelinePage](t, h.svc, usage.MethodTimeline, sessionParams)
	if page.Source != usage.SourceUnreadable || len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("page after a read failure = %+v; want an empty unreadable page", page)
	}

	h.timeline.err = fmt.Errorf("cursor inside a line: %w", usage.ErrBadRequest)
	if _, err := h.svc.Handle(context.Background(), usage.MethodTimeline, json.RawMessage(sessionParams)); !errors.Is(err, usage.ErrBadRequest) {
		t.Fatalf("a cursor the reader rejects = %v; want usage.ErrBadRequest", err)
	}
}

func TestHandleTimelineStopsCollectingExtensionEvents(t *testing.T) {
	const stored = maxTimelineExtEvents + 2*usage.MaxPageSize
	h := settled(t)
	h.timeline.page = usage.TimelinePage{Items: samplePage()}
	h.store.with(func(f *fakeStore) {
		for i := range stored {
			f.events = append(f.events, usage.ExtEvent{ID: "e" + strconv.Itoa(i), SessionID: testSession, At: at(25)})
		}
	})
	page := call[usage.TimelinePage](t, h.svc, usage.MethodTimeline, sessionParams)
	inserted := 0
	for _, item := range page.Items {
		if item.Kind == usage.ItemExt {
			inserted++
		}
	}
	if inserted != maxTimelineExtEvents {
		t.Fatalf("%d extension events were inserted; want the cap of %d", inserted, maxTimelineExtEvents)
	}
	h.store.with(func(f *fakeStore) {
		var cursors []string
		for _, q := range f.extQueries {
			cursors = append(cursors, q.Cursor)
		}
		want := []string{"", "200", "400", "600", "800"}
		if !slices.Equal(cursors, want) {
			t.Fatalf("extension pages were read at %v\nwant %v", cursors, want)
		}
	})
}

func TestHandleTimelineLimitsConcurrentReads(t *testing.T) {
	h := settled(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var active, peak atomic.Int32
	h.timeline.hook = func() {
		raise(&peak, active.Add(1))
		entered <- struct{}{}
		<-release
		active.Add(-1)
	}
	const waiting = 3
	results := make(chan error, usage.MaxTimelineReads+waiting)
	read := func(ctx context.Context) {
		_, err := h.svc.Handle(ctx, usage.MethodTimeline, json.RawMessage(sessionParams))
		results <- err
	}
	for range usage.MaxTimelineReads {
		go read(context.Background())
	}
	for range usage.MaxTimelineReads {
		<-entered
	}

	// Every slot is taken: a further request can only wait, so one whose
	// context is already cancelled comes back with that error.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.svc.Handle(cancelled, usage.MethodTimeline, json.RawMessage(sessionParams)); !errors.Is(err, context.Canceled) {
		t.Fatalf("request beyond the limit = %v; want it to wait and see the cancel", err)
	}

	for range waiting {
		go read(context.Background())
	}
	close(release)
	for range waiting {
		<-entered
	}
	for range usage.MaxTimelineReads + waiting {
		if err := <-results; err != nil {
			t.Fatalf("timeline: %v", err)
		}
	}
	if got := peak.Load(); got != usage.MaxTimelineReads {
		t.Fatalf("%d timelines were read at once; want at most %d", got, usage.MaxTimelineReads)
	}
}
