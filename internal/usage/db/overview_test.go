package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// The aggregate tests query one synthetic week. Rows are placed one
// millisecond before aggFrom and exactly on aggUntil to prove that the range
// is [from, until).
var (
	aggFrom  = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	aggUntil = time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)
	aggWeek  = usage.Query{Range: usage.Range{From: aggFrom, Until: aggUntil}}
)

// aggAt is a time on a day of March 2026 in UTC.
func aggAt(day, hour, minute, second int) time.Time {
	return time.Date(2026, 3, day, hour, minute, second, 0, time.UTC)
}

func aggF(v float64) *float64 { return &v }

// aggEqualJSON compares two values by their JSON encoding, which follows
// pointers and tells a nil list from an empty one, as the UI does.
func aggEqualJSON(t *testing.T, what string, got, want any) {
	t.Helper()
	g, err := json.MarshalIndent(got, "", " ")
	if err != nil {
		t.Fatalf("%s: encode got: %v", what, err)
	}
	w, err := json.MarshalIndent(want, "", " ")
	if err != nil {
		t.Fatalf("%s: encode want: %v", what, err)
	}
	if !bytes.Equal(g, w) {
		t.Errorf("%s mismatch\n got: %s\nwant: %s", what, g, w)
	}
}

// aggHeat builds the 168 heatmap cells with the given [weekday, hour] counts.
func aggHeat(counts map[[2]int]int64) []usage.HeatCell {
	cells := make([]usage.HeatCell, 0, 7*24)
	for weekday := range 7 {
		for hour := range 24 {
			cells = append(cells, usage.HeatCell{Weekday: weekday, Hour: hour, Prompts: counts[[2]int{weekday, hour}]})
		}
	}
	return cells
}

