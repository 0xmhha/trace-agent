package db

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// The seeded world of the session tests. Every count below is relative to
// the range [sessTestFrom, sessTestUntil).
var (
	sessTestFrom  = time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	sessTestUntil = sessTestFrom.Add(24 * time.Hour)
)

const (
	sessTestAlpha  = "s-alpha"  // root and two sub-agent streams, rows on both sides of each bound
	sessTestBeta   = "s-beta"   // root only, the heaviest and the most expensive
	sessTestDelta  = "s-delta"  // in range through one prompt only, no cost
	sessTestOrphan = "s-orphan" // sub-agent stream only, no root stream, no cost
	sessTestOld    = "s-old"    // entirely before the range
	sessTestProjA  = "proj-a"
	sessTestProjB  = "proj-b"
	sessTestLarge  = "model-large"
	sessTestSmall  = "model-small"
)

func sessTestAt(d time.Duration) time.Time { return sessTestFrom.Add(d) }

func sessTestPtr(v float64) *float64 { return &v }

func sessTestJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		t.Fatalf("encode for display: %v", err)
	}
	return string(raw)
}

// sessTestEqual compares with reflect.DeepEqual and prints both sides as
// JSON, so that pointer fields show their values.
func sessTestEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s mismatch\n got: %s\nwant: %s", what, sessTestJSON(t, got), sessTestJSON(t, want))
	}
}

func sessTestModel(session, agent, key string, at time.Time, model string, in, out, cacheRead, cacheCreate int64) usage.ModelCall {
	return usage.ModelCall{
		Key: key, SessionID: session, AgentID: agent, At: at, Model: model,
		Input: in, Output: out, CacheRead: cacheRead, CacheCreate: cacheCreate,
	}
}

func sessTestFile(path, project string) usage.FileRef {
	return usage.FileRef{Path: path, ProjectKey: project, Size: 1, ModTime: testNow}
}

