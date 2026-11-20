package readmodel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

// TestInstalledClaude runs the installed client against an isolated loopback
// model and checks its real on-disk format through the collector and read model.
func TestInstalledClaude(t *testing.T) {
	if os.Getenv("TRACE_CLAUDE_COMPAT") != "1" {
		t.Skip("set TRACE_CLAUDE_COMPAT=1 to run the local CLI compatibility check")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	a, err := collect.New(collect.Transcript, "qualification", "test", strings.Fields(string(version))[0],
		[]byte(rand.Text()+rand.Text()), []string{"Read", "Agent", "TaskCreate", "TodoWrite"}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "trace.sqlite")
	open := func() *store.Store {
		db, err := store.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	db := open()
	defer func() { _ = db.Close() }()
	read := func() (store.Point, map[string]any) {
		root, err := os.OpenRoot(filepath.Join(dir, "profile", "projects"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = root.Close() }()
		err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(name, ".jsonl") {
				return err
			}
			cursor, err := db.Cursor(ctx, a.Source(), a.StreamKey(name))
			if err != nil {
				return err
			}
			_, err = a.ReadFile(ctx, root, name, cursor, db.FileCommit)
			return err
		})
		if err != nil {
			t.Fatal(err)
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
		view := q(t, model, "overview", nil)
		ended := 0
		for _, task := range model.tasks {
			for _, attempt := range task.attempts {
				if model.phase(attempt) == "ENDED" {
					ended++
				}
			}
		}
		view["endedAttempts"] = ended
		return point, view
	}
	cmd := exec.CommandContext(ctx, "node", "../../projects/trace-agent/tests/compat/claude.mjs", dir) // #nosec G204 -- fixed local test script, test-owned directory.
	cmd.Env = append(os.Environ(), "TRACE_COLLECT_HANDSHAKE=1")
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		_ = input.Close()
		if !waited {
			cancel()
			_ = cmd.Wait()
		}
	}()
	scan := bufio.NewScanner(output)
	var prev store.Point
	for _, phase := range []string{"running", "first", "resumed"} {
		if !scan.Scan() || scan.Text() != phase {
			_ = input.Close()
			_ = cmd.Wait()
			waited = true
			t.Fatalf("expected client checkpoint %s: %s", phase, diagnostic.String())
		}
		point, view := read()
		if phase == "running" && count(view, "tasks") == 0 {
			// The client flushes transcripts asynchronously while awaiting the model.
			deadline := time.NewTimer(8 * time.Second)
			poll := time.NewTicker(100 * time.Millisecond)
			defer deadline.Stop()
			defer poll.Stop()
		awaitFlush:
			for count(view, "tasks") == 0 {
				select {
				case <-poll.C:
					point, view = read()
				case <-deadline.C:
					break awaitFlush
				}
			}
		}
		tasks, calls, agents := count(view, "tasks"), count(view, "attempts"), count(obj(view["agents"]), "total")
		t.Logf("%s %s revision=%d tasks=%d attempts=%d children=%d", strings.TrimSpace(string(version)), phase, point.Revision, tasks, calls, agents)
		if point.Revision <= prev.Revision {
			t.Fatal("new evidence missing")
		}
		switch phase {
		case "running":
			if tasks != 1 {
				t.Fatal("active root task missing")
			}
		case "first", "resumed":
			wantTasks := 1
			if phase == "resumed" {
				wantTasks = 2
			}
			if tasks != wantTasks || calls != 3 || agents != 1 || view["endedAttempts"] != 3 {
				t.Errorf("want %d tasks, 3 matched and ended tool attempts, and 1 child", wantTasks)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db = open()
			again, _ := read()
			if again != point {
				t.Fatal("restarting changed the durable checkpoint")
			}
		}
		prev = point
		if _, err := io.WriteString(input, "ok\n"); err != nil {
			t.Fatal(err)
		}
	}
	_ = input.Close()
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("client probe: %v %s", err, diagnostic.String())
	}
}
