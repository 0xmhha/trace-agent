package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
	"github.com/0xmhha/trace-agent/internal/transport"
)

type testVault struct{ r registry }

func (v *testVault) Load() (registry, error) { return v.r, nil }
func (v *testVault) Save(r registry) error   { v.r = r; return nil }

// TestDesktopRelease uses only generated files, an isolated DB and test-only native picker credentials.
func TestDesktopRelease(t *testing.T) {
	mode := os.Getenv("TRACE_VERIFY")
	if mode != "" && mode != "read" && mode != "live" && mode != "smoke" {
		t.Fatal("unknown test mode")
	}
	if mode == "" {
		t.Skip("set TRACE_VERIFY=smoke, read, or live")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "test.sqlite")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	v := &testVault{registry{Key: secret}}
	c, err := newCollector(ctx, db, v)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); c.wg.Wait() }()
	for _, kind := range []collect.Kind{collect.Rollout, collect.Transcript} {
		src := selectedSource{Kind: kind, Path: filepath.Join(dir, string(kind)+".jsonl")}
		if err := os.WriteFile(src.Path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		a, err := c.adapter(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Register(ctx, a); err != nil {
			t.Fatal(err)
		}
		if mode == "read" && kind == collect.Rollout {
			seedReads(ctx, t, db, a)
		}
		v.r.Sources = append(v.r.Sources, src)
	}
	if mode != "read" {
		for _, src := range v.r.Sources {
			a, _ := c.adapter(src)
			c.start(a, src)
		}
	}
	mux := http.NewServeMux()
	web := httptest.NewServer(mux)
	defer web.Close()
	srv, err := transport.New(db, web.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close(context.Background()) }()
	endpoint, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	token := rand.Text()
	testAPI := func(pattern string, fn http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Trace-Test") != token {
				http.Error(w, "denied", http.StatusForbidden)
				return
			}
			fn(w, r)
		})
	}
	testAPI("/_ticket", func(w http.ResponseWriter, _ *http.Request) {
		ticket, err := srv.Ticket(true)
		if err != nil {
			http.Error(w, "ticket", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(BootstrapResult{endpoint, ticket})
	})
	assets, err := fs.Sub(files, "frontend/dist")
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(assets)))
	// The browser starts the writer only after its authenticated screen is painted.
	start := make(chan struct{}, 1)
	testAPI("/_start", func(w http.ResponseWriter, _ *http.Request) {
		select {
		case start <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	type sample struct {
		Rev   int64
		Ready int64
	}
	var samples []sample
	done := make(chan struct{})
	testAPI("/_samples", func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-done:
			_ = json.NewEncoder(w).Encode(samples)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	})
	// #nosec G204 G702 -- Test entry point and executable are fixed, mode is allowlisted above.
	cmd := exec.CommandContext(ctx, "node", "frontend/test/release.mjs", mode, web.URL)
	cmd.Env = append(os.Environ(), "TRACE_TEST_TOKEN="+token)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	if mode != "read" {
		select {
		case <-start:
		case err := <-wait:
			t.Fatalf("browser: %v %s", err, output.String())
		case <-time.After(45 * time.Second):
			t.Fatal("screen did not start")
		}
		n := 30000
		if mode == "smoke" {
			n = 40
		}
		if err = writeLive(ctx, v.r.Sources, n); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			var count int
			reader, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			err = reader.QueryRow("SELECT count(*) FROM evidence").Scan(&count)
			_ = reader.Close()
			if err != nil {
				t.Fatal(err)
			}
			if count == n {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		pt, err := db.Point(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rs, err := db.Records(ctx, pt)
		if err != nil {
			t.Fatal(err)
		}
		if len(rs) != n {
			t.Fatalf("saved %d of %d complete lines", len(rs), n)
		}
		for _, r := range rs {
			if r.Evidence.OccurredMS == nil {
				t.Fatal("missing line timestamp")
			}
			samples = append(samples, sample{r.Rev, *r.Evidence.OccurredMS})
		}
		close(done)
	}
	err = <-wait
	t.Log(output.String())
	if err != nil {
		t.Fatalf("browser: %v", err)
	}
}

func writeLive(ctx context.Context, sources []selectedSource, n int) error {
	var files []*os.File
	for _, s := range sources {
		f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		files = append(files, f)
	}
	start := time.Now()
	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Until(start.Add(time.Duration(i) * 20 * time.Millisecond))):
		}
		// Timestamp immediately before writing the whole line provides a conservative read-ready bound.
		now := time.Now().UTC().Format(time.RFC3339Nano)
		var raw string
		if i%2 == 0 {
			raw = rolloutLine(i/2, now)
		} else {
			raw = fmt.Sprintf(`{"timestamp":%q,"type":"user","sessionId":"live-claude","uuid":"u%d","promptId":"p%d","userType":"external","isMeta":false,"message":{"role":"user","content":"synthetic"}}`, now, i, i)
		}
		if _, err := fmt.Fprintln(files[i%2], raw); err != nil {
			return err
		}
	}
	return nil
}

func rolloutLine(i int, stamp string) string {
	offset := i % 100
	var typ, payload string
	switch {
	case offset == 0:
		typ = "session_meta"
		payload = fmt.Sprintf(`{"id":"s%d"}`, i/100)
	case offset == 1:
		typ = "event_msg"
		payload = fmt.Sprintf(`{"type":"task_started","turn_id":"t%d"}`, i/100)
	case offset%2 == 0:
		typ = "response_item"
		payload = fmt.Sprintf(`{"type":"function_call","call_id":"c%d","name":"Read"}`, i/2)
	default:
		typ = "response_item"
		payload = fmt.Sprintf(`{"type":"function_call_output","call_id":"c%d","output":"done"}`, i/2)
	}
	return fmt.Sprintf(`{"timestamp":%q,"type":%q,"payload":%s}`, stamp, typ, payload)
}
func seedReads(ctx context.Context, t *testing.T, db *store.Store, a *collect.Adapter) {
	t.Helper()
	now := time.Now().UTC().Add(-24*time.Hour + time.Minute)
	for base := 0; base < 100000; base += 100 {
		batch := make([]collect.Evidence, 0, 100)
		for n := base; n < base+100; n++ {
			at := now.Add(time.Duration(n) * 850 * time.Millisecond)
			e, err := a.Project(collect.Position{Stream: "sample-" + strconv.Itoa(base), Generation: "1", Offset: int64(n - base)}, []byte(rolloutLine(n, at.Format(time.RFC3339Nano))))
			if err != nil {
				t.Fatal(err)
			}
			batch = append(batch, e)
		}
		if err := db.AppendBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	pt, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Records(ctx, pt)
	if err != nil || len(rows) != 100000 {
		t.Fatalf("seed: rows %d, error %v", len(rows), err)
	}
	t.Logf("stored=%d sessions=1000 tasks=1000 calls=49000 results=49000 revisions=%d", len(rows), pt.Revision)
}