// aggSeedWorld stores two active projects, an idle one and an excluded one:
//
//	alpha  s1  root transcript plus sub-agents a1 (in range) and a2 (outside)
//	beta   s2  one prompt, one model call, two tool calls, no cost
//	beta   s4  a prompt and a cost, no model call
//	delta  s3  everything after the range
//	gamma      excluded, no rows
func aggSeedWorld(t *testing.T, s *Store) {
	t.Helper()
	before, after := aggFrom.Add(-time.Millisecond), aggUntil
	call := func(key, name string, category usage.ToolCategory, at time.Time) usage.ToolCall {
		return usage.ToolCall{Key: key, SessionID: "s1", At: at, Name: name, Category: category}
	}
	bash := func(key, target string, at time.Time) usage.ToolCall {
		c := call(key, usage.ToolNameBash, usage.ToolBuiltin, at)
		c.Target = target
		return c
	}
	mcp := call("t5", "mcp__plugin_devkit_search__query", usage.ToolMCP, aggAt(2, 10, 17, 0))
	mcp.MCPServer, mcp.MCPTool, mcp.Plugin = "plugin_devkit_search", "query", "devkit"
	skill := call("t7", usage.ToolNameSkill, usage.ToolSkill, aggAt(2, 10, 18, 0))
	skill.Skill, skill.Plugin = "devkit:deploy", "devkit"
	agent := call("t8", usage.ToolNameAgent, usage.ToolAgent, aggAt(2, 10, 19, 0))
	agent.SubagentType = "Explore"
	hook := func(key, event, name string, status usage.HookStatus, ms int64, at time.Time) usage.HookRun {
		return usage.HookRun{Key: key, SessionID: "s1", At: at, Event: event, Name: name, Status: status, DurationMs: ms}
	}
	seedBatch(t, s, usage.FileRef{Path: "alpha/s1.jsonl", ProjectKey: "alpha", Size: 100, ModTime: testNow}, usage.Batch{
		Session: usage.SessionPatch{SessionID: "s1", ProjectPath: "/work/alpha", Title: "one", FirstAt: before, LastAt: after},
		Prompts: []usage.Prompt{
			{Key: "p-before", SessionID: "s1", At: before},
			{Key: "p1", SessionID: "s1", At: aggFrom},
			{Key: "p2", SessionID: "s1", At: aggAt(2, 10, 15, 0)},
			{Key: "p-after", SessionID: "s1", At: after},
		},
		ModelCalls: []usage.ModelCall{
			{Key: "m-before", SessionID: "s1", At: before, Model: "model-a", Input: 7000, Output: 7000, CacheRead: 7000, CacheCreate: 7000},
			{Key: "m1", SessionID: "s1", At: aggAt(1, 0, 0, 5), Model: "model-a", Input: 100, Output: 50, CacheRead: 1000, CacheCreate: 200},
			{Key: "m2", SessionID: "s1", At: aggAt(2, 10, 16, 0), Model: "model-a", Input: 10, Output: 20, CacheRead: 300, Skill: "deploy", Plugin: "devkit"},
			{Key: "m-after", SessionID: "s1", At: after, Model: "model-a", Input: 9000, Output: 9000, CacheRead: 9000, CacheCreate: 9000},
		},
		ToolCalls: []usage.ToolCall{
			call("t-before", "Read", usage.ToolBuiltin, before),
			bash("t1", "git", aggAt(1, 0, 0, 10)),
			bash("t2", "git", aggAt(2, 10, 16, 10)),
			bash("t3", "", aggAt(2, 10, 16, 20)),
			call("t4", "Read", usage.ToolBuiltin, aggAt(2, 10, 16, 30)),
			mcp,
			skill,
			agent,
			call("t9", "Write", usage.ToolBuiltin, aggAt(2, 10, 19, 30)),
			bash("t-after", "git", after),
		},
		ToolResults: []usage.ToolResult{
			// The call is outside the range, so its failure must not count
			// although the result itself is inside.
			{Key: "t-before", SessionID: "s1", At: aggFrom.Add(time.Second), IsError: true},
			// No reported duration: derived from the two timestamps.
			{Key: "t1", SessionID: "s1", At: aggAt(1, 0, 0, 12)},
			{Key: "t2", SessionID: "s1", At: aggAt(2, 10, 16, 11), IsError: true, DurationMs: 500},
			{Key: "t4", SessionID: "s1", At: aggAt(2, 10, 16, 31), DurationMs: 100},
			{Key: "t5", SessionID: "s1", At: aggAt(2, 10, 17, 1), IsError: true, DurationMs: 300},
			{Key: "t7", SessionID: "s1", At: aggAt(2, 10, 18, 1), DurationMs: 50},
			{Key: "t8", SessionID: "s1", At: aggAt(2, 10, 19, 9), DurationMs: 9000},
			{Key: "t-after", SessionID: "s1", At: after.Add(time.Second), IsError: true, DurationMs: 10},
		},
		Hooks: []usage.HookRun{
			hook("h-before", "PreToolUse", "lint", usage.HookError, 5000, before),
			hook("h1", "PreToolUse", "lint", usage.HookSuccess, 100, aggAt(1, 0, 0, 9)),
			hook("h2", "PreToolUse", "lint", usage.HookError, 300, aggAt(2, 10, 16, 9)),
			hook("h3", "PreToolUse", "lint", usage.HookBlocked, 0, aggAt(2, 10, 16, 19)),
			hook("h4", "Stop", "notify", usage.HookSuccess, 0, aggAt(2, 10, 30, 0)),
			hook("h5", "Stop", "notify", usage.HookUnknown, 0, aggAt(2, 10, 31, 0)),
			hook("h-after", "Stop", "notify", usage.HookError, 1, after),
		},
		Compactions: []usage.Compaction{
			{Key: "k-before", SessionID: "s1", At: before},
			{Key: "k1", SessionID: "s1", At: aggAt(2, 11, 0, 0), Trigger: "auto", PreTokens: 1000},
			{Key: "k-after", SessionID: "s1", At: after},
		},
		Cost:       &usage.CostState{SessionID: "s1", At: aggAt(2, 11, 0, 0), TotalCostUSD: 1.25},
		NextOffset: 100,
	})
	files := call("t6", "mcp__files__list", usage.ToolMCP, aggAt(2, 10, 20, 10))
	files.AgentID, files.MCPServer, files.MCPTool = "a1", "files", "list"
	seedBatch(t, s, usage.FileRef{Path: "alpha/s1/subagents/agent-a1.jsonl", ProjectKey: "alpha", Size: 50, ModTime: testNow}, usage.Batch{
		Session: usage.SessionPatch{SessionID: "s1", AgentID: "a1", AgentType: "Explore", FirstAt: aggAt(2, 10, 20, 0), LastAt: aggAt(2, 10, 21, 0)},
		ModelCalls: []usage.ModelCall{
			{Key: "ma1", SessionID: "s1", AgentID: "a1", At: aggAt(2, 10, 20, 0), Model: "model-b", Input: 40, Output: 10, CacheCreate: 60},
		},
		ToolCalls:   []usage.ToolCall{files},
		ToolResults: []usage.ToolResult{{Key: "t6", SessionID: "s1", At: aggAt(2, 10, 20, 11), DurationMs: 700}},
		NextOffset:  50,
	})
	seedBatch(t, s, usage.FileRef{Path: "alpha/s1/subagents/agent-a2.jsonl", ProjectKey: "alpha", Size: 50, ModTime: testNow}, usage.Batch{
		Session: usage.SessionPatch{SessionID: "s1", AgentID: "a2", AgentType: "Plan", FirstAt: after, LastAt: after},
		ModelCalls: []usage.ModelCall{
			{Key: "ma2", SessionID: "s1", AgentID: "a2", At: after.Add(time.Hour), Model: "model-b", Input: 8000},
		},
		NextOffset: 50,
	})
	seedBatch(t, s, usage.FileRef{Path: "beta/s2.jsonl", ProjectKey: "beta", Size: 80, ModTime: testNow}, usage.Batch{
		Session: usage.SessionPatch{SessionID: "s2", ProjectPath: "/work/beta", FirstAt: aggAt(3, 9, 0, 0), LastAt: aggAt(3, 9, 4, 0)},
		Prompts: []usage.Prompt{{Key: "q1", SessionID: "s2", At: aggAt(3, 9, 0, 0)}},
		ModelCalls: []usage.ModelCall{
			{Key: "n1", SessionID: "s2", At: aggAt(3, 9, 1, 0), Model: "model-b", Input: 1, Output: 2, CacheRead: 3, CacheCreate: 4, Skill: "review"},
		},
		ToolCalls: []usage.ToolCall{
			{Key: "u1", SessionID: "s2", At: aggAt(3, 9, 2, 0), Name: usage.ToolNameBash, Category: usage.ToolBuiltin, Target: "go"},
			{Key: "u2", SessionID: "s2", At: aggAt(3, 9, 3, 0), Name: usage.ToolNameSkill, Category: usage.ToolSkill, Skill: "deploy"},
		},
		ToolResults: []usage.ToolResult{
			{Key: "u1", SessionID: "s2", At: aggAt(3, 9, 2, 1), DurationMs: 1000},
			{Key: "u2", SessionID: "s2", At: aggAt(3, 9, 3, 1), DurationMs: 150},
		},
		NextOffset: 80,
	})
	seedBatch(t, s, usage.FileRef{Path: "beta/s4.jsonl", ProjectKey: "beta", Size: 20, ModTime: testNow}, usage.Batch{
		Session:    usage.SessionPatch{SessionID: "s4", ProjectPath: "/work/beta", FirstAt: aggAt(5, 23, 30, 0), LastAt: aggAt(5, 23, 31, 0)},
		Prompts:    []usage.Prompt{{Key: "r1", SessionID: "s4", At: aggAt(5, 23, 30, 0)}},
		Cost:       &usage.CostState{SessionID: "s4", At: aggAt(5, 23, 31, 0), TotalCostUSD: 0.75},
		NextOffset: 20,
	})
	seedBatch(t, s, usage.FileRef{Path: "delta/s3.jsonl", ProjectKey: "delta", Size: 30, ModTime: testNow}, usage.Batch{
		Session:    usage.SessionPatch{SessionID: "s3", ProjectPath: "/work/delta", FirstAt: aggAt(9, 8, 0, 0), LastAt: aggAt(9, 8, 1, 0)},
		Prompts:    []usage.Prompt{{Key: "d1", SessionID: "s3", At: aggAt(9, 8, 0, 0)}},
		ModelCalls: []usage.ModelCall{{Key: "dm1", SessionID: "s3", At: aggAt(9, 8, 1, 0), Model: "model-c", Input: 6000}},
		Cost:       &usage.CostState{SessionID: "s3", At: aggAt(9, 8, 1, 0), TotalCostUSD: 5},
		NextOffset: 30,
	})
	if _, err := s.db.ExecContext(context.Background(),
		"INSERT INTO project(project_key, path, excluded) VALUES('gamma', '/work/gamma', 1)"); err != nil {
		t.Fatalf("seed excluded project: %v", err)
	}
}

