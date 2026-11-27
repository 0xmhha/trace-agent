package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Synthetic identifiers shared by the ingest tests.
const (
	ingestProjectA  = "proj-a"
	ingestProjectB  = "proj-b"
	ingestSession   = "sess-1"
	ingestAgent     = "agent-1"
	ingestRootFile  = "proj-a/sess-1.jsonl"
	ingestAgentFile = "proj-a/sess-1/subagents/agent-agent-1.jsonl"
	ingestOtherFile = "proj-b/sess-9.jsonl"
	// ingestVolatileColumn changes with the clock and is left out of row
	// comparisons.
	ingestVolatileColumn = "scanned_ms"
)

// ingestStart is the first line timestamp of the synthetic transcripts.
var ingestStart = testNow.Add(-2 * time.Hour)

// ingestControlTables are compared separately from the data tables.
func ingestControlTables() []string { return []string{"setting", "data_version"} }

// ingestGrant turns consent on the way the settings API does.
func ingestGrant(t *testing.T, s *Store) {
	t.Helper()
	if err := s.SetConsent(context.Background(), true); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
}

// ingestVersion reads data_version.
func ingestVersion(t *testing.T, s *Store) int64 {
	t.Helper()
	n, err := s.DataVersion(context.Background())
	if err != nil {
		t.Fatalf("data version: %v", err)
	}
	return n
}

// ingestCount counts the rows of table that match where.
func ingestCount(t *testing.T, s *Store, table, where string, args ...any) int64 {
	t.Helper()
	query := "SELECT count(*) FROM " + table
	if where != "" {
		query += " WHERE " + where
	}
	var n int64
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// ingestTableNames lists every table of the schema except the control ones.
func ingestTableNames(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatalf("list tables: %v", err)
		}
		if !slices.Contains(ingestControlTables(), name) {
			names = append(names, name)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	return names
}

// ingestTableRows renders every row of table as "column=value" text in a
// stable order.
func ingestTableRows(t *testing.T, s *Store, table string) []string {
	t.Helper()
	// The table name comes from the schema or from a literal of the test.
	rows, err := s.db.QueryContext(context.Background(), "SELECT * FROM "+table) //nolint:gosec // not user input
	if err != nil {
		t.Fatalf("dump %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("dump %s: %v", table, err)
	}
	out := []string{}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err = rows.Scan(targets...); err != nil {
			t.Fatalf("dump %s: %v", table, err)
		}
		var b strings.Builder
		for i, column := range columns {
			if column == ingestVolatileColumn {
				continue
			}
			if raw, isBytes := values[i].([]byte); isBytes {
				values[i] = string(raw)
			}
			fmt.Fprintf(&b, "%s=%v|", column, values[i])
		}
		out = append(out, b.String())
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("dump %s: %v", table, err)
	}
	sort.Strings(out)
	return out
}

// ingestDump renders every data table of the store.
func ingestDump(t *testing.T, s *Store) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, table := range ingestTableNames(t, s) {
		out[table] = ingestTableRows(t, s, table)
	}
	return out
}

// ingestSameRows fails when two dumps differ and names the first table that
// does.
func ingestSameRows(t *testing.T, label string, got, want map[string][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d tables, want %d", label, len(got), len(want))
	}
	tables := make([]string, 0, len(want))
	for table := range want {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		if !slices.Equal(got[table], want[table]) {
			t.Fatalf("%s: table %s differs\n got: %q\nwant: %q", label, table, got[table], want[table])
		}
	}
}

// ingestRef builds a file reference of the synthetic root.
func ingestRef(path, project string, size int64) usage.FileRef {
	return usage.FileRef{Path: path, ProjectKey: project, Size: size, ModTime: testNow.Add(-time.Minute)}
}

// ingestRootRef is the root transcript of the synthetic session.
func ingestRootRef() usage.FileRef { return ingestRef(ingestRootFile, ingestProjectA, 900) }

// ingestApply applies a batch and fails the test on any error.
func ingestApply(t *testing.T, s *Store, file usage.FileRef, b usage.Batch) {
	t.Helper()
	if err := s.Apply(context.Background(), file, b); err != nil {
		t.Fatalf("apply %s at %d: %v", file.Path, b.BaseOffset, err)
	}
}

// ingestRichBatch is the first chunk of the root transcript with one record
// of every kind.
func ingestRichBatch() usage.Batch {
	at := func(minutes int) time.Time { return ingestStart.Add(time.Duration(minutes) * time.Minute) }
	return usage.Batch{
		BaseOffset: 0,
		NextOffset: 400,
		Lines:      12,
		Skipped:    2,
		Foreign:    1,
		Session: usage.SessionPatch{
			SessionID: ingestSession, ProjectPath: "/work/proj-a", Title: "first title", Entrypoint: "cli",
			ClientVersion: "2.1.0", GitBranch: "main", FirstAt: at(0), LastAt: at(5),
		},
		ModelCalls: []usage.ModelCall{
			{Key: "msg-1", SessionID: ingestSession, At: at(1), Model: "model-a", Input: 10, Output: 20, CacheRead: 30,
				CacheCreate: 40, Thinking: 5, StopReason: "tool_use", Skill: "review", Plugin: "tools"},
			{Key: "msg-2", SessionID: ingestSession, At: at(3), Model: "model-b", Input: 1, Output: 2},
		},
		ToolCalls: []usage.ToolCall{
			{Key: "call-1", SessionID: ingestSession, At: at(1), Name: usage.ToolNameBash, Category: usage.ToolBuiltin,
				Target: "go", MessageKey: "msg-1"},
			{Key: "call-2", SessionID: ingestSession, At: at(2), Name: "mcp__plugin_tools_search__find", Category: usage.ToolMCP,
				MCPServer: "plugin_tools_search", MCPTool: "find", Plugin: "tools", MessageKey: "msg-1"},
			{Key: "call-3", SessionID: ingestSession, At: at(3), Name: usage.ToolNameSkill, Category: usage.ToolSkill,
				Skill: "tools:review", Plugin: "tools", MessageKey: "msg-2"},
			{Key: "call-4", SessionID: ingestSession, At: at(3), Name: usage.ToolNameAgent, Category: usage.ToolAgent,
				SubagentType: "explorer", MessageKey: "msg-2"},
		},
		ToolResults: []usage.ToolResult{
			{Key: "call-1", SessionID: ingestSession, At: at(2), IsError: true, Interrupted: true, DurationMs: 1500, SandboxViolations: 2},
		},
		Prompts: []usage.Prompt{{Key: "prompt-1", SessionID: ingestSession, At: at(0), Source: "typed", Chars: 42}},
		Hooks: []usage.HookRun{
			{Key: "hook-1", SessionID: ingestSession, At: at(1), Event: "PreToolUse", Name: "guard", ToolUseKey: "call-1",
				Status: usage.HookBlocked, ExitCode: 2, DurationMs: 30},
		},
		Turns:       []usage.TurnEnd{{Key: "turn-1", SessionID: ingestSession, At: at(5), DurationMs: 300000, Messages: 7}},
		Compactions: []usage.Compaction{{Key: "compact-1", SessionID: ingestSession, At: at(4), Trigger: "auto", PreTokens: 150000}},
		Cost: &usage.CostState{SessionID: ingestSession, At: at(5), TotalCostUSD: 1.25, APIDurationMs: 9000,
			ToolDurationMs: 1500, LinesAdded: 12, LinesRemoved: 3},
	}
}

