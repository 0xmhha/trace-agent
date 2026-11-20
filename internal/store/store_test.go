package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
)

func adapter(t *testing.T, kind collect.Kind) *collect.Adapter {
	t.Helper()
	a, err := collect.New(kind, "synthetic-profile", "source", "2.1.329", bytes.Repeat([]byte{17}, 32), nil, func() time.Time { return time.UnixMilli(1700000000000) })
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func openStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

const lineA = `{"type":"user","uuid":"A","sessionId":"s","promptId":"p","userType":"external","isMeta":false,"timestamp":"2023-11-14T22:13:20Z","message":{"role":"user","content":"private-synthetic-marker"}}`
const lineB = `{"type":"user","uuid":"B","sessionId":"s","promptId":"q","userType":"external","isMeta":false,"timestamp":"2023-11-14T22:13:22Z","message":{"role":"user","content":"private-synthetic-marker"}}`

func ingestFile(t *testing.T, s *Store, dir string, restart bool) collect.FileCursor {
	t.Helper()
	a := adapter(t, collect.Transcript)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	c := collect.FileCursor{}
	if restart {
		c, err = s.Cursor(t.Context(), a.Source(), a.StreamKey("trace.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err = a.ReadFile(t.Context(), root, "trace.jsonl", c, s.FileCommit)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func count(t *testing.T, s *Store, query string) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestCrashBoundary(t *testing.T) {
	if when := os.Getenv("TRACE_TEST_EXIT"); when != "" {
		dir := os.Getenv("TRACE_TEST_DIR")
		s := openStore(t, filepath.Join(dir, "db"))
		s.boundary = func(k string) {
			if k == when {
				os.Exit(79)
			}
		}
		ingestFile(t, s, dir, false)
		t.Fatal("missing crash boundary")
	}
	for _, phase := range []string{"before", "after"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "trace.jsonl"), []byte(lineA+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			// #nosec G204 -- Execute this test binary in a child to terminate it at the transaction boundary.
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestCrashBoundary$")
			cmd.Env = append(os.Environ(), "TRACE_TEST_EXIT="+phase, "TRACE_TEST_DIR="+dir)
			out, err := cmd.CombinedOutput()
			var status *exec.ExitError
			if !errors.As(err, &status) || status.ExitCode() != 79 {
				t.Fatalf("subprocess: %v %s", err, out)
			}
			s := openStore(t, filepath.Join(dir, "db"))
			want := int64(0)
			if phase == "after" {
				want = 1
			}
			for _, q := range []string{"SELECT count(*) FROM evidence", "SELECT count(*) FROM checkpoint", "SELECT count(*) FROM fact_version", "SELECT count(*) FROM revision WHERE rev>0"} {
				if n := count(t, s, q); n != want {
					t.Fatalf("%s: got %d want %d", q, n, want)
				}
			}
			c := ingestFile(t, s, dir, false)
			ingestFile(t, s, dir, true)
			if c.Offset != int64(len(lineA)+1) || count(t, s, "SELECT count(*) FROM evidence") != 1 || count(t, s, "SELECT max(rev) FROM revision") != 1 {
				t.Fatal("replay was not idempotent")
			}
			if count(t, s, "SELECT count(*) FROM pragma_foreign_key_check") != 0 {
				t.Fatal("foreign key check")
			}
		})
	}
}
func TestDeletionAndReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db")
	a := adapter(t, collect.Transcript)
	if err := os.WriteFile(filepath.Join(dir, "trace.jsonl"), []byte(lineA+"\n"+lineB+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := openStore(t, path)
	ingestFile(t, s, dir, false)
	old, err := s.Point(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f := Filter{Source: a.Source(), Kind: "all", FromMS: 1700000000000, UntilMS: 1700000001000}
	p, err := s.Preview(t.Context(), f)
	if err != nil || p.Evidence != 1 {
		t.Fatal(p, err)
	}
	d, err := s.Delete(t.Context(), f, p)
	if err != nil || d.Evidence != 1 || d.Facts != 1 || d.Point.Epoch != 2 {
		t.Fatal(d, err)
	}
	if _, err = s.Records(t.Context(), old); !errors.Is(err, ErrStale) {
		t.Fatal("prior epoch readable", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, path)
	ingestFile(t, s, dir, false)
	// Rotation changes source positions. The interval must still suppress A.
	if err = os.Rename(filepath.Join(dir, "trace.jsonl"), filepath.Join(dir, "previous")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "trace.jsonl"), []byte(lineA+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ingestFile(t, s, dir, true)
	current, err := s.Point(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.Records(t.Context(), current)
	if err != nil || len(rows) != 1 || *rows[0].Evidence.OccurredMS != 1700000002000 {
		t.Fatalf("remaining records %d, %v", len(rows), err)
	}
	if count(t, s, "SELECT count(*) FROM fact_version") != 1 || count(t, s, "SELECT count(*) FROM pragma_foreign_key_check") != 0 {
		t.Fatal("deleted facts reappeared")
	}
	for _, p := range []string{path, path + "-wal"} {
		// #nosec G304 -- Both paths are inside the test-owned temporary directory.
		data, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("private-synthetic-marker")) {
			t.Fatal("body marker persisted")
		}
	}
}
func TestStageTwoProjectionAndAppVersion(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, filepath.Join(dir, "db"))
	a := adapter(t, collect.Telemetry)
	raw := []byte(`{"event.name":"api_request_body","request_body_id":"C1","body":"private"}`)
	e, err := a.Project(collect.Position{Stream: "otel", Generation: "g"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	v := collect.ItemVersion{Sequence: 1, Current: e.SemanticTag}
	for range 2 {
		if err := s.Commit(t.Context(), e, v); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.Latest(t.Context(), e.RecordKey); err != nil || got != v {
		t.Fatal(got, err)
	}
	if count(t, s, "SELECT count(*) FROM source_model_observation") != 1 {
		t.Fatal("duplicate model evidence")
	}
	e.Metadata["body"] = "private"
	if err = s.Append(t.Context(), e); err == nil {
		t.Fatal("modified metadata persisted")
	}
}
func TestMigrations(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		version int
		zero    bool
	}{{"v11.sql", 0, false}, {"v11.sql", 11, false}, {"v12.sql", 12, false}, {"v12.sql", 12, true}} {
		dir := t.TempDir()
		path := filepath.Join(dir, "db")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		conn, err := sql.Open("sqlite3", path)
		if err != nil {
			t.Fatal(err)
		}
		ddl, err := os.ReadFile(filepath.Join("testdata", tc.fixture))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = conn.Exec(string(ddl)); err != nil {
			t.Fatal(err)
		}
		if tc.version == 11 {
			_, err = conn.Exec("PRAGMA user_version=11")
		}
		if err != nil {
			t.Fatal(err)
		}
		if tc.zero {
			if _, err = conn.Exec("INSERT INTO revision VALUES(0,77)"); err != nil {
				t.Fatal(err)
			}
		}
		if err = conn.Close(); err != nil {
			t.Fatal(err)
		}
		s := openStore(t, path)
		want := int64(0)
		if tc.zero {
			want = 77
		}
		if count(t, s, "PRAGMA user_version") != 13 || count(t, s, "SELECT committed_ms FROM revision WHERE rev=0") != want || count(t, s, "PRAGMA foreign_keys") != 1 {
			t.Fatal("migration", tc)
		}
	}
}
func TestPrivacyGate(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "db"))
	a := adapter(t, collect.Telemetry)
	p, err := s.Register(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetConsent(t.Context(), a.Source(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.WithEpoch(t.Context(), p.Epoch, func() error { t.Fatal("stale frame"); return nil }); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
}
func TestMetadataRoundTrip(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "db"))
	a := adapter(t, collect.Transcript)
	e, err := a.Project(collect.Position{Stream: "s", Generation: "g"}, []byte(lineA))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	p, err := s.Point(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Records(t.Context(), p)
	if err != nil || len(r) != 1 {
		t.Fatal(err)
	}
	before, _ := json.Marshal(e.Metadata)
	after, _ := json.Marshal(r[0].Evidence.Metadata)
	if !bytes.Equal(before, after) {
		t.Fatal("metadata mismatch")
	}
}

func TestBodyConsentDeletion(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "db"))
	ctx := t.Context()
	a := adapter(t, collect.Transcript).WithBodyCollection()
	s.now = func() time.Time { return time.UnixMilli(1699999999000) }
	if _, err := s.Register(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetConsent(ctx, a.Source(), true); err != nil {
		t.Fatal(err)
	}
	raw := bytes.Replace([]byte(lineA), []byte("private-synthetic-marker"), []byte("Review the plan"), 1)
	e, err := a.Project(collect.Position{Stream: "fixture", Generation: "generation"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	p, err := s.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	records, err := s.Records(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	id := records[0].ID
	slots, err := s.BodyFields(ctx, p, id)
	if err != nil || len(slots) != 1 || slots[0].Text != "Review the plan" {
		t.Fatal("body with consent", slots, err)
	}
	revoked, err := s.SetConsent(ctx, a.Source(), false)
	if err != nil {
		t.Fatal(err)
	}
	slots, err = s.BodyFields(ctx, revoked, id)
	if err != nil || len(slots) != 0 {
		t.Fatal("revoked body visible")
	}
	f := Filter{Source: a.Source(), Kind: "body", FromMS: 1, UntilMS: 1800000000000}
	preview, err := s.Preview(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Delete(ctx, f, preview); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetConsent(ctx, a.Source(), true); err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	again, err := a.Project(collect.Position{Stream: "fixture", Generation: "rotated"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, again); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "SELECT count(*) FROM body WHERE clean_text IS NOT NULL") != 0 || count(t, s, "SELECT count(*) FROM body WHERE status='deleted'") != 1 {
		t.Fatal("body resurrected")
	}
}
func TestDependentVersionsAndStalePreview(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "db"))
	ctx := t.Context()
	a := adapter(t, collect.Transcript)
	put := func(line string, offset int64) collect.Evidence {
		r, err := a.Project(collect.Position{Stream: "s", Generation: "g", Offset: offset}, []byte(line))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	a1 := put(lineA, 0)
	f := Filter{Source: a.Source(), Kind: "all", FromMS: 1600000000000, UntilMS: 1800000000000}
	p, err := s.Preview(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	put(lineB, 1)
	if _, err = s.Delete(ctx, f, p); !errors.Is(err, ErrStale) {
		t.Fatal("accepted changed scope")
	}
	_, task := scopes(a1)
	for _, pair := range [][2]string{{"middle", a1.RecordKey}, {"derived", "middle"}} {
		_, err = s.db.Exec(`INSERT INTO fact VALUES(?,?,?); INSERT INTO fact_version(task_id,fact_id,rev,kind,grade) VALUES(?,?,1,'derived','direct'); INSERT INTO fact_dependency VALUES(?,?,1,?,1)`, task, pair[0], pair[0], task, pair[0], task, pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
	}
	f.UntilMS = 1700000001000
	p, err = s.Preview(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Delete(ctx, f, p)
	if err != nil || d.Facts != 3 {
		t.Fatal(d, err)
	}
	if count(t, s, "SELECT count(*) FROM fact_version") != 1 {
		t.Fatal("derived historical content survived")
	}
}

func TestRegistrationBatchAndPolicy(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "db"))
	ctx := t.Context()
	a := adapter(t, collect.Telemetry)
	p, err := s.Register(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetEnabled(ctx, a.Source(), false); err != nil {
		t.Fatal(err)
	}
	sources, err := s.Sources(ctx, p)
	if err != nil || len(sources) != 1 || !sources[0].Enabled {
		t.Fatal(sources, err)
	}
	es := make([]collect.Evidence, 2)
	for i := range es {
		es[i], err = a.Project(collect.Position{Stream: "s", Generation: "g", Offset: int64(i)}, []byte(`{"event.name":"api_request_body","request_body_id":"C"}`))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = s.AppendBatch(ctx, es); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled batch accepted", err)
	}
	if _, err = s.SetEnabled(ctx, a.Source(), true); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendBatch(ctx, es); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "SELECT count(DISTINCT rev) FROM evidence") != 1 {
		t.Fatal("batch split into several points")
	}
	bad := es[0]
	bad.Metadata = map[string]any{"body": "not projected"}
	if err = s.AppendBatch(ctx, []collect.Evidence{es[0], bad}); err == nil {
		t.Fatal("invalid batch accepted")
	}
}

func TestExpiration(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "db"))
	a := adapter(t, collect.Transcript)
	ctx := t.Context()
	e, err := a.Project(collect.Position{Stream: "s", Generation: "g"}, []byte(lineA))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err = s.Expire(ctx, time.UnixMilli(1700000000000).AddDate(0, 0, 8)); err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "SELECT count(*) FROM evidence") != 0 {
		t.Fatal("expired evidence restored")
	}
}
