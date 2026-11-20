package observer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/observer"
	"github.com/0xmhha/trace-agent/internal/readmodel"
	"github.com/0xmhha/trace-agent/internal/store"
)

type obj = map[string]any

func TestSourceScopedDelivery(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	a, err := collect.New(collect.Transcript, "selected", "family", "2.1.329", bytes.Repeat([]byte{13}, 32), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	appendRaw := func(stream string, n int64, raw string) {
		e, err := a.Project(collect.Position{Stream: stream, Generation: "one", Offset: n}, []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	addCall := func(stream, prompt string) {
		appendRaw(stream, 1, `{"type":"user","sessionId":"session","uuid":"u","promptId":"`+prompt+`","userType":"external","isMeta":false,"timestamp":"2025-01-01T00:00:00Z","message":{"role":"user","content":"request"}}`)
		appendRaw(stream, 2, `{"type":"assistant","sessionId":"session","uuid":"c","parentUuid":"u","timestamp":"2025-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call","name":"mcp__private_north__lookup"}]}}`)
	}
	addCall("root", "first")
	one := strings.Repeat("a", 40)
	two := strings.Repeat("b", 40)
	h, err := observer.Listen(db, a, observer.Hook, "", 0, one)
	if err != nil {
		t.Fatal(err)
	}
	m, err := observer.Listen(db, a, observer.MCP, "private_north", 0, two)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close(ctx); _ = m.Close(ctx) }()
	post := func(endpoint, token, raw string, code int) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		c := http.Client{Timeout: time.Second}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != code {
			msg, _ := io.ReadAll(res.Body)
			t.Fatal(res.StatusCode, string(msg))
		}
	}
	hook := `{"hook_event_name":"PreToolUse","session_id":"session","tool_use_id":"call","tool_input":{"secret":"private_input"}}`
	post(h.URL(), one, hook, 403)
	for _, r := range []*observer.Receiver{h, m} {
		if _, err := db.Register(ctx, r.Source()); err != nil {
			t.Fatal(err)
		}
	}
	post(h.URL(), two, hook, 401)
	post(h.URL(), one, hook, 200)
	post(h.URL(), one, hook, 200)
	pkt := func(action, connection, extra string) string {
		return `{"version":1,"server":"private_north","connection_id":"` + connection + `","rpc_id":"22","clock_id":"server-clock","action":"` + action + `","timestamp":"2025-01-01T00:00:02Z"` + extra + `}`
	}
	post(m.URL(), two, pkt("return", "one", `,"rpc_error_code":-32603,"detail":"private_error"`), 200)
	post(m.URL(), two, pkt("receive", "one", ""), 200)
	lookup := func() (obj, []store.Record) {
		p, err := db.Point(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rs, err := db.Records(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		view := readmodel.New(rs, readmodel.Options{})
		v, err := view.Query("attempts", nil)
		if err != nil {
			t.Fatal(err)
		}
		rows := v.(obj)["items"].([]obj)
		if len(rows) != 1 {
			t.Fatal("expected one client attempt", len(rows))
		}
		v, err = view.Query("attempt", obj{"key": obj{"taskId": rows[0]["taskId"], "id": rows[0]["id"]}})
		if err != nil {
			t.Fatal(err)
		}
		return v.(obj), rs
	}
	count := func(o obj, actor, action, grade string) int {
		var n int
		for _, r := range o["edges"].([]obj) {
			if r["actor"] == actor && r["action"] == action && r["grade"] == grade {
				n++
			}
		}
		return n
	}
	detail, _ := lookup()
	if count(detail, "TOOL", "RECEIVE", "UNKNOWN") != 1 || count(detail, "TOOL", "RETURN", "UNKNOWN") != 1 || count(detail, "CLIENT", "PROPOSE", "DIRECT") != 1 {
		t.Fatal("unbridged delivery", detail["edges"])
	}
	post(m.URL(), two, pkt("bridge", "other", `,"session_id":"session","client_call_id":"call"`), 200)
	detail, _ = lookup()
	if count(detail, "TOOL", "RECEIVE", "DIRECT") != 0 {
		t.Fatal("mixed connections")
	}
	post(m.URL(), two, pkt("bridge", "one", `,"session_id":"session","client_call_id":"call"`), 200)
	detail, rs := lookup()
	if count(detail, "TOOL", "RECEIVE", "DIRECT") != 1 || count(detail, "TOOL", "RETURN", "DIRECT") != 1 || detail["attempt"].(obj)["result"] != "RPC_ERROR" || len(detail["durations"].([]obj)) != 0 {
		t.Fatal("missing direct segments or mixed-clock duration", detail)
	}
	for _, r := range rs {
		v, err := json.Marshal(r.Evidence.Metadata)
		if err != nil || bytes.Contains(v, []byte("private_")) {
			t.Fatal("plaintext stored")
		}
	}
	// An equally plausible client call prevents attribution, even when time,
	// name, server, session and the call ID all match.
	addCall("copy", "second")
	p, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rs, err = db.Records(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	view := readmodel.New(rs, readmodel.Options{})
	v, err := view.Query("unlinked", obj{"sourceId": m.Source().Source()})
	if err != nil || len(v.(obj)["items"].([]obj)) != 4 {
		t.Fatal("ambiguous deliveries", v, err)
	}
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