// ingestStep is one Apply of a scenario.
type ingestStep struct {
	name  string
	file  usage.FileRef
	batch usage.Batch
}

// ingestScenario is a sequence of batches that exercises every upsert rule:
// a continuation, a sub-agent stream, a restart from offset zero, a batch
// without a session and a pure cursor advance.
func ingestScenario() []ingestStep {
	at := func(minutes int) time.Time { return ingestStart.Add(time.Duration(minutes) * time.Minute) }
	root := ingestRootRef()
	return []ingestStep{
		{name: "first chunk", file: root, batch: ingestRichBatch()},
		{name: "continuation", file: root, batch: usage.Batch{
			BaseOffset: 400, NextOffset: 900, Skipped: 1,
			// Empty labels and a zero first time must keep the stored values.
			Session: usage.SessionPatch{SessionID: ingestSession, GitBranch: "feature", LastAt: at(9)},
			ModelCalls: []usage.ModelCall{
				{Key: "msg-1", SessionID: ingestSession, At: at(1), Model: "model-a", Input: 11, Output: 22},
				{Key: "msg-3", SessionID: ingestSession, At: at(8), Model: "model-a", Input: 3, Output: 4},
			},
			ToolResults: []usage.ToolResult{{Key: "call-2", SessionID: ingestSession, At: at(8)}},
			// An older cost state must not replace the stored one.
			Cost: &usage.CostState{SessionID: ingestSession, At: at(1), TotalCostUSD: 0.5},
		}},
		{name: "sub-agent", file: ingestRef(ingestAgentFile, ingestProjectA, 300), batch: usage.Batch{
			NextOffset: 300,
			Session: usage.SessionPatch{SessionID: ingestSession, AgentID: ingestAgent, AgentType: "explorer",
				ProjectPath: "/work/elsewhere", FirstAt: at(3), LastAt: at(4)},
			ModelCalls: []usage.ModelCall{{Key: "msg-9", SessionID: ingestSession, AgentID: ingestAgent, At: at(3), Model: "model-b", Input: 7, Output: 8}},
			ToolCalls: []usage.ToolCall{{Key: "call-9", SessionID: ingestSession, AgentID: ingestAgent, At: at(3), Name: "Read",
				Category: usage.ToolBuiltin, MessageKey: "msg-9"}},
			Hooks: []usage.HookRun{{Key: "hook-9", SessionID: ingestSession, AgentID: ingestAgent, At: at(3), Event: "PostToolUse",
				Name: "format", Status: usage.HookSuccess}},
			Compactions: []usage.Compaction{{Key: "compact-9", SessionID: ingestSession, AgentID: ingestAgent, At: at(4), Trigger: "manual"}},
			// A sub-agent transcript never owns the session cost.
			Cost: &usage.CostState{SessionID: ingestSession, At: at(30), TotalCostUSD: 99},
		}},
		{name: "restart from zero", file: root, batch: usage.Batch{
			BaseOffset: 0, NextOffset: 900, Skipped: 5,
			Session: usage.SessionPatch{SessionID: ingestSession, Title: "second title", FirstAt: at(-1), LastAt: at(2)},
			Prompts: []usage.Prompt{{Key: "prompt-1", SessionID: ingestSession, At: at(0), Source: "typed", Chars: 42}},
			Cost:    &usage.CostState{SessionID: ingestSession, At: at(10), TotalCostUSD: 2.5, LinesAdded: 20},
		}},
		{name: "no session", file: ingestRef(ingestOtherFile, ingestProjectB, 80), batch: usage.Batch{
			NextOffset: 80,
			Prompts:    []usage.Prompt{{Key: "prompt-9", SessionID: "sess-9", At: at(6), Chars: 3}},
		}},
		{name: "cursor advance", file: root, batch: usage.Batch{BaseOffset: 900, NextOffset: 950}},
	}
}

