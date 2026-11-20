package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/mattn/go-sqlite3"
)

func TestInterruptedV12Migration(t *testing.T) {
	const childVar = "TRACE_TEST_MIGRATE_COPY"
	if path := os.Getenv(childVar); path != "" {
		db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = migrate(t.Context(), tx, 12); err != nil {
			t.Fatal(err)
		}
		// No Close or rollback. Reopen must recover SQLite's original schema.
		os.Exit(79)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "old.sqlite")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	ddl, err := os.ReadFile("testdata/v12.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), `INSERT INTO revision VALUES(0,77),(1,88);
INSERT INTO source VALUES('source','claude','claude-transcript','profile','instance');
INSERT INTO evidence VALUES(9,'source','stream','generation','record','1','tag',123,124,NULL,1,'user');
INSERT INTO evidence_key VALUES(9,'claude','scope','key')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestInterruptedV12Migration$") // #nosec G204 -- same test executable with fixed arguments.
	cmd.Env = append(os.Environ(), childVar+"="+path)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 79 {
		t.Fatalf("unexpected migration exit: %v %s", err, output)
	}
	db, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err = db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != 12 {
		t.Fatal("interrupted schema changed", version, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s := openStore(t, path)
		old, err := s.Records(t.Context(), Point{Revision: 0, Epoch: 1})
		if err != nil || len(old) != 0 {
			t.Fatal("wrong initial point", err)
		}
		stored, err := s.Records(t.Context(), Point{Revision: 1, Epoch: 1})
		if err != nil || len(stored) != 1 || stored[0].ID != 9 || stored[0].Evidence.Observer != "unknown" {
			t.Fatal("historical observation missing", err)
		}
		if count(t, s, "SELECT count(*) FROM evidence_key WHERE evidence_id=9") != 1 || count(t, s, "SELECT committed_ms FROM revision WHERE rev=0") != 77 || count(t, s, "SELECT count(*) FROM pragma_foreign_key_check") != 0 {
			t.Fatal("migration changed keys or history")
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFullDatabaseDoesNotAcknowledgeInput(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, filepath.Join(dir, "db"))
	limit := count(t, s, "PRAGMA page_count")
	if _, err := s.db.ExecContext(t.Context(), "PRAGMA max_page_count="+strconv.FormatInt(limit, 10)); err != nil {
		t.Fatal(err)
	}
	blocks := make([]map[string]string, 1024)
	for i := range blocks {
		blocks[i] = map[string]string{"type": "tool_use", "id": strconv.Itoa(i), "name": "Read"}
	}
	row := map[string]any{"type": "assistant", "uuid": "fixture", "sessionId": "test", "message": map[string]any{"role": "assistant", "content": blocks}}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "large.jsonl"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	a := adapter(t, collect.Transcript)
	c, err := a.ReadFile(t.Context(), root, "large.jsonl", collect.FileCursor{}, s.FileCommit)
	var sqliteErr sqlite3.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code != sqlite3.ErrFull {
		t.Fatalf("expected SQLITE_FULL, got %v", err)
	}
	if c.Offset != 0 || count(t, s, "SELECT count(*) FROM checkpoint") != 0 || count(t, s, "SELECT count(*) FROM evidence") != 0 || count(t, s, "SELECT max(rev) FROM revision") != 0 {
		t.Fatal("failed write advanced durable state")
	}
	if _, err = s.db.ExecContext(t.Context(), fmt.Sprintf("PRAGMA max_page_count=%d", limit+1024)); err != nil {
		t.Fatal(err)
	}
	c, err = a.ReadFile(t.Context(), root, "large.jsonl", c, s.FileCommit)
	if err != nil || c.Offset != int64(len(raw)+1) {
		t.Fatal("retry did not complete", err)
	}
	if _, err = a.ReadFile(t.Context(), root, "large.jsonl", c, s.FileCommit); err != nil || count(t, s, "SELECT count(*) FROM evidence") != 1 {
		t.Fatal("replay changed recovered entry", err)
	}
}
