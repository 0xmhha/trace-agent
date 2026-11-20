package collect

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

type memorySink struct {
	last   map[string]ItemVersion
	output []Evidence
	fail   bool
}

func (s *memorySink) Latest(_ context.Context, key string) (ItemVersion, error) {
	return s.last[key], nil
}
func (s *memorySink) Commit(_ context.Context, e Evidence, v ItemVersion) error {
	if s.fail {
		return errors.New("commit refused")
	}
	if s.last == nil {
		s.last = make(map[string]ItemVersion)
	}
	s.last[e.RecordKey] = v
	s.output = append(s.output, e)
	return nil
}

// Exercise the same newline framing, page request sequence and interleaved notices
// as a connected app-server. The responder returns errors rather than failing a test goroutine.
func scriptServer(conn net.Conn, fallback bool) error {
	defer func() { _ = conn.Close() }()
	dec := json.NewDecoder(bufio.NewReader(conn))
	enc := json.NewEncoder(conn)
	const private = "private-fixture"
	item := object{"type": "mcpToolCall", "id": "same", "server": "north", "tool": "lookup", "status": "completed", "arguments": private, "error": private}
	for {
		var req struct {
			ID     int
			Method string
			Params object
		}
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var result any
		switch req.Method {
		case "initialize":
			result = object{}
		case "initialized":
			continue
		case "thread/list":
			if str(req.Params, "cwd") != "/selected" {
				return errors.New("unscoped query")
			}
			rows := []object{}
			if req.Params["archived"] == false && req.Params["cursor"] == nil {
				result = object{"data": rows, "nextCursor": "page-two"}
				break
			}
			if req.Params["archived"] == false {
				rows = append(rows, object{"id": "t", "cwd": "/selected", "preview": private})
			}
			result = object{"data": rows}
		case "thread/turns/list":
			turn := object{"id": "turn", "status": "completed"}
			if req.Params["itemsView"] == "full" {
				turn["items"] = []object{item}
			}
			result = object{"data": []object{turn}}
		case "thread/items/list":
			if fallback {
				if err := enc.Encode(object{"id": req.ID, "error": object{"code": -32601, "message": private}}); err != nil {
					return err
				}
				continue
			}
			if req.Params["cursor"] != nil {
				result = object{"data": []object{}}
				break
			}
			for _, thread := range []string{"outside", "t", "t"} {
				if err := enc.Encode(object{"method": "item/completed", "params": object{"threadId": thread, "turnId": "turn", "item": item}}); err != nil {
					return err
				}
			}
			result = object{"data": []object{{"turnId": "turn", "item": item}}, "nextCursor": "p2"}
		default:
			return fmt.Errorf("unexpected read method %q", req.Method)
		}
		if err := enc.Encode(object{"id": req.ID, "result": result}); err != nil {
			return err
		}
	}
}
func TestAppOverlapAndFallback(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint("fallback=", fallback), func(t *testing.T) {
			a := adapter(t, AppServer)
			sink := new(memorySink)
			for range 2 {
				client, server := net.Pipe()
				done := make(chan error, 1)
				go func() { done <- scriptServer(server, fallback) }()
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				err := a.CollectApp(ctx, client, AppOptions{Directory: "/selected"}, sink)
				cancel()
				if serverErr := <-done; serverErr != nil {
					t.Fatal(serverErr)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(sink.output) != 3 {
				t.Fatalf("thread, turn and one item wanted, got %d", len(sink.output))
			}
			raw, err := json.Marshal(sink.output)
			if err != nil || bytes.Contains(raw, []byte("private-fixture")) || bytes.Contains(raw, []byte("lookup")) {
				t.Fatal("unsafe metadata")
			}
		})
	}
}
func TestItemVersions(t *testing.T) {
	a := adapter(t, AppServer)
	s := new(memorySink)
	raw := object{"type": "commandExecution", "id": "i"}
	send := func(status string, notice bool) error {
		raw["status"] = status
		e, err := a.item("thread", "turn", raw, "")
		if err != nil {
			return err
		}
		return e.accept(t.Context(), s, notice)
	}
	for _, status := range []string{"inProgress", "completed", "inProgress", "inProgress"} {
		if err := send(status, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.output) != 3 || s.output[0].RecordKey != s.output[2].RecordKey || s.output[2].VersionKey != "started/3" {
		t.Fatal("snapshot A/B/A/retry")
	}
	s = new(memorySink)
	for _, status := range []string{"completed", "inProgress", "inProgress", "completed"} {
		if err := send(status, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := send("completed", false); err != nil || len(s.output) != 2 {
		t.Fatal("late start or history counted twice", err)
	}
	s.fail = true
	if err := send("failed", false); err == nil {
		t.Fatal("lost sink error")
	}
	s.fail = false
	if err := send("failed", false); err != nil || len(s.output) != 3 {
		t.Fatal("retry", err)
	}
	other, err := a.item("thread", "another-turn", raw, "")
	if err != nil || other.e.RecordKey == s.output[0].RecordKey {
		t.Fatal("turn scope collision")
	}
}
func TestCancelBlockedConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	a := adapter(t, AppServer)
	client, server := net.Pipe()
	defer func() { _ = server.Close() }()
	done := make(chan error, 1)
	go func() { done <- a.CollectApp(ctx, client, AppOptions{Directory: "/selected"}, new(memorySink)) }()
	if _, err := bufio.NewReader(server).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked read survived cancel")
	}
}
func TestLocalCodex(t *testing.T) {
	dir := os.Getenv("TRACE_TEST_CODEX_DIRECTORY")
	if dir == "" {
		t.Skip("set TRACE_TEST_CODEX_DIRECTORY to explicitly select local history")
	}
	a := adapter(t, AppServer)
	s := new(memorySink)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if err := a.CollectLocalCodex(ctx, AppOptions{Directory: dir}, s); err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, e := range s.output {
		counts[str(e.Metadata, "type")]++
	}
	if counts["thread"] == 0 || counts["turn"] == 0 {
		t.Fatal("no history for selected directory")
	}
	// Only fixed event types and counts leave the process.
	t.Log("verified local history", strings.TrimSpace(fmt.Sprint(counts)))
}

// An idle peer must release the read-only collector so the desktop can reconnect.
func TestAppRPCDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = server.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	err := adapter(t, AppServer).CollectApp(ctx, client, AppOptions{Directory: "/selected", RequestTimeout: 20 * time.Millisecond}, new(memorySink))
	if err == nil || ctx.Err() != nil {
		t.Fatalf("RPC did not terminate within its own deadline: %v", err)
	}
}

