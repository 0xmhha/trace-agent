package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
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
	"github.com/0xmhha/trace-agent/internal/scenario"
	"github.com/0xmhha/trace-agent/internal/store"
	"github.com/0xmhha/trace-agent/internal/transport"
)

// Shift only the wall clock, preserving the same ordering, validity and durations.
// This lets the real desktop's moving 24h filter read the published IA fixture.
func present(t *testing.T, name string) *bytes.Reader {
	t.Helper()
	root, err := os.OpenRoot("../projects/trace-agent/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	var out bytes.Buffer
	var offset int64
	for s.Scan() {
		var row store.FactProjection
		if err = json.Unmarshal(s.Bytes(), &row); err != nil || row.Occurred == nil {
			t.Fatal("invalid published fixture")
		}
		if offset == 0 {
			offset = time.Now().Add(-time.Minute).UnixMilli() - *row.Occurred
		}
		*row.Occurred += offset
		if row.ValidUntil != nil {
			*row.ValidUntil += offset
		}
		row.Observed += offset
		if err = json.NewEncoder(&out).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Err(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(out.Bytes())
}

func seedSource(ctx context.Context, t *testing.T, db *store.Store) scenario.SeedResult {
	t.Helper()
	a, err := collect.New(collect.Telemetry, "browser", "otel", "2.1.329", make([]byte, 32), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Register(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SetConsent(ctx, a.Source(), true); err != nil {
		t.Fatal(err)
	}
	a = a.WithBodyCollection()
	f, err := os.Open("../projects/trace-agent/fixtures/stage-1/claude-otel.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	labels := map[int64]string{1: "C1-request", 2: "C1-response", 3: "U1"}
	bodies := map[int64]string{
		1: `{"messages":[{"role":"user","content":"C one request sample"}]}`,
		2: `{"content":[{"type":"text","text":"C one response sample"}]}`,
		3: `{"content":[{"type":"text","text":"U one unlinked sample"}]}`,
	}
	for s.Scan() {
		var native struct {
			Stream string
			Seq    int64
			Record map[string]any
		}
		if err = json.Unmarshal(s.Bytes(), &native); err != nil {
			t.Fatal(err)
		}
		native.Record["event.timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
		native.Record["body"] = bodies[native.Seq]
		raw, err := json.Marshal(native.Record)
		if err != nil {
			t.Fatal(err)
		}
		e, err := a.Project(collect.Position{Stream: native.Stream, Generation: "browser", Offset: native.Seq}, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Err(); err != nil {
		t.Fatal(err)
	}
	p, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	records, err := db.Records(ctx, p)
	if err != nil || len(records) != len(labels) {
		t.Fatalf("source fixture records=%d error=%v", len(records), err)
	}
	ids := make(map[string]string)
	for _, rec := range records {
		ids[labels[rec.Evidence.Position]] = strconv.FormatInt(rec.ID, 10)
	}
	return scenario.SeedResult{Point: p, Source: a.Source(), IDs: ids}
}

func TestDesktopIA(t *testing.T) {
	if os.Getenv("TRACE_IA_BROWSER") != "1" {
		t.Skip("set TRACE_IA_BROWSER=1 to run the production UI in Chrome")
	}
	for _, tc := range []struct {
		name, path string
		revision   int64
	}{{"IA120", "stage-4/ia120.jsonl", 120}, {"sequence", "stage-6/sequence.jsonl", 0}, {"source", "", 0}, {"delete", "", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "browser.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			var seed scenario.SeedResult
			switch tc.name {
			case "delete":
				seed = seedDeletion(ctx, t, db)
			case "source":
				seed = seedSource(ctx, t, db)
			default:
				seed, err = scenario.Seed(ctx, db, present(t, tc.path), tc.revision)
				if err != nil {
					t.Fatal(err)
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
			api := func(path string, handler func(http.ResponseWriter, *http.Request)) {
				mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-Trace-Test") != token {
						http.Error(w, "denied", http.StatusForbidden)
						return
					}
					handler(w, r)
				})
			}
			api("/_ticket", func(w http.ResponseWriter, _ *http.Request) {
				ticket, err := srv.Ticket(true)
				if err != nil {
					http.Error(w, "busy", http.StatusServiceUnavailable)
					return
				}
				_ = json.NewEncoder(w).Encode(BootstrapResult{endpoint, ticket})
			})
			api("/_seed", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(seed) })
			api("/_change", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					http.Error(w, "method", http.StatusMethodNotAllowed)
					return
				}
				p, err := db.SetRetention(r.Context(), seed.Source, 12)
				if err != nil {
					http.Error(w, "configuration", 500)
					return
				}
				_ = json.NewEncoder(w).Encode(p)
			})
			api("/_erase", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					http.Error(w, "method", http.StatusMethodNotAllowed)
					return
				}
				bounds := store.Filter{Source: seed.Source, Kind: "all", FromMS: time.Now().Add(-time.Hour).UnixMilli(), UntilMS: time.Now().Add(time.Hour).UnixMilli()}
				pv, err := db.Preview(r.Context(), bounds)
				if err != nil {
					http.Error(w, "preview", 500)
					return
				}
				result, err := db.Delete(r.Context(), bounds, pv)
				if err != nil {
					http.Error(w, "delete", 500)
					return
				}
				_ = json.NewEncoder(w).Encode(result.Point)
			})
			assets, err := fs.Sub(files, "frontend/dist")
			if err != nil {
				t.Fatal(err)
			}
			mux.Handle("/", http.FileServer(http.FS(assets)))
			cmd := exec.CommandContext(ctx, "node", "frontend/test/ia.mjs", tc.name, web.URL) // #nosec G204 -- fixed checked-in script, program and loopback test origin.
			cmd.Env = append(os.Environ(), "TRACE_TEST_TOKEN="+token)
			output, err := cmd.CombinedOutput()
			t.Log(string(output))
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "delete" {
				p, err := db.Point(ctx)
				if err != nil {
					t.Fatal(err)
				}
				rs, err := db.Records(ctx, p)
				if err != nil || len(rs) != 2 {
					t.Fatal("expected only the selected task to be removed", len(rs), err)
				}
				for _, r := range rs {
					if r.Evidence.Position == 0 {
						t.Fatal("selected task survived deletion")
					}
				}
			}
		})
	}
}
