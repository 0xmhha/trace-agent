package db

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

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
			// "deploy" comes from no plugin and "devkit:deploy" from one: they
			// are two skills. No model call is attributed to the first.
			{Name: "deploy", Calls: 1, Sessions: 1, Tokens: &usage.TokenTotals{}, AvgMs: aggF(150)},
			// The plugin skill, with the tokens of the model call attributed
			// to its full name.
			{Name: "devkit:deploy", Calls: 1, Sessions: 1, Tokens: &usage.TokenTotals{Input: 10, Output: 20, CacheRead: 300}, AvgMs: aggF(50)},
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

// The Go and the SQL form of the skill group rule must name every attribution
// alike, or the two read paths drift apart again.
func TestSkillGroupRule(t *testing.T) {
	s := openTest(t)
	query := "SELECT " + skillGroupSQL("x.skill", "x.plugin") + " FROM (SELECT ? AS skill, ? AS plugin) x"
	tests := []struct{ name, skill, plugin, want string }{
		{"plugin skill by its full name", "devkit:deploy", "devkit", "devkit:deploy"},
		{"skill without a plugin", "deploy", "", "deploy"},
		{"bare name beside a plugin is qualified", "deploy", "devkit", "devkit:deploy"},
		{"full name without a plugin", "devkit:deploy", "", "devkit:deploy"},
		{"full name of another plugin is kept", "other:deploy", "devkit", "other:deploy"},
		{"name with two qualifiers is kept", "market:devkit:deploy", "devkit", "market:devkit:deploy"},
		{"trailing qualifier is kept", "devkit:", "devkit", "devkit:"},
		{"leading qualifier is kept", ":deploy", "devkit", ":deploy"},
		{"name outside ASCII", "d\u00e9ploiement", "bo\u00eete", "bo\u00eete:d\u00e9ploiement"},
		{"no attribution", "", "", ""},
		{"plugin without a skill is no skill group", "", "devkit", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inGo := skillGroupName(tc.skill, tc.plugin)
			var inSQL string
			if err := s.db.QueryRowContext(context.Background(), query, tc.skill, tc.plugin).Scan(&inSQL); err != nil {
				t.Fatal(err)
			}
			if inGo != tc.want || inSQL != tc.want {
				t.Fatalf("group of skill %q with plugin %q = %q in Go, %q in SQL; want %q",
					tc.skill, tc.plugin, inGo, inSQL, tc.want)
			}
		})
	}
}

// aggSeedOneSession stores one root session whose rows all fall on the first
// instant of the seeded week, so that the range aggregate and the session
// detail cover the same rows.
func aggSeedOneSession(t *testing.T, s *Store, id string, batch usage.Batch) {
	t.Helper()
	batch.Session = usage.SessionPatch{SessionID: id, FirstAt: aggFrom, LastAt: aggFrom}
	batch.NextOffset = 1
	seedBatch(t, s, usage.FileRef{Path: "p/" + id + ".jsonl", ProjectKey: "p", Size: 1, ModTime: testNow}, batch)
}

// The tools page and the session detail show one skill under one name with
// the same numbers, for a plugin skill and for a skill without a plugin.
func TestSkillGroupsAgreeAcrossReads(t *testing.T) {
	s := openTest(t)
	const id = "s1"
	skillCall := func(key, skill, plugin string) usage.ToolCall {
		return usage.ToolCall{Key: key, SessionID: id, At: aggFrom, Name: usage.ToolNameSkill,
			Category: usage.ToolSkill, Skill: skill, Plugin: plugin}
	}
	attributed := func(key, skill, plugin string, input int64) usage.ModelCall {
		return usage.ModelCall{Key: key, SessionID: id, At: aggFrom, Model: "model-a", Input: input, Skill: skill, Plugin: plugin}
	}
	aggSeedOneSession(t, s, id, usage.Batch{
		ToolCalls: []usage.ToolCall{
			skillCall("c1", "devkit:deploy", "devkit"),
			skillCall("c2", "devkit:deploy", "devkit"), // pending
			skillCall("c3", "notes", ""),
			skillCall("c4", "", ""), // names no skill: no group on either page
			// Only a Skill call belongs to a skill group, whatever its row holds.
			{Key: "c5", SessionID: id, At: aggFrom, Name: "Read", Category: usage.ToolBuiltin, Skill: "notes"},
		},
		ToolResults: []usage.ToolResult{
			{Key: "c1", SessionID: id, At: aggFrom.Add(time.Second), DurationMs: 40},
			{Key: "c3", SessionID: id, At: aggFrom.Add(time.Second), IsError: true, DurationMs: 10},
		},
		ModelCalls: []usage.ModelCall{
			attributed("m1", "devkit:deploy", "devkit", 1), // the shape of a real transcript
			attributed("m2", "deploy", "devkit", 10),       // a bare name beside its plugin is the same skill
			attributed("m3", "notes", "", 100),
			attributed("m4", "review", "", 1000),  // attributed to, never called
			attributed("m5", "", "devkit", 10000), // a plugin alone names no skill
			attributed("m6", "", "", 100000),
		},
	})
	want := []usage.GroupUsage{
		{Name: "devkit:deploy", Calls: 2, Sessions: 1, Tokens: &usage.TokenTotals{Input: 11}, AvgMs: aggF(40)},
		{Name: "notes", Calls: 1, Errors: 1, Sessions: 1, Tokens: &usage.TokenTotals{Input: 100}, AvgMs: aggF(10)},
		{Name: "review", Sessions: 1, Tokens: &usage.TokenTotals{Input: 1000}},
	}
	tools, err := s.Tools(context.Background(), aggWeek)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := s.Session(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	aggEqualJSON(t, "skills of the tools page", tools.Skills, want)
	aggEqualJSON(t, "skills of the session detail", detail.Skills, want)
}

// A hook run fails on the tools page exactly when it fails in the session
// detail: on an error or a block, and on nothing else.
func TestHookFailuresAgreeAcrossReads(t *testing.T) {
	s := openTest(t)
	const id = "s1"
	tests := []struct {
		status   usage.HookStatus
		failures int64
	}{
		{usage.HookBlocked, 1},
		{usage.HookError, 1},
		{usage.HookSuccess, 0},
		{usage.HookUnknown, 0},
	}
	var batch usage.Batch
	want := make([]usage.HookUsage, 0, len(tests))
	// One hook per status, listed by name, which is the status.
	for _, tc := range tests {
		name := string(tc.status)
		batch.Hooks = append(batch.Hooks, usage.HookRun{
			Key: "h-" + name, SessionID: id, At: aggFrom, Event: "PreToolUse", Name: name, Status: tc.status, DurationMs: 20,
		})
		want = append(want, usage.HookUsage{
			Event: "PreToolUse", Name: name, Runs: 1, Failures: tc.failures, AvgMs: aggF(20), MaxMs: aggF(20),
		})
	}
	aggSeedOneSession(t, s, id, batch)
	tools, err := s.Tools(context.Background(), aggWeek)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := s.Session(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	aggEqualJSON(t, "hooks of the tools page", tools.Hooks, want)
	aggEqualJSON(t, "hooks of the session detail", detail.Hooks, want)
}