func TestFollowPollsMissedTurns(t *testing.T) {
	a := adapter(t, AppServer)
	sink := new(memorySink)
	client, server := net.Pipe()
	defer func() { _ = server.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	if err := server.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	done := make(chan error, 1)
	synced := make(chan struct{}, 2)
	go func() {
		done <- a.CollectApp(ctx, client, AppOptions{Directory: "/selected", Follow: true, PollInterval: time.Millisecond, Synced: func() { synced <- struct{}{} }}, sink)
	}()
	dec := json.NewDecoder(server)
	enc := json.NewEncoder(server)
	scans := 0
	for {
		var req struct {
			ID     int
			Method string
			Params object
		}
		if err := dec.Decode(&req); err != nil {
			t.Fatal(err)
		}
		var rows []object
		switch req.Method {
		case "initialize":
			if err := enc.Encode(object{"id": req.ID, "result": object{}}); err != nil {
				t.Fatal(err)
			}
			continue
		case "initialized":
			continue
		case "thread/list":
			if req.Params["archived"] == false {
				scans++
				rows = []object{{"id": "t", "cwd": "/selected"}}
			}
			if req.Params["archived"] == true && scans == 2 {
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("stop: %v", err)
				}
				if len(synced) != 1 {
					t.Fatalf("expected one completed initial scan, got %d", len(synced))
				}
				if len(sink.output) != 4 {
					t.Fatalf("want thread and three distinct turns, got %d", len(sink.output))
				}
				return
			}
		case "thread/turns/list":
			rows = []object{{"id": "old", "status": "completed"}}
			if scans > 1 {
				rows = append([]object{{"id": "new", "status": "completed"}, {"id": "middle", "status": "completed"}}, rows...)
			}
		case "thread/items/list":
		default:
			t.Fatal("unexpected method", req.Method)
		}
		if rows == nil {
			rows = []object{}
		}
		if err := enc.Encode(object{"id": req.ID, "result": object{"data": rows}}); err != nil {
			t.Fatal(err)
		}
	}
}
