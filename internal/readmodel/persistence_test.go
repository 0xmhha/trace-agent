package readmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/scenario"
	"github.com/0xmhha/trace-agent/internal/store"
)

func seedDB(t *testing.T, dir, filename string, rev int64) (*store.Store, scenario.SeedResult) {
	t.Helper()
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	root, err := os.OpenRoot(filepath.Join("../../projects/trace-agent/fixtures", dir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	s, err := scenario.Seed(ctx, db, f, rev)
	if err != nil {
		t.Fatal(err)
	}
	return db, s
}

func TestStoredIA120(t *testing.T) {
	ctx := t.Context()
	db, s := seedDB(t, "stage-4", "ia120.jsonl", 120)
	fixed := func(p store.Point) *Model {
		t.Helper()
		records, err := db.Records(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return New(records, options())
	}
	if s.Point.Revision != 120 || s.Point.Epoch != 1 {
		t.Fatal(s.Point)
	}
	if n := count(q(t, fixed(store.Point{Revision: 119, Epoch: 1}), "overview", nil), "tasks"); n != 0 {
		t.Fatal(n)
	}
	m := fixed(s.Point)
	v := q(t, m, "overview", nil)
	agents := obj(v["agents"])
	if count(v, "attempts") != 5 || count(v, "waiting") != 1 || count(agents, "total") != 4 || count(agents, "ended") != 2 || count(agents, "running") != 1 || count(agents, "unknown") != 1 || obj(v["failureRate"])["value"] != 0.25 {
		t.Fatal(v)
	}
	k := row{"key": row{"taskId": s.IDs["A"], "id": s.IDs["attempt-5"]}}
	detail := q(t, m, "attempt", k)
	edges := detail["edges"].([]row)
	if len(edges) == 0 || len(edges[0]["evidenceIds"].([]string)) != 1 {
		t.Fatal(detail)
	}
	proof := edges[0]["evidenceIds"].([]string)[0]
	e := q(t, m, "evidence", row{"evidenceId": proof})
	if obj(e["evidence"])["sourceId"] != s.Source {
		t.Fatal(e)
	}
}

func TestStoredSequenceAndDeletion(t *testing.T) {
	ctx := t.Context()
	db, s := seedDB(t, "stage-6", "sequence.jsonl", 0)
	rows, err := db.Records(ctx, s.Point)
	if err != nil {
		t.Fatal(err)
	}
	m := New(rows, options())
	k := row{"key": row{"taskId": s.IDs["L7"]}, "eventId": s.IDs["L7"], "laneId": s.IDs["B"]}
	w := q(t, m, "sequenceInspector", k)
	if obj(w["waits"])["items"].([]row)[0]["attemptId"] != s.IDs["b-mcp"] {
		t.Fatal(w)
	}
	var ret FactEvent
	for _, r := range rows {
		for _, f := range r.Facts {
			if f.ID == s.IDs["L11"] {
				ret = f
			}
		}
	}
	if ret.ID == "" {
		t.Fatal("missing return proof")
	}
	filter := store.Filter{Source: s.Source, Kind: "all", FromMS: *ret.Occurred, UntilMS: *ret.Occurred + 1}
	preview, err := db.Preview(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	erased, err := db.Delete(ctx, filter, preview)
	if err != nil {
		t.Fatal(err)
	}
	if erased.Facts < 2 {
		t.Fatalf("erased return and dependent join: %+v", erased)
	}
	if _, err = db.Records(ctx, s.Point); !errors.Is(err, store.ErrStale) {
		t.Fatal(err)
	}
	rows, err = db.Records(ctx, erased.Point)
	if err != nil {
		t.Fatal(err)
	}
	got := q(t, New(rows, options()), "sequence", k)
	for _, r := range got["events"].([]row) {
		if r["id"] == s.IDs["L11"] || r["id"] == s.IDs["L15"] {
			t.Fatal("deleted return or dependent join restored", r)
		}
	}
}

func TestErasedCorrectionCannotRevivePreviousFact(t *testing.T) {
	ctx := t.Context()
	db, s := seedDB(t, "stage-6", "sequence.jsonl", 0)
	rows, err := db.Records(ctx, s.Point)
	if err != nil {
		t.Fatal(err)
	}
	var previous, proof FactEvent
	for _, r := range rows {
		for _, f := range r.Facts {
			switch f.ID {
			case s.IDs["L11"]:
				previous = f
			case s.IDs["L12"]:
				proof = f
			}
		}
	}
	revised := previous
	revised.Evidence = proof.Evidence
	p, err := db.PublishFacts(ctx, s.Point, []FactEvent{revised})
	if err != nil {
		t.Fatal(err)
	}
	filter := store.Filter{Source: s.Source, Kind: "all", FromMS: *proof.Occurred, UntilMS: *proof.Occurred + 1}
	pre, err := db.Preview(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	erased, err := db.Delete(ctx, filter, pre)
	if err != nil {
		t.Fatal(err)
	}
	for _, rev := range []int64{s.Point.Revision, p.Revision, erased.Point.Revision} {
		rs, err := db.Records(ctx, store.Point{Revision: rev, Epoch: erased.Point.Epoch})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			for _, f := range r.Facts {
				if f.ID == previous.ID || f.ID == s.IDs["L15"] {
					t.Fatal("resurrected previous fact or dependent", rev, f.ID)
				}
			}
		}
	}
}

func TestStoredSequenceFrozenAndFollow(t *testing.T) {
	ctx := t.Context()
	opts := options()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var fixture bytes.Buffer
	for i := 1; i <= 51; i++ {
		timeMS := opts.From.Add(time.Duration(i) * time.Second).UnixMilli()
		f := FactEvent{ID: strconv.Itoa(i), Task: "task", Session: "session", Root: "root", Actor: "root", Source: "source", Client: "CODEX", Grade: "DIRECT", Kind: "message", Action: "SEND", Clock: "clock", Occurred: &timeMS, Observed: timeMS, Evidence: []string{strconv.Itoa(i)}}
		if err := json.NewEncoder(&fixture).Encode(f); err != nil {
			t.Fatal(err)
		}
	}
	s, err := scenario.Seed(ctx, db, &fixture, 0)
	if err != nil {
		t.Fatal(err)
	}
	opts.Until = opts.From.Add(52 * time.Second)
	pageAt := func(p store.Point, o Options) []row {
		rows, err := db.Records(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		k := row{"key": row{"taskId": s.IDs["task"]}, "window": "LATEST"}
		return q(t, New(rows, o), "sequence", k)["events"].([]row)
	}
	initial := pageAt(s.Point, opts)
	appendEvent := func(id string, at time.Time) store.Point {
		t.Helper()
		adapter, err := collect.New(collect.Rollout, "synthetic", "acceptance", "1", bytes.Repeat([]byte{7}, 32), nil, func() time.Time { return at })
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(row{"timestamp": at.UTC().Format(time.RFC3339Nano), "type": "response_item", "payload": row{"type": "message", "id": id, "role": "system"}})
		if err != nil {
			t.Fatal(err)
		}
		e, err := adapter.Project(collect.Position{Stream: id, Generation: "test", Offset: 1}, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
		base, err := db.Point(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.Records(ctx, base)
		if err != nil {
			t.Fatal(err)
		}
		f := FactEvent{ID: key(id), Task: s.IDs["task"], Session: s.IDs["session"], Root: s.IDs["root"], Actor: s.IDs["root"], Source: e.Source, Client: "CODEX", Kind: "message", Action: "SEND", Grade: "DIRECT", Clock: s.IDs["clock"], Occurred: e.OccurredMS, Observed: e.ObservedMS, Evidence: []string{recID(rows[len(rows)-1])}}
		p, err := db.PublishFacts(ctx, base, []FactEvent{f})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	live := opts
	live.Until = opts.Until.Add(2 * time.Second)
	head := appendEvent("52", opts.Until.Add(time.Second))
	second := pageAt(head, live)
	if initial[0]["id"] != s.IDs["2"] || initial[49]["id"] != s.IDs["51"] || second[0]["id"] != s.IDs["3"] || second[49]["id"] != key("52") {
		t.Fatal("sliding page failed")
	}
	late := appendEvent("late", opts.From.Add(49*time.Second+100*time.Millisecond))
	latest := pageAt(late, live)
	if latest[0]["id"] != s.IDs["4"] || latest[47]["id"] != s.IDs["50"] || latest[46]["id"] != key("late") {
		t.Fatal(latest[46:])
	}
	frozen := pageAt(s.Point, opts)
	if !slices.EqualFunc(frozen, initial, func(x, y row) bool { return x["id"] == y["id"] }) {
		t.Fatal("fixed selection moved")
	}
}
