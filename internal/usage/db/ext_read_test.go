package db

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

const (
	extReadTestDeploy = "deploy-script"
	extReadTestLinter = "linter"
)

// extReadTestEvents are the seeded events, newest first. x5 lies just before
// and x6 at the open end of [sessTestFrom, sessTestUntil); x4 and x7 share a
// millisecond.
func extReadTestEvents() []usage.ExtEvent {
	received := func(at time.Time) time.Time { return at.Add(time.Second) }
	event := func(id, source string, kind usage.ExtKind, at time.Time, status usage.ExtStatus, level usage.ExtLevel) usage.ExtEvent {
		return usage.ExtEvent{ID: id, Source: source, Kind: kind, Name: "step-" + id, At: at, ReceivedAt: received(at), Status: status, Level: level}
	}
	x1 := event("x1", extReadTestDeploy, usage.ExtSpan, sessTestAt(time.Minute), usage.ExtError, usage.ExtFail)
	x1.DurationMs, x1.SessionID, x1.ToolUseID = sessTestPtr(100), sessTestAlpha, "t1"
	x1.TraceID, x1.SpanID, x1.ParentSpanID, x1.Message = "trace-1", "span-1", "span-0", "exit status 1"
	x1.Attributes = map[string]string{"step": "3", "retries": "2"}
	x2 := event("x2", extReadTestDeploy, usage.ExtLog, sessTestAt(2*time.Minute), usage.ExtOK, usage.ExtInfo)
	x2.DurationMs, x2.SessionID = sessTestPtr(300), sessTestAlpha
	x3 := event("x3", extReadTestLinter, usage.ExtMetric, sessTestAt(3*time.Minute), usage.ExtOK, usage.ExtWarn)
	x3.SessionID, x3.Value, x3.Unit = sessTestAlpha, sessTestPtr(1.5), "count"
	x4 := event("x4", extReadTestLinter, usage.ExtLog, sessTestAt(5*time.Hour), usage.ExtUnknown, usage.ExtDebug)
	x5 := event("x5", extReadTestDeploy, usage.ExtLog, sessTestAt(-time.Millisecond), usage.ExtError, usage.ExtFail)
	x6 := event("x6", extReadTestLinter, usage.ExtLog, sessTestUntil, usage.ExtOK, usage.ExtInfo)
	x6.SessionID = sessTestBeta
	x7 := event("x7", extReadTestLinter, usage.ExtLog, sessTestAt(5*time.Hour), usage.ExtOK, usage.ExtInfo)
	return []usage.ExtEvent{x6, x4, x7, x3, x2, x1, x5}
}

// extReadTestSeed stores the events in an order unrelated to the expected
// one, their attributes, and the rejection counters.
func extReadTestSeed(t *testing.T, s *Store) {
	t.Helper()
	events := extReadTestEvents()
	for _, i := range []int{3, 0, 6, 2, 5, 1, 4} {
		e := events[i]
		seedExt(t, s, e)
		if e.Attributes == nil {
			continue
		}
		raw, err := json.Marshal(e.Attributes)
		if err != nil {
			t.Fatal(err)
		}
		extReadTestExec(t, s, "UPDATE ext_event SET attributes = ? WHERE id = ?", string(raw), e.ID)
	}
	for source, n := range map[string]int{extReadTestDeploy: 3, extReadTestLinter: 1, "": 4, "ghost": 2} {
		extReadTestExec(t, s, "INSERT INTO ext_reject(source, n) VALUES(?, ?)", source, n)
	}
}

func extReadTestExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("seed %q: %v", query, err)
	}
}