func TestApplyMatchesSeedBatch(t *testing.T) {
	applied, seeded := openTest(t), openTest(t)
	ingestGrant(t, applied)
	for _, step := range ingestScenario() {
		ingestApply(t, applied, step.file, step.batch)
		seedBatch(t, seeded, step.file, step.batch)
		ingestSameRows(t, step.name, ingestDump(t, applied), ingestDump(t, seeded))
	}
	// The comparison is only meaningful when the scenario filled the tables.
	for _, table := range []string{"file", "project", "stream", "model_call", "tool_call", "tool_result", "prompt",
		"hook_run", "turn_end", "compaction", "cost_state"} {
		if ingestCount(t, applied, table, "") == 0 {
			t.Errorf("scenario left %s empty", table)
		}
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	s := openTest(t)
	ingestGrant(t, s)
	for _, step := range ingestScenario() {
		ingestApply(t, s, step.file, step.batch)
	}
	want := ingestDump(t, s)
	// A rescan reads every file again from offset zero in the same order.
	if err := s.ResetCursors(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, step := range ingestScenario() {
		ingestApply(t, s, step.file, step.batch)
	}
	// Every row, the cursors and skipped counts included, must be exactly
	// what the first pass stored.
	ingestSameRows(t, "second pass", ingestDump(t, s), want)
}

func TestApplyTwiceKeepsRows(t *testing.T) {
	s := openTest(t)
	ingestGrant(t, s)
	ingestApply(t, s, ingestRootRef(), ingestRichBatch())
	want := ingestDump(t, s)
	ingestApply(t, s, ingestRootRef(), ingestRichBatch())
	ingestSameRows(t, "same batch twice", ingestDump(t, s), want)
}

func TestApplyStale(t *testing.T) {
	seedCursor := func(t *testing.T, s *Store) {
		t.Helper()
		ingestApply(t, s, ingestRootRef(), usage.Batch{NextOffset: 400, Skipped: 2})
	}
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, s *Store)
		base    int64
		stale   bool
	}{
		{name: "no consent", prepare: func(*testing.T, *Store) {}, base: 0, stale: true},
		{name: "consent withdrawn", prepare: func(t *testing.T, s *Store) {
			ingestGrant(t, s)
			seedCursor(t, s)
			if err := s.SetConsent(context.Background(), false); err != nil {
				t.Fatal(err)
			}
		}, base: 400, stale: true},
		{name: "excluded project", prepare: func(t *testing.T, s *Store) {
			ingestGrant(t, s)
			if err := s.SetExcluded(context.Background(), ingestProjectA, true); err != nil {
				t.Fatal(err)
			}
		}, base: 0, stale: true},
		{name: "offset without a cursor", prepare: ingestGrant, base: 400, stale: true},
		{name: "offset behind the cursor", prepare: func(t *testing.T, s *Store) {
			ingestGrant(t, s)
			seedCursor(t, s)
		}, base: 200, stale: true},
		{name: "offset ahead of the cursor", prepare: func(t *testing.T, s *Store) {
			ingestGrant(t, s)
			seedCursor(t, s)
		}, base: 500, stale: true},
		{name: "offset after a reset", prepare: func(t *testing.T, s *Store) {
			ingestGrant(t, s)
			seedCursor(t, s)
			if err := s.ResetCursors(context.Background()); err != nil {
				t.Fatal(err)
			}
		}, base: 400, stale: true},
		{name: "first batch of a new file", prepare: ingestGrant, base: 0},
		{name: "offset at the cursor", prepare: func(t *testing.T, s *Store) {
			ingestGrant(t, s)
			seedCursor(t, s)
		}, base: 400},
		{name: "restart of a tracked file", prepare: func(t *testing.T, s *Store) {
			ingestGrant(t, s)
			seedCursor(t, s)
		}, base: 0},
		{name: "other project excluded", prepare: func(t *testing.T, s *Store) {
			ingestGrant(t, s)
			if err := s.SetExcluded(context.Background(), ingestProjectB, true); err != nil {
				t.Fatal(err)
			}
		}, base: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			tc.prepare(t, s)
			before, version := ingestDump(t, s), ingestVersion(t, s)
			batch := ingestRichBatch()
			batch.BaseOffset, batch.NextOffset = tc.base, tc.base+300
			err := s.Apply(context.Background(), ingestRootRef(), batch)
			if !tc.stale {
				if err != nil {
					t.Fatalf("Apply = %v; want success", err)
				}
				if got := ingestCount(t, s, "model_call", ""); got != int64(len(batch.ModelCalls)) {
					t.Fatalf("model_call rows = %d; want %d", got, len(batch.ModelCalls))
				}
				return
			}
			if !errors.Is(err, usage.ErrStale) {
				t.Fatalf("Apply = %v; want usage.ErrStale", err)
			}
			ingestSameRows(t, "stale batch", ingestDump(t, s), before)
			if got := ingestVersion(t, s); got != version {
				t.Fatalf("data_version = %d after a stale batch; want %d", got, version)
			}
		})
	}
}

func TestApplyRejectsImpossibleBatch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		file  usage.FileRef
		batch usage.Batch
	}{
		{name: "no path", file: usage.FileRef{ProjectKey: ingestProjectA}, batch: usage.Batch{NextOffset: 1}},
		{name: "no project", file: usage.FileRef{Path: ingestRootFile}, batch: usage.Batch{NextOffset: 1}},
		{name: "negative base", file: ingestRootRef(), batch: usage.Batch{BaseOffset: -1, NextOffset: 1}},
		{name: "next before base", file: ingestRootRef(), batch: usage.Batch{BaseOffset: 10, NextOffset: 5}},
		{name: "negative skipped", file: ingestRootRef(), batch: usage.Batch{NextOffset: 5, Skipped: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			ingestGrant(t, s)
			if err := s.Apply(context.Background(), tc.file, tc.batch); !errors.Is(err, usage.ErrBadRequest) {
				t.Fatalf("Apply = %v; want usage.ErrBadRequest", err)
			}
			if got := ingestCount(t, s, "file", ""); got != 0 {
				t.Fatalf("file rows = %d; want 0", got)
			}
		})
	}
}

func TestApplySkippedCount(t *testing.T) {
	s := openTest(t)
	ingestGrant(t, s)
	root := ingestRootRef()
	for _, step := range []struct {
		name       string
		base, next int64
		skipped    int
		want       int64
	}{
		{name: "first chunk", base: 0, next: 100, skipped: 2, want: 2},
		{name: "continuation adds", base: 100, next: 200, skipped: 3, want: 5},
		{name: "clean continuation keeps", base: 200, next: 300, skipped: 0, want: 5},
		{name: "restart replaces", base: 0, next: 100, skipped: 1, want: 1},
		{name: "clean restart clears", base: 0, next: 100, skipped: 0, want: 0},
	} {
		ingestApply(t, s, root, usage.Batch{BaseOffset: step.base, NextOffset: step.next, Skipped: step.skipped})
		cursor, ok, err := s.Cursor(context.Background(), root.Path)
		if err != nil || !ok {
			t.Fatalf("%s: Cursor = %v, %v", step.name, ok, err)
		}
		if cursor.Skipped != step.want || cursor.Offset != step.next {
			t.Fatalf("%s: cursor = %+v; want skipped %d at offset %d", step.name, cursor, step.want, step.next)
		}
	}
}

