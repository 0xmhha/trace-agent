package db

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func TestToolsWorld(t *testing.T) {
	s := openTest(t)
	aggSeedWorld(t, s)
	got, err := s.Tools(context.Background(), aggWeek)
	if err != nil {
		t.Fatal(err)
	}
	want := usage.Tools{
		Range: aggWeek.Range,
		Tools: aggWorldTools(),
		MCPServers: []usage.GroupUsage{
			{Name: "files", Calls: 1, Sessions: 1, AvgMs: aggF(700)},
			{Name: "plugin_devkit_search", Calls: 1, Errors: 1, Sessions: 1, AvgMs: aggF(300)},
		},
		Skills: []usage.GroupUsage{
			// "devkit:deploy" and "deploy" are one skill; its tokens come from
			// the model call attributed to "deploy".
			{Name: "deploy", Calls: 2, Sessions: 2, Tokens: &usage.TokenTotals{Input: 10, Output: 20, CacheRead: 300}, AvgMs: aggF(100)},
			// Attributed model calls without a Skill call in the range.
			{Name: "review", Sessions: 1, Tokens: &usage.TokenTotals{Input: 1, Output: 2, CacheRead: 3, CacheCreate: 4}},
		},
		Plugins: []usage.GroupUsage{
			{Name: "devkit", Calls: 2, Errors: 1, Sessions: 1, Tokens: &usage.TokenTotals{Input: 10, Output: 20, CacheRead: 300}, AvgMs: aggF(175)},
		},
		Subagents: []usage.GroupUsage{
			{Name: "Explore", Calls: 1, Sessions: 1, AvgMs: aggF(9000)},
		},
		Programs: []usage.GroupUsage{
			{Name: "git", Calls: 2, Errors: 1, Sessions: 1, AvgMs: aggF(1250)},
			{Name: "go", Calls: 1, Sessions: 1, AvgMs: aggF(1000)},
		},
		Hooks: []usage.HookUsage{
			// Blocked and error runs fail; the run without a duration does
			// not lower the average.
			{Event: "PreToolUse", Name: "lint", Runs: 3, Failures: 2, AvgMs: aggF(200), MaxMs: aggF(300)},
			// An unknown outcome is not a failure, and no run reported a duration.
			{Event: "Stop", Name: "notify", Runs: 2},
		},
	}
	aggEqualJSON(t, "tools", got, want)
	if got.Coverage.State != "" || got.Coverage.Reasons != nil {
		t.Errorf("coverage = %+v; the store must leave it zero", got.Coverage)
	}
}