func sessTestSeedAlpha(t *testing.T, s *Store) {
	t.Helper()
	const id = sessTestAlpha
	msec := time.Millisecond
	attributed := sessTestModel(id, "", "m2", sessTestAt(2*time.Minute), sessTestSmall, 1, 2, 3, 4)
	// A transcript attributes a plugin skill by its full name and names the
	// plugin again beside it.
	attributed.Skill, attributed.Plugin = "devkit:deploy-check", "devkit"
	unqualified := sessTestModel(id, "", "m1", sessTestAt(time.Minute), sessTestLarge, 10, 20, 300, 40)
	unqualified.Skill = "notes"
	seedBatch(t, s, sessTestFile("proj-a/s-alpha.jsonl", sessTestProjA), usage.Batch{
		Session: usage.SessionPatch{
			SessionID: id, ProjectPath: "/work/alpha", Title: "Fix Build_100%", Entrypoint: "cli",
			ClientVersion: "2.1.0", GitBranch: "main", FirstAt: sessTestAt(-time.Hour), LastAt: sessTestUntil,
		},
		ModelCalls: []usage.ModelCall{
			sessTestModel(id, "", "m0", sessTestAt(-msec), sessTestLarge, 1000, 1000, 1000, 1000), // just before the range
			unqualified,
			attributed,
			sessTestModel(id, "", "m3", sessTestUntil, sessTestLarge, 5000, 5000, 5000, 5000), // at the open end
		},
		Prompts: []usage.Prompt{
			{Key: "p1", SessionID: id, At: sessTestFrom, Chars: 10}, // at the closed start
			{Key: "p2", SessionID: id, At: sessTestUntil, Chars: 10},
		},
		ToolCalls: []usage.ToolCall{
			{Key: "t0", SessionID: id, At: sessTestAt(-2 * msec), Name: usage.ToolNameBash, Category: usage.ToolBuiltin, Target: "ls", MessageKey: "m0"},
			{Key: "t1", SessionID: id, At: sessTestAt(time.Minute), Name: usage.ToolNameBash, Category: usage.ToolBuiltin, Target: "go", MessageKey: "m1"},
			{Key: "t2", SessionID: id, At: sessTestAt(time.Minute), Name: "mcp__plugin_devkit_chain__status", Category: usage.ToolMCP, MCPServer: "chain", MCPTool: "status", Plugin: "devkit", MessageKey: "m1"},
			{Key: "t3", SessionID: id, At: sessTestAt(2 * time.Minute), Name: usage.ToolNameSkill, Category: usage.ToolSkill, Skill: "devkit:deploy-check", Plugin: "devkit", MessageKey: "m2"},
			{Key: "t4", SessionID: id, At: sessTestAt(2*time.Minute + time.Second), Name: "mcp__issues__search", Category: usage.ToolMCP, MCPServer: "issues", MCPTool: "search", MessageKey: "m2"},
		},
		ToolResults: []usage.ToolResult{
			{Key: "t0", SessionID: id, At: sessTestAt(-msec), IsError: true},                       // derived duration 1 ms
			{Key: "t1", SessionID: id, At: sessTestAt(time.Minute + 1500*msec), IsError: true},     // derived duration 1500 ms
			{Key: "t2", SessionID: id, At: sessTestAt(time.Minute + time.Second), DurationMs: 250}, // reported duration wins
			{Key: "t4", SessionID: id, At: sessTestAt(2*time.Minute + 2*time.Second), IsError: true, DurationMs: 100},
		},
		Hooks: []usage.HookRun{
			{Key: "h1", SessionID: id, At: sessTestAt(time.Minute), Event: "PreToolUse", Name: "PreToolUse:Bash", Status: usage.HookSuccess, DurationMs: 30},
			{Key: "h2", SessionID: id, At: sessTestAt(time.Minute), Event: "PreToolUse", Name: "PreToolUse:Bash", Status: usage.HookError, ExitCode: 1, DurationMs: 90},
			{Key: "h3", SessionID: id, At: sessTestAt(3 * time.Minute), Event: "Stop", Name: "Stop", Status: usage.HookSuccess, DurationMs: 40},
			{Key: "h4", SessionID: id, At: sessTestAt(4 * time.Minute), Event: "PreToolUse", Name: "PreToolUse:Bash", Status: usage.HookBlocked, ExitCode: 2, DurationMs: 60},
			{Key: "h5", SessionID: id, At: sessTestAt(4 * time.Minute), Event: "PreToolUse", Name: "PreToolUse:Bash", Status: usage.HookError, ExitCode: 1, DurationMs: 20},
			{Key: "h6", SessionID: id, At: sessTestAt(5 * time.Minute), Event: "Stop", Name: "Stop", Status: usage.HookUnknown, DurationMs: 40},
		},
		Compactions: []usage.Compaction{
			{Key: "c0", SessionID: id, At: sessTestAt(-time.Minute), Trigger: "auto", PreTokens: 100},
			{Key: "c1", SessionID: id, At: sessTestAt(3 * time.Minute), Trigger: "auto", PreTokens: 100},
		},
		Cost: &usage.CostState{SessionID: id, At: sessTestUntil, TotalCostUSD: 1.25},
	})
	// The sub-agent reuses the message ID m3 of the root transcript, as a
	// copied line does. Its tool call must not count toward the root turn.
	seedBatch(t, s, sessTestFile("proj-a/s-alpha/subagents/agent-ag1.jsonl", sessTestProjA), usage.Batch{
		Session: usage.SessionPatch{
			SessionID: id, AgentID: "ag1", AgentType: "Explore", ProjectPath: "/work/alpha", Entrypoint: "cli",
			GitBranch: "main", FirstAt: sessTestAt(5 * time.Minute), LastAt: sessTestAt(6 * time.Minute),
		},
		ModelCalls: []usage.ModelCall{
			sessTestModel(id, "ag1", "a1", sessTestAt(5*time.Minute), sessTestSmall, 100, 200, 0, 0),
			sessTestModel(id, "ag1", "m3", sessTestAt(6*time.Minute), sessTestSmall, 5, 5, 5, 5),
		},
		ToolCalls: []usage.ToolCall{
			{Key: "ta1", SessionID: id, AgentID: "ag1", At: sessTestAt(5 * time.Minute), Name: "WebFetch", Category: usage.ToolBuiltin, MessageKey: "m3"},
		},
		ToolResults: []usage.ToolResult{
			{Key: "ta1", SessionID: id, At: sessTestAt(5*time.Minute + time.Second), DurationMs: 850},
		},
	})
	// A stream that never reported a timestamp and made no call.
	seedBatch(t, s, sessTestFile("proj-a/s-alpha/subagents/agent-ag0.jsonl", sessTestProjA), usage.Batch{
		Session: usage.SessionPatch{SessionID: id, AgentID: "ag0"},
	})
}

