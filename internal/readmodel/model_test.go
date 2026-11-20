package readmodel

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func fixture(t *testing.T, stage, file string) []FactEvent {
	t.Helper()
	root, err := os.OpenRoot(filepath.Join("../../projects/trace-agent/fixtures", stage))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var events []FactEvent
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var e FactEvent
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}
func options() Options {
	from := time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC)
	return Options{From: from, Until: from.Add(10 * time.Minute), Cohort: "fixture"}
}
func q(t *testing.T, m *Model, op string, args row) row {
	t.Helper()
	v, err := m.Query(op, args)
	if err != nil || v == nil {
		t.Fatalf("%s returned %v: %v", op, v, err)
	}
	return obj(v)
}
func count(v row, k string) int { return obj(v[k])["count"].(int) }
func TestIA120(t *testing.T) {
	events := fixture(t, "stage-4", "ia120.jsonl")
	opts := options()
	m := NewFacts(events, opts)
	v := q(t, m, "overview", nil)
	agents := obj(v["agents"])
	if count(v, "tasks") != 1 || count(v, "attempts") != 5 || count(v, "waiting") != 1 || obj(v["failureRate"])["value"] != 0.25 || obj(v["failureRate"])["eligible"] != 4 {
		t.Fatal(v)
	}
	if count(agents, "total") != 4 || count(agents, "ended") != 2 || count(agents, "running") != 1 || count(agents, "unknown") != 1 {
		t.Fatal(agents)
	}
	if count(v, "latencyP95Ms") != 4 {
		t.Fatal(v["latencyP95Ms"])
	}
	task := q(t, m, "task", row{"key": row{"taskId": "A"}})
	if len(task["currentSteps"].([]row)) != 2 {
		t.Fatal(task)
	}
	opts.Sample = Sample{Metric: "WAITING"}
	m = NewFacts(events, opts)
	calls := q(t, m, "attempts", nil)["items"].([]row)
	if len(calls) != 1 || calls[0]["id"] != "attempt-5" {
		t.Fatal(calls)
	}
	for _, module := range []string{"S", "T"} {
		result := q(t, m, "module", row{"key": row{"taskId": "A", "id": module}})
		if obj(result["attempts"])["items"].([]row)[0]["id"] != "attempt-5" {
			t.Fatal(result)
		}
	}
	detail := q(t, m, "attempt", row{"key": row{"taskId": "A", "id": "attempt-5"}})
	if obj(detail["attempt"])["phase"] != "RUNNING" {
		t.Fatal(detail)
	}
	if data, err := m.Query("attempt", row{"key": row{"taskId": "A", "id": "attempt-1"}}); err != nil || data != nil {
		t.Fatal("sample leaked")
	}
	opts.Sample = Sample{}
	opts.Until = opts.Until.Add(2 * time.Hour)
	v = q(t, NewFacts(events, opts), "overview", nil)
	if count(v, "waiting") != 0 || count(obj(v["agents"]), "running") != 0 {
		t.Fatal("expired sample", v)
	}
}
func TestSequenceMoments(t *testing.T) {
	events := fixture(t, "stage-6", "sequence.jsonl")
	m := NewFacts(events, options())
	args := row{"key": row{"taskId": "L7"}, "laneIds": []string{"P", "A", "B", "G"}}
	for _, x := range []struct {
		id                      string
		children, running, wait int
	}{{"L7", 3, 3, 2}, {"L8", 3, 3, 1}, {"L9", 3, 2, 1}, {"L16", 3, 0, 0}, {"L7", 3, 3, 2}} {
		args["eventId"] = x.id
		v := q(t, m, "sequenceMoment", args)
		metrics := obj(v["metrics"])
		if count(metrics, "childAgents") != x.children || count(metrics, "runningChildAgents") != x.running || count(metrics, "waitingAttempts") != x.wait {
			t.Fatalf("%s: %v", x.id, metrics)
		}
	}
	args["eventId"] = "L7"
	args["laneId"] = "B"
	v := q(t, m, "sequenceInspector", args)
	if obj(v["waits"])["items"].([]row)[0]["attemptId"] != "b-mcp" {
		t.Fatal(v)
	}
	for _, n := range []int{2, 0} {
		if n == 0 {
			events[14].Parents = nil
			m = NewFacts(events, options())
		}
		v := q(t, m, "sequence", args)
		joined := 0
		for _, r := range v["relations"].([]row) {
			if r["kind"] == "JOIN_INPUT" {
				joined++
			}
		}
		if joined != n {
			t.Fatal("join inputs", joined, n)
		}
		if (v["events"].([]row)[14]["action"] == "JOIN") != (n == 2) {
			t.Fatal("unproven join")
		}
	}
}
func TestSequencePagesAndPrefix(t *testing.T) {
	opts := options()
	ts := opts.From.UnixMilli()
	events := make([]FactEvent, 52)
	for i := range events {
		at := ts + int64(i)*1000
		events[i] = FactEvent{ID: strconv.Itoa(i + 1), Task: "T", Session: "S", Source: "src", Client: "CODEX", Root: "P", Actor: "P", Kind: "message", Action: "SEND", Clock: "clock", Occurred: &at}
	}
	until := opts.Until.UnixMilli()
	events[0].Kind = "call"
	events[0].Action = "CALL"
	events[0].Actor = "B"
	events[0].Attempt = "w"
	events[0].Phase = "RUNNING"
	events[0].ValidUntil = &until
	first := NewFacts(events[:51], opts)
	args := row{"key": row{"taskId": "T"}}
	check := func(m *Model, from, to string, waiting int) {
		v := q(t, m, "sequence", args)
		es := v["events"].([]row)
		if es[0]["id"] != from || es[len(es)-1]["id"] != to || count(obj(v["metricsBeforePage"]), "waitingAttempts") != waiting {
			t.Fatal(v["page"], from, to, v["metricsBeforePage"])
		}
	}
	check(first, "1", "50", 0)
	args["page"] = row{"after": "50"}
	check(first, "51", "51", 1)
	delete(args, "page")
	args["window"] = "LATEST"
	check(first, "2", "51", 1)
	updated := NewFacts(events, opts)
	check(updated, "3", "52", 1)
	check(first, "2", "51", 1)
	args["focusAgentId"] = "P"
	check(updated, "3", "52", 1)
}