func TestToolsProjectFilter(t *testing.T) {
	s := openTest(t)
	aggSeedWorld(t, s)
	q := aggWeek
	q.Project = "beta"
	got, err := s.Tools(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	aggEqualJSON(t, "beta tools", got, usage.Tools{
		Range: q.Range,
		Tools: []usage.ToolUsage{
			{Name: "Bash", Category: usage.ToolBuiltin, Calls: 1, Sessions: 1, P50Ms: aggF(1000), P95Ms: aggF(1000)},
			{Name: "Skill", Category: usage.ToolSkill, Calls: 1, Sessions: 1, P50Ms: aggF(150), P95Ms: aggF(150)},
		},
		MCPServers: []usage.GroupUsage{},
		Skills: []usage.GroupUsage{
			{Name: "deploy", Calls: 1, Sessions: 1, Tokens: &usage.TokenTotals{}, AvgMs: aggF(150)},
			{Name: "review", Sessions: 1, Tokens: &usage.TokenTotals{Input: 1, Output: 2, CacheRead: 3, CacheCreate: 4}},
		},
		Plugins:   []usage.GroupUsage{},
		Subagents: []usage.GroupUsage{},
		Programs:  []usage.GroupUsage{{Name: "go", Calls: 1, Sessions: 1, AvgMs: aggF(1000)}},
		Hooks:     []usage.HookUsage{},
	})
}

func TestToolsEmptyStore(t *testing.T) {
	s := openTest(t)
	got, err := s.Tools(context.Background(), aggWeek)
	if err != nil {
		t.Fatal(err)
	}
	aggEqualJSON(t, "empty tools", got, usage.Tools{
		Range:      aggWeek.Range,
		Tools:      []usage.ToolUsage{},
		MCPServers: []usage.GroupUsage{},
		Skills:     []usage.GroupUsage{},
		Plugins:    []usage.GroupUsage{},
		Subagents:  []usage.GroupUsage{},
		Programs:   []usage.GroupUsage{},
		Hooks:      []usage.HookUsage{},
	})
}

func TestToolsErrors(t *testing.T) {
	s := openTest(t)
	tests := []struct {
		name string
		r    usage.Range
	}{
		{"empty range", usage.Range{From: aggFrom, Until: aggFrom}},
		{"reversed range", usage.Range{From: aggUntil, Until: aggFrom}},
		{"zero range", usage.Range{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Tools(context.Background(), usage.Query{Range: tc.r}); !errors.Is(err, usage.ErrBadRequest) {
				t.Fatalf("error = %v; want usage.ErrBadRequest", err)
			}
		})
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tools(context.Background(), aggWeek); err == nil || errors.Is(err, usage.ErrBadRequest) {
		t.Fatalf("error = %v; want a store failure", err)
	}
}

// Lists are capped, and ties in the count are broken by name.
func TestToolsCapsAndOrdersRows(t *testing.T) {
	s := openTest(t)
	const extra = 5
	batch := usage.Batch{Session: usage.SessionPatch{SessionID: "s1", FirstAt: aggFrom, LastAt: aggFrom}, NextOffset: 1}
	for i := range aggMaxRows + extra {
		batch.ToolCalls = append(batch.ToolCalls, usage.ToolCall{
			Key: fmt.Sprintf("c%03d", i), SessionID: "s1", At: aggFrom, Category: usage.ToolBuiltin,
			Name: usage.ToolNameBash, Target: fmt.Sprintf("prog%03d", i),
		})
	}
	// One more call of the last program lifts it to the top of the list.
	last := fmt.Sprintf("prog%03d", aggMaxRows+extra-1)
	batch.ToolCalls = append(batch.ToolCalls, usage.ToolCall{
		Key: "again", SessionID: "s1", At: aggFrom, Category: usage.ToolBuiltin, Name: usage.ToolNameBash, Target: last,
	})
	seedBatch(t, s, usage.FileRef{Path: "p/s1.jsonl", ProjectKey: "p", Size: 1, ModTime: testNow}, batch)
	got, err := s.Tools(context.Background(), aggWeek)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Programs) != aggMaxRows {
		t.Fatalf("programs = %d rows; want the cap of %d", len(got.Programs), aggMaxRows)
	}
	if first := got.Programs[0]; first.Name != last || first.Calls != 2 || first.AvgMs != nil {
		t.Errorf("first program = %+v; want %s with 2 pending calls", first, last)
	}
	for i, program := range got.Programs[1:] {
		if want := fmt.Sprintf("prog%03d", i); program.Name != want || program.Calls != 1 {
			t.Fatalf("program %d = %+v; want %s with 1 call", i+1, program, want)
		}
	}
}

func TestAggBareName(t *testing.T) {
	s := openTest(t)
	tests := []struct{ in, want string }{
		{"deploy", "deploy"},
		{"devkit:deploy", "deploy"},
		{"market:devkit:deploy", "deploy"},
		{"devkit:", ""},
		{":deploy", "deploy"},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			var got string
			err := s.db.QueryRowContext(context.Background(),
				"SELECT "+aggBareName("x.v")+" FROM (SELECT ? AS v) x", tc.in).Scan(&got)
			if err != nil || got != tc.want {
				t.Fatalf("bare name of %q = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}