// sessTestSeed fills the store with two projects and five sessions.
func sessTestSeed(t *testing.T, s *Store) {
	t.Helper()
	sessTestSeedAlpha(t, s)
	seedBatch(t, s, sessTestFile("proj-b/s-beta.jsonl", sessTestProjB), usage.Batch{
		Session: usage.SessionPatch{
			SessionID: sessTestBeta, ProjectPath: "/work/beta", Title: "Refactor parser", Entrypoint: "sdk-ts",
			GitBranch: "feature/x", FirstAt: sessTestAt(2 * time.Hour), LastAt: sessTestAt(3 * time.Hour),
		},
		ModelCalls: []usage.ModelCall{sessTestModel(sessTestBeta, "", "b1", sessTestAt(2*time.Hour), sessTestLarge, 1000, 2000, 3000, 4000)},
		Prompts: []usage.Prompt{
			{Key: "bp1", SessionID: sessTestBeta, At: sessTestAt(2 * time.Hour)},
			{Key: "bp2", SessionID: sessTestBeta, At: sessTestAt(3 * time.Hour)},
		},
		ToolCalls: []usage.ToolCall{
			{Key: "tb1", SessionID: sessTestBeta, At: sessTestAt(2 * time.Hour), Name: "Read", Category: usage.ToolBuiltin, MessageKey: "b1"},
			{Key: "tb2", SessionID: sessTestBeta, At: sessTestAt(2 * time.Hour), Name: "Read", Category: usage.ToolBuiltin, MessageKey: "b1"},
		},
		ToolResults: []usage.ToolResult{
			{Key: "tb1", SessionID: sessTestBeta, At: sessTestAt(2*time.Hour + time.Second), DurationMs: 5},
			{Key: "tb2", SessionID: sessTestBeta, At: sessTestAt(2*time.Hour + time.Second), IsError: true, DurationMs: 7},
		},
		Cost: &usage.CostState{SessionID: sessTestBeta, At: sessTestAt(3 * time.Hour), TotalCostUSD: 3.5},
	})
	seedBatch(t, s, sessTestFile("proj-a/s-delta.jsonl", sessTestProjA), usage.Batch{
		Session: usage.SessionPatch{
			SessionID: sessTestDelta, ProjectPath: "/work/alpha", Title: "Notes", Entrypoint: "cli",
			FirstAt: sessTestAt(4 * time.Hour), LastAt: sessTestAt(4 * time.Hour),
		},
		Prompts: []usage.Prompt{{Key: "dp1", SessionID: sessTestDelta, At: sessTestUntil.Add(-time.Millisecond)}}, // just inside
	})
	// The labels of a sub-agent stream must not stand in for the root's.
	seedBatch(t, s, sessTestFile("proj-b/s-orphan/subagents/agent-ag9.jsonl", sessTestProjB), usage.Batch{
		Session: usage.SessionPatch{
			SessionID: sessTestOrphan, AgentID: "ag9", AgentType: "general-purpose", ProjectPath: "/work/beta",
			Title: "Sub-agent title", Entrypoint: "cli", GitBranch: "main",
			FirstAt: sessTestAt(30 * time.Minute), LastAt: sessTestAt(31 * time.Minute),
		},
		ModelCalls: []usage.ModelCall{sessTestModel(sessTestOrphan, "ag9", "o1", sessTestAt(30*time.Minute), sessTestSmall, 7, 8, 9, 10)},
	})
	seedBatch(t, s, sessTestFile("proj-a/s-old.jsonl", sessTestProjA), usage.Batch{
		Session: usage.SessionPatch{
			SessionID: sessTestOld, ProjectPath: "/work/alpha", Title: "Old work", Entrypoint: "cli",
			FirstAt: sessTestAt(-90 * time.Minute), LastAt: sessTestAt(-80 * time.Minute),
		},
		ModelCalls: []usage.ModelCall{sessTestModel(sessTestOld, "", "old1", sessTestAt(-time.Hour), sessTestSmall, 1, 1, 0, 0)},
		Prompts:    []usage.Prompt{{Key: "oldp1", SessionID: sessTestOld, At: sessTestAt(-time.Hour)}},
		Cost:       &usage.CostState{SessionID: sessTestOld, At: sessTestAt(-80 * time.Minute), TotalCostUSD: 9.9},
	})
	duration := func(ms float64) *float64 { return &ms }
	seedExt(t, s,
		usage.ExtEvent{ID: "se1", Source: "deploy-script", Kind: usage.ExtSpan, Name: "upload", At: sessTestAt(time.Minute), ReceivedAt: sessTestAt(time.Minute), DurationMs: duration(100), Status: usage.ExtError, Level: usage.ExtFail, SessionID: sessTestAlpha, ToolUseID: "t1"},
		usage.ExtEvent{ID: "se2", Source: "deploy-script", Kind: usage.ExtSpan, Name: "verify", At: sessTestAt(2 * time.Minute), ReceivedAt: sessTestAt(2 * time.Minute), DurationMs: duration(300), Status: usage.ExtOK, Level: usage.ExtInfo, SessionID: sessTestAlpha},
		usage.ExtEvent{ID: "se3", Source: "linter", Kind: usage.ExtLog, Name: "lint", At: sessTestAt(3 * time.Minute), ReceivedAt: sessTestAt(3 * time.Minute), Status: usage.ExtOK, Level: usage.ExtInfo, SessionID: sessTestAlpha},
		usage.ExtEvent{ID: "se4", Source: "linter", Kind: usage.ExtLog, Name: "lint", At: sessTestAt(3 * time.Minute), ReceivedAt: sessTestAt(3 * time.Minute), Status: usage.ExtError, Level: usage.ExtFail}, // no session
	)
}

