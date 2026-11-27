package usage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/extension"
	"github.com/0xmhha/trace-agent/internal/usage"
	"github.com/0xmhha/trace-agent/internal/usage/db"
	"github.com/0xmhha/trace-agent/internal/usage/insight"
	"github.com/0xmhha/trace-agent/internal/usage/service"
	"github.com/0xmhha/trace-agent/internal/usage/timeline"
	"github.com/0xmhha/trace-agent/internal/usage/transcript"
)

// These tests run the real packages end to end over the synthetic transcripts
// in testdata: scan -> store -> service -> JSON, plus the extension endpoint.
// Expected numbers are the ones documented in testdata/README.md.

const (
	pipelineSession = "11111111-1111-4111-8111-111111111111"
	pipelineProject = "sample-project"
	pipelineAgent   = "a1b2c3"
	pipelineHost    = "127.0.0.1:43999"
)

var (
	pipelineNow      = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	pipelineFileTime = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	pipelineRange    = usage.Range{From: pipelineNow.AddDate(0, 0, -7), Until: pipelineNow}
)

type pipeline struct {
	root    string
	store   *db.Store
	service *service.Service
}

// newPipeline lays the fixtures out like a transcript root and wires the real
// implementations with a fixed clock.
func newPipeline(t *testing.T) *pipeline {
	t.Helper()
	root := t.TempDir()
	rootFile := filepath.Join(root, pipelineProject, pipelineSession+".jsonl")
	agentFile := filepath.Join(root, pipelineProject, pipelineSession, "subagents", "agent-"+pipelineAgent+".jsonl")
	copyFixture(t, "session.jsonl", rootFile)
	copyFixture(t, "agent-"+pipelineAgent+".jsonl", agentFile)

	now := func() time.Time { return pipelineNow }
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "usage.sqlite"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc, err := service.New(service.Config{
		Store: store, Root: root, RootLabel: "~/sample", Now: now, Location: time.UTC,
		Scan: transcript.Scan, Timeline: timeline.Read, Evaluate: insight.Evaluate,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &pipeline{root: root, store: store, service: svc}
}

func copyFixture(t *testing.T, name, dst string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(dst, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(dst, pipelineFileTime, pipelineFileTime); err != nil {
		t.Fatal(err)
	}
}

func (p *pipeline) scan(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := p.service.SetConsent(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := p.service.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
}

// call runs one service method and decodes the response into out.
func (p *pipeline) call(t *testing.T, method string, params, out any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.service.Handle(context.Background(), method, raw)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	if out != nil {
		if err = json.Unmarshal(resp, out); err != nil {
			t.Fatalf("%s: decode %s: %v", method, resp, err)
		}
	}
	return resp
}

func TestPipelineNumbers(t *testing.T) {
	p := newPipeline(t)
	p.scan(t)
	query := usage.Query{Range: pipelineRange}

	var o usage.Overview
	p.call(t, usage.MethodOverview, query, &o)
	wantTokens := usage.TokenTotals{Input: 52, Output: 1970, CacheRead: 323600, CacheCreate: 3200}
	if o.Sessions != 1 || o.Prompts != 2 || o.ModelCalls != 8 || o.ToolCalls != 6 || o.ToolErrors != 1 ||
		o.Subagents != 1 || o.Compactions != 1 || o.Tokens != wantTokens {
		t.Fatalf("overview = %+v", o)
	}
	if o.CostUSD == nil || *o.CostUSD != 1.25 || o.CostSessions != 1 {
		t.Fatalf("cost = %v over %d sessions; want 1.25 over 1", o.CostUSD, o.CostSessions)
	}
	// One line of the root fixture is broken JSON: the numbers are partial and
	// the response says so instead of looking complete.
	if o.Coverage.State != usage.CoveragePartial || o.Coverage.LinesSkipped != 1 || o.Coverage.FilesTracked != 2 ||
		len(o.Coverage.Reasons) != 1 || o.Coverage.Reasons[0] != usage.ReasonSkippedLines {
		t.Fatalf("coverage = %+v", o.Coverage)
	}

	var tools usage.Tools
	p.call(t, usage.MethodTools, query, &tools)
	if got := toolNames(tools.Tools); got != "Agent,Bash,Skill,WebFetch,mcp__issues__search,mcp__plugin_devkit_chain__status" {
		t.Fatalf("tools = %s", got)
	}
	if len(tools.Hooks) == 0 || len(tools.MCPServers) != 2 || len(tools.Programs) != 1 || tools.Programs[0].Name != "tail" {
		t.Fatalf("groups: hooks %d, mcp %+v, programs %+v", len(tools.Hooks), tools.MCPServers, tools.Programs)
	}

	var page usage.SessionPage
	p.call(t, usage.MethodSessions, usage.SessionQuery{Range: pipelineRange}, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("sessions = %+v", page)
	}
	row := page.Items[0]
	if row.ID != pipelineSession || row.Title != "Fix failing build" || row.ProjectKey != pipelineProject ||
		row.ProjectPath != "/work/sample" || row.Tokens != o.Tokens || row.Subagents != 1 {
		t.Fatalf("session row = %+v", row)
	}

	var detail usage.SessionDetail
	p.call(t, usage.MethodSession, usage.SessionRef{SessionID: pipelineSession}, &detail)
	if detail.Source != usage.SourceAvailable || len(detail.Agents) != 2 || len(detail.Turns) != 6 {
		t.Fatalf("detail: source %q, agents %d, turns %d", detail.Source, len(detail.Agents), len(detail.Turns))
	}
	// One skill has one name on the tools page and in the session detail: its
	// full name, as the Skill call and the attribution of the fixture give it.
	const wantSkills = "devkit:deploy-check"
	if fromTools, fromSession := groupNames(tools.Skills), groupNames(detail.Skills); fromTools != wantSkills || fromSession != wantSkills {
		t.Fatalf("skill groups: tools %q, session %q; want %q from both", fromTools, fromSession, wantSkills)
	}

	var report usage.InsightReport
	p.call(t, usage.MethodInsights, query, &report)
	if total := report.RulesFired + report.RulesPassed + report.RulesSkipped; total == 0 || report.Coverage.State != usage.CoveragePartial {
		t.Fatalf("insights = %+v", report)
	}
}

// groupNames lists the names of groups, sorted so that the result compares as
// a set.
func groupNames(groups []usage.GroupUsage) string {
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		names = append(names, group.Name)
	}
	slices.Sort(names)
	return strings.Join(names, ",")
}

func toolNames(tools []usage.ToolUsage) string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	// Sorted so the assertion does not depend on tie order.
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return strings.Join(names, ",")
}

func TestPipelineSecondPassChangesNothing(t *testing.T) {
	p := newPipeline(t)
	p.scan(t)
	var before, after usage.Status
	p.call(t, usage.MethodStatus, struct{}{}, &before)
	if err := p.service.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.call(t, usage.MethodStatus, struct{}{}, &after)
	if before.DataVersion != after.DataVersion {
		t.Fatalf("data version moved from %d to %d on an unchanged root", before.DataVersion, after.DataVersion)
	}
	if after.Scanning || !after.Consent || after.Coverage.FilesTracked != 2 {
		t.Fatalf("status = %+v", after)
	}
}

func TestPipelineTimelineAndSourceStates(t *testing.T) {
	p := newPipeline(t)
	p.scan(t)
	request := usage.TimelineRequest{SessionID: pipelineSession, Limit: usage.MaxPageSize}

	var page usage.TimelinePage
	p.call(t, usage.MethodTimeline, request, &page)
	if page.Source != usage.SourceAvailable || page.Skipped != 1 || len(page.Items) == 0 {
		t.Fatalf("timeline: source %q, skipped %d, items %d", page.Source, page.Skipped, len(page.Items))
	}
	var result *usage.TimelineItem
	for i := range page.Items {
		if item := &page.Items[i]; item.Kind == usage.ItemToolResult && item.Tool != nil && item.Tool.UseID == "toolu_01" {
			result = item
		}
	}
	if result == nil || result.Tool.Name != usage.ToolNameBash || !result.Tool.IsError || result.Tool.DurationMs == nil {
		t.Fatalf("tool result of the Bash call = %+v", result)
	}

	var agent usage.TimelinePage
	p.call(t, usage.MethodTimeline, usage.TimelineRequest{SessionID: pipelineSession, AgentID: pipelineAgent}, &agent)
	if agent.Source != usage.SourceAvailable || len(agent.Items) == 0 {
		t.Fatalf("sub-agent timeline = %+v", agent)
	}

	// The original transcript is gone: the numbers stay, the conversation is
	// reported as missing rather than as an empty session.
	if err := os.Remove(filepath.Join(p.root, pipelineProject, pipelineSession+".jsonl")); err != nil {
		t.Fatal(err)
	}
	p.call(t, usage.MethodTimeline, request, &page)
	if page.Source != usage.SourceMissing || len(page.Items) != 0 {
		t.Fatalf("timeline after delete: source %q, items %d", page.Source, len(page.Items))
	}
	var detail usage.SessionDetail
	p.call(t, usage.MethodSession, usage.SessionRef{SessionID: pipelineSession}, &detail)
	if detail.Source != usage.SourceMissing || detail.Session.ModelCalls != 8 {
		t.Fatalf("detail after delete: source %q, model calls %d", detail.Source, detail.Session.ModelCalls)
	}
}

func TestPipelineConsentAndExclusion(t *testing.T) {
	p := newPipeline(t)
	ctx := context.Background()
	query := usage.Query{Range: pipelineRange}

	// Without consent nothing is read.
	if err := p.service.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var o usage.Overview
	p.call(t, usage.MethodOverview, query, &o)
	if o.ModelCalls != 0 || o.Coverage.State != usage.CoverageDisabled {
		t.Fatalf("before consent: %d model calls, coverage %q", o.ModelCalls, o.Coverage.State)
	}
	if _, err := p.service.Handle(ctx, usage.MethodTimeline, json.RawMessage(`{"sessionId":"`+pipelineSession+`"}`)); err == nil {
		t.Fatal("timeline without consent must be refused")
	}

	p.scan(t)
	p.call(t, usage.MethodOverview, query, &o)
	if o.ModelCalls != 8 {
		t.Fatalf("after consent: %d model calls; want 8", o.ModelCalls)
	}

	// Excluding the project deletes its numbers and later passes skip it.
	var status usage.Status
	p.call(t, usage.MethodSetExcluded, usage.SetExcluded{Project: pipelineProject, Excluded: true}, &status)
	if len(status.Excluded) != 1 || status.Excluded[0] != pipelineProject {
		t.Fatalf("excluded = %v", status.Excluded)
	}
	if err := p.service.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	p.call(t, usage.MethodOverview, query, &o)
	if o.ModelCalls != 0 || o.Sessions != 0 {
		t.Fatalf("after exclusion: %d model calls, %d sessions", o.ModelCalls, o.Sessions)
	}

	// clearData also withdraws consent.
	p.call(t, usage.MethodClearData, struct{}{}, &status)
	if status.Consent {
		t.Fatal("clearData must withdraw consent")
	}
}

// TestPipelineEmptyResponsesHaveNoNullLists calls every read method on an
// empty store: the UI maps over these lists without checking for null.
func TestPipelineEmptyResponsesHaveNoNullLists(t *testing.T) {
	p := newPipeline(t)
	if err := p.service.SetConsent(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	query := usage.Query{Range: pipelineRange}
	for method, params := range map[string]any{
		usage.MethodStatus:     struct{}{},
		usage.MethodOverview:   query,
		usage.MethodTools:      query,
		usage.MethodProjects:   query,
		usage.MethodInsights:   query,
		usage.MethodSessions:   usage.SessionQuery{Range: pipelineRange},
		usage.MethodExtensions: query,
		usage.MethodExtEvents:  usage.ExtQuery{},
	} {
		raw := p.call(t, method, params, nil)
		if bytes.Contains(raw, []byte(":null")) {
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("%s: %v", method, err)
			}
			for key, value := range decoded {
				if value == nil && !nullableKeys[key] {
					t.Errorf("%s: %q is null in %s", method, key, raw)
				}
			}
		}
	}
}

// nullableKeys are scalar fields that are legitimately null when unknown.
var nullableKeys = map[string]bool{"costUsd": true, "cacheHitRatio": true}

func TestPipelineExtensionEvents(t *testing.T) {
	p := newPipeline(t)
	p.scan(t)
	bearer := strings.Repeat("b", 40)
	handler, err := extension.NewHandler(p.store, pipelineHost, bearer, func() time.Time { return pipelineNow })
	if err != nil {
		t.Fatal(err)
	}
	body := `{"events":[
	  {"version":1,"id":"evt-1","source":"deploy-script","kind":"span","name":"upload","status":"error","level":"error",
	   "timestamp":"2026-03-01T09:00:09.500Z","duration_ms":812.5,"session_id":"` + pipelineSession + `","tool_use_id":"toolu_01",
	   "message":"exit status 1","attributes":{"step":"3"}},
	  {"version":1,"source":"deploy-script","kind":"nope","name":"bad"}
	]}`
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://"+pipelineHost+extension.EventsPath, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	rec := post()
	var reply extension.WireResponse
	if err = json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || rec.Code != http.StatusAccepted || reply.Accepted != 1 || len(reply.Rejected) != 1 {
		t.Fatalf("post: %d %s (%v)", rec.Code, rec.Body, err)
	}
	// The sender retries: the event with an id is stored once.
	if rec = post(); rec.Code != http.StatusAccepted {
		t.Fatalf("second post: %d %s", rec.Code, rec.Body)
	}

	var overview usage.ExtOverview
	p.call(t, usage.MethodExtensions, usage.Query{Range: pipelineRange}, &overview)
	if len(overview.Sources) != 1 || overview.Sources[0].Events != 1 || overview.Sources[0].Errors != 1 ||
		overview.Sources[0].Linked != 1 || overview.Rejected != 2 {
		t.Fatalf("extension overview = %+v", overview)
	}
	var events usage.ExtPage
	p.call(t, usage.MethodExtEvents, usage.ExtQuery{SessionID: pipelineSession}, &events)
	if events.Total != 1 || len(events.Items) != 1 || events.Items[0].Attributes["step"] != "3" {
		t.Fatalf("extension events = %+v", events)
	}

	// The event shows up in the session timeline right after its tool call.
	var page usage.TimelinePage
	p.call(t, usage.MethodTimeline, usage.TimelineRequest{SessionID: pipelineSession, Limit: usage.MaxPageSize}, &page)
	for i, item := range page.Items {
		if item.Kind != usage.ItemToolCall || item.Tool == nil || item.Tool.UseID != "toolu_01" {
			continue
		}
		if i+1 >= len(page.Items) || page.Items[i+1].Kind != usage.ItemExt || page.Items[i+1].Ext == nil ||
			page.Items[i+1].Ext.Name != "upload" {
			t.Fatalf("item after the tool call = %+v", page.Items[i+1])
		}
		return
	}
	t.Fatal("tool call toolu_01 not found in the timeline")
}
