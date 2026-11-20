package compat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/readmodel"
	"github.com/0xmhha/trace-agent/internal/store"
)

type object = map[string]any

// Only the model endpoint is a fixture. An installed Codex performs each turn,
// invokes its real tool handlers and owns the native session directory.
type mockResponses struct {
	mu           sync.Mutex
	requests     int
	outcomes     map[string]string
	inProgress   chan struct{}
	continueTurn chan struct{}
}

func (m *mockResponses) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/responses" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = r.Body.Close() }()
	var req struct {
		Input []object
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	m.mu.Lock()
	m.requests++
	n := m.requests
	for _, input := range req.Input {
		if input["type"] == "function_call_output" {
			id, _ := input["call_id"].(string)
			out, _ := input["output"].(string)
			if m.outcomes == nil {
				m.outcomes = map[string]string{}
			}
			m.outcomes[id] = out
		}
	}
	m.mu.Unlock()
	if n == 2 {
		close(m.inProgress)
		select {
		case <-m.continueTurn:
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(kind string, body object) {
		body["type"] = kind
		b, _ := json.Marshal(body)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b)
	}
	id := fmt.Sprintf("resp_fixture_%d", n)
	response := object{"id": id, "object": "response", "status": "in_progress", "output": []any{}}
	emit("response.created", object{"response": response})
	var item object
	if n == 1 || n == 2 {
		name, call, args := "exec_command", "call_sample_command", object{"cmd": "printf trace_cli_check"}
		if n == 2 {
			name, call, args = "spawn_agent", "call_sample_child", object{"message": "Confirm the isolated synthetic example."}
		}
		arguments, _ := json.Marshal(args)
		item = object{"type": "function_call", "id": "fc_" + call, "name": name, "call_id": call, "arguments": string(arguments), "status": "completed"}
		if n == 2 {
			item["namespace"] = "multi_agent_v1"
		}
	} else {
		item = object{"type": "message", "id": "msg_" + id, "role": "assistant", "status": "completed", "content": []object{{"type": "output_text", "text": "Synthetic turn complete.", "annotations": []any{}}}}
	}
	emit("response.output_item.added", object{"output_index": 0, "item": item})
	emit("response.output_item.done", object{"output_index": 0, "item": item})
	response["status"] = "completed"
	response["output"] = []object{item}
	response["usage"] = object{"input_tokens": 16, "output_tokens": 8, "total_tokens": 24}
	emit("response.completed", object{"response": response})
}

type cliRun struct {
	cmd      *exec.Cmd
	output   bytes.Buffer
	finished chan error
}

func isolatedEnv(profile string) []string {
	return []string{
		"HOME=" + filepath.Dir(profile), "CODEX_HOME=" + profile, "TMPDIR=" + filepath.Dir(profile),
		"PATH=" + os.Getenv("PATH"), "TERM=dumb", "OTEL_SDK_DISABLED=true",
		"HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1",
		"ALL_PROXY=http://127.0.0.1:1", "NO_PROXY=127.0.0.1,localhost",
	}
}

func startCLI(ctx context.Context, work, profile, endpoint, session string) (*cliRun, error) {
	args := []string{"exec", "--skip-git-repo-check", "--json", "--ignore-rules", "--ignore-user-config", "-s", "read-only", "-m", "gpt-5.2",
		"-c", `model_provider="fixture"`, "-c", `model_providers.fixture.name="Fixture"`, "-c", fmt.Sprintf("model_providers.fixture.base_url=%q", endpoint+"/v1"), "-c", `model_providers.fixture.wire_api="responses"`, "-c", `model_providers.fixture.supports_websockets=false`, "-c", `model_providers.fixture.request_max_retries=0`, "-c", `model_providers.fixture.stream_max_retries=0`}
	if session != "" {
		args = append(args, "resume", session, "Confirm the synthetic result.")
	} else {
		args = append(args, "Print the sample marker and spawn a child for the isolated example.")
	}
	c := &cliRun{finished: make(chan error, 1)}
	c.cmd = exec.CommandContext(ctx, "codex", args...) //nolint:gosec // The compatibility test invokes an explicitly selected installed client.
	c.cmd.Dir = work
	c.cmd.Env = isolatedEnv(profile)
	c.cmd.Stdout = &c.output
	c.cmd.Stderr = &c.output
	if err := c.cmd.Start(); err != nil {
		return nil, err
	}
	go func() { c.finished <- c.cmd.Wait() }()
	return c, nil
}

func cliThread(c *cliRun) string {
	scan := bufio.NewScanner(bytes.NewReader(c.output.Bytes()))
	for scan.Scan() {
		var e struct {
			Type string
			ID   string `json:"thread_id"`
		}
		if json.Unmarshal(scan.Bytes(), &e) == nil && e.Type == "thread.started" {
			return e.ID
		}
	}
	return ""
}

func readRollouts(ctx context.Context, dir string, a *collect.Adapter, db *store.Store) (int, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		count++
		cursor, err := db.Cursor(ctx, a.Source(), a.StreamKey(name))
		if err != nil {
			return err
		}
		_, err = a.ReadFile(ctx, root, name, cursor, db.FileCommit)
		return err
	})
	return count, err
}
func evidenceAt(ctx context.Context, db *store.Store) ([]store.Record, store.Point, error) {
	p, err := db.Point(ctx)
	if err != nil {
		return nil, p, err
	}
	r, err := db.Records(ctx, p)
	return r, p, err
}