// sessTestRows are the list rows of the seeded range.
func sessTestRows() map[string]usage.SessionRow {
	return map[string]usage.SessionRow{
		sessTestAlpha: {
			ID: sessTestAlpha, Title: "Fix Build_100%", ProjectKey: sessTestProjA, ProjectPath: "/work/alpha",
			Entrypoint: "cli", GitBranch: "main", StartedAt: sessTestAt(-time.Hour), LastAt: sessTestUntil,
			Prompts: 1, ModelCalls: 4, ToolCalls: 5, ToolErrors: 2, Subagents: 1, Compactions: 1,
			Tokens:  usage.TokenTotals{Input: 116, Output: 227, CacheRead: 308, CacheCreate: 49},
			CostUSD: sessTestPtr(1.25), Models: []string{sessTestLarge, sessTestSmall},
		},
		sessTestBeta: {
			ID: sessTestBeta, Title: "Refactor parser", ProjectKey: sessTestProjB, ProjectPath: "/work/beta",
			Entrypoint: "sdk-ts", GitBranch: "feature/x", StartedAt: sessTestAt(2 * time.Hour), LastAt: sessTestAt(3 * time.Hour),
			Prompts: 2, ModelCalls: 1, ToolCalls: 2, ToolErrors: 1,
			Tokens:  usage.TokenTotals{Input: 1000, Output: 2000, CacheRead: 3000, CacheCreate: 4000},
			CostUSD: sessTestPtr(3.5), Models: []string{sessTestLarge},
		},
		sessTestDelta: {
			ID: sessTestDelta, Title: "Notes", ProjectKey: sessTestProjA, ProjectPath: "/work/alpha",
			Entrypoint: "cli", StartedAt: sessTestAt(4 * time.Hour), LastAt: sessTestAt(4 * time.Hour),
			Prompts: 1, Models: []string{},
		},
		sessTestOrphan: {
			ID: sessTestOrphan, ProjectKey: sessTestProjB,
			StartedAt: sessTestAt(30 * time.Minute), LastAt: sessTestAt(31 * time.Minute),
			ModelCalls: 1, Subagents: 1,
			Tokens: usage.TokenTotals{Input: 7, Output: 8, CacheRead: 9, CacheCreate: 10},
			Models: []string{sessTestSmall},
		},
	}
}

func sessTestQuery() usage.SessionQuery {
	return usage.SessionQuery{Range: usage.Range{From: sessTestFrom, Until: sessTestUntil}}
}

func sessTestIDs(rows []usage.SessionRow) []string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestSessionsRows(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	page, err := s.Sessions(context.Background(), sessTestQuery())
	if err != nil {
		t.Fatal(err)
	}
	want := sessTestRows()
	if page.Total != len(want) || len(page.Items) != len(want) || page.NextCursor != "" {
		t.Fatalf("page = %d items, total %d, cursor %q; want %d rows on one page", len(page.Items), page.Total, page.NextCursor, len(want))
	}
	for _, row := range page.Items {
		sessTestEqual(t, "row "+row.ID, row, want[row.ID])
	}
	sessTestEqual(t, "coverage", page.Coverage, usage.Coverage{})
}

