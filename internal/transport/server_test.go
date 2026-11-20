package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/scenario"
	"github.com/0xmhha/trace-agent/internal/store"
	"github.com/gorilla/websocket"
)

const origin = "wails://wails"

type testDB struct {
	mu     sync.RWMutex
	p      store.Point
	floor  int64
	onRead func()
	gate   func()
}

func (db *testDB) Point(context.Context) (store.Point, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.p, nil
}
func (db *testDB) CommittedAt(_ context.Context, r int64) (time.Time, error) {
	return time.Unix(20+r, 0), nil
}
func (db *testDB) ReplayFloor(context.Context) (int64, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.floor, nil
}
func (db *testDB) Records(_ context.Context, p store.Point) ([]store.Record, error) {
	db.mu.Lock()
	hook := db.onRead
	db.onRead = nil
	cur := db.p
	db.mu.Unlock()
	if p.Epoch != cur.Epoch || p.Revision > cur.Revision {
		return nil, errScope
	}
	if hook != nil {
		hook()
	}
	return nil, nil
}
func (db *testDB) WithEpoch(_ context.Context, epoch int64, send func() error) error {
	db.mu.Lock()
	hook := db.gate
	db.gate = nil
	db.mu.Unlock()
	if hook != nil {
		hook()
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if epoch != db.p.Epoch {
		return errScope
	}
	return send()
}
func (db *testDB) move(r, e int64) {
	db.mu.Lock()
	db.p = store.Point{Revision: r, Epoch: e}
	db.mu.Unlock()
}

func serveTest(t *testing.T, db Backend) (*Server, string) {
	t.Helper()
	s, err := New(db, origin)
	if err != nil {
		t.Fatal(err)
	}
	url, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return s, url
}
func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	d := websocket.Dialer{HandshakeTimeout: time.Second, Subprotocols: []string{protocol}}
	ws, resp, err := d.Dial(url, http.Header{"Origin": {origin}})
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}
func sendTest(t *testing.T, ws *websocket.Conn, v any) {
	t.Helper()
	if err := ws.WriteJSON(v); err != nil {
		t.Fatal(err)
	}
}
func recv(t *testing.T, ws *websocket.Conn) objMsg {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(4 * time.Second))
	var out objMsg
	if err := ws.ReadJSON(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

type objMsg struct {
	Type, ID string
	Payload  json.RawMessage
}

func (m objMsg) value(t *testing.T, op string) object {
	t.Helper()
	var out object
	if m.Type != "next" || json.Unmarshal(m.Payload, &out) != nil {
		t.Fatalf("want next(%s), got %s %s", op, m.Type, m.Payload)
	}
	return obj(obj(out, "data"), op)
}
func auth(t *testing.T, s *Server, url string, manage bool) *websocket.Conn {
	t.Helper()
	ticket, err := s.Ticket(manage)
	if err != nil {
		t.Fatal(err)
	}
	ws := dial(t, url)
	sendTest(t, ws, envelope{Type: "connection_init", Payload: object{"ticket": ticket}})
	if recv(t, ws).Type != "connection_ack" {
		t.Fatal("missing ack")
	}
	return ws
}
func subscribe(t *testing.T, ws *websocket.Conn, id, op string, vars object) {
	t.Helper()
	raw, err := documents.ReadFile("operations/" + op + ".graphql")
	if err != nil {
		t.Fatal(err)
	}
	sendTest(t, ws, envelope{ID: id, Type: "subscribe", Payload: object{"query": string(raw), "operationName": op, "variables": vars}})
}
func watchInput() object {
	return object{"scope": object{"mode": "LIVE", "filter": object{"interval": object{"from": "1970-01-01T00:00:00Z", "until": "1970-01-01T00:00:10Z"}}}, "selection": object{"kind": "TASKS"}}
}
func waitHook(t *testing.T, ch <-chan bool) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("read never reached barrier")
	}
}
func TestSnapshotCatchUpResumeAndPrivacy(t *testing.T) {
	db := &testDB{p: store.Point{Epoch: 1}}
	s, url := serveTest(t, db)
	ws := auth(t, s, url, true)
	started := make(chan bool)
	release := make(chan bool)
	db.mu.Lock()
	db.onRead = func() { close(started); <-release }
	db.mu.Unlock()
	input := watchInput()
	subscribe(t, ws, "w", "watch", object{"input": input})
	waitHook(t, started)
	db.move(3, 1)
	close(release)
	snap := recv(t, ws).value(t, "watch")
	if snap["__typename"] != "Snapshot" || str(obj(obj(snap, "stamp"), "point"), "revision") != "0" {
		t.Fatal(snap)
	}
	repl := recv(t, ws).value(t, "watch")
	if repl["__typename"] != "Replacement" || str(obj(repl, "base"), "revision") != "0" {
		t.Fatal(repl)
	}
	stamp := obj(obj(repl, "snapshot"), "stamp")
	if str(obj(stamp, "point"), "revision") != "3" {
		t.Fatal(stamp)
	}
	// Fixed query is validated against the snapshot's exact scope.
	fixed := object{"point": stamp["point"], "cohort": stamp["cohort"], "filter": obj(input, "scope")["filter"]}
	subscribe(t, ws, "q", "tasks", object{"scope": fixed})
	_ = recv(t, ws).value(t, "tasks")
	if recv(t, ws).Type != "complete" {
		t.Fatal("query did not finish")
	}
	sendTest(t, ws, envelope{ID: "w", Type: "complete"})
	ws2 := auth(t, s, url, true)
	db.move(5, 1)
	input["resume"] = stamp["resume"]
	subscribe(t, ws2, "r", "watch", object{"input": input})
	res := recv(t, ws2).value(t, "watch")
	if res["__typename"] != "Replacement" || str(obj(res, "base"), "revision") != "3" {
		t.Fatal(res)
	}
	// Invalidate after resolving a replacement, before the final epoch check.
	aboutToSend := make(chan bool)
	allowSend := make(chan bool)
	db.mu.Lock()
	db.gate = func() { close(aboutToSend); <-allowSend }
	db.mu.Unlock()
	db.move(6, 1)
	waitHook(t, aboutToSend)
	db.move(7, 2)
	close(allowSend)
	reset := recv(t, ws2).value(t, "watch")
	if reset["reason"] != "EPOCH_CHANGED" {
		t.Fatal("stale frame crossed privacy barrier", reset)
	}
	if recv(t, ws2).Type != "complete" {
		t.Fatal("watch not terminated")
	}
	subscribe(t, ws2, "ack", "acknowledgeEpoch", object{"epoch": "2"})
	_ = recv(t, ws2).value(t, "acknowledgeEpoch")
	_ = recv(t, ws2)
	subscribe(t, ws2, "old", "tasks", object{"scope": fixed})
	if recv(t, ws2).Type != "error" {
		t.Fatal("old scope survived epoch")
	}
}
func TestResumeReasonsAndFollowEnd(t *testing.T) {
	db := &testDB{p: store.Point{Revision: 2, Epoch: 1}}
	s, url := serveTest(t, db)
	ws := auth(t, s, url, false)
	input := watchInput()
	obj(input, "scope")["liveWindow"] = "FOLLOW_END"
	subscribe(t, ws, "w", "watch", object{"input": input})
	snap := recv(t, ws).value(t, "watch")
	stamp := obj(snap, "stamp")
	if str(obj(stamp, "effectiveInterval"), "until") != "1970-01-01T00:00:22.001Z" {
		t.Fatal(stamp)
	}
	sendTest(t, ws, envelope{ID: "w", Type: "complete"})
	input["resume"] = stamp["resume"]
	for _, test := range []struct {
		id, reason string
		edit       func()
	}{
		{"filter", "FILTER_CHANGED", func() { obj(input, "selection")["kind"] = "SOURCES" }},
		{"expired", "REPLAY_EXPIRED", func() {
			obj(input, "selection")["kind"] = "TASKS"
			db.mu.Lock()
			db.floor = 3
			db.p.Revision = 4
			db.mu.Unlock()
		}},
	} {
		test.edit()
		subscribe(t, ws, test.id, "watch", object{"input": input})
		if recv(t, ws).value(t, "watch")["reason"] != test.reason {
			t.Fatal(test.reason)
		}
		_ = recv(t, ws)
	}
}
func TestAdmissionAndOperationLimits(t *testing.T) {
	db := &testDB{p: store.Point{Epoch: 1}}
	s, url := serveTest(t, db)
	for _, h := range []http.Header{{"Origin": {"https://untrusted.invalid"}}, {"Origin": {"null"}}, {"Origin": {origin}, "Host": {"foreign.invalid"}}} {
		d := websocket.Dialer{Subprotocols: []string{protocol}}
		ws, resp, err := d.Dial(url, h)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			_ = ws.Close()
			t.Fatal("untrusted handshake accepted")
		}
	}
	ticket, _ := s.Ticket(false)
	for n := range 2 {
		ws := dial(t, url)
		sendTest(t, ws, envelope{Type: "connection_init", Payload: object{"ticket": ticket}})
		if n == 0 {
			if recv(t, ws).Type != "connection_ack" {
				t.Fatal("ack")
			}
		} else {
			_, _, err := ws.ReadMessage()
			if !websocket.IsCloseError(err, 4403) {
				t.Fatal("ticket reused", err)
			}
		}
	}
	ws := auth(t, s, url, false)
	input := watchInput()
	obj(input, "selection")["page"] = object{"first": 201}
	subscribe(t, ws, "big", "watch", object{"input": input})
	if recv(t, ws).Type != "error" {
		t.Fatal("oversize page")
	}
	sendTest(t, ws, envelope{ID: "unknown", Type: "subscribe", Payload: object{"query": "{ __schema { types { name } } }"}})
	if recv(t, ws).Type != "error" {
		t.Fatal("unregistered operation")
	}
	subscribe(t, ws, "manage", "setConsent", object{"sourceId": "s", "enabled": false})
	if recv(t, ws).Type != "error" {
		t.Fatal("read-only ticket mutated policy")
	}
}
func TestDeleteReceiptsThroughSocket(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a, err := collect.New(collect.Transcript, "profile", "instance", "2.1.329", make([]byte, 32), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Register(ctx, a); err != nil {
		t.Fatal(err)
	}
	s, url := serveTest(t, db)
	ws := auth(t, s, url, true)
	input := object{"sourceId": a.Source(), "interval": object{"from": "2026-01-01T00:00:00Z", "until": "2026-01-02T00:00:00Z"}, "kind": "ALL"}
	subscribe(t, ws, "preview", "previewDelete", object{"input": input})
	preview := recv(t, ws).value(t, "previewDelete")
	_ = recv(t, ws)
	if _, ok := preview["receipt"].(string); !ok {
		t.Fatal(preview)
	}
	subscribe(t, ws, "confirm", "confirmDelete", object{"receipt": preview["receipt"]})
	result := recv(t, ws).value(t, "confirmDelete")
	_ = recv(t, ws)
	ep := str(obj(result, "point"), "epoch")
	n, _ := strconv.Atoi(ep)
	if n < 2 {
		t.Fatal(result)
	}
	subscribe(t, ws, "ack", "acknowledgeEpoch", object{"epoch": ep})
	_ = recv(t, ws)
	_ = recv(t, ws)
	subscribe(t, ws, "again", "confirmDelete", object{"receipt": preview["receipt"]})
	if recv(t, ws).Type != "error" {
		t.Fatal(errors.New("reused deletion receipt"))
	}
}

type guardedStore struct {
	*store.Store
	guard  sync.Mutex
	before func()
}

func (db *guardedStore) WithEpoch(ctx context.Context, epoch int64, send func() error) error {
	db.guard.Lock()
	f := db.before
	db.before = nil
	db.guard.Unlock()
	if f != nil {
		f()
	}
	return db.Store.WithEpoch(ctx, epoch, send)
}

func TestBodyFramesAndEmptySourceAtFixedPoint(t *testing.T) {
	ctx := t.Context()
	base, err := store.Open(ctx, filepath.Join(t.TempDir(), "frames.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	db := &guardedStore{Store: base}
	a, err := collect.New(collect.Transcript, "synthetic", "synthetic", "2.1.329", make([]byte, 32), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	a = a.WithBodyCollection()
	first, err := db.Register(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	s, url := serveTest(t, db)
	f := filter{From: time.Now().UTC().Add(-time.Minute), Until: time.Now().UTC().Add(time.Hour), Clients: []string{}, Tasks: []string{}, Sources: []string{a.Source()}}
	row, err := s.data(ctx, view{f: f, p: first}, "source", object{"sourceId": a.Source()})
	if err != nil {
		t.Fatal(err)
	}
	if obj(row.(object), "source")["id"] != a.Source() || row.(object)["consent"] != false {
		t.Fatal("registered empty source missing")
	}
	if _, err = db.SetConsent(ctx, a.Source(), true); err != nil {
		t.Fatal(err)
	}
	input := object{"type": "user", "uuid": "message", "sessionId": "session", "promptId": "prompt", "userType": "external", "isMeta": false, "timestamp": iso(time.Now().Add(time.Second)), "message": object{"role": "user", "content": "Review the sample plan"}}
	raw, _ := json.Marshal(input)
	e, err := a.Project(collect.Position{Stream: "test", Generation: "1"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	pt, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := db.Records(ctx, pt)
	if err != nil || len(r) != 1 {
		t.Fatal(r, err)
	}
	id := strconv.FormatInt(r[0].ID, 10)
	filterArg := object{"interval": object{"from": iso(f.From), "until": iso(f.Until)}, "sources": []any{a.Source()}}
	scope := func(p store.Point) object {
		v := view{p: p, f: f}
		return object{"point": pointMap(p), "filter": filterArg, "cohort": v.token(s, v.sample)}
	}
	ws := auth(t, s, url, false)
	read := func(seq string, p store.Point) {
		subscribe(t, ws, seq, "evidence", object{"scope": scope(p), "evidenceId": id})
	}
	read("allowed", pt)
	d := obj(recv(t, ws).value(t, "evidence"), "data")
	slot := anyList(d, "fields")[0].(object)
	if slot["state"] != "AVAILABLE" || slot["text"] != "Review the sample plan" || slot["conversationRole"] != "USER" {
		t.Fatal(slot)
	}
	_ = recv(t, ws)
	start, release := make(chan bool), make(chan bool)
	db.guard.Lock()
	db.before = func() { close(start); <-release }
	db.guard.Unlock()
	read("race", pt)
	waitHook(t, start)
	next, err := db.SetConsent(ctx, a.Source(), false)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if msg := recv(t, ws); msg.Type != "error" || strings.Contains(string(msg.Payload), "Review the sample plan") {
		t.Fatal("revoked body crossed write barrier", msg.Type)
	}
	subscribe(t, ws, "ack", "acknowledgeEpoch", object{"epoch": pointMap(next)["epoch"]})
	_ = recv(t, ws)
	_ = recv(t, ws)
	read("revoked", next)
	d = obj(recv(t, ws).value(t, "evidence"), "data")
	slot = anyList(d, "fields")[0].(object)
	if slot["state"] != "REVOKED" || slot["text"] != nil {
		t.Fatal(slot)
	}
}

func TestBodyDisplayBudgetAtUTF8Boundary(t *testing.T) {
	b := bodyView{bytes: 5}
	a := b.field("1", collect.BodySlot{Field: "result.3", Status: "truncated", Text: "가나다", Role: "tool"}, nil)
	if a["text"] != "가" || a["truncation"] != "SOURCE_AND_DISPLAY" || a["ordinal"] != 3 {
		t.Fatal(a)
	}
	a = b.field("2", collect.BodySlot{Field: "message", Status: "sanitized", Text: "aaa"}, nil)
	if a["text"] != "aa" || a["truncation"] != "DISPLAY_LIMIT" || b.bytes != 0 {
		t.Fatal(a)
	}
}

func TestCardAndAttemptShareSQLitePoint(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "point.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fixture, err := os.Open("../../projects/trace-agent/fixtures/stage-4/ia120.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fixture.Close() }()
	seed, err := scenario.Seed(t.Context(), db, fixture, 120)
	if err != nil {
		t.Fatal(err)
	}
	s, url := serveTest(t, db)
	ws := auth(t, s, url, false)
	input := watchInput()
	selection := obj(input, "selection")
	selection["kind"] = "OVERVIEW"
	f := object{"interval": object{"from": "2026-11-19T01:00:00Z", "until": "2026-11-19T01:10:00Z"}}
	obj(input, "scope")["filter"] = f
	subscribe(t, ws, "cards", "watch", object{"input": input})
	snap := recv(t, ws).value(t, "watch")
	stamp := obj(snap, "stamp")
	screen := obj(snap, "screen")
	if str(obj(stamp, "point"), "revision") != "120" || obj(screen, "attempts")["count"] != float64(5) || obj(screen, "failureRate")["eligible"] != float64(4) || obj(screen, "failureRate")["value"] != 0.25 || obj(screen, "waiting")["count"] != float64(1) {
		t.Fatal("incorrect fixed card", screen["waiting"], stamp["point"])
	}
	scope := object{"point": stamp["point"], "cohort": obj(screen, "waiting")["cohort"], "filter": f}
	subscribe(t, ws, "list", "attempts", object{"scope": scope})
	read := recv(t, ws).value(t, "attempts")
	_ = recv(t, ws)
	items := anyList(obj(read, "data"), "items")
	if len(items) != 1 || str(items[0].(object), "id") != seed.IDs["attempt-5"] || obj(obj(read, "stamp"), "point")["revision"] != "120" {
		t.Fatal(items)
	}
	subscribe(t, ws, "attempt", "attempt", object{"scope": scope, "key": object{"taskId": seed.IDs["A"], "id": seed.IDs["attempt-5"]}})
	d := obj(recv(t, ws).value(t, "attempt"), "data")
	_ = recv(t, ws)
	if str(obj(d, "attempt"), "phase") != "RUNNING" || len(anyList(d, "edges")) == 0 {
		t.Fatal(d)
	}
	subscribe(t, ws, "wrong", "attempt", object{"scope": scope, "key": object{"taskId": seed.IDs["A"], "id": seed.IDs["attempt-1"]}})
	m := recv(t, ws)
	if !bytes.Contains(m.Payload, []byte(`"code":"NOT_FOUND"`)) || m.value(t, "attempt")["data"] != nil {
		t.Fatal(m.Type)
	}
}

func TestScopedTelemetryOverSocket(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "telemetry.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a, err := collect.New(collect.Telemetry, "profile", "otel", "1.0", make([]byte, 32), nil, func() time.Time { return time.Unix(2, 0) })
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("../../projects/trace-agent/fixtures/stage-1/claude-otel.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var native struct {
			Stream string
			Seq    int64
			Record json.RawMessage
		}
		if err = json.Unmarshal(scan.Bytes(), &native); err != nil {
			t.Fatal(err)
		}
		e, err := a.Project(collect.Position{Stream: native.Stream, Generation: "g", Offset: native.Seq}, native.Record)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Append(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	s, url := serveTest(t, db)
	ws := auth(t, s, url, false)
	input := watchInput()
	obj(input, "selection")["kind"] = "SOURCES"
	subscribe(t, ws, "w", "watch", object{"input": input})
	snap := recv(t, ws).value(t, "watch")
	stamp := obj(snap, "stamp")
	filt := obj(obj(input, "scope"), "filter")
	base := object{"point": stamp["point"], "cohort": stamp["cohort"], "filter": filt}
	subscribe(t, ws, "unscoped", "sourceModelAttempts", object{"scope": base, "sourceId": a.Source()})
	if recv(t, ws).Type != "error" {
		t.Fatal("unscoped model attempt")
	}
	subscribe(t, ws, "scope", "sourceScope", object{"scope": base, "sourceId": a.Source()})
	narrow := recv(t, ws).value(t, "sourceScope")
	_ = recv(t, ws)
	scoped := object{"point": narrow["point"], "cohort": narrow["cohort"], "filter": object{"interval": filt["interval"], "sources": []any{a.Source()}}}
	query := func(name string, args object) object {
		args["scope"] = scoped
		subscribe(t, ws, "query", name, args)
		d := obj(recv(t, ws).value(t, name), "data")
		_ = recv(t, ws)
		return d
	}
	items := anyList(query("sourceModelAttempts", object{"sourceId": a.Source()}), "items")
	if len(items) != 1 || items[0].(object)["phase"] != "ENDED" {
		t.Fatal(items)
	}
	d := query("sourceModelAttempt", object{"sourceId": a.Source(), "id": items[0].(object)["id"]})
	if len(anyList(d, "edges")) != 2 || len(anyList(obj(d, "evidence"), "items")) != 2 {
		t.Fatal(d)
	}
	unlinked := anyList(query("unlinked", object{"sourceId": a.Source()}), "items")
	if len(unlinked) != 1 {
		t.Fatal(unlinked)
	}
	d = query("evidence", object{"evidenceId": unlinked[0].(object)["id"]})
	if d["candidates"] != float64(0) || anyList(d, "fields")[0].(object)["state"] != "DISABLED" {
		t.Fatal(d)
	}
}

func TestBoundPageCursor(t *testing.T) {
	s, err := New(&testDB{p: store.Point{Epoch: 1}}, origin)
	if err != nil {
		t.Fatal(err)
	}
	f := filter{From: time.Unix(1, 0).UTC(), Until: time.Unix(100, 0).UTC()}
	v := view{f: f, p: store.Point{Revision: 9, Epoch: 1}}
	args := object{"key": object{"taskId": "A"}, "page": object{"first": 2}, "window": "LATEST", "relationsPage": object{"first": 1}}
	_, bindings, err := unsealPages(s, v, "sequence", args)
	if err != nil {
		t.Fatal(err)
	}
	result := object{"relationPage": object{"endCursor": "1"}, "previousPage": "0"}
	signPages(s, v, result, "page", bindings)
	token := str(obj(result, "relationPage"), "endCursor")
	obj(args, "relationsPage")["after"] = token
	got, _, err := unsealPages(s, v, "sequence", args)
	if err != nil || str(obj(got, "relationsPage"), "after") != "1" {
		t.Fatal(got, err)
	}
	for _, apply := range []func(){func() { obj(args, "page")["after"] = str(result, "previousPage") }, func() { delete(obj(args, "page"), "after"); obj(args, "relationsPage")["first"] = 2 }, func() { obj(args, "relationsPage")["first"] = 1; v.p.Revision++ }, func() { v.p.Revision--; args["window"] = "FIRST" }} {
		apply()
		if _, _, err = unsealPages(s, v, "sequence", args); err == nil {
			t.Fatal("cross-scope page accepted")
		}
	}
}

func TestSourceCapabilitiesAndGapsAtPoint(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "sources.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a, err := collect.New(collect.Rollout, "synthetic", "file", "0.169.1", make([]byte, 32), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Register(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err = db.ReportHealth(ctx, a, "connected"); err != nil {
		t.Fatal(err)
	}
	early, _ := db.Point(ctx)
	s, url := serveTest(t, db)
	v := view{p: early, f: filter{From: time.Now().UTC().Add(-time.Hour), Until: time.Now().UTC().Add(time.Hour)}}
	detail := func(p store.Point) object {
		v.p = p
		raw, err := s.data(ctx, v, "source", object{"sourceId": a.Source()})
		if err != nil {
			t.Fatal(err)
		}
		return raw.(object)
	}
	capAt := func(d object, code string) string {
		for _, raw := range d["capabilities"].([]any) {
			c := raw.(object)
			if c["code"] == code {
				return str(c, "state")
			}
		}
		return ""
	}
	first := detail(early)
	if capAt(first, "history") != "UNVERIFIED" || capAt(first, "telemetry") != "UNAVAILABLE" || str(obj(first, "source"), "asOfHealth") != "CONNECTED" {
		t.Fatal(first)
	}
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	line := []byte(`{"type":"session_meta","payload":{"id":"example","cli_version":"0.169.1"}}` + "\n")
	if err = root.WriteFile("session.jsonl", line, 0600); err != nil {
		t.Fatal(err)
	}
	cur, err := a.ReadFile(ctx, root, "session.jsonl", collect.FileCursor{}, db.FileCommit)
	if err != nil {
		t.Fatal(err)
	}
	if err = root.WriteFile("next.jsonl", line, 0600); err != nil {
		t.Fatal(err)
	}
	if err = root.Rename("next.jsonl", "session.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ReadFile(ctx, root, "session.jsonl", cur, db.FileCommit); err != nil {
		t.Fatal(err)
	}
	if err = db.ReportHealth(ctx, a, "lost"); err != nil {
		t.Fatal(err)
	}
	p, _ := db.Point(ctx)
	later := detail(p)
	if capAt(later, "history") != "AVAILABLE" || obj(later, "source")["clientVersion"] != "0.169.1" || obj(later, "source")["asOfHealth"] != "LOST" || len(anyList(later, "gaps")) != 2 {
		t.Fatal(later)
	}
	old := detail(early)
	if len(anyList(old, "gaps")) != 0 || capAt(old, "history") != "UNVERIFIED" || obj(old, "source")["asOfHealth"] != "CONNECTED" {
		t.Fatal("fixed source changed")
	}
	ws := auth(t, s, url, false)
	subscribe(t, ws, "status", "currentStatus", object{})
	current := recv(t, ws).value(t, "currentStatus")
	live := anyList(current, "sources")[0].(object)
	if live["health"] != "LOST" || live["checkedAt"] != obj(later, "source")["asOfCheckedAt"] {
		t.Fatal(live, current)
	}
}