func extReadTestIDs(events []usage.ExtEvent) []string {
	ids := make([]string, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	return ids
}

func TestExtSources(t *testing.T) {
	s := openTest(t)
	extReadTestSeed(t, s)
	const rejectedTotal = 10
	deploy := usage.ExtSourceRow{Source: extReadTestDeploy, Events: 2, Errors: 1, Linked: 2, Rejected: 3, LastAt: sessTestAt(2 * time.Minute)}
	linter := usage.ExtSourceRow{Source: extReadTestLinter, Events: 3, Linked: 1, Unlinked: 2, Rejected: 1, LastAt: sessTestAt(5 * time.Hour)}
	tests := []struct {
		name string
		rng  usage.Range
		want []usage.ExtSourceRow
	}{
		{"whole range, most events first", usage.Range{From: sessTestFrom, Until: sessTestUntil}, []usage.ExtSourceRow{linter, deploy}},
		{"equal counts by name", usage.Range{From: sessTestAt(2 * time.Minute), Until: sessTestAt(4 * time.Hour)}, []usage.ExtSourceRow{
			{Source: extReadTestDeploy, Events: 1, Linked: 1, Rejected: 3, LastAt: sessTestAt(2 * time.Minute)},
			{Source: extReadTestLinter, Events: 1, Linked: 1, Rejected: 1, LastAt: sessTestAt(3 * time.Minute)},
		}},
		{"both bounds just outside", usage.Range{From: sessTestAt(-time.Millisecond), Until: sessTestUntil.Add(time.Millisecond)}, []usage.ExtSourceRow{
			{Source: extReadTestLinter, Events: 4, Linked: 2, Unlinked: 2, Rejected: 1, LastAt: sessTestUntil},
			{Source: extReadTestDeploy, Events: 3, Errors: 2, Linked: 2, Unlinked: 1, Rejected: 3, LastAt: sessTestAt(2 * time.Minute)},
		}},
		{"no event in range", usage.Range{From: sessTestAt(6 * time.Hour), Until: sessTestAt(7 * time.Hour)}, []usage.ExtSourceRow{}},
		{"empty range", usage.Range{From: sessTestUntil, Until: sessTestFrom}, []usage.ExtSourceRow{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A project never narrows the senders: events need no session.
			got, rejected, err := s.ExtSources(context.Background(), usage.Query{Range: tt.rng, Project: sessTestProjA})
			if err != nil {
				t.Fatal(err)
			}
			sessTestEqual(t, "sources", got, tt.want)
			if rejected != rejectedTotal {
				t.Errorf("rejected = %d; want %d", rejected, rejectedTotal)
			}
		})
	}
}

func TestExtEventsFilters(t *testing.T) {
	s := openTest(t)
	extReadTestSeed(t, s)
	tests := []struct {
		name string
		q    usage.ExtQuery
		want []string
	}{
		{"zero range is all time", usage.ExtQuery{}, []string{"x6", "x4", "x7", "x3", "x2", "x1", "x5"}},
		{"from only", usage.ExtQuery{Range: usage.Range{From: sessTestFrom}}, []string{"x6", "x4", "x7", "x3", "x2", "x1"}},
		{"until only", usage.ExtQuery{Range: usage.Range{Until: sessTestUntil}}, []string{"x4", "x7", "x3", "x2", "x1", "x5"}},
		{"both bounds", usage.ExtQuery{Range: usage.Range{From: sessTestFrom, Until: sessTestUntil}}, []string{"x4", "x7", "x3", "x2", "x1"}},
		{"source", usage.ExtQuery{Source: extReadTestLinter}, []string{"x6", "x4", "x7", "x3"}},
		{"session", usage.ExtQuery{SessionID: sessTestAlpha}, []string{"x3", "x2", "x1"}},
		{"tool use", usage.ExtQuery{ToolUseID: "t1"}, []string{"x1"}},
		{"level", usage.ExtQuery{Level: usage.ExtFail}, []string{"x1", "x5"}},
		{"unknown level", usage.ExtQuery{Level: "fatal"}, []string{}},
		{"source and level", usage.ExtQuery{Source: extReadTestDeploy, Level: usage.ExtInfo}, []string{"x2"}},
		{"session and range", usage.ExtQuery{SessionID: sessTestAlpha, Range: usage.Range{From: sessTestAt(2 * time.Minute)}}, []string{"x3", "x2"}},
		{"unknown source", usage.ExtQuery{Source: "nobody"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := s.ExtEvents(context.Background(), tt.q)
			if err != nil {
				t.Fatal(err)
			}
			sessTestEqual(t, "ids", extReadTestIDs(page.Items), tt.want)
			if page.Total != len(tt.want) || page.NextCursor != "" || page.Items == nil {
				t.Errorf("total = %d, cursor = %q, nil items = %v; want %d on one page", page.Total, page.NextCursor, page.Items == nil, len(tt.want))
			}
		})
	}
}

// Every column comes back: nullable numbers as pointers, attributes decoded,
// and an event without attributes with none.
func TestExtEventsDecodeRows(t *testing.T) {
	s := openTest(t)
	extReadTestSeed(t, s)
	page, err := s.ExtEvents(context.Background(), usage.ExtQuery{})
	if err != nil {
		t.Fatal(err)
	}
	sessTestEqual(t, "events", page.Items, extReadTestEvents())
}

func TestExtEventsPaging(t *testing.T) {
	s := openTest(t)
	extReadTestSeed(t, s)
	ctx := context.Background()
	want := extReadTestEvents()
	for limit := 1; limit <= len(want)+1; limit++ {
		q := usage.ExtQuery{Limit: limit}
		var got []usage.ExtEvent
		pages := 0
		for {
			page, err := s.ExtEvents(ctx, q)
			if err != nil {
				t.Fatalf("limit %d: %v", limit, err)
			}
			pages++
			if page.Total != len(want) || len(page.Items) == 0 || len(page.Items) > limit {
				t.Fatalf("limit %d page %d: %d items, total %d", limit, pages, len(page.Items), page.Total)
			}
			got = append(got, page.Items...)
			if page.NextCursor == "" {
				break
			}
			q.Cursor = page.NextCursor
		}
		sessTestEqual(t, "pages", got, want)
		if wantPages := (len(want) + limit - 1) / limit; pages != wantPages {
			t.Errorf("limit %d: %d pages; want %d", limit, pages, wantPages)
		}
	}
	// A filter keeps its total on every page.
	q := usage.ExtQuery{Source: extReadTestLinter, Limit: 3}
	first, err := s.ExtEvents(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Cursor = first.NextCursor
	second, err := s.ExtEvents(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	sessTestEqual(t, "filtered pages", append(extReadTestIDs(first.Items), extReadTestIDs(second.Items)...), []string{"x6", "x4", "x7", "x3"})
	if first.Total != 4 || second.Total != 4 || second.NextCursor != "" {
		t.Errorf("totals = %d, %d, last cursor %q; want 4, 4 and no cursor", first.Total, second.Total, second.NextCursor)
	}
}

func TestExtEventsBadRequest(t *testing.T) {
	s := openTest(t)
	extReadTestSeed(t, s)
	encode := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	tests := []struct {
		name string
		q    usage.ExtQuery
	}{
		{"negative limit", usage.ExtQuery{Limit: -1}},
		{"inverted range", usage.ExtQuery{Range: usage.Range{From: sessTestUntil, Until: sessTestFrom}}},
		{"empty range", usage.ExtQuery{Range: usage.Range{From: sessTestFrom, Until: sessTestFrom}}},
		{"cursor is not base64", usage.ExtQuery{Cursor: "!!"}},
		{"cursor is not JSON", usage.ExtQuery{Cursor: base64.RawURLEncoding.EncodeToString([]byte("nope"))}},
		{"cursor of another version", usage.ExtQuery{Cursor: encode(extReadCursor{Version: extReadCursorVersion + 1, ID: "x1"})}},
		{"cursor without an id", usage.ExtQuery{Cursor: encode(extReadCursor{Version: extReadCursorVersion})}},
		{"session cursor", usage.ExtQuery{Cursor: encode(sessCursor{Version: sessCursorVersion, Sort: usage.SortLast, ID: "x1"})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.ExtEvents(context.Background(), tt.q); !errors.Is(err, usage.ErrBadRequest) {
				t.Fatalf("error = %v; want usage.ErrBadRequest", err)
			}
		})
	}
}

func TestExtEventsReportUndecodableAttributes(t *testing.T) {
	s := openTest(t)
	extReadTestSeed(t, s)
	extReadTestExec(t, s, "UPDATE ext_event SET attributes = ? WHERE id = ?", "not json", "x2")
	_, err := s.ExtEvents(context.Background(), usage.ExtQuery{})
	if err == nil || errors.Is(err, usage.ErrBadRequest) {
		t.Fatalf("error = %v; want a decode failure that is not a bad request", err)
	}
	// Rows that do not hold the broken event stay readable.
	page, err := s.ExtEvents(context.Background(), usage.ExtQuery{Source: extReadTestLinter})
	if err != nil || len(page.Items) != 4 {
		t.Fatalf("other sender = %d events, %v; want 4", len(page.Items), err)
	}
}

func TestExtReadsOnEmptyStore(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	sources, rejected, err := s.ExtSources(ctx, usage.Query{Range: usage.Range{From: sessTestFrom, Until: sessTestUntil}})
	if err != nil || sources == nil || len(sources) != 0 || rejected != 0 {
		t.Errorf("ExtSources = %v, %d, %v; want an empty, non-nil list", sources, rejected, err)
	}
	page, err := s.ExtEvents(ctx, usage.ExtQuery{})
	if err != nil || page.Items == nil || len(page.Items) != 0 || page.Total != 0 || page.NextCursor != "" {
		t.Errorf("ExtEvents = %s, %v; want an empty, non-nil page", sessTestJSON(t, page), err)
	}
}

func TestExtReadsReportQueryFailure(t *testing.T) {
	s := openTest(t)
	extReadTestSeed(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.ExtSources(ctx, usage.Query{Range: usage.Range{From: sessTestFrom, Until: sessTestUntil}}); !errors.Is(err, context.Canceled) {
		t.Errorf("ExtSources error = %v; want context.Canceled", err)
	}
	if _, err := s.ExtEvents(ctx, usage.ExtQuery{}); !errors.Is(err, context.Canceled) {
		t.Errorf("ExtEvents error = %v; want context.Canceled", err)
	}
}