// aggWorldTools is the per-tool list of the seeded week, shared by the
// overview and the tools page.
func aggWorldTools() []usage.ToolUsage {
	return []usage.ToolUsage{
		{Name: "Bash", Category: usage.ToolBuiltin, Calls: 4, Errors: 1, Pending: 1, Sessions: 2, P50Ms: aggF(1000), P95Ms: aggF(2000)},
		// One call names a plugin skill and one does not, so no plugin is shared.
		{Name: "Skill", Category: usage.ToolSkill, Calls: 2, Sessions: 2, P50Ms: aggF(50), P95Ms: aggF(150)},
		{Name: "Agent", Category: usage.ToolAgent, Calls: 1, Sessions: 1, P50Ms: aggF(9000), P95Ms: aggF(9000)},
		{Name: "Read", Category: usage.ToolBuiltin, Calls: 1, Sessions: 1, P50Ms: aggF(100), P95Ms: aggF(100)},
		{Name: "Write", Category: usage.ToolBuiltin, Calls: 1, Pending: 1, Sessions: 1},
		{Name: "mcp__files__list", Category: usage.ToolMCP, MCPServer: "files", Calls: 1, Sessions: 1, P50Ms: aggF(700), P95Ms: aggF(700)},
		{Name: "mcp__plugin_devkit_search__query", Category: usage.ToolMCP, MCPServer: "plugin_devkit_search", Plugin: "devkit", Calls: 1, Errors: 1, Sessions: 1, P50Ms: aggF(300), P95Ms: aggF(300)},
	}
}