func TestApplyKeepsStoredSessionValues(t *testing.T) {
	s := openTest(t)
	ingestGrant(t, s)
	root := ingestRootRef()
	first, last := ingestStart, ingestStart.Add(10*time.Minute)
	ingestApply(t, s, root, usage.Batch{NextOffset: 100, Session: usage.SessionPatch{
		SessionID: ingestSession, ProjectPath: "/work/proj-a", Title: "kept title", Entrypoint: "cli",
		ClientVersion: "2.1.0", GitBranch: "main", FirstAt: first, LastAt: last,
	}})
	type stream struct {
		projectPath, title, entrypoint, version, branch string
		firstMS, lastMS                                 int64
	}
	read := func() stream {
		t.Helper()
		var got stream
		if err := s.db.QueryRowContext(context.Background(),
			`SELECT project_path,title,entrypoint,client_version,git_branch,first_ms,last_ms FROM stream WHERE session_id = ? AND agent_id = ''`,
			ingestSession).Scan(&got.projectPath, &got.title, &got.entrypoint, &got.version, &got.branch, &got.firstMS, &got.lastMS); err != nil {
			t.Fatalf("read stream: %v", err)
		}
		return got
	}
	kept := stream{"/work/proj-a", "kept title", "cli", "2.1.0", "main", toMS(first), toMS(last)}
	for _, tc := range []struct {
		name  string
		patch usage.SessionPatch
		want  stream
	}{
		{name: "empty patch keeps everything", patch: usage.SessionPatch{SessionID: ingestSession}, want: kept},
		{name: "times inside the stored span keep it", patch: usage.SessionPatch{SessionID: ingestSession,
			FirstAt: first.Add(time.Minute), LastAt: last.Add(-time.Minute)}, want: kept},
		{name: "wider times extend the span", patch: usage.SessionPatch{SessionID: ingestSession,
			FirstAt: first.Add(-time.Minute), LastAt: last.Add(time.Minute)},
			want: stream{"/work/proj-a", "kept title", "cli", "2.1.0", "main", toMS(first.Add(-time.Minute)), toMS(last.Add(time.Minute))}},
		{name: "new labels replace", patch: usage.SessionPatch{SessionID: ingestSession, Title: "new title", GitBranch: "feature"},
			want: stream{"/work/proj-a", "new title", "cli", "2.1.0", "feature", toMS(first.Add(-time.Minute)), toMS(last.Add(time.Minute))}},
	} {
		cursor, _, err := s.Cursor(context.Background(), root.Path)
		if err != nil {
			t.Fatal(err)
		}
		ingestApply(t, s, root, usage.Batch{BaseOffset: cursor.Offset, NextOffset: cursor.Offset + 10, Session: tc.patch})
		if got := read(); got != tc.want {
			t.Fatalf("%s: stream = %+v; want %+v", tc.name, got, tc.want)
		}
	}
}

func TestApplyStreamWithoutTimes(t *testing.T) {
	s := openTest(t)
	ingestGrant(t, s)
	root := ingestRootRef()
	ingestApply(t, s, root, usage.Batch{NextOffset: 10, Session: usage.SessionPatch{SessionID: ingestSession, Title: "untimed"}})
	if got := ingestCount(t, s, "stream", "first_ms = 0 AND last_ms = 0"); got != 1 {
		t.Fatalf("untimed stream rows = %d; want 1", got)
	}
	ingestApply(t, s, root, usage.Batch{BaseOffset: 10, NextOffset: 20,
		Session: usage.SessionPatch{SessionID: ingestSession, FirstAt: ingestStart, LastAt: ingestStart}})
	if got := ingestCount(t, s, "stream", "first_ms = ? AND last_ms = ?", toMS(ingestStart), toMS(ingestStart)); got != 1 {
		t.Fatalf("stream did not take the first timestamps; rows = %d", got)
	}
}

func TestApplyCost(t *testing.T) {
	s := openTest(t)
	ingestGrant(t, s)
	root, agent := ingestRootRef(), ingestRef(ingestAgentFile, ingestProjectA, 300)
	readCost := func() (float64, bool) {
		t.Helper()
		var total float64
		err := s.db.QueryRowContext(context.Background(), "SELECT total_cost_usd FROM cost_state WHERE session_id = ?", ingestSession).Scan(&total)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false
		}
		if err != nil {
			t.Fatalf("read cost: %v", err)
		}
		return total, true
	}
	ingestApply(t, s, agent, usage.Batch{NextOffset: 100,
		Session: usage.SessionPatch{SessionID: ingestSession, AgentID: ingestAgent},
		Cost:    &usage.CostState{SessionID: ingestSession, At: ingestStart, TotalCostUSD: 9}})
	if _, ok := readCost(); ok {
		t.Fatal("a sub-agent batch stored a cost state")
	}
	for _, step := range []struct {
		name string
		base int64
		cost *usage.CostState
		want float64
	}{
		{name: "first cost", base: 0, cost: &usage.CostState{SessionID: ingestSession, At: ingestStart.Add(time.Minute), TotalCostUSD: 1}, want: 1},
		{name: "newer cost wins", base: 100, cost: &usage.CostState{SessionID: ingestSession, At: ingestStart.Add(2 * time.Minute), TotalCostUSD: 2}, want: 2},
		{name: "older cost is ignored", base: 200, cost: &usage.CostState{SessionID: ingestSession, At: ingestStart, TotalCostUSD: 0.5}, want: 2},
		{name: "same time replaces", base: 300, cost: &usage.CostState{SessionID: ingestSession, At: ingestStart.Add(2 * time.Minute), TotalCostUSD: 3}, want: 3},
		{name: "no cost keeps", base: 400, cost: nil, want: 3},
	} {
		ingestApply(t, s, root, usage.Batch{BaseOffset: step.base, NextOffset: step.base + 100,
			Session: usage.SessionPatch{SessionID: ingestSession}, Cost: step.cost})
		if got, ok := readCost(); !ok || got != step.want {
			t.Fatalf("%s: cost = %v, %v; want %v", step.name, got, ok, step.want)
		}
	}
}

