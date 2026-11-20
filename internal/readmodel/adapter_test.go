package readmodel

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

func TestStageOneStoredEvidence(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	root, err := os.OpenRoot("../../projects/trace-agent/fixtures/stage-1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	var otelSource string
	for _, sample := range []struct {
		name string
		kind collect.Kind
	}{{"codex", collect.Rollout}, {"claude", collect.Transcript}, {"claude-otel", collect.Telemetry}} {
		func() {
			f, err := root.Open(sample.name + ".jsonl")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				var r struct {
					Stream string
					Seq    int64
					Record json.RawMessage
				}
				if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
					t.Fatal(err)
				}
				kind := sample.kind
				if r.Stream == "app-plan" {
					kind = collect.AppServer
				}
				a, err := collect.New(kind, "profile", "instance", "1.0", bytes.Repeat([]byte{7}, 32), []string{"Read", "Agent", "mcp__north__lookup", "mcp__south__lookup", "wait_agent"}, time.Now)
				if err != nil {
					t.Fatal(err)
				}
				e, err := a.Project(collect.Position{Stream: r.Stream, Generation: "g", Offset: r.Seq}, r.Record)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Append(ctx, e); err != nil {
					t.Fatal(err)
				}
				if kind == collect.Telemetry {
					otelSource = e.Source
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
		}()
	}
	point, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	records, err := db.Records(ctx, point)
	if err != nil {
		t.Fatal(err)
	}
	model := New(records, Options{})
	v := q(t, model, "overview", nil)
	if count(v, "tasks") != 2 || count(v, "attempts") != 6 || count(v, "waiting") != 0 || count(obj(v["agents"]), "total") != 1 || count(obj(v["agents"]), "ended") != 0 || obj(v["failureRate"])["eligible"] != 2 {
		t.Fatal(v)
	}
	if len(model.unlinked) != 5 {
		t.Fatalf("want 5 unlinked got %d", len(model.unlinked))
	}
	restricted := New(records, Options{Sources: []string{otelSource}})
	page := q(t, restricted, "sourceModelAttempts", row{"sourceId": otelSource})
	if len(page["items"].([]row)) != 1 || page["items"].([]row)[0]["phase"] != "ENDED" {
		t.Fatal(page)
	}
}
