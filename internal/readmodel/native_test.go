package readmodel

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

func TestNativeClaudeLineageAndUnlistedMCP(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	a, err := collect.New(collect.Transcript, "profile", "family", "2.1.329", bytes.Repeat([]byte{7}, 32), []string{"Agent"}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	add := func(stream string, pos int64, raw string) {
		t.Helper()
		e, err := a.Project(collect.Position{Stream: stream, Generation: "one", Offset: pos}, []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(e.Metadata)
		if err != nil || bytes.Contains(b, []byte("private_")) {
			t.Fatalf("unsafe projection: %v", err)
		}
		if err = db.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	add("p", 1, `{"type":"user","sessionId":"s","uuid":"u","promptId":"p","isSidechain":false,"userType":"external","message":{"role":"user","content":"private_request"}}`)
	add("p", 2, `{"type":"attachment","sessionId":"s","uuid":"a","parentUuid":"u","attachment":{"private_marker":"private_attachment"}}`)
	add("p", 3, `{"type":"assistant","sessionId":"s","uuid":"c","parentUuid":"a","message":{"role":"assistant","content":[{"type":"tool_use","id":"delegate","name":"Agent"}]}}`)
	add("p", 4, `{"type":"user","sessionId":"s","uuid":"r","parentUuid":"c","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"delegate"}]},"toolUseResult":{"agentId":"child"}}`)
	add("side", 1, `{"type":"user","sessionId":"s","uuid":"su","promptId":"cm","agentId":"child","isSidechain":true,"userType":"external","message":{"role":"user","content":"private_mission"}}`)
	add("side", 2, `{"type":"assistant","sessionId":"s","uuid":"sc","parentUuid":"su","agentId":"child","message":{"role":"assistant","content":[{"type":"tool_use","id":"1","name":"mcp__private_north__private_lookup"},{"type":"tool_use","id":"2","name":"mcp__private_south__private_lookup"}]}}`)
	point, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	records, err := db.Records(ctx, point)
	if err != nil {
		t.Fatal(err)
	}
	m := New(records, Options{})
	v := q(t, m, "overview", nil)
	if count(v, "tasks") != 1 || count(v, "attempts") != 3 || count(v, "waiting") != 0 || count(obj(v["agents"]), "total") != 1 {
		t.Fatal(v)
	}
	modules := q(t, m, "modules", row{"kind": "MCP_TOOL"})["items"].([]row)
	if len(modules) != 2 || modules[0]["parentId"] == modules[1]["parentId"] || strings.Contains(text(modules[0], "name"), "private") {
		t.Fatal(modules)
	}
	agents := 0
	for _, task := range m.tasks {
		for _, a := range task.attempts {
			if a.actor != task.root {
				agents++
			}
		}
	}
	if agents != 2 {
		t.Fatal("child calls lost their actor")
	}
}

func TestLegacyAttachmentReplayAndSuppression(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	a, err := collect.New(collect.Transcript, "profile", "test", "2.1.329", bytes.Repeat([]byte{9}, 32), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	pos := collect.Position{Stream: "p", Generation: "one", Offset: 1}
	// The old adapter stored only the unsupported marker for an attachment.
	old, err := a.Project(pos, []byte(`{"type":"unsupported","timestamp":"2030-01-01T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	current, err := a.Project(pos, []byte(`{"type":"attachment","timestamp":"2030-01-01T00:00:00Z","sessionId":"s","uuid":"a","parentUuid":"u","attachment":{"content":"private"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if current.PreviousSemanticTag() != old.SemanticTag || current.SemanticTag == old.SemanticTag {
		t.Fatal("legacy semantic proof")
	}
	for _, e := range []collect.Evidence{old, current} {
		if err := db.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	before, err := db.Point(ctx)
	if err != nil || before.Revision != 1 {
		t.Fatal(before, err)
	}
	filter := store.Filter{Source: old.Source, Kind: "all", FromMS: *old.OccurredMS, UntilMS: *old.OccurredMS + 1}
	p, err := db.Preview(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := db.Delete(ctx, filter, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Append(ctx, current); err != nil {
		t.Fatal(err)
	}
	latest, err := db.Point(ctx)
	if err != nil || latest != deleted.Point {
		t.Fatal(latest, err)
	}
	rs, err := db.Records(ctx, latest)
	if err != nil || len(rs) != 0 {
		t.Fatal(len(rs), err)
	}
}
