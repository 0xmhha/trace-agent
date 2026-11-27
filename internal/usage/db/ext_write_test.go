package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// extWriteRow is one stored extension event as the table holds it.
type extWriteRow struct {
	source, kind, name, status, level                  string
	atMS, receivedMS                                   int64
	duration, value                                    sql.NullFloat64
	session, toolUse, trace, span, parentSpan, message string
	attributes, unit                                   string
}

// extWriteRead loads the stored event with the given ID.
func extWriteRead(t *testing.T, s *Store, id string) extWriteRow {
	t.Helper()
	var row extWriteRow
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT source,kind,name,at_ms,received_ms,duration_ms,status,level,session_id,tool_use_id,trace_id,span_id,parent_span_id,message,attributes,value,unit
		 FROM ext_event WHERE id = ?`, id).Scan(&row.source, &row.kind, &row.name, &row.atMS, &row.receivedMS, &row.duration,
		&row.status, &row.level, &row.session, &row.toolUse, &row.trace, &row.span, &row.parentSpan, &row.message,
		&row.attributes, &row.value, &row.unit); err != nil {
		t.Fatalf("read extension event %s: %v", id, err)
	}
	return row
}

// extWriteFloat returns a pointer to v, the way the endpoint reports an
// optional number.
func extWriteFloat(v float64) *float64 { return &v }

func TestAppendExtStoresEveryField(t *testing.T) {
	s := openTest(t)
	at, received := testNow.Add(-time.Minute), testNow
	full := usage.ExtEvent{
		ID: "ext-full", Source: "deploy-script", Kind: usage.ExtSpan, Name: "upload", At: at, ReceivedAt: received,
		DurationMs: extWriteFloat(812.5), Status: usage.ExtError, Level: usage.ExtFail, SessionID: "sess-1",
		ToolUseID: "call-1", TraceID: "trace-1", SpanID: "span-1", ParentSpanID: "span-0", Message: "exit status 1",
		Attributes: map[string]string{"step": "3", "retries": "2", "quote": `a "b"`}, Value: extWriteFloat(0), Unit: "count",
	}
	bare := usage.ExtEvent{ID: "ext-bare", Source: "tool", Kind: usage.ExtLog, Name: "note", At: at, ReceivedAt: received,
		Status: usage.ExtUnknown, Level: usage.ExtInfo}
	empty := bare
	empty.ID, empty.Kind, empty.Attributes = "ext-empty", usage.ExtMetric, map[string]string{}
	if err := s.AppendExt(context.Background(), []usage.ExtEvent{full, bare, empty}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		want extWriteRow
	}{
		{id: full.ID, want: extWriteRow{
			source: "deploy-script", kind: "span", name: "upload", status: "error", level: "error",
			atMS: toMS(at), receivedMS: toMS(received),
			duration: sql.NullFloat64{Float64: 812.5, Valid: true}, value: sql.NullFloat64{Float64: 0, Valid: true},
			session: "sess-1", toolUse: "call-1", trace: "trace-1", span: "span-1", parentSpan: "span-0", message: "exit status 1",
			// Keys are stored in ascending order.
			attributes: `{"quote":"a \"b\"","retries":"2","step":"3"}`, unit: "count",
		}},
		{id: bare.ID, want: extWriteRow{source: "tool", kind: "log", name: "note", status: "unknown", level: "info",
			atMS: toMS(at), receivedMS: toMS(received), attributes: "{}"}},
		{id: empty.ID, want: extWriteRow{source: "tool", kind: "metric", name: "note", status: "unknown", level: "info",
			atMS: toMS(at), receivedMS: toMS(received), attributes: "{}"}},
	} {
		if got := extWriteRead(t, s, tc.id); got != tc.want {
			t.Errorf("event %s = %+v; want %+v", tc.id, got, tc.want)
		}
	}
}

func TestAppendExtIgnoresStoredIDs(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	first := ingestExtEvent("ext-1", "", 0)
	first.Message = "first"
	for _, step := range []struct {
		name     string
		events   []usage.ExtEvent
		wantRows int64
		bumps    bool
	}{
		{name: "nil slice", events: nil, wantRows: 0},
		{name: "empty slice", events: []usage.ExtEvent{}, wantRows: 0},
		{name: "first event", events: []usage.ExtEvent{first}, wantRows: 1, bumps: true},
		{name: "same event again", events: []usage.ExtEvent{first}, wantRows: 1},
		{name: "same id with other content", events: []usage.ExtEvent{func() usage.ExtEvent {
			e := first
			e.Message = "second"
			return e
		}()}, wantRows: 1},
		{name: "known and new together", events: []usage.ExtEvent{first, ingestExtEvent("ext-2", "", 1)}, wantRows: 2, bumps: true},
		{name: "same id twice in one call", events: []usage.ExtEvent{ingestExtEvent("ext-3", "", 2), ingestExtEvent("ext-3", "", 3)}, wantRows: 3, bumps: true},
	} {
		version := ingestVersion(t, s)
		if err := s.AppendExt(ctx, step.events); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := ingestCount(t, s, "ext_event", ""); got != step.wantRows {
			t.Fatalf("%s: ext_event rows = %d; want %d", step.name, got, step.wantRows)
		}
		want := version
		if step.bumps {
			want++
		}
		if got := ingestVersion(t, s); got != want {
			t.Fatalf("%s: data_version = %d; want %d", step.name, got, want)
		}
	}
	if got := extWriteRead(t, s, "ext-1").message; got != "first" {
		t.Fatalf("message of a re-sent event = %q; want the first one kept", got)
	}
}

func TestAppendExtRefusesWholeCall(t *testing.T) {
	ctx := context.Background()
	good := ingestExtEvent("ext-good", "", 0)
	for _, tc := range []struct {
		name    string
		mutate  func(e *usage.ExtEvent)
		wantErr error
	}{
		{name: "empty id", mutate: func(e *usage.ExtEvent) { e.ID = "" }, wantErr: usage.ErrBadRequest},
		// The schema refuses values outside the documented sets. Such a row
		// must fail the call instead of vanishing.
		{name: "unlisted kind", mutate: func(e *usage.ExtEvent) { e.Kind = "trace" }},
		{name: "unlisted status", mutate: func(e *usage.ExtEvent) { e.Status = "failed" }},
		{name: "unlisted level", mutate: func(e *usage.ExtEvent) { e.Level = "fatal" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			bad := ingestExtEvent("ext-bad", "", 1)
			tc.mutate(&bad)
			err := s.AppendExt(ctx, []usage.ExtEvent{good, bad})
			if err == nil {
				t.Fatal("AppendExt accepted an invalid event")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("AppendExt = %v; want %v", err, tc.wantErr)
			}
			if got := ingestCount(t, s, "ext_event", ""); got != 0 {
				t.Fatalf("ext_event rows = %d after a refused call; want 0", got)
			}
			if got := ingestVersion(t, s); got != 0 {
				t.Fatalf("data_version = %d after a refused call; want 0", got)
			}
		})
	}
}

func TestAppendExtHonoursContext(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.AppendExt(ctx, []usage.ExtEvent{ingestExtEvent("ext-1", "", 0)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("AppendExt = %v; want context.Canceled", err)
	}
	if err := s.CountRejected(ctx, "tool", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("CountRejected = %v; want context.Canceled", err)
	}
	if got := ingestCount(t, s, "ext_event", "") + ingestCount(t, s, "ext_reject", ""); got != 0 {
		t.Fatalf("rows written by cancelled calls = %d; want 0", got)
	}
}

func TestCountRejected(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	read := func(source string) int64 {
		t.Helper()
		var n int64
		err := s.db.QueryRowContext(ctx, "SELECT n FROM ext_reject WHERE source = ?", source).Scan(&n)
		if errors.Is(err, sql.ErrNoRows) {
			return 0
		}
		if err != nil {
			t.Fatalf("read rejection counter: %v", err)
		}
		return n
	}
	for _, step := range []struct {
		name   string
		source string
		n      int
		want   int64
		bumps  bool
	}{
		{name: "zero is a no-op", source: "tool", n: 0, want: 0},
		{name: "negative is a no-op", source: "tool", n: -4, want: 0},
		{name: "first count", source: "tool", n: 3, want: 3, bumps: true},
		{name: "counts add up", source: "tool", n: 2, want: 5, bumps: true},
		{name: "negative does not subtract", source: "tool", n: -1, want: 5},
		{name: "unattributed input", source: "", n: 1, want: 1, bumps: true},
		{name: "other sender", source: "other", n: 7, want: 7, bumps: true},
	} {
		version := ingestVersion(t, s)
		if err := s.CountRejected(ctx, step.source, step.n); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := read(step.source); got != step.want {
			t.Fatalf("%s: counter of %q = %d; want %d", step.name, step.source, got, step.want)
		}
		want := version
		if step.bumps {
			want++
		}
		if got := ingestVersion(t, s); got != want {
			t.Fatalf("%s: data_version = %d; want %d", step.name, got, want)
		}
	}
	if got := read("tool"); got != 5 {
		t.Fatalf("counter of the first sender = %d after other senders counted; want 5", got)
	}
	if got := ingestCount(t, s, "ext_reject", ""); got != 3 {
		t.Fatalf("rejection counters = %d; want one per sender", got)
	}
}

// extWriteIDs lists the stored extension event IDs in ascending order.
func extWriteIDs(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), "SELECT id FROM ext_event ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// extWriteStep is one AppendExt call of a scenario and what it must leave.
type extWriteStep struct {
	name     string
	events   []usage.ExtEvent
	wantKept []string
	bumps    bool
}

// extWriteRun runs the steps in order through appendExt with extCap.
func extWriteRun(t *testing.T, s *Store, extCap int64, steps []extWriteStep) {
	t.Helper()
	ctx := context.Background()
	for _, step := range steps {
		version := ingestVersion(t, s)
		if err := s.appendExt(ctx, step.events, extCap); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := extWriteIDs(t, s); !slices.Equal(got, step.wantKept) {
			t.Fatalf("%s: stored = %v; want %v", step.name, got, step.wantKept)
		}
		want := version
		if step.bumps {
			want++
		}
		if got := ingestVersion(t, s); got != want {
			t.Fatalf("%s: data_version = %d; want %d", step.name, got, want)
		}
	}
}

func TestAppendExtEnforcesTheCap(t *testing.T) {
	const extCap = 3
	// Event times run against the receive order, so the test tells the two
	// apart: the cap follows the receive time, as Prune does.
	event := func(i int) usage.ExtEvent {
		e := ingestExtEvent(fmt.Sprintf("ext-%d", i), "", i)
		e.At = ingestStart.Add(-time.Duration(i) * time.Minute)
		return e
	}
	extWriteRun(t, openTest(t), extCap, []extWriteStep{
		{name: "under the cap", events: []usage.ExtEvent{event(0), event(1)}, wantKept: []string{"ext-0", "ext-1"}, bumps: true},
		{name: "reaching the cap", events: []usage.ExtEvent{event(2)}, wantKept: []string{"ext-0", "ext-1", "ext-2"}, bumps: true},
		{name: "oldest go first", events: []usage.ExtEvent{event(3), event(4)}, wantKept: []string{"ext-2", "ext-3", "ext-4"}, bumps: true},
		{name: "one call above the cap", events: []usage.ExtEvent{event(5), event(6), event(7), event(8)},
			wantKept: []string{"ext-6", "ext-7", "ext-8"}, bumps: true},
		{name: "repeats only", events: []usage.ExtEvent{event(8)}, wantKept: []string{"ext-6", "ext-7", "ext-8"}},
	})
}

func TestAppendExtTrimsOnlyAfterAnInsert(t *testing.T) {
	s := openTest(t)
	extWriteRun(t, s, 10, []extWriteStep{{name: "two events", wantKept: []string{"ext-1", "ext-2"}, bumps: true,
		events: []usage.ExtEvent{ingestExtEvent("ext-1", "", 0), ingestExtEvent("ext-2", "", 1)}}})
	// A call that stores nothing new leaves the table alone even above a
	// smaller cap; the hourly Prune deals with that.
	extWriteRun(t, s, 1, []extWriteStep{{name: "repeat under a smaller cap", wantKept: []string{"ext-1", "ext-2"},
		events: []usage.ExtEvent{ingestExtEvent("ext-1", "", 0)}}})
}

func TestAppendExtUsesTheSharedCap(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.AppendExt(ctx, []usage.ExtEvent{ingestExtEvent("ext-1", "", 0), ingestExtEvent("ext-2", "", 1)}); err != nil {
		t.Fatal(err)
	}
	if got := ingestCount(t, s, "ext_event", ""); got != 2 {
		t.Fatalf("ext_event rows = %d; AppendExt must keep a store under usage.MaxExtEvents", got)
	}
}

func TestAppendExtDropsEventsOfExcludedSessions(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	// Streams of an excluded project (root and one sub-agent) and of an
	// included one. The rows are written directly so that the excluded
	// project's streams are present.
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{query: "INSERT INTO project(project_key,excluded) VALUES(?,1),(?,0)", args: []any{"proj-out", "proj-in"}},
		{query: "INSERT INTO stream(session_id,agent_id,project_key,file_path,first_ms,last_ms) VALUES(?,'',?,?,0,0),(?,?,?,?,0,0),(?,'',?,?,0,0)",
			args: []any{"sess-out", "proj-out", "out/sess-out.jsonl",
				"sess-out", "agent-1", "proj-out", "out/sess-out/subagents/agent-1.jsonl",
				"sess-in", "proj-in", "in/sess-in.jsonl"}},
	} {
		if _, err := s.db.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	extWriteRun(t, s, usage.MaxExtEvents, []extWriteStep{
		{name: "only excluded sessions", wantKept: []string{},
			events: []usage.ExtEvent{ingestExtEvent("ext-out-1", "sess-out", 0), ingestExtEvent("ext-out-2", "sess-out", 1)}},
		{name: "mixed body", bumps: true, wantKept: []string{"ext-in", "ext-none", "ext-unknown"},
			events: []usage.ExtEvent{
				ingestExtEvent("ext-out-3", "sess-out", 2),
				ingestExtEvent("ext-in", "sess-in", 3),
				ingestExtEvent("ext-none", "", 4),
				ingestExtEvent("ext-unknown", "sess-unknown", 5),
			}},
	})
	if got := ingestCount(t, s, "ext_reject", ""); got != 0 {
		t.Fatalf("rejection counters = %d; dropped events must not be counted", got)
	}
}
