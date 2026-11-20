package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/readmodel"
	"github.com/0xmhha/trace-agent/internal/store"
)

// Run only on an explicitly selected, completed single-call test session.
func TestDesktopClientHistory(t *testing.T) {
	path := os.Getenv("TRACE_DESKTOP_SESSION_FILE")
	if path == "" {
		t.Skip("set TRACE_DESKTOP_SESSION_FILE and TRACE_DESKTOP_SESSION_KIND for a desktop qualification session")
	}
	kind := collect.Kind(os.Getenv("TRACE_DESKTOP_SESSION_KIND"))
	if kind != collect.Rollout && kind != collect.Transcript {
		t.Fatal("select codex-rollout or claude-transcript")
	}
	if !filepath.IsAbs(path) {
		t.Fatal("select one absolute session path")
	}
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	key := rand.Text() + rand.Text()
	var first string
	for _, phase := range []string{"initial", "restart"} {
		cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestDesktopClientReaderProcess$") // #nosec G204 -- same test executable and fixed selector.
		cmd.Env = append(os.Environ(), "TRACE_DESKTOP_READER_DIR="+dir, "TRACE_DESKTOP_READER_KEY="+key)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s reader: %v: %s", phase, err, out)
		}
		state, _, _ := strings.Cut(string(out), "\n")
		if !strings.HasPrefix(state, "revision=") {
			t.Fatal("reader did not report its history")
		}
		t.Logf("%s %s", phase, state)
		if first != "" && state != first {
			t.Fatal("reader restart changed stored history or checkpoint")
		}
		first = state
	}
}

// Each worker opens the same SQLite independently and uses the desktop file collector.
func TestDesktopClientReaderProcess(t *testing.T) {
	dir := os.Getenv("TRACE_DESKTOP_READER_DIR")
	if dir == "" {
		t.Skip("started by TestDesktopClientHistory")
	}
	ctx := t.Context()
	path, err := filepath.EvalSymlinks(os.Getenv("TRACE_DESKTOP_SESSION_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, filepath.Join(dir, "activity.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	c := collector{ctx: ctx, db: db, config: registry{Key: []byte(os.Getenv("TRACE_DESKTOP_READER_KEY"))}}
	src := selectedSource{Kind: collect.Kind(os.Getenv("TRACE_DESKTOP_SESSION_KIND")), Path: path}
	a, err := c.adapter(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.readFiles(a, src); err != nil {
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
	model := readmodel.New(records, readmodel.Options{})
	tasks := desktopQueryItems(t, model, "tasks")
	attempts := desktopQueryItems(t, model, "attempts")
	if len(tasks) != 1 || len(attempts) != 1 || attempts[0]["phase"] != "ENDED" || attempts[0]["taskId"] != tasks[0]["id"] {
		t.Fatalf("expected one task and one ended attempt in that task, found tasks=%d attempts=%d", len(tasks), len(attempts))
	}
	cursor, err := db.Cursor(ctx, a.Source(), a.StreamKey(filepath.Base(path)))
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	stat, err := root.Stat(filepath.Base(path))
	if err != nil || cursor.Offset == 0 || cursor.Offset != stat.Size() {
		t.Fatal("checkpoint did not reach the end of the selected complete session")
	}
	data, err := json.Marshal([]any{tasks[0]["id"], attempts[0]["id"], cursor})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("revision=%d epoch=%d records=%d tasks=1 attempts=1 phase=ENDED bytes=%d identity=%x\n", point.Revision, point.Epoch, len(records), cursor.Offset, sha256.Sum256(data))
}

func desktopQueryItems(t *testing.T, m *readmodel.Model, operation string) []map[string]any {
	t.Helper()
	data, err := m.Query(operation, nil)
	if err != nil {
		t.Fatal(err)
	}
	return data.(map[string]any)["items"].([]map[string]any)
}