func TestApplyProjectPathComesFromRoot(t *testing.T) {
	s := openTest(t)
	ingestGrant(t, s)
	readPath := func() string {
		t.Helper()
		var path string
		if err := s.db.QueryRowContext(context.Background(), "SELECT path FROM project WHERE project_key = ?", ingestProjectA).Scan(&path); err != nil {
			t.Fatalf("read project: %v", err)
		}
		return path
	}
	ingestApply(t, s, ingestRef(ingestAgentFile, ingestProjectA, 300), usage.Batch{NextOffset: 100,
		Session: usage.SessionPatch{SessionID: ingestSession, AgentID: ingestAgent, ProjectPath: "/work/elsewhere"}})
	if got := readPath(); got != "" {
		t.Fatalf("project path = %q after a sub-agent batch; want empty", got)
	}
	ingestApply(t, s, ingestRootRef(), usage.Batch{NextOffset: 100,
		Session: usage.SessionPatch{SessionID: ingestSession, ProjectPath: "/work/proj-a"}})
	ingestApply(t, s, ingestRootRef(), usage.Batch{BaseOffset: 100, NextOffset: 200, Session: usage.SessionPatch{SessionID: ingestSession}})
	if got := readPath(); got != "/work/proj-a" {
		t.Fatalf("project path = %q; want the root transcript's path kept", got)
	}
}

func TestApplyDataVersion(t *testing.T) {
	cost := &usage.CostState{SessionID: ingestSession, At: ingestStart, TotalCostUSD: 1}
	for _, tc := range []struct {
		name  string
		batch usage.Batch
		bumps bool
	}{
		{name: "pure cursor advance", batch: usage.Batch{NextOffset: 10, Lines: 3}},
		{name: "session id alone", batch: usage.Batch{NextOffset: 10, Session: usage.SessionPatch{SessionID: ingestSession}}},
		{name: "skipped and foreign lines alone", batch: usage.Batch{NextOffset: 10, Skipped: 2, Foreign: 1}},
		{name: "sub-agent cost alone", batch: usage.Batch{NextOffset: 10,
			Session: usage.SessionPatch{SessionID: ingestSession, AgentID: ingestAgent}, Cost: cost}},
		{name: "session label", batch: usage.Batch{NextOffset: 10, Session: usage.SessionPatch{SessionID: ingestSession, Title: "t"}}, bumps: true},
		{name: "session time", batch: usage.Batch{NextOffset: 10, Session: usage.SessionPatch{SessionID: ingestSession, LastAt: ingestStart}}, bumps: true},
		{name: "root cost", batch: usage.Batch{NextOffset: 10, Session: usage.SessionPatch{SessionID: ingestSession}, Cost: cost}, bumps: true},
		{name: "model call", batch: usage.Batch{NextOffset: 10, ModelCalls: []usage.ModelCall{{Key: "m", SessionID: ingestSession}}}, bumps: true},
		{name: "tool call", batch: usage.Batch{NextOffset: 10, ToolCalls: []usage.ToolCall{{Key: "c", SessionID: ingestSession, Name: "Read", Category: usage.ToolBuiltin}}}, bumps: true},
		{name: "tool result", batch: usage.Batch{NextOffset: 10, ToolResults: []usage.ToolResult{{Key: "c", SessionID: ingestSession}}}, bumps: true},
		{name: "prompt", batch: usage.Batch{NextOffset: 10, Prompts: []usage.Prompt{{Key: "p", SessionID: ingestSession}}}, bumps: true},
		{name: "hook", batch: usage.Batch{NextOffset: 10, Hooks: []usage.HookRun{{Key: "h", SessionID: ingestSession, Status: usage.HookUnknown}}}, bumps: true},
		{name: "turn end", batch: usage.Batch{NextOffset: 10, Turns: []usage.TurnEnd{{Key: "t", SessionID: ingestSession}}}, bumps: true},
		{name: "compaction", batch: usage.Batch{NextOffset: 10, Compactions: []usage.Compaction{{Key: "k", SessionID: ingestSession}}}, bumps: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			ingestGrant(t, s)
			before := ingestVersion(t, s)
			ingestApply(t, s, ingestRootRef(), tc.batch)
			want := before
			if tc.bumps {
				want++
			}
			if got := ingestVersion(t, s); got != want {
				t.Fatalf("data_version = %d; want %d", got, want)
			}
			if cursor, ok, err := s.Cursor(context.Background(), ingestRootFile); err != nil || !ok || cursor.Offset != tc.batch.NextOffset {
				t.Fatalf("cursor = %+v, %v, %v; want offset %d", cursor, ok, err, tc.batch.NextOffset)
			}
		})
	}
}

func TestApplyRollsBackOnFailure(t *testing.T) {
	s := openTest(t)
	ingestGrant(t, s)
	before, version := ingestDump(t, s), ingestVersion(t, s)
	batch := ingestRichBatch()
	// The schema refuses an unknown category; the rows written before it in
	// the same batch must not survive.
	batch.ToolCalls = append(batch.ToolCalls, usage.ToolCall{Key: "bad", SessionID: ingestSession, Name: "x", Category: "unlisted"})
	err := s.Apply(context.Background(), ingestRootRef(), batch)
	if err == nil || errors.Is(err, usage.ErrStale) {
		t.Fatalf("Apply = %v; want a storage error", err)
	}
	ingestSameRows(t, "failed batch", ingestDump(t, s), before)
	if got := ingestVersion(t, s); got != version {
		t.Fatalf("data_version = %d after a failed batch; want %d", got, version)
	}
}

