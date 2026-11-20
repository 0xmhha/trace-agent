package observer_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/observer"
	"github.com/0xmhha/trace-agent/internal/readmodel"
	"github.com/0xmhha/trace-agent/internal/store"
)

func TestInstalledClaudeHooks(t *testing.T) {
	if os.Getenv("TRACE_HOOK_COMPAT") != "1" {
		t.Skip("set TRACE_HOOK_COMPAT=1 to run the installed client")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var key [32]byte
	if _, err = rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(key[:])
	a, err := collect.New(collect.Transcript, "isolated", "hook", "2.1.329", key[:], []string{"Read", "Agent", "TaskCreate", "TodoWrite"}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	r, err := observer.Listen(db, a, observer.Hook, "", 0, token)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(ctx) }()
	if _, err = db.Register(ctx, r.Source()); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "node", "../../projects/trace-agent/tests/compat/claude.mjs", dir) // #nosec G204 -- fixed isolated test runner and a test-owned directory.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TRACE_TEST_HOOK_ENDPOINT=" + r.URL(), "TRACE_TEST_HOOK_TOKEN=" + token}
	if err := cmd.Run(); err != nil {
		t.Fatalf("isolated client: %v", err)
	}
	root, err := os.OpenRoot(filepath.Join(dir, "profile", "projects"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			return err
		}
		_, err = a.ReadFile(ctx, root, name, collect.FileCursor{}, db.FileCommit)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := db.Records(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	var hooks, matched int
	for _, rec := range rs {
		if rec.Evidence.Kind == collect.ClaudeHook {
			hooks++
		}
	}
	view := readmodel.New(rs, readmodel.Options{})
	raw, err := view.Query("attempts", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range raw.(obj)["items"].([]obj) {
		detail, err := view.Query("attempt", obj{"key": obj{"taskId": row["taskId"], "id": row["id"]}})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range detail.(obj)["edges"].([]obj) {
			if e["action"] == "PROPOSE" && e["grade"] == "DIRECT" && e["observer"] == "CLIENT" {
				matched++
			}
		}
	}
	t.Logf("native hook observations=%d linked proposals=%d", hooks, matched)
	if hooks < 2 || matched == 0 {
		t.Fatal("installed hook delivery or correlation missing")
	}
}