func TestSessionsOrderSearchAndProject(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	tests := []struct {
		name   string
		mutate func(*usage.SessionQuery)
		want   []string
	}{
		{"default order is latest first", func(*usage.SessionQuery) {}, []string{sessTestAlpha, sessTestDelta, sessTestBeta, sessTestOrphan}},
		{"last", func(q *usage.SessionQuery) { q.Sort = usage.SortLast }, []string{sessTestAlpha, sessTestDelta, sessTestBeta, sessTestOrphan}},
		{"tokens", func(q *usage.SessionQuery) { q.Sort = usage.SortTokens }, []string{sessTestBeta, sessTestAlpha, sessTestOrphan, sessTestDelta}},
		{"errors, ties by id", func(q *usage.SessionQuery) { q.Sort = usage.SortErrors }, []string{sessTestAlpha, sessTestBeta, sessTestDelta, sessTestOrphan}},
		{"cost, unknown cost last", func(q *usage.SessionQuery) { q.Sort = usage.SortCost }, []string{sessTestBeta, sessTestAlpha, sessTestDelta, sessTestOrphan}},
		{"project", func(q *usage.SessionQuery) { q.Project = sessTestProjB }, []string{sessTestBeta, sessTestOrphan}},
		{"unknown project", func(q *usage.SessionQuery) { q.Project = "proj-none" }, []string{}},
		{"search project path ignoring case", func(q *usage.SessionQuery) { q.Search = "ALPHA" }, []string{sessTestAlpha, sessTestDelta}},
		{"search title ignoring case", func(q *usage.SessionQuery) { q.Search = "refactor" }, []string{sessTestBeta}},
		{"search with surrounding spaces", func(q *usage.SessionQuery) { q.Search = "  notes " }, []string{sessTestDelta}},
		{"search with literal wildcards", func(q *usage.SessionQuery) { q.Search = "build_100%" }, []string{sessTestAlpha}},
		{"underscore is not a wildcard", func(q *usage.SessionQuery) { q.Search = "Fix_Build" }, []string{}},
		{"percent is not a wildcard", func(q *usage.SessionQuery) { q.Search = "%" }, []string{sessTestAlpha}},
		{"backslash is literal", func(q *usage.SessionQuery) { q.Search = `\` }, []string{}},
		{"search and project", func(q *usage.SessionQuery) { q.Search = "alpha"; q.Project = sessTestProjB }, []string{}},
		{"search longer than any label", func(q *usage.SessionQuery) { q.Search = strings.Repeat("a", usage.MaxLabelLen+1) }, []string{}},
		{"sub-agent labels are not searched", func(q *usage.SessionQuery) { q.Search = "Sub-agent" }, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := sessTestQuery()
			tt.mutate(&q)
			page, err := s.Sessions(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			sessTestEqual(t, "ids", sessTestIDs(page.Items), tt.want)
			if page.Total != len(tt.want) {
				t.Errorf("total = %d; want %d", page.Total, len(tt.want))
			}
			if page.Items == nil {
				t.Error("items must not be nil")
			}
		})
	}
}

func TestSessionsRangeBounds(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	alpha, old := sessTestRows()[sessTestAlpha], usage.SessionRow{
		ID: sessTestOld, Title: "Old work", ProjectKey: sessTestProjA, ProjectPath: "/work/alpha", Entrypoint: "cli",
		StartedAt: sessTestAt(-90 * time.Minute), LastAt: sessTestAt(-80 * time.Minute),
		Prompts: 1, ModelCalls: 1, Tokens: usage.TokenTotals{Input: 1, Output: 1},
		CostUSD: sessTestPtr(9.9), Models: []string{sessTestSmall},
	}
	// Only the rows before the seeded range: one model call, its failed tool
	// call and one compaction. Labels, times and cost describe the session.
	alpha.Prompts, alpha.ModelCalls, alpha.ToolCalls, alpha.ToolErrors, alpha.Subagents, alpha.Compactions = 0, 1, 1, 1, 0, 1
	alpha.Tokens = usage.TokenTotals{Input: 1000, Output: 1000, CacheRead: 1000, CacheCreate: 1000}
	alpha.Models = []string{sessTestLarge}
	page, err := s.Sessions(context.Background(), usage.SessionQuery{
		Range: usage.Range{From: sessTestAt(-2 * time.Hour), Until: sessTestFrom},
	})
	if err != nil {
		t.Fatal(err)
	}
	sessTestEqual(t, "rows before the range", page.Items, []usage.SessionRow{alpha, old})

	// The open end of the seeded range starts the next one.
	page, err = s.Sessions(context.Background(), usage.SessionQuery{
		Range: usage.Range{From: sessTestUntil, Until: sessTestUntil.Add(time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != sessTestAlpha || page.Items[0].Prompts != 1 || page.Items[0].ModelCalls != 1 {
		t.Fatalf("rows after the range = %s; want s-alpha with one prompt and one model call", sessTestJSON(t, page.Items))
	}
}

func TestSessionsPaging(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	ctx := context.Background()
	for _, order := range []usage.SessionSort{usage.SortLast, usage.SortTokens, usage.SortErrors, usage.SortCost} {
		q := sessTestQuery()
		q.Sort = order
		full, err := s.Sessions(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for limit := 1; limit <= len(full.Items)+1; limit++ {
			q.Limit, q.Cursor = limit, ""
			var got []usage.SessionRow
			pages := 0
			for {
				page, pageErr := s.Sessions(ctx, q)
				if pageErr != nil {
					t.Fatalf("sort %s limit %d: %v", order, limit, pageErr)
				}
				pages++
				if page.Total != len(full.Items) || len(page.Items) == 0 || len(page.Items) > limit {
					t.Fatalf("sort %s limit %d page %d: %d items, total %d", order, limit, pages, len(page.Items), page.Total)
				}
				got = append(got, page.Items...)
				if page.NextCursor == "" {
					break
				}
				q.Cursor = page.NextCursor
			}
			sessTestEqual(t, "pages of sort "+string(order), got, full.Items)
			if want := (len(full.Items) + limit - 1) / limit; pages != want {
				t.Errorf("sort %s limit %d: %d pages; want %d", order, limit, pages, want)
			}
		}
	}
}

func TestSessionsBadRequest(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	ctx := context.Background()
	first := sessTestQuery()
	first.Limit = 1
	page, err := s.Sessions(ctx, first)
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page cursor = %q, %v", page.NextCursor, err)
	}
	encode := func(v any) string {
		raw, encErr := json.Marshal(v)
		if encErr != nil {
			t.Fatal(encErr)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	tests := []struct {
		name   string
		mutate func(*usage.SessionQuery)
	}{
		{"unknown sort", func(q *usage.SessionQuery) { q.Sort = "newest" }},
		{"zero range", func(q *usage.SessionQuery) { q.Range = usage.Range{} }},
		{"empty range", func(q *usage.SessionQuery) { q.Range.Until = q.Range.From }},
		{"inverted range", func(q *usage.SessionQuery) { q.Range.From, q.Range.Until = q.Range.Until, q.Range.From }},
		{"negative limit", func(q *usage.SessionQuery) { q.Limit = -1 }},
		{"cursor is not base64", func(q *usage.SessionQuery) { q.Cursor = "!!" }},
		{"cursor is not JSON", func(q *usage.SessionQuery) { q.Cursor = base64.RawURLEncoding.EncodeToString([]byte("nope")) }},
		{"cursor of another sort", func(q *usage.SessionQuery) { q.Cursor, q.Sort = page.NextCursor, usage.SortTokens }},
		{"cursor of another version", func(q *usage.SessionQuery) {
			q.Cursor = encode(sessCursor{Version: sessCursorVersion + 1, Sort: usage.SortLast, ID: sessTestAlpha})
		}},
		{"cursor without an id", func(q *usage.SessionQuery) {
			q.Cursor = encode(sessCursor{Version: sessCursorVersion, Sort: usage.SortLast})
		}},
		{"cursor with unknown fields", func(q *usage.SessionQuery) {
			q.Cursor = encode(map[string]any{"v": sessCursorVersion, "s": usage.SortLast, "i": sessTestAlpha, "x": 1})
		}},
		{"cursor too long", func(q *usage.SessionQuery) { q.Cursor = strings.Repeat("A", sessMaxCursorLen+1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := sessTestQuery()
			tt.mutate(&q)
			if _, err := s.Sessions(ctx, q); !errors.Is(err, usage.ErrBadRequest) {
				t.Fatalf("error = %v; want usage.ErrBadRequest", err)
			}
		})
	}
}

func TestSessPageLimit(t *testing.T) {
	tests := []struct {
		in, want int
		bad      bool
	}{
		{in: 0, want: usage.DefaultPageSize},
		{in: 1, want: 1},
		{in: usage.MaxPageSize, want: usage.MaxPageSize},
		{in: usage.MaxPageSize + 1, want: usage.MaxPageSize},
		{in: -1, bad: true},
	}
	for _, tt := range tests {
		got, err := sessPageLimit(tt.in)
		if errors.Is(err, usage.ErrBadRequest) != tt.bad || got != tt.want {
			t.Errorf("sessPageLimit(%d) = %d, %v; want %d, bad request %v", tt.in, got, err, tt.want, tt.bad)
		}
	}
}

// sessTestAlphaDetail is the whole-session detail of s-alpha.
func sessTestAlphaDetail() usage.SessionDetail {
	session := sessTestRows()[sessTestAlpha]
	session.Prompts, session.ModelCalls, session.ToolCalls, session.ToolErrors, session.Compactions = 2, 6, 6, 3, 2
	session.Tokens = usage.TokenTotals{Input: 6116, Output: 6227, CacheRead: 6308, CacheCreate: 6049}
	return usage.SessionDetail{
		Session: session,
		Models: []usage.ModelUsage{
			{Model: sessTestLarge, Calls: 3, Sessions: 1, Tokens: usage.TokenTotals{Input: 6010, Output: 6020, CacheRead: 6300, CacheCreate: 6040}},
			{Model: sessTestSmall, Calls: 3, Sessions: 1, Tokens: usage.TokenTotals{Input: 106, Output: 207, CacheRead: 8, CacheCreate: 9}},
		},
		Tools: []usage.ToolUsage{
			{Name: usage.ToolNameBash, Category: usage.ToolBuiltin, Calls: 2, Errors: 2, Sessions: 1, P50Ms: sessTestPtr(1), P95Ms: sessTestPtr(1500)},
			{Name: usage.ToolNameSkill, Category: usage.ToolSkill, Calls: 1, Pending: 1, Sessions: 1},
			{Name: "WebFetch", Category: usage.ToolBuiltin, Calls: 1, Sessions: 1, P50Ms: sessTestPtr(850), P95Ms: sessTestPtr(850)},
			{Name: "mcp__issues__search", Category: usage.ToolMCP, MCPServer: "issues", Calls: 1, Errors: 1, Sessions: 1, P50Ms: sessTestPtr(100), P95Ms: sessTestPtr(100)},
			{Name: "mcp__plugin_devkit_chain__status", Category: usage.ToolMCP, MCPServer: "chain", Plugin: "devkit", Calls: 1, Sessions: 1, P50Ms: sessTestPtr(250), P95Ms: sessTestPtr(250)},
		},
		MCPServers: []usage.GroupUsage{
			{Name: "chain", Calls: 1, Sessions: 1, AvgMs: sessTestPtr(250)},
			{Name: "issues", Calls: 1, Errors: 1, Sessions: 1, AvgMs: sessTestPtr(100)},
		},
		Skills: []usage.GroupUsage{
			{Name: "devkit:deploy-check", Calls: 1, Sessions: 1, Tokens: &usage.TokenTotals{Input: 1, Output: 2, CacheRead: 3, CacheCreate: 4}},
			{Name: "notes", Sessions: 1, Tokens: &usage.TokenTotals{Input: 10, Output: 20, CacheRead: 300, CacheCreate: 40}},
		},
		Plugins: []usage.GroupUsage{
			{Name: "devkit", Calls: 2, Sessions: 1, Tokens: &usage.TokenTotals{Input: 1, Output: 2, CacheRead: 3, CacheCreate: 4}, AvgMs: sessTestPtr(250)},
		},
		Hooks: []usage.HookUsage{
			// Two errors and one block fail, as on the tools page.
			{Event: "PreToolUse", Name: "PreToolUse:Bash", Runs: 4, Failures: 3, AvgMs: sessTestPtr(50), MaxMs: sessTestPtr(90)},
			// An unknown outcome is not a failure.
			{Event: "Stop", Name: "Stop", Runs: 2, AvgMs: sessTestPtr(40), MaxMs: sessTestPtr(40)},
		},
		Agents: []usage.AgentRow{
			{ModelCalls: 4, ToolCalls: 5, ToolErrors: 3, Tokens: usage.TokenTotals{Input: 6011, Output: 6022, CacheRead: 6303, CacheCreate: 6044}, StartedAt: sessTestAt(-time.Hour), LastAt: sessTestUntil},
			{AgentID: "ag1", AgentType: "Explore", ModelCalls: 2, ToolCalls: 1, Tokens: usage.TokenTotals{Input: 105, Output: 205, CacheRead: 5, CacheCreate: 5}, StartedAt: sessTestAt(5 * time.Minute), LastAt: sessTestAt(6 * time.Minute)},
			{AgentID: "ag0"},
		},
		Turns: []usage.TurnPoint{
			{At: sessTestAt(-time.Millisecond), Model: sessTestLarge, Context: 3000, Output: 1000, ToolCalls: 1},
			{At: sessTestAt(time.Minute), Model: sessTestLarge, Context: 350, Output: 20, ToolCalls: 2},
			{At: sessTestAt(2 * time.Minute), Model: sessTestSmall, Context: 8, Output: 2, ToolCalls: 2},
			{At: sessTestUntil, Model: sessTestLarge, Context: 15000, Output: 5000},
		},
		ExtSources: []usage.GroupUsage{
			{Name: "deploy-script", Calls: 2, Errors: 1, Sessions: 1, AvgMs: sessTestPtr(200)},
			{Name: "linter", Calls: 1, Sessions: 1},
		},
	}
}

func TestSessionDetail(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	got, err := s.Session(context.Background(), sessTestAlpha)
	if err != nil {
		t.Fatal(err)
	}
	want := sessTestAlphaDetail()
	sessTestEqual(t, "session", got.Session, want.Session)
	sessTestEqual(t, "models", got.Models, want.Models)
	sessTestEqual(t, "tools", got.Tools, want.Tools)
	sessTestEqual(t, "mcp servers", got.MCPServers, want.MCPServers)
	sessTestEqual(t, "skills", got.Skills, want.Skills)
	sessTestEqual(t, "plugins", got.Plugins, want.Plugins)
	sessTestEqual(t, "hooks", got.Hooks, want.Hooks)
	sessTestEqual(t, "agents", got.Agents, want.Agents)
	sessTestEqual(t, "turns", got.Turns, want.Turns)
	sessTestEqual(t, "extension sources", got.ExtSources, want.ExtSources)
	if got.TurnsTruncated || got.Source != "" {
		t.Errorf("truncated = %v, source = %q; want all turns and an empty source", got.TurnsTruncated, got.Source)
	}
}

func TestSessionDetailTurnCap(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	all := sessTestAlphaDetail().Turns
	tests := []struct {
		maxTurns  int
		truncated bool
	}{
		{maxTurns: 0, truncated: true},
		{maxTurns: 2, truncated: true},
		{maxTurns: len(all), truncated: false},
		{maxTurns: len(all) + 1, truncated: false},
	}
	for _, tt := range tests {
		got, err := s.sessDetail(context.Background(), sessTestAlpha, tt.maxTurns)
		if err != nil {
			t.Fatal(err)
		}
		sessTestEqual(t, "turns", got.Turns, all[:min(tt.maxTurns, len(all))])
		if got.TurnsTruncated != tt.truncated {
			t.Errorf("cap %d: truncated = %v; want %v", tt.maxTurns, got.TurnsTruncated, tt.truncated)
		}
		// The cap drops chart points only; the totals still cover every call.
		sessTestEqual(t, "models", got.Models, sessTestAlphaDetail().Models)
	}
}

func TestSessionDetailWithoutRootStream(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	got, err := s.Session(context.Background(), sessTestOrphan)
	if err != nil {
		t.Fatal(err)
	}
	sessTestEqual(t, "session", got.Session, sessTestRows()[sessTestOrphan])
	sessTestEqual(t, "agents", got.Agents, []usage.AgentRow{{
		AgentID: "ag9", AgentType: "general-purpose", ModelCalls: 1,
		Tokens:    usage.TokenTotals{Input: 7, Output: 8, CacheRead: 9, CacheCreate: 10},
		StartedAt: sessTestAt(30 * time.Minute), LastAt: sessTestAt(31 * time.Minute),
	}})
	for name, n := range map[string]int{
		"turns": len(got.Turns), "tools": len(got.Tools), "mcp servers": len(got.MCPServers), "skills": len(got.Skills),
		"plugins": len(got.Plugins), "hooks": len(got.Hooks), "extension sources": len(got.ExtSources),
	} {
		if n != 0 {
			t.Errorf("%s has %d rows; want none", name, n)
		}
	}
	if got.Turns == nil || got.Tools == nil || got.MCPServers == nil || got.Skills == nil || got.Plugins == nil || got.Hooks == nil || got.ExtSources == nil {
		t.Error("empty detail lists must not be nil")
	}
}

// The detail row and the list row of a range that holds the whole session
// come from one definition and must agree.
func TestSessionDetailMatchesList(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	ctx := context.Background()
	page, err := s.Sessions(ctx, usage.SessionQuery{
		Range: usage.Range{From: sessTestFrom.AddDate(-1, 0, 0), Until: sessTestUntil.AddDate(1, 0, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 5 {
		t.Fatalf("sessions of all time = %d; want 5", len(page.Items))
	}
	for _, row := range page.Items {
		detail, detailErr := s.Session(ctx, row.ID)
		if detailErr != nil {
			t.Fatalf("session %s: %v", row.ID, detailErr)
		}
		sessTestEqual(t, "session "+row.ID, detail.Session, row)
	}
}

func TestSessionNotFound(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	for _, id := range []string{"s-none", ""} {
		if _, err := s.Session(context.Background(), id); !errors.Is(err, usage.ErrNotFound) {
			t.Errorf("Session(%q) error = %v; want usage.ErrNotFound", id, err)
		}
	}
}

func TestStreamFile(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	tests := []struct {
		session, agent, want string
		ok                   bool
	}{
		{sessTestAlpha, "", "proj-a/s-alpha.jsonl", true},
		{sessTestAlpha, "ag1", "proj-a/s-alpha/subagents/agent-ag1.jsonl", true},
		{sessTestOrphan, "ag9", "proj-b/s-orphan/subagents/agent-ag9.jsonl", true},
		{sessTestOrphan, "", "", false},
		{sessTestAlpha, "ag-none", "", false},
		{"s-none", "", "", false},
	}
	for _, tt := range tests {
		got, ok, err := s.StreamFile(context.Background(), tt.session, tt.agent)
		if err != nil || ok != tt.ok || got != tt.want {
			t.Errorf("StreamFile(%q, %q) = %q, %v, %v; want %q, %v", tt.session, tt.agent, got, ok, err, tt.want, tt.ok)
		}
	}
}

func TestSessionReadsOnEmptyStore(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	page, err := s.Sessions(ctx, sessTestQuery())
	if err != nil {
		t.Fatal(err)
	}
	if page.Items == nil || len(page.Items) != 0 || page.Total != 0 || page.NextCursor != "" {
		t.Errorf("empty store page = %s; want an empty, non-nil list", sessTestJSON(t, page))
	}
	if _, err = s.Session(ctx, sessTestAlpha); !errors.Is(err, usage.ErrNotFound) {
		t.Errorf("Session error = %v; want usage.ErrNotFound", err)
	}
	if path, ok, fileErr := s.StreamFile(ctx, sessTestAlpha, ""); fileErr != nil || ok || path != "" {
		t.Errorf("StreamFile = %q, %v, %v; want no stream", path, ok, fileErr)
	}
}

func TestSessionReadsReportQueryFailure(t *testing.T) {
	s := openTest(t)
	sessTestSeed(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Sessions(ctx, sessTestQuery()); !errors.Is(err, context.Canceled) {
		t.Errorf("Sessions error = %v; want context.Canceled", err)
	}
	if _, err := s.Session(ctx, sessTestAlpha); !errors.Is(err, context.Canceled) {
		t.Errorf("Session error = %v; want context.Canceled", err)
	}
	if _, _, err := s.StreamFile(ctx, sessTestAlpha, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("StreamFile error = %v; want context.Canceled", err)
	}
}