func TestApplyHonoursContext(t *testing.T) {
	s := openTest(t)
	ingestGrant(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Apply(ctx, ingestRootRef(), ingestRichBatch()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply = %v; want context.Canceled", err)
	}
	if got := ingestCount(t, s, "file", ""); got != 0 {
		t.Fatalf("file rows = %d after a cancelled Apply; want 0", got)
	}
}

func TestCursor(t *testing.T) {
	s := openTest(t)
	ingestGrant(t, s)
	if cursor, ok, err := s.Cursor(context.Background(), ingestRootFile); err != nil || ok || cursor != (usage.FileCursor{}) {
		t.Fatalf("Cursor of an unknown file = %+v, %v, %v; want zero, false, nil", cursor, ok, err)
	}
	root := ingestRootRef()
	ingestApply(t, s, root, usage.Batch{NextOffset: 321, Skipped: 4})
	cursor, ok, err := s.Cursor(context.Background(), root.Path)
	if err != nil || !ok {
		t.Fatalf("Cursor = %v, %v; want a stored cursor", ok, err)
	}
	want := usage.FileCursor{Offset: 321, Size: root.Size, ModTime: root.ModTime, Skipped: 4}
	if cursor.Offset != want.Offset || cursor.Size != want.Size || cursor.Skipped != want.Skipped || !cursor.ModTime.Equal(want.ModTime) {
		t.Fatalf("Cursor = %+v; want %+v", cursor, want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = s.Cursor(ctx, root.Path); !errors.Is(err, context.Canceled) {
		t.Fatalf("Cursor with a cancelled context = %v; want context.Canceled", err)
	}
}

func TestMarkUnreadable(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	ingestGrant(t, s)
	root := ingestRootRef()
	unreadable := func() int64 { return ingestCount(t, s, "file", "path = ? AND unreadable = 1", root.Path) }

	version := ingestVersion(t, s)
	if err := s.MarkUnreadable(ctx, root); err != nil {
		t.Fatal(err)
	}
	if unreadable() != 1 || ingestVersion(t, s) != version+1 {
		t.Fatalf("first mark: unreadable rows = %d, data_version = %d; want 1 and %d", unreadable(), ingestVersion(t, s), version+1)
	}
	// A file that was never read keeps a zero cursor, so it still looks
	// changed and is tried again.
	if cursor, ok, err := s.Cursor(ctx, root.Path); err != nil || !ok || cursor != (usage.FileCursor{}) {
		t.Fatalf("cursor of a new unreadable file = %+v, %v, %v; want a zero cursor", cursor, ok, err)
	}
	if err := s.MarkUnreadable(ctx, root); err != nil {
		t.Fatal(err)
	}
	if got := ingestVersion(t, s); got != version+1 {
		t.Fatalf("data_version = %d after an unchanged mark; want %d", got, version+1)
	}

	ingestApply(t, s, root, usage.Batch{NextOffset: 200, Skipped: 3})
	if unreadable() != 0 {
		t.Fatal("a successful Apply did not clear the unreadable flag")
	}
	version = ingestVersion(t, s)
	if err := s.MarkUnreadable(ctx, root); err != nil {
		t.Fatal(err)
	}
	if unreadable() != 1 || ingestVersion(t, s) != version+1 {
		t.Fatalf("mark after Apply: unreadable rows = %d, data_version = %d; want 1 and %d", unreadable(), ingestVersion(t, s), version+1)
	}
	cursor, _, err := s.Cursor(ctx, root.Path)
	if err != nil || cursor.Offset != 200 || cursor.Size != root.Size || cursor.Skipped != 3 {
		t.Fatalf("cursor after marking a tracked file = %+v, %v; want the stored position kept", cursor, err)
	}
}

func TestMarkUnreadableRefusals(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, s *Store)
		file    usage.FileRef
		wantErr error
	}{
		{name: "no consent", prepare: func(*testing.T, *Store) {}, file: ingestRootRef()},
		{name: "excluded project", prepare: func(t *testing.T, s *Store) {
			ingestGrant(t, s)
			if err := s.SetExcluded(ctx, ingestProjectA, true); err != nil {
				t.Fatal(err)
			}
		}, file: ingestRootRef()},
		{name: "no path", prepare: ingestGrant, file: usage.FileRef{ProjectKey: ingestProjectA}, wantErr: usage.ErrBadRequest},
		{name: "no project", prepare: ingestGrant, file: usage.FileRef{Path: ingestRootFile}, wantErr: usage.ErrBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			tc.prepare(t, s)
			version := ingestVersion(t, s)
			if err := s.MarkUnreadable(ctx, tc.file); !errors.Is(err, tc.wantErr) {
				t.Fatalf("MarkUnreadable = %v; want %v", err, tc.wantErr)
			}
			if got := ingestCount(t, s, "file", ""); got != 0 {
				t.Fatalf("file rows = %d; want none written", got)
			}
			if got := ingestVersion(t, s); got != version {
				t.Fatalf("data_version = %d; want %d", got, version)
			}
		})
	}
}

// ingestExtEvent builds a synthetic extension event received at the given
// number of minutes after ingestStart.
func ingestExtEvent(id, session string, receivedMinutes int) usage.ExtEvent {
	at := ingestStart.Add(time.Duration(receivedMinutes) * time.Minute)
	return usage.ExtEvent{ID: id, Source: "tool", Kind: usage.ExtLog, Name: "step", At: at, ReceivedAt: at,
		Status: usage.ExtOK, Level: usage.ExtInfo, SessionID: session}
}

