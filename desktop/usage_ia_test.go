package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/scenario"
	"github.com/0xmhha/trace-agent/internal/store"
	"github.com/0xmhha/trace-agent/internal/transport"
	"github.com/0xmhha/trace-agent/internal/usage"
	"github.com/0xmhha/trace-agent/internal/usage/db"
	"github.com/0xmhha/trace-agent/internal/usage/insight"
	"github.com/0xmhha/trace-agent/internal/usage/service"
	"github.com/0xmhha/trace-agent/internal/usage/timeline"
	"github.com/0xmhha/trace-agent/internal/usage/transcript"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// The synthetic transcripts of the usage pipeline and where a transcript root
// keeps them. The expected numbers are in the README next to the fixtures.
const (
	usageIAFixtures     = "../internal/usage/testdata"
	usageIARootFixture  = "session.jsonl"
	usageIAAgentFixture = "agent-a1b2c3.jsonl"
	usageIASession      = "11111111-1111-4111-8111-111111111111"
	usageIAAgent        = "a1b2c3"
	usageIAProject      = "sample-project"
	usageIARootLabel    = "~/sample-transcripts"
	usageIASubagentsDir = "subagents"
	usageIAAgentPrefix  = "agent-"
	usageIATranscript   = ".jsonl"
)

// Expected overview of the two fixtures together, checked before the browser
// starts so that a broken time shift fails here and not as a missing tile.
const (
	usageIASessions   = 1
	usageIAPrompts    = 2
	usageIAModelCalls = 8
	usageIAToolCalls  = 6
)

// Refused extension inputs recorded before the scan, the way the event
// receiver records them. The transcripts alone fire no insight rule; refused
// inputs fire ext-rejected, so the insights page shows a title that Go
// generated in the page's language.
const (
	usageIAExtSender = "sample-tool"
	usageIARejected  = 3
	usageIAInsightID = "ext-rejected"
)

// usageIAInsightTitles is the title Go gives the ext-rejected insight for
// usageIARejected refused inputs, per locale the page sends.
var usageIAInsightTitles = map[usage.Locale]string{
	usage.LocaleEnglish: "3 extension inputs were rejected",
	usage.LocaleKorean:  "확장 입력 3건이 거부됐습니다",
}

const (
	// The Korean script walks every page in depth; the English one checks the
	// default language and the language selector. They share one pipeline.
	usageIAScript        = "frontend/test/usage.mjs"
	usageIAEnglishScript = "frontend/test/english.mjs"
	// The script reads what the harness laid out from here and reaches the
	// pipeline through the request path of the installed app's bridge.
	usageIASeedPath    = "/_seed"
	usageIATicketPath  = "/_ticket"
	usageIARequestPath = "/__usage"
	usageIATokenHeader = "X-Trace-Test"
	usageIATokenEnv    = "TRACE_TEST_TOKEN"
	usageIATimeout     = 3 * time.Minute
	usageIAMaxBody     = 1 << 20
	// The newest fixture record lands this long before the test starts, which
	// puts the whole session inside the pages' default range.
	usageIAAge = time.Minute
	// The pages default to this many days; the Go-side check asks the same.
	usageIARangeDays   = 7
	usageIAStampLayout = "2006-01-02T15:04:05.000Z"
	// The trace workspace shows the published IA fixture, as TestDesktopIA does.
	usageIATraceFixture  = "stage-4/ia120.jsonl"
	usageIATraceRevision = 120
)

// usageIAStamp matches the record time of one transcript line. Every time in
// the fixtures is written in this field.
var usageIAStamp = regexp.MustCompile(`"timestamp":"([^"]*)"`)

// usageIASeed is what the browser script needs to know about the layout.
type usageIASeed struct {
	SessionID string
	AgentID   string
	RootLabel string
	// InsightTitles maps a locale code to the title of the insight the
	// pipeline fires on the presented data.
	InsightTitles map[usage.Locale]string
}

// usageIARequest is one call of the fake bridge's Usage method.
type usageIARequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// usageRecordTime reads the record time out of one match of usageIAStamp.
func usageRecordTime(field []byte) (time.Time, error) {
	return time.Parse(usageIAStampLayout, string(usageIAStamp.FindSubmatch(field)[1]))
}