func TestOverviewWorld(t *testing.T) {
	s := openTest(t)
	aggSeedWorld(t, s)
	got, err := s.Overview(context.Background(), aggWeek, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	day := func(d int, bin usage.Bin) usage.Bin {
		bin.From = aggAt(d, 0, 0, 0)
		return bin
	}
	want := usage.Overview{
		Range:         aggWeek.Range,
		Sessions:      3,
		Prompts:       4,
		ModelCalls:    4,
		ToolCalls:     11,
		ToolErrors:    2,
		Subagents:     1,
		Compactions:   1,
		ActiveDays:    4,
		Tokens:        usage.TokenTotals{Input: 151, Output: 82, CacheRead: 1303, CacheCreate: 264},
		CostUSD:       aggF(2),
		CostSessions:  2,
		CacheHitRatio: aggF(float64(1303) / float64(151+1303+264)),
		Series: []usage.Bin{
			day(1, usage.Bin{Tokens: usage.TokenTotals{Input: 100, Output: 50, CacheRead: 1000, CacheCreate: 200}, Sessions: 1, Prompts: 1, ToolCalls: 1}),
			day(2, usage.Bin{Tokens: usage.TokenTotals{Input: 50, Output: 30, CacheRead: 300, CacheCreate: 60}, Sessions: 1, Prompts: 1, ToolCalls: 8, ToolErrors: 2}),
			day(3, usage.Bin{Tokens: usage.TokenTotals{Input: 1, Output: 2, CacheRead: 3, CacheCreate: 4}, Sessions: 1, Prompts: 1, ToolCalls: 2}),
			day(4, usage.Bin{}),
			day(5, usage.Bin{Sessions: 1, Prompts: 1}),
			day(6, usage.Bin{}),
			day(7, usage.Bin{}),
		},
		Models: []usage.ModelUsage{
			{Model: "model-a", Calls: 2, Sessions: 1, Tokens: usage.TokenTotals{Input: 110, Output: 70, CacheRead: 1300, CacheCreate: 200}},
			{Model: "model-b", Calls: 2, Sessions: 2, Tokens: usage.TokenTotals{Input: 41, Output: 12, CacheRead: 3, CacheCreate: 64}},
		},
		Projects: []usage.ProjectUsage{
			{Key: "alpha", Path: "/work/alpha", Sessions: 1, Prompts: 2, Tokens: usage.TokenTotals{Input: 150, Output: 80, CacheRead: 1300, CacheCreate: 260}, LastAt: aggAt(2, 10, 20, 0)},
			{Key: "beta", Path: "/work/beta", Sessions: 2, Prompts: 2, Tokens: usage.TokenTotals{Input: 1, Output: 2, CacheRead: 3, CacheCreate: 4}, LastAt: aggAt(5, 23, 30, 0)},
		},
		Tools: aggWorldTools(),
		// 1 March 2026 is a Sunday.
		Heatmap: aggHeat(map[[2]int]int64{{0, 0}: 1, {1, 10}: 1, {2, 9}: 1, {4, 23}: 1}),
	}
	aggEqualJSON(t, "overview", got, want)
	if len(got.Heatmap) != 168 {
		t.Errorf("heatmap cells = %d; want 168", len(got.Heatmap))
	}
	if got.Coverage.State != "" || got.Coverage.Reasons != nil {
		t.Errorf("coverage = %+v; the store must leave it zero", got.Coverage)
	}
}

func TestOverviewProjectFilter(t *testing.T) {
	s := openTest(t)
	aggSeedWorld(t, s)
	q := aggWeek
	q.Project = "beta"
	got, err := s.Overview(context.Background(), q, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	beta := usage.TokenTotals{Input: 1, Output: 2, CacheRead: 3, CacheCreate: 4}
	// The lists are compared in full; the scalars and the series below.
	aggEqualJSON(t, "models", got.Models, []usage.ModelUsage{{Model: "model-b", Calls: 1, Sessions: 1, Tokens: beta}})
	aggEqualJSON(t, "projects", got.Projects, []usage.ProjectUsage{
		{Key: "beta", Path: "/work/beta", Sessions: 2, Prompts: 2, Tokens: beta, LastAt: aggAt(5, 23, 30, 0)},
	})
	aggEqualJSON(t, "tools", got.Tools, []usage.ToolUsage{
		{Name: "Bash", Category: usage.ToolBuiltin, Calls: 1, Sessions: 1, P50Ms: aggF(1000), P95Ms: aggF(1000)},
		{Name: "Skill", Category: usage.ToolSkill, Calls: 1, Sessions: 1, P50Ms: aggF(150), P95Ms: aggF(150)},
	})
	got.Models, got.Projects, got.Tools, got.Heatmap = nil, nil, nil, nil
	series := got.Series
	got.Series = nil
	aggEqualJSON(t, "scalars", got, usage.Overview{
		Range: q.Range, Sessions: 2, Prompts: 2, ModelCalls: 1, ToolCalls: 2, ActiveDays: 2,
		Tokens: beta, CostUSD: aggF(0.75), CostSessions: 1, CacheHitRatio: aggF(float64(3) / float64(8)),
	})
	if len(series) != 7 {
		t.Fatalf("series = %d bins; want 7", len(series))
	}
	for i, bin := range series {
		want := usage.Bin{From: aggAt(i+1, 0, 0, 0)}
		switch i {
		case 2:
			want.Tokens, want.Sessions, want.Prompts, want.ToolCalls = beta, 1, 1, 2
		case 4:
			want.Sessions, want.Prompts = 1, 1
		}
		aggEqualJSON(t, "series bin", bin, want)
	}
	q.Project = "missing"
	got, err = s.Overview(context.Background(), q, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != 0 || got.ToolCalls != 0 || got.CostUSD != nil || len(got.Projects) != 0 {
		t.Errorf("unknown project = %+v; want no activity", got)
	}
}

// aggSeedLocal stores one session whose rows straddle midnights of a zone
// nine hours ahead of UTC.
func aggSeedLocal(t *testing.T, s *Store) {
	t.Helper()
	seedBatch(t, s, usage.FileRef{Path: "east/k1.jsonl", ProjectKey: "east", Size: 10, ModTime: testNow}, usage.Batch{
		Session: usage.SessionPatch{SessionID: "k1", ProjectPath: "/work/east", FirstAt: aggAt(1, 16, 0, 0), LastAt: aggAt(3, 16, 0, 0)},
		Prompts: []usage.Prompt{
			{Key: "early", SessionID: "k1", At: aggAt(1, 16, 0, 0)},  // local 2 March 01:00, before the range
			{Key: "late", SessionID: "k1", At: aggAt(2, 14, 59, 0)},  // local 2 March 23:59
			{Key: "first", SessionID: "k1", At: aggAt(2, 15, 0, 0)},  // local 3 March 00:00
			{Key: "later", SessionID: "k1", At: aggAt(2, 23, 30, 0)}, // local 3 March 08:30
		},
		ModelCalls: []usage.ModelCall{
			{Key: "km", SessionID: "k1", At: aggAt(3, 16, 0, 0), Model: "model-a", Input: 5}, // local 4 March 01:00
		},
		NextOffset: 10,
	})
}

func TestOverviewLocalTime(t *testing.T) {
	s := openTest(t)
	aggSeedLocal(t, s)
	east := time.FixedZone("east", 9*60*60)
	// Local 2 March 05:00 until 4 March 12:00.
	q := usage.Query{Range: usage.Range{From: aggAt(1, 20, 0, 0), Until: aggAt(4, 3, 0, 0)}, BinHours: 24}
	got, err := s.Overview(context.Background(), q, east)
	if err != nil {
		t.Fatal(err)
	}
	// Buckets start at local midnight, which is 15:00 UTC the day before. The
	// first one starts before the range and still leaves the early prompt out.
	aggEqualJSON(t, "local series", got.Series, []usage.Bin{
		{From: aggAt(1, 15, 0, 0), Sessions: 1, Prompts: 1},
		{From: aggAt(2, 15, 0, 0), Sessions: 1, Prompts: 2},
		{From: aggAt(3, 15, 0, 0), Sessions: 1, Tokens: usage.TokenTotals{Input: 5}},
	})
	if got.ActiveDays != 3 {
		t.Errorf("local active days = %d; want 3", got.ActiveDays)
	}
	// 2 March 2026 is a Monday.
	aggEqualJSON(t, "local heatmap", got.Heatmap, aggHeat(map[[2]int]int64{{1, 23}: 1, {2, 0}: 1, {2, 8}: 1}))

	utc, err := s.Overview(context.Background(), q, nil)
	if err != nil {
		t.Fatal(err)
	}
	aggEqualJSON(t, "utc series", utc.Series, []usage.Bin{
		{From: aggAt(1, 0, 0, 0)},
		{From: aggAt(2, 0, 0, 0), Sessions: 1, Prompts: 3},
		{From: aggAt(3, 0, 0, 0), Sessions: 1, Tokens: usage.TokenTotals{Input: 5}},
		{From: aggAt(4, 0, 0, 0)},
	})
	if utc.ActiveDays != 2 {
		t.Errorf("utc active days = %d; want 2", utc.ActiveDays)
	}
	aggEqualJSON(t, "utc heatmap", utc.Heatmap, aggHeat(map[[2]int]int64{{1, 14}: 1, {1, 15}: 1, {1, 23}: 1}))
}

func TestOverviewWidensBins(t *testing.T) {
	s := openTest(t)
	aggSeedWorld(t, s)
	// 30 days of hourly buckets would be 720; doubling once gives 360.
	q := usage.Query{Range: usage.Range{From: aggAt(1, 0, 0, 0), Until: aggAt(31, 0, 0, 0)}, BinHours: 1}
	got, err := s.Overview(context.Background(), q, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Series) != 360 {
		t.Fatalf("series = %d bins; want 360", len(got.Series))
	}
	var sum usage.Bin
	for i, bin := range got.Series {
		if want := q.Range.From.Add(time.Duration(i) * 2 * time.Hour); !bin.From.Equal(want) {
			t.Fatalf("bin %d starts %v; want %v", i, bin.From, want)
		}
		aggAddBin(&sum, bin)
	}
	// Buckets partition the range, so they add up to the totals. Sessions are
	// distinct per bucket and are therefore left out of the comparison.
	sum.Sessions = 0
	aggEqualJSON(t, "series sum", sum, usage.Bin{Tokens: got.Tokens, Prompts: got.Prompts, ToolCalls: got.ToolCalls, ToolErrors: got.ToolErrors})
	// The month also holds the rows placed on the end of the seeded week.
	if got.Prompts != 6 || got.ToolCalls != 12 || got.ToolErrors != 3 {
		t.Errorf("month totals = %d prompts, %d tool calls, %d errors; want 6, 12, 3", got.Prompts, got.ToolCalls, got.ToolErrors)
	}
}

func TestOverviewEmptyStore(t *testing.T) {
	s := openTest(t)
	got, err := s.Overview(context.Background(), aggWeek, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	series := make([]usage.Bin, 7)
	for i := range series {
		series[i].From = aggAt(i+1, 0, 0, 0)
	}
	aggEqualJSON(t, "empty overview", got, usage.Overview{
		Range:    aggWeek.Range,
		Series:   series,
		Models:   []usage.ModelUsage{},
		Projects: []usage.ProjectUsage{},
		Tools:    []usage.ToolUsage{},
		Heatmap:  aggHeat(nil),
	})
}

func TestOverviewRejectsBadQueries(t *testing.T) {
	s := openTest(t)
	tests := []struct {
		name string
		q    usage.Query
	}{
		{"empty range", usage.Query{Range: usage.Range{From: aggFrom, Until: aggFrom}}},
		{"reversed range", usage.Query{Range: usage.Range{From: aggUntil, Until: aggFrom}}},
		{"zero range", usage.Query{}},
		{"negative bin", usage.Query{Range: aggWeek.Range, BinHours: -1}},
		{"bin above a year", usage.Query{Range: aggWeek.Range, BinHours: aggMaxBinHours + 1}},
		{"range beyond any series", usage.Query{Range: usage.Range{From: aggFrom, Until: aggFrom.AddDate(200_000, 0, 0)}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Overview(context.Background(), tc.q, time.UTC); !errors.Is(err, usage.ErrBadRequest) {
				t.Fatalf("error = %v; want usage.ErrBadRequest", err)
			}
		})
	}
}

func TestOverviewReportsStoreFailure(t *testing.T) {
	s := openTest(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := s.Overview(context.Background(), aggWeek, time.UTC)
	if err == nil || errors.Is(err, usage.ErrBadRequest) {
		t.Fatalf("error = %v; want a store failure", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = openTest(t).Overview(ctx, aggWeek, time.UTC); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v; want context.Canceled", err)
	}
}

func TestAggBinStarts(t *testing.T) {
	east := time.FixedZone("east", 9*60*60)
	half := time.FixedZone("half", 5*60*60+30*60)
	tests := []struct {
		name  string
		r     usage.Range
		width int
		loc   *time.Location
		limit int
		want  []time.Time
		over  bool
	}{
		{
			name: "daily in utc starts on the day of the range", width: 24, loc: time.UTC, limit: 10,
			r:    usage.Range{From: aggAt(1, 10, 0, 0), Until: aggAt(3, 0, 0, 1)},
			want: []time.Time{aggAt(1, 0, 0, 0), aggAt(2, 0, 0, 0), aggAt(3, 0, 0, 0)},
		},
		{
			name: "until is exclusive", width: 24, loc: time.UTC, limit: 10,
			r:    usage.Range{From: aggAt(1, 0, 0, 0), Until: aggAt(3, 0, 0, 0)},
			want: []time.Time{aggAt(1, 0, 0, 0), aggAt(2, 0, 0, 0)},
		},
		{
			name: "daily follows local midnight", width: 24, loc: east, limit: 10,
			r:    usage.Range{From: aggAt(1, 20, 0, 0), Until: aggAt(3, 14, 0, 0)},
			want: []time.Time{aggAt(1, 15, 0, 0), aggAt(2, 15, 0, 0)},
		},
		{
			name: "two-day buckets count calendar days", width: 48, loc: time.UTC, limit: 10,
			r:    usage.Range{From: aggAt(1, 10, 0, 0), Until: aggAt(6, 0, 0, 0)},
			want: []time.Time{aggAt(1, 0, 0, 0), aggAt(3, 0, 0, 0), aggAt(5, 0, 0, 0)},
		},
		{
			name: "hourly in a half-hour zone lands on local hours", width: 1, loc: half, limit: 10,
			r:    usage.Range{From: aggAt(1, 10, 0, 0), Until: aggAt(1, 13, 0, 0)},
			want: []time.Time{aggAt(1, 9, 30, 0), aggAt(1, 10, 30, 0), aggAt(1, 11, 30, 0), aggAt(1, 12, 30, 0)},
		},
		{
			name: "six-hour buckets step from local midnight", width: 6, loc: east, limit: 10,
			r:    usage.Range{From: aggAt(1, 20, 0, 0), Until: aggAt(2, 8, 0, 0)},
			want: []time.Time{aggAt(1, 15, 0, 0), aggAt(1, 21, 0, 0), aggAt(2, 3, 0, 0)},
		},
		{
			name: "buckets before the range are skipped", width: 6, loc: time.UTC, limit: 10,
			r:    usage.Range{From: aggAt(1, 13, 0, 0), Until: aggAt(1, 19, 0, 0)},
			want: []time.Time{aggAt(1, 12, 0, 0), aggAt(1, 18, 0, 0)},
		},
		{
			name: "exactly the limit fits", width: 24, loc: time.UTC, limit: 2,
			r:    usage.Range{From: aggAt(1, 0, 0, 0), Until: aggAt(3, 0, 0, 0)},
			want: []time.Time{aggAt(1, 0, 0, 0), aggAt(2, 0, 0, 0)},
		},
		{
			name: "one over the limit does not", width: 24, loc: time.UTC, limit: 2,
			r:    usage.Range{From: aggAt(1, 0, 0, 0), Until: aggAt(3, 0, 0, 1)},
			over: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := aggBinStarts(tc.r, tc.width, tc.loc, tc.limit)
			if ok == tc.over {
				t.Fatalf("ok = %v; want %v", ok, !tc.over)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("starts = %v; want %v", got, tc.want)
			}
			for i := range got {
				if !got[i].Equal(tc.want[i]) {
					t.Errorf("start %d = %v; want %v", i, got[i].UTC(), tc.want[i])
				}
			}
		})
	}
}

// A calendar-day bucket is 23 hours long on the day clocks move forward.
func TestAggBinStartsAcrossClockChange(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("zone database not available: %v", err)
	}
	// Clocks move from 02:00 to 03:00 on 8 March 2026.
	r := usage.Range{From: time.Date(2026, 3, 7, 12, 0, 0, 0, zone), Until: time.Date(2026, 3, 10, 0, 0, 0, 0, zone)}
	got, ok := aggBinStarts(r, 24, zone, 10)
	want := []time.Time{aggAt(7, 5, 0, 0), aggAt(8, 5, 0, 0), aggAt(9, 4, 0, 0)}
	if !ok || len(got) != len(want) {
		t.Fatalf("starts = %v, %v; want %v", got, ok, want)
	}
	for i := range got {
		if !got[i].Equal(want[i]) {
			t.Errorf("start %d = %v; want %v", i, got[i].UTC(), want[i])
		}
	}
}

// A zone that starts summer time at midnight has a day without a midnight.
// That day begins at the transition, and no bucket may precede the range.
func TestAggBinStartsWithoutMidnight(t *testing.T) {
	zone, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("zone database not available: %v", err)
	}
	// Clocks moved from 00:00 to 01:00 on 4 November 2018, so that day began
	// at 03:00 UTC, like the day before, and the next one at 02:00 UTC.
	utc := func(day, hour int) time.Time { return time.Date(2018, 11, day, hour, 0, 0, 0, time.UTC) }
	tests := []struct {
		name string
		r    usage.Range
		want []time.Time
	}{
		{
			"range starting on the short day",
			usage.Range{From: time.Date(2018, 11, 4, 10, 0, 0, 0, zone), Until: time.Date(2018, 11, 6, 12, 0, 0, 0, zone)},
			[]time.Time{utc(4, 3), utc(5, 2), utc(6, 2)},
		},
		{
			"range crossing the short day",
			usage.Range{From: time.Date(2018, 11, 3, 12, 0, 0, 0, zone), Until: time.Date(2018, 11, 6, 0, 0, 0, 0, zone)},
			[]time.Time{utc(3, 3), utc(4, 3), utc(5, 2)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := aggBinStarts(tc.r, 24, zone, 10)
			if !ok || len(got) != len(tc.want) {
				t.Fatalf("starts = %v, %v; want %v", got, ok, tc.want)
			}
			for i := range got {
				if !got[i].Equal(tc.want[i]) {
					t.Errorf("start %d = %v; want %v", i, got[i].UTC(), tc.want[i])
				}
			}
		})
	}
}

func TestAggSeriesStarts(t *testing.T) {
	days := func(n int) usage.Range { return usage.Range{From: aggFrom, Until: aggFrom.AddDate(0, 0, n)} }
	tests := []struct {
		name      string
		r         usage.Range
		requested int
		bins      int
		width     time.Duration
		bad       bool
	}{
		{name: "two days are hourly", r: days(2), bins: 48, width: time.Hour},
		{name: "longer than two days is daily", r: usage.Range{From: aggFrom, Until: aggFrom.Add(49 * time.Hour)}, bins: 3, width: 24 * time.Hour},
		{name: "a year of days fits", r: days(365), bins: 365, width: 24 * time.Hour},
		{name: "four hundred days fit exactly", r: days(400), bins: 400, width: 24 * time.Hour},
		{name: "one more day doubles the width", r: days(401), bins: 201, width: 48 * time.Hour},
		{name: "doubling repeats until it fits", r: days(801), bins: 201, width: 96 * time.Hour},
		{name: "a requested width is kept", r: days(7), requested: 12, bins: 14, width: 12 * time.Hour},
		{name: "a requested width is widened too", r: days(30), requested: 1, bins: 360, width: 2 * time.Hour},
		{name: "the widest request is one bucket a year", r: days(365), requested: aggMaxBinHours, bins: 1, width: 365 * 24 * time.Hour},
		// 201 steps of this width do not fit one time.Duration.
		{name: "odd widths survive centuries", r: usage.Range{From: aggFrom, Until: aggFrom.AddDate(400, 0, 0)}, requested: aggMaxBinHours - 1, bins: 201, width: 2 * (aggMaxBinHours - 1) * time.Hour},
		{name: "negative width", r: days(7), requested: -24, bad: true},
		{name: "width above a year", r: days(7), requested: aggMaxBinHours + 1, bad: true},
		{name: "range beyond any bucket", r: usage.Range{From: aggFrom, Until: aggFrom.AddDate(200_000, 0, 0)}, bad: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := aggSeriesStarts(tc.r, tc.requested, time.UTC)
			if tc.bad {
				if !errors.Is(err, usage.ErrBadRequest) {
					t.Fatalf("error = %v; want usage.ErrBadRequest", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.bins {
				t.Fatalf("bins = %d; want %d", len(got), tc.bins)
			}
			if !got[0].Equal(tc.r.From) {
				t.Errorf("first bin = %v; want %v", got[0], tc.r.From)
			}
			if len(got) > 1 && got[1].Sub(got[0]) != tc.width {
				t.Errorf("width = %v; want %v", got[1].Sub(got[0]), tc.width)
			}
			for i := 1; i < len(got); i++ {
				if !got[i].After(got[i-1]) {
					t.Fatalf("bin %d starts %v, not after %v", i, got[i], got[i-1])
				}
			}
			if last := got[len(got)-1]; !last.Before(tc.r.Until) {
				t.Errorf("last bin starts %v, outside the range", last)
			}
		})
	}
}
