package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// testNow is the fixed clock of the package tests.
var testNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

// openTest opens an empty store in a temporary directory.
func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "usage.sqlite"), func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedSetting stores one setting with the encoding documented in db.go.
func seedSetting(t *testing.T, s *Store, key, value string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		"INSERT INTO setting(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
		t.Fatalf("seed setting %s: %v", key, err)
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// seedBatch writes the rows Apply must produce for batch, with plain inserts.
// Read-side tests use it so that they do not depend on the ingest code, and
// it is the reference for the row contents of Apply.
func seedBatch(t *testing.T, s *Store, file usage.FileRef, b usage.Batch) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}
	p := file.ProjectKey
	sess := b.Session
	// A batch read from offset 0 restarts the file, so its skipped count
	// replaces the stored one instead of adding to it.
	exec(`INSERT INTO file(path,project_key,offset,size,mod_ms,skipped,unreadable,scanned_ms) VALUES(?1,?2,?3,?4,?5,?6,0,?7)
		ON CONFLICT(path) DO UPDATE SET offset=excluded.offset,size=excluded.size,mod_ms=excluded.mod_ms,
		 skipped=CASE WHEN ?8=0 THEN excluded.skipped ELSE file.skipped+excluded.skipped END,unreadable=0,scanned_ms=excluded.scanned_ms`,
		file.Path, p, b.NextOffset, file.Size, toMS(file.ModTime), b.Skipped, toMS(s.now()), b.BaseOffset)
	exec(`INSERT INTO project(project_key,path) VALUES(?,?)
		ON CONFLICT(project_key) DO UPDATE SET path=CASE WHEN excluded.path<>'' THEN excluded.path ELSE project.path END`,
		p, rootPath(sess))
	if sess.SessionID != "" {
		exec(`INSERT INTO stream(session_id,agent_id,project_key,file_path,agent_type,project_path,title,entrypoint,client_version,git_branch,first_ms,last_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(session_id,agent_id) DO UPDATE SET
			 agent_type=CASE WHEN excluded.agent_type<>'' THEN excluded.agent_type ELSE stream.agent_type END,
			 project_path=CASE WHEN excluded.project_path<>'' THEN excluded.project_path ELSE stream.project_path END,
			 title=CASE WHEN excluded.title<>'' THEN excluded.title ELSE stream.title END,
			 entrypoint=CASE WHEN excluded.entrypoint<>'' THEN excluded.entrypoint ELSE stream.entrypoint END,
			 client_version=CASE WHEN excluded.client_version<>'' THEN excluded.client_version ELSE stream.client_version END,
			 git_branch=CASE WHEN excluded.git_branch<>'' THEN excluded.git_branch ELSE stream.git_branch END,
			 first_ms=CASE WHEN excluded.first_ms=0 THEN stream.first_ms WHEN stream.first_ms=0 THEN excluded.first_ms ELSE MIN(stream.first_ms,excluded.first_ms) END,
			 last_ms=MAX(stream.last_ms,excluded.last_ms)`,
			sess.SessionID, sess.AgentID, p, file.Path, sess.AgentType, sess.ProjectPath, sess.Title, sess.Entrypoint, sess.ClientVersion, sess.GitBranch, toMS(sess.FirstAt), toMS(sess.LastAt))
	}
	for _, m := range b.ModelCalls {
		exec(`INSERT OR REPLACE INTO model_call(session_id,agent_id,key,project_key,at_ms,model,input,output,cache_read,cache_create,thinking,stop_reason,skill,plugin) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			m.SessionID, m.AgentID, m.Key, p, toMS(m.At), m.Model, m.Input, m.Output, m.CacheRead, m.CacheCreate, m.Thinking, m.StopReason, m.Skill, m.Plugin)
	}
	for _, c := range b.ToolCalls {
		exec(`INSERT OR REPLACE INTO tool_call(session_id,key,agent_id,project_key,at_ms,name,category,mcp_server,mcp_tool,plugin,skill,subagent_type,target,message_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.SessionID, c.Key, c.AgentID, p, toMS(c.At), c.Name, string(c.Category), c.MCPServer, c.MCPTool, c.Plugin, c.Skill, c.SubagentType, c.Target, c.MessageKey)
	}
	for _, r := range b.ToolResults {
		exec(`INSERT OR REPLACE INTO tool_result(session_id,key,project_key,at_ms,is_error,interrupted,duration_ms,sandbox_violations) VALUES(?,?,?,?,?,?,?,?)`,
			r.SessionID, r.Key, p, toMS(r.At), boolInt(r.IsError), boolInt(r.Interrupted), r.DurationMs, r.SandboxViolations)
	}
	for _, q := range b.Prompts {
		exec(`INSERT OR REPLACE INTO prompt(session_id,key,agent_id,project_key,at_ms,source,chars) VALUES(?,?,?,?,?,?,?)`,
			q.SessionID, q.Key, q.AgentID, p, toMS(q.At), q.Source, q.Chars)
	}
	for _, h := range b.Hooks {
		exec(`INSERT OR REPLACE INTO hook_run(session_id,key,agent_id,project_key,at_ms,event,name,tool_use_key,status,exit_code,duration_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			h.SessionID, h.Key, h.AgentID, p, toMS(h.At), h.Event, h.Name, h.ToolUseKey, string(h.Status), h.ExitCode, h.DurationMs)
	}
	for _, e := range b.Turns {
		exec(`INSERT OR REPLACE INTO turn_end(session_id,key,project_key,at_ms,duration_ms,messages) VALUES(?,?,?,?,?,?)`,
			e.SessionID, e.Key, p, toMS(e.At), e.DurationMs, e.Messages)
	}
	for _, c := range b.Compactions {
		exec(`INSERT OR REPLACE INTO compaction(session_id,key,agent_id,project_key,at_ms,trigger_code,pre_tokens) VALUES(?,?,?,?,?,?,?)`,
			c.SessionID, c.Key, c.AgentID, p, toMS(c.At), c.Trigger, c.PreTokens)
	}
	if c := b.Cost; c != nil && sess.AgentID == "" {
		exec(`INSERT INTO cost_state(session_id,project_key,at_ms,total_cost_usd,api_duration_ms,tool_duration_ms,lines_added,lines_removed) VALUES(?,?,?,?,?,?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET at_ms=CASE WHEN excluded.at_ms=0 THEN cost_state.at_ms ELSE excluded.at_ms END,total_cost_usd=excluded.total_cost_usd,api_duration_ms=excluded.api_duration_ms,tool_duration_ms=excluded.tool_duration_ms,lines_added=excluded.lines_added,lines_removed=excluded.lines_removed
			WHERE excluded.at_ms = 0 OR excluded.at_ms >= cost_state.at_ms`,
			c.SessionID, p, toMS(c.At), c.TotalCostUSD, c.APIDurationMs, c.ToolDurationMs, c.LinesAdded, c.LinesRemoved)
	}
	exec(`UPDATE data_version SET n = n + 1 WHERE singleton = 1`)
}

// rootPath is the project path a batch contributes: only the root transcript
// names the project directory.
func rootPath(s usage.SessionPatch) string {
	if s.AgentID != "" {
		return ""
	}
	return s.ProjectPath
}

// seedExt writes extension events with plain inserts.
func seedExt(t *testing.T, s *Store, events ...usage.ExtEvent) {
	t.Helper()
	for _, e := range events {
		if _, err := s.db.ExecContext(context.Background(),
			`INSERT INTO ext_event(id,source,kind,name,at_ms,received_ms,duration_ms,status,level,session_id,tool_use_id,trace_id,span_id,parent_span_id,message,attributes,value,unit)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'{}',?,?)`,
			e.ID, e.Source, string(e.Kind), e.Name, toMS(e.At), toMS(e.ReceivedAt), e.DurationMs, string(e.Status), string(e.Level),
			e.SessionID, e.ToolUseID, e.TraceID, e.SpanID, e.ParentSpanID, e.Message, e.Value, e.Unit); err != nil {
			t.Fatalf("seed ext event: %v", err)
		}
	}
}

func TestOpenCreatesSchema(t *testing.T) {
	s := openTest(t)
	var n int64
	if err := s.db.QueryRowContext(context.Background(), "SELECT n FROM data_version").Scan(&n); err != nil || n != 0 {
		t.Fatalf("data_version = %d, %v; want 0", n, err)
	}
	seedBatch(t, s, usage.FileRef{Path: "p/a.jsonl", ProjectKey: "p", Size: 10, ModTime: testNow}, usage.Batch{
		Session:    usage.SessionPatch{SessionID: "s1", ProjectPath: "/work/p", FirstAt: testNow, LastAt: testNow},
		ModelCalls: []usage.ModelCall{{Key: "m1", SessionID: "s1", At: testNow, Model: "model-a", Input: 1, Output: 2}},
		NextOffset: 10,
	})
	seedSetting(t, s, settingConsent, settingOn)
	seedSetting(t, s, settingWindowDays, "7")
	from, err := s.windowFrom(context.Background())
	if err != nil || !from.Equal(testNow.AddDate(0, 0, -7)) {
		t.Fatalf("windowFrom = %v, %v; want 7 days before the test clock", from, err)
	}
	if p := percentile([]float64{10, 20, 30, 40}, 0.5); p == nil || *p != 20 {
		t.Fatalf("percentile p50 = %v; want 20", p)
	}
	if percentile(nil, 0.95) != nil {
		t.Fatal("percentile of no values must be nil")
	}
	var ms int64
	if err = s.db.QueryRowContext(context.Background(), "SELECT "+toolDurationSQL+" FROM (SELECT 0 AS duration_ms, 2500 AS at_ms) r, (SELECT 1000 AS at_ms) c").Scan(&ms); err != nil || ms != 1500 {
		t.Fatalf("derived tool duration = %d, %v; want 1500", ms, err)
	}
	seedExt(t, s, usage.ExtEvent{ID: "e1", Source: "tool", Kind: usage.ExtLog, Name: "n", At: testNow, ReceivedAt: testNow, Status: usage.ExtOK, Level: usage.ExtInfo})
	if err := s.db.QueryRowContext(context.Background(), "SELECT count(*) FROM model_call").Scan(&n); err != nil || n != 1 {
		t.Fatalf("model_call rows = %d, %v; want 1", n, err)
	}
}