// presentUsageRoot lays the fixtures out like a transcript root with every
// record time moved by one offset, so order and intervals stay as published
// and the session ends shortly before now. The pages' moving range can then
// read a fixture whose dates are fixed in the past.
func presentUsageRoot(t *testing.T, now time.Time) string {
	t.Helper()
	root := t.TempDir()
	session := filepath.Join(root, usageIAProject, usageIASession)
	layout := []struct{ fixture, path string }{
		{usageIARootFixture, session + usageIATranscript},
		{usageIAAgentFixture, filepath.Join(session, usageIASubagentsDir, usageIAAgentPrefix+usageIAAgent+usageIATranscript)},
	}
	raws := make([][]byte, len(layout))
	var newest time.Time
	for i, file := range layout {
		raw, err := os.ReadFile(filepath.Join(usageIAFixtures, file.fixture))
		if err != nil {
			t.Fatal(err)
		}
		raws[i] = raw
		for _, field := range usageIAStamp.FindAll(raw, -1) {
			at, err := usageRecordTime(field)
			if err != nil {
				t.Fatalf("%s: %v", file.fixture, err)
			}
			if at.After(newest) {
				newest = at
			}
		}
	}
	if newest.IsZero() {
		t.Fatal("fixtures have no record time")
	}
	offset := now.Add(-usageIAAge).Sub(newest)
	for i, file := range layout {
		shifted := usageIAStamp.ReplaceAllFunc(raws[i], func(field []byte) []byte {
			at, err := usageRecordTime(field)
			if err != nil {
				// Every match was parsed above; an unparsed one stays as it is.
				return field
			}
			return []byte(`"timestamp":"` + at.Add(offset).UTC().Format(usageIAStampLayout) + `"`)
		})
		if err := os.MkdirAll(filepath.Dir(file.path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file.path, shifted, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file.path, now, now); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// declineUsagePrompt answers every native confirmation with cancel. The
// script never asks for one; if a page did, nothing would be widened.
func declineUsagePrompt(context.Context, wruntime.MessageDialogOptions) (string, error) {
	return usagePromptCancel, nil
}

// newUsageIARuntime runs the real pipeline over the presented fixtures and
// returns it behind the bridge's own runtime, so that the page gets the
// responses and the error texts of the installed app.
func newUsageIARuntime(ctx context.Context, t *testing.T) *usageRuntime {
	t.Helper()
	numbers, err := db.Open(ctx, filepath.Join(t.TempDir(), usageDatabaseFile), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = numbers.Close() })
	svc, err := service.New(service.Config{
		Store: numbers, Root: presentUsageRoot(t, time.Now()), RootLabel: usageIARootLabel,
		Scan: transcript.Scan, Timeline: timeline.Read, Evaluate: insight.Evaluate,
		Now: time.Now, Location: time.Local,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.SetConsent(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err = numbers.CountRejected(ctx, usageIAExtSender, usageIARejected); err != nil {
		t.Fatal(err)
	}
	if err = svc.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	return &usageRuntime{service: svc, rootLabel: usageIARootLabel, dialog: declineUsagePrompt}
}

// checkUsageIAOverview asks for the range the dashboard opens with.
func checkUsageIAOverview(ctx context.Context, t *testing.T, u *usageRuntime) {
	t.Helper()
	until := time.Now()
	params, err := json.Marshal(usage.Query{Range: usage.Range{From: until.AddDate(0, 0, -usageIARangeDays), Until: until}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := u.request(ctx, usage.MethodOverview, string(params))
	if err != nil {
		t.Fatal(err)
	}
	var o usage.Overview
	if err = json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	if o.Sessions != usageIASessions || o.Prompts != usageIAPrompts || o.ModelCalls != usageIAModelCalls || o.ToolCalls != usageIAToolCalls {
		t.Fatalf("overview of the presented fixtures = %d sessions, %d prompts, %d model calls, %d tool calls",
			o.Sessions, o.Prompts, o.ModelCalls, o.ToolCalls)
	}
}

// checkUsageIAInsights asks for the insights in every locale the page can
// send and checks that the title of the fired rule is in that language.
func checkUsageIAInsights(ctx context.Context, t *testing.T, u *usageRuntime) {
	t.Helper()
	until := time.Now()
	for _, locale := range usage.Locales() {
		want, ok := usageIAInsightTitles[locale]
		if !ok {
			t.Fatalf("no expected insight title for locale %q", locale)
		}
		params, err := json.Marshal(usage.Query{Range: usage.Range{From: until.AddDate(0, 0, -usageIARangeDays), Until: until}, Locale: locale})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := u.request(ctx, usage.MethodInsights, string(params))
		if err != nil {
			t.Fatal(err)
		}
		var report usage.InsightReport
		if err = json.Unmarshal([]byte(raw), &report); err != nil {
			t.Fatal(err)
		}
		var titles []string
		for _, item := range report.Items {
			if item.RuleID == usageIAInsightID {
				titles = append(titles, item.Title)
			}
		}
		if len(titles) != 1 || titles[0] != want {
			t.Fatalf("%s insight titles in %s = %q, want [%q]", usageIAInsightID, locale, titles, want)
		}
	}
}

// usageIAStatus picks the status of a refused request. The page only reads
// the text; the status keeps a refusal visible in the browser's log.
func usageIAStatus(err error) int {
	switch {
	case errors.Is(err, errUsageRequest):
		return http.StatusBadRequest
	case errors.Is(err, errUsageNotFound):
		return http.StatusNotFound
	case errors.Is(err, errUsageNoConsent):
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

// serveUsageIA answers the fake bridge the way Bridge.Usage answers the
// WebView: the response JSON, or the bridge's error text with an error status.
func serveUsageIA(u *usageRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var req usageIARequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, usageIAMaxBody)).Decode(&req); err != nil {
			http.Error(w, "request", http.StatusBadRequest)
			return
		}
		response, err := u.request(r.Context(), req.Method, string(req.Params))
		if err != nil {
			http.Error(w, err.Error(), usageIAStatus(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}
}

// TestDesktopUsageIA drives the usage pages of the built frontend in Chrome.
// The bridge is fake, the pipeline behind it is real: the synthetic
// transcripts are scanned into a usage store and every number on the pages
// comes from there. The trace collector runs too, so the script can cross
// between the two workspaces.
func TestDesktopUsageIA(t *testing.T) {
	if os.Getenv("TRACE_IA_BROWSER") != "1" {
		t.Skip("set TRACE_IA_BROWSER=1 to run the production UI in Chrome")
	}
	ctx, cancel := context.WithTimeout(t.Context(), usageIATimeout)
	defer cancel()

	pipeline := newUsageIARuntime(ctx, t)
	checkUsageIAOverview(ctx, t, pipeline)
	checkUsageIAInsights(ctx, t, pipeline)

	traces, err := store.Open(ctx, filepath.Join(t.TempDir(), "browser.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = traces.Close() }()
	if _, err = scenario.Seed(ctx, traces, present(t, usageIATraceFixture), usageIATraceRevision); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	web := httptest.NewServer(mux)
	defer web.Close()
	srv, err := transport.New(traces, web.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close(context.Background()) }()
	endpoint, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}

	token := rand.Text()
	api := func(path string, handler http.HandlerFunc) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(usageIATokenHeader) != token {
				http.Error(w, "denied", http.StatusForbidden)
				return
			}
			handler(w, r)
		})
	}
	api(usageIATicketPath, func(w http.ResponseWriter, _ *http.Request) {
		ticket, err := srv.Ticket(true)
		if err != nil {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(BootstrapResult{endpoint, ticket})
	})
	api(usageIASeedPath, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(usageIASeed{
			SessionID: usageIASession, AgentID: usageIAAgent, RootLabel: usageIARootLabel,
			InsightTitles: usageIAInsightTitles,
		})
	})
	api(usageIARequestPath, serveUsageIA(pipeline))
	assets, err := fs.Sub(files, "frontend/dist")
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(assets)))

	// The Korean script runs first: it expects the pipeline as the harness left
	// it, and the English one changes only what the browser stores.
	for _, script := range []struct{ name, path string }{
		{"korean", usageIAScript},
		{"english", usageIAEnglishScript},
	} {
		t.Run(script.name, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, "node", script.path, web.URL) // #nosec G204 -- fixed checked-in script, program and loopback test origin.
			cmd.Env = append(os.Environ(), usageIATokenEnv+"="+token)
			output, err := cmd.CombinedOutput()
			t.Log(string(output))
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