// ingestFill applies the whole scenario and stores extension data, so that
// every table holds rows.
func ingestFill(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	ingestGrant(t, s)
	for _, step := range ingestScenario() {
		ingestApply(t, s, step.file, step.batch)
	}
	if err := s.AppendExt(ctx, []usage.ExtEvent{ingestExtEvent("ext-1", ingestSession, 1), ingestExtEvent("ext-2", "", 2)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CountRejected(ctx, "tool", 3); err != nil {
		t.Fatal(err)
	}
}

func TestClear(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	ingestFill(t, s)
	if err := s.SetWindowDays(ctx, 90); err != nil {
		t.Fatal(err)
	}
	if err := s.SetExtension(ctx, true, 4100); err != nil {
		t.Fatal(err)
	}
	if err := s.SetExcluded(ctx, "proj-hidden", true); err != nil {
		t.Fatal(err)
	}
	version := ingestVersion(t, s)
	if err := s.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range ingestTableNames(t, s) {
		want := int64(0)
		if table == "project" {
			want = 1
		}
		if got := ingestCount(t, s, table, ""); got != want {
			t.Errorf("%s rows after Clear = %d; want %d", table, got, want)
		}
	}
	if got := ingestVersion(t, s); got != version+1 {
		t.Fatalf("data_version = %d after Clear; want %d", got, version+1)
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := usage.Settings{Consent: true, WindowDays: 90, ExtEnabled: true, ExtPort: 4100, Excluded: []string{"proj-hidden"}}
	if settings.Consent != want.Consent || settings.WindowDays != want.WindowDays || settings.ExtEnabled != want.ExtEnabled ||
		settings.ExtPort != want.ExtPort || !slices.Equal(settings.Excluded, want.Excluded) {
		t.Fatalf("settings after Clear = %+v; want %+v", settings, want)
	}
	if _, ok, err := s.Cursor(ctx, ingestRootFile); err != nil || ok {
		t.Fatalf("Cursor after Clear = %v, %v; want no cursor", ok, err)
	}
	if err = s.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if got := ingestVersion(t, s); got != version+1 {
		t.Fatalf("data_version = %d after clearing an empty store; want %d", got, version+1)
	}
}

func TestResetCursors(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	ingestFill(t, s)
	before, version := ingestDump(t, s), ingestVersion(t, s)
	if err := s.ResetCursors(ctx); err != nil {
		t.Fatal(err)
	}
	if got := ingestVersion(t, s); got != version {
		t.Fatalf("data_version = %d after ResetCursors; want %d", got, version)
	}
	after := ingestDump(t, s)
	if len(after["file"]) != len(before["file"]) {
		t.Fatalf("file rows = %d after ResetCursors; want %d", len(after["file"]), len(before["file"]))
	}
	delete(after, "file")
	delete(before, "file")
	ingestSameRows(t, "rows other than cursors", after, before)
	for _, path := range []string{ingestRootFile, ingestAgentFile, ingestOtherFile} {
		cursor, ok, err := s.Cursor(ctx, path)
		if err != nil || !ok || cursor.Offset != 0 || cursor.Size != 0 || !cursor.ModTime.IsZero() {
			t.Fatalf("cursor of %s = %+v, %v, %v; want a tracked file at offset zero", path, cursor, ok, err)
		}
	}
	// The skipped count stays until the re-read replaces it, so coverage
	// does not look complete while the files are being read again.
	if cursor, _, _ := s.Cursor(ctx, ingestRootFile); cursor.Skipped != 5 {
		t.Fatalf("skipped = %d after ResetCursors; want 5", cursor.Skipped)
	}
	if err := s.Apply(ctx, ingestRootRef(), usage.Batch{BaseOffset: 900, NextOffset: 950}); !errors.Is(err, usage.ErrStale) {
		t.Fatalf("Apply at the old offset = %v; want usage.ErrStale", err)
	}
	ingestApply(t, s, ingestRootRef(), ingestRichBatch())
	if got := ingestCount(t, s, "prompt", "session_id = ?", ingestSession); got != 1 {
		t.Fatalf("prompt rows = %d after re-reading; want 1", got)
	}
}

func TestPruneByTime(t *testing.T) {
	ctx := context.Background()
	at := func(minutes int) time.Time { return ingestStart.Add(time.Duration(minutes) * time.Minute) }
	// One old and one recent session, each with every kind of record.
	fill := func(t *testing.T, s *Store) {
		t.Helper()
		ingestGrant(t, s)
		for i, minutes := range []int{0, 60} {
			session := fmt.Sprintf("sess-%d", i)
			when := at(minutes)
			ingestApply(t, s, ingestRef(ingestProjectA+"/"+session+".jsonl", ingestProjectA, 10), usage.Batch{
				NextOffset:  10,
				Session:     usage.SessionPatch{SessionID: session, FirstAt: when, LastAt: when},
				ModelCalls:  []usage.ModelCall{{Key: "m", SessionID: session, At: when}},
				ToolCalls:   []usage.ToolCall{{Key: "c", SessionID: session, At: when, Name: "Read", Category: usage.ToolBuiltin}},
				ToolResults: []usage.ToolResult{{Key: "c", SessionID: session, At: when}},
				Prompts:     []usage.Prompt{{Key: "p", SessionID: session, At: when}},
				Hooks:       []usage.HookRun{{Key: "h", SessionID: session, At: when, Status: usage.HookSuccess}},
				Turns:       []usage.TurnEnd{{Key: "t", SessionID: session, At: when}},
				Compactions: []usage.Compaction{{Key: "k", SessionID: session, At: when}},
				Cost:        &usage.CostState{SessionID: session, At: when, TotalCostUSD: 1},
			})
		}
		if err := s.AppendExt(ctx, []usage.ExtEvent{ingestExtEvent("ext-old", "", 0), ingestExtEvent("ext-new", "", 60)}); err != nil {
			t.Fatal(err)
		}
	}
	usageTables := []string{"stream", "model_call", "tool_call", "tool_result", "prompt", "hook_run", "turn_end", "compaction", "cost_state"}
	for _, tc := range []struct {
		name                   string
		usageBefore, extBefore time.Time
		wantUsage, wantExt     int64
		bumps                  bool
	}{
		{name: "zero times prune nothing", wantUsage: 2, wantExt: 2},
		{name: "cut-off before every row", usageBefore: at(-1), extBefore: at(-1), wantUsage: 2, wantExt: 2},
		{name: "cut-off equal to a row keeps it", usageBefore: at(0), extBefore: at(0), wantUsage: 2, wantExt: 2},
		{name: "usage only", usageBefore: at(30), wantUsage: 1, wantExt: 2, bumps: true},
		{name: "extension only", extBefore: at(30), wantUsage: 2, wantExt: 1, bumps: true},
		{name: "both", usageBefore: at(30), extBefore: at(30), wantUsage: 1, wantExt: 1, bumps: true},
		{name: "everything", usageBefore: at(90), extBefore: at(90), wantUsage: 0, wantExt: 0, bumps: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			fill(t, s)
			version := ingestVersion(t, s)
			if err := s.Prune(ctx, tc.usageBefore, tc.extBefore); err != nil {
				t.Fatal(err)
			}
			for _, table := range usageTables {
				if got := ingestCount(t, s, table, ""); got != tc.wantUsage {
					t.Errorf("%s rows = %d; want %d", table, got, tc.wantUsage)
				}
			}
			if got := ingestCount(t, s, "ext_event", ""); got != tc.wantExt {
				t.Errorf("ext_event rows = %d; want %d", got, tc.wantExt)
			}
			// Cursors stay, otherwise the pruned range would be read again.
			if got := ingestCount(t, s, "file", ""); got != 2 {
				t.Errorf("file rows = %d; want 2", got)
			}
			want := version
			if tc.bumps {
				want++
			}
			if got := ingestVersion(t, s); got != want {
				t.Errorf("data_version = %d; want %d", got, want)
			}
		})
	}
}

func TestPruneCap(t *testing.T) {
	ctx := context.Background()
	const stored = 6
	for _, tc := range []struct {
		name     string
		extCap   int64
		wantKept []string
	}{
		{name: "under the cap", extCap: stored + 1, wantKept: []string{"ext-0", "ext-1", "ext-2", "ext-3", "ext-4", "ext-5"}},
		{name: "at the cap", extCap: stored, wantKept: []string{"ext-0", "ext-1", "ext-2", "ext-3", "ext-4", "ext-5"}},
		{name: "oldest go first", extCap: 2, wantKept: []string{"ext-4", "ext-5"}},
		{name: "zero cap", extCap: 0, wantKept: []string{}},
		{name: "negative cap", extCap: -1, wantKept: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			events := make([]usage.ExtEvent, 0, stored)
			for i := range stored {
				// Event times run against the receive order, so the test
				// tells the two apart: the cap follows the receive time.
				e := ingestExtEvent(fmt.Sprintf("ext-%d", i), "", i)
				e.At = ingestStart.Add(-time.Duration(i) * time.Minute)
				events = append(events, e)
			}
			if err := s.AppendExt(ctx, events); err != nil {
				t.Fatal(err)
			}
			version := ingestVersion(t, s)
			if err := s.ingestPrune(ctx, time.Time{}, time.Time{}, tc.extCap); err != nil {
				t.Fatal(err)
			}
			rows, err := s.db.QueryContext(ctx, "SELECT id FROM ext_event ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			kept := []string{}
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				kept = append(kept, id)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(kept, tc.wantKept) {
				t.Fatalf("kept = %v; want %v", kept, tc.wantKept)
			}
			want := version
			if len(tc.wantKept) < stored {
				want++
			}
			if got := ingestVersion(t, s); got != want {
				t.Fatalf("data_version = %d; want %d", got, want)
			}
		})
	}
}

func TestPruneUsesTheSharedCap(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.AppendExt(ctx, []usage.ExtEvent{ingestExtEvent("ext-1", "", 0), ingestExtEvent("ext-2", "", 1)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, time.Time{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got := ingestCount(t, s, "ext_event", ""); got != 2 {
		t.Fatalf("ext_event rows = %d; Prune must keep a store under usage.MaxExtEvents", got)
	}
}

func TestIngestTablesCoverTheSchema(t *testing.T) {
	s := openTest(t)
	listed := map[string]bool{}
	for _, table := range ingestTables() {
		listed[table.name] = true
	}
	// Every table that carries project_key must be cleaned by Clear, Prune
	// and exclusion. The project table holds the exclusion flag and
	// excluded_session the sessions of excluded projects; both must outlive
	// the exclusion.
	exclusionState := map[string]bool{"project": true, "excluded_session": true}
	for _, table := range ingestTableNames(t, s) {
		hasProject := ingestCount(t, s, "pragma_table_info('"+table+"')", "name = 'project_key'") == 1
		if want := hasProject && !exclusionState[table]; listed[table] != want {
			t.Errorf("table %s: listed in ingestTables = %v; want %v", table, listed[table], want)
		}
	}
}

func TestConcurrentWrites(t *testing.T) {
	const (
		writers = 4
		chunks  = 15
		senders = 3
	)
	ctx := context.Background()
	s := openTest(t)
	ingestGrant(t, s)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	fail := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	}
	for w := range writers {
		wg.Go(func() {
			session := fmt.Sprintf("sess-%d", w)
			file := ingestRef(fmt.Sprintf("%s/%s.jsonl", ingestProjectA, session), ingestProjectA, chunks*100)
			for c := range chunks {
				base := int64(c * 100)
				when := ingestStart.Add(time.Duration(c) * time.Second)
				err := s.Apply(ctx, file, usage.Batch{
					BaseOffset: base, NextOffset: base + 100,
					Session:    usage.SessionPatch{SessionID: session, FirstAt: when, LastAt: when},
					ModelCalls: []usage.ModelCall{{Key: fmt.Sprintf("m-%d", c), SessionID: session, At: when, Input: 1}},
				})
				if err != nil {
					fail(fmt.Errorf("apply %s chunk %d: %w", session, c, err))
					return
				}
				if _, _, err = s.Cursor(ctx, file.Path); err != nil {
					fail(err)
					return
				}
			}
		})
	}
	for sender := range senders {
		wg.Go(func() {
			for c := range chunks {
				id := fmt.Sprintf("ext-%d-%d", sender, c)
				if err := s.AppendExt(ctx, []usage.ExtEvent{ingestExtEvent(id, "", c)}); err != nil {
					fail(fmt.Errorf("append %s: %w", id, err))
					return
				}
				if err := s.CountRejected(ctx, "tool", 1); err != nil {
					fail(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent writes failed: %v", errors.Join(errs...))
	}
	if got := ingestCount(t, s, "model_call", ""); got != writers*chunks {
		t.Fatalf("model_call rows = %d; want %d", got, writers*chunks)
	}
	if got := ingestCount(t, s, "ext_event", ""); got != senders*chunks {
		t.Fatalf("ext_event rows = %d; want %d", got, senders*chunks)
	}
	if got := ingestCount(t, s, "ext_reject", "source = 'tool' AND n = ?", senders*chunks); got != 1 {
		t.Fatalf("rejection counter did not reach %d", senders*chunks)
	}
	if got := ingestCount(t, s, "file", "offset = ?", chunks*100); got != writers {
		t.Fatalf("files at the final offset = %d; want %d", got, writers)
	}
}