func TestInstalledCodexCLI(t *testing.T) {
	if os.Getenv("TRACE_CODEX_COMPAT") != "1" {
		t.Skip("set TRACE_CODEX_COMPAT=1 to run the installed Codex")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	temp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(temp, "profile")
	work := filepath.Join(temp, "work")
	source := filepath.Join(profile, "sessions")
	for _, p := range []string{profile, work, source} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	versionCmd := exec.CommandContext(ctx, "codex", "--version")
	versionCmd.Env = isolatedEnv(profile)
	versionCmd.Dir = work
	out, err := versionCmd.Output()
	if err != nil {
		t.Fatal("cannot read installed Codex version", err)
	}
	version := strings.TrimSpace(strings.TrimPrefix(string(out), "codex-cli "))
	t.Logf("codex-cli %s", version)
	mock := &mockResponses{inProgress: make(chan struct{}), continueTurn: make(chan struct{})}
	httpd := httptest.NewServer(mock)
	defer httpd.Close()
	release := sync.OnceFunc(func() { close(mock.continueTurn) })
	defer release()
	dbfile := filepath.Join(temp, "trace.sqlite")
	db, err := store.Open(ctx, dbfile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()
	a, err := collect.New(collect.Rollout, "selected", "cli", version, make([]byte, 32), []string{"exec_command", "spawn_agent"}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := startCLI(ctx, work, profile, httpd.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cli.cmd.Process != nil {
			_ = cli.cmd.Process.Kill()
		}
	}()
	select {
	case <-mock.inProgress:
	case err := <-cli.finished:
		t.Fatalf("initial turn exited before its tool callback: %v %s", err, cli.output.String())
	case <-ctx.Done():
		t.Fatal("Codex did not reach the fixture's second request")
	}
	if _, err = readRollouts(ctx, source, a, db); err != nil {
		t.Fatal(err)
	}
	inflight, _, err := evidenceAt(ctx, db)
	if err != nil || len(inflight) == 0 {
		t.Fatal("no in-progress evidence", err)
	}
	release()
	if err = <-cli.finished; err != nil {
		t.Fatalf("first turn: %v %s", err, cli.output.String())
	}
	if _, err = readRollouts(ctx, source, a, db); err != nil {
		t.Fatal(err)
	}
	before, _, err := evidenceAt(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	rootThread := cliThread(cli)
	if rootThread == "" {
		t.Fatal("first CLI thread identifier missing")
	}
	next, err := startCLI(ctx, work, profile, httpd.URL, rootThread)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-next.finished; err != nil {
		t.Fatalf("resumed turn: %v %s", err, next.output.String())
	}
	if cliThread(next) != rootThread {
		t.Fatal("CLI resumed another thread")
	}
	mock.mu.Lock()
	outcome := maps.Clone(mock.outcomes)
	requests := mock.requests
	mock.mu.Unlock()
	var spawn struct {
		ID string `json:"agent_id"`
	}
	if err = json.Unmarshal([]byte(outcome["call_sample_child"]), &spawn); err != nil || spawn.ID == "" {
		t.Fatal("native spawn did not return a child identifier")
	}
	if !strings.Contains(outcome["call_sample_command"], "Process exited with code 0") || !strings.Contains(outcome["call_sample_command"], "trace_cli_check") {
		t.Fatal("native shell tool failed")
	}
	files, err := readRollouts(ctx, source, a, db)
	if err != nil || files != 2 {
		t.Fatal("parent and child sessions expected", files, err)
	}
	rows, pt, err := evidenceAt(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) <= len(before) {
		t.Fatal("no new turn")
	}
	// Reopen the durable checkpoint, then poll the same original session again.
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, dbfile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = readRollouts(ctx, source, a, db); err != nil {
		t.Fatal(err)
	}
	replayed, again, err := evidenceAt(ctx, db)
	if err != nil || pt != again || len(replayed) != len(rows) {
		t.Fatal("checkpoint replay changed point", pt, again, err)
	}
	m := readmodel.New(rows, readmodel.Options{})
	tasks, err := m.Query("tasks", nil)
	if err != nil {
		t.Fatal(err)
	}
	overview, err := m.Query("overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	cards := overview.(object)
	nt := tasks.(object)["page"].(object)["total"].(int)
	attempts := cards["attempts"].(object)["count"].(int)
	children := cards["agents"].(object)["total"].(object)["count"].(int)
	success := map[string]bool{}
	for _, r := range rows {
		p, _ := r.Evidence.Metadata["payload"].(object)
		name, _ := p["name"].(string)
		if name != "" {
			success[name] = true
		}
	}
	events := []string{}
	scan := bufio.NewScanner(bytes.NewReader(cli.output.Bytes()))
	for scan.Scan() {
		var e struct {
			Type string
			Item struct{ Type, Status, Message string }
		}
		if json.Unmarshal(scan.Bytes(), &e) == nil && e.Type == "item.completed" {
			if e.Item.Type == "error" {
				if !strings.Contains(e.Item.Message, "fallback metadata") {
					t.Fatal("unexpected CLI error event")
				}
			} else {
				events = append(events, e.Item.Type+"/"+e.Item.Status)
			}
		}
	}
	t.Logf("requests=%d live=%d first=%d resumed=%d revision=%d tasks=%d attempts=%d descendants=%d completed=%v projectedTools=%v", requests, len(inflight), len(before), len(rows), pt.Revision, nt, attempts, children, events, success)

	app, err := collect.New(collect.AppServer, "selected", "app", version, make([]byte, 32), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.Open(ctx, filepath.Join(temp, "app.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = history.Close() }()
	if err = inspectApp(ctx, work, profile, app, history); err != nil {
		t.Fatalf("local read-only app-server protocol: %v", err)
	}
	hr, hp, err := evidenceAt(ctx, history)
	if err != nil || len(hr) == 0 {
		t.Fatal("no app-server history", err)
	}
	if err = inspectApp(ctx, work, profile, app, history); err != nil {
		t.Fatal(err)
	}
	rr, rp, err := evidenceAt(ctx, history)
	if err != nil || hp != rp || len(hr) != len(rr) {
		t.Fatal("duplicate history on restart", hp, rp)
	}
	kinds := map[string]int{}
	for _, r := range hr {
		k, _ := r.Evidence.Metadata["type"].(string)
		kinds[k]++
	}
	t.Logf("installed read-only app-server history=%d restart revision=%d kinds=%v", len(hr), rp.Revision, kinds)
	if kinds["thread"] != 2 || kinds["turn"] != 3 || kinds["commandExecution"] < 1 || kinds["collabAgentToolCall"] < 1 {
		t.Fatal("expected tool, spawn, parent turns and child history")
	}
	if nt != 3 || attempts != 2 || children != 1 || !success["exec_command"] || !success["spawn_agent"] {
		t.Fatal("expected two parent turns, one child turn and two real tool attempts")
	}
}

type stdio struct {
	io.ReadCloser
	io.WriteCloser
}

func (s stdio) Close() error { _ = s.WriteCloser.Close(); return s.ReadCloser.Close() }

// Open a second real installed process with the same test-only profile. The
// adapter drives its normal read-only discovery, turn and item requests.
func inspectApp(ctx context.Context, work, profile string, a *collect.Adapter, db *store.Store) error {
	processCtx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(processCtx, "codex", "app-server", "--stdio")
	cmd.Env = isolatedEnv(profile)
	cmd.Dir = work
	dst, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	src, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	err = a.CollectApp(ctx, stdio{src, dst}, collect.AppOptions{Directory: work}, db)
	stop()
	_ = cmd.Wait()
	return err
}
