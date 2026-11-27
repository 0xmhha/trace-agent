package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// testRange is a valid request range that ends at the test clock.
const testRange = `"range":{"from":"2026-03-03T12:00:00Z","until":"2026-03-10T12:00:00Z"}`

var (
	rangeFrom  = time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)
	rangeUntil = testNow
)

// call serves one request and decodes the response.
func call[T any](t *testing.T, svc *Service, method, params string) T {
	t.Helper()
	raw, err := svc.Handle(context.Background(), method, json.RawMessage(params))
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var response T
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("%s response %s: %v", method, raw, err)
	}
	return response
}

// settled is a harness whose full pass is done, so that coverage shows what
// the store reports.
func settled(t *testing.T, options ...func(*Config)) *harness {
	t.Helper()
	h := newHarness(t, options...)
	h.fixtures()
	h.scanOnce()
	h.store.with(func(f *fakeStore) {
		f.details[testSession] = usage.SessionDetail{Session: usage.SessionRow{ID: testSession}}
		f.streams[streamKey{testSession, ""}] = rootRel
		f.streams[streamKey{testSession, testAgent}] = agentRel
	})
	return h
}

func TestNewValidatesConfig(t *testing.T) {
	valid := func() Config {
		return Config{
			Store:    newFakeStore(),
			Root:     t.TempDir(),
			Scan:     lineScan,
			Timeline: (&timelineLog{}).read,
			Evaluate: (&evaluateLog{}).evaluate,
			Now:      time.Now,
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"store", func(c *Config) { c.Store = nil }},
		{"root", func(c *Config) { c.Root = "" }},
		{"relative root", func(c *Config) { c.Root = "relative/projects" }},
		{"scan", func(c *Config) { c.Scan = nil }},
		{"timeline", func(c *Config) { c.Timeline = nil }},
		{"evaluate", func(c *Config) { c.Evaluate = nil }},
		{"clock", func(c *Config) { c.Now = nil }},
		{"negative interval", func(c *Config) { c.Interval = -time.Second }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid()
			tc.change(&cfg)
			if svc, err := New(cfg); err == nil || svc != nil {
				t.Fatalf("New = %v, %v; want an error and no service", svc, err)
			}
		})
	}
	cfg := valid()
	cfg.Root = "/synthetic/base/projects"
	svc, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if svc.cfg.Interval != DefaultInterval || svc.cfg.Location != time.UTC || svc.cfg.RootLabel != "projects" {
		t.Fatalf("defaults = interval %v, location %v, label %q", svc.cfg.Interval, svc.cfg.Location, svc.cfg.RootLabel)
	}
	if svc.chunkBytes != usage.ScanChunkBytes {
		t.Fatalf("chunk size = %d; want usage.ScanChunkBytes", svc.chunkBytes)
	}
}

func TestHandleRejectsBadRequests(t *testing.T) {
	h := settled(t)
	wide := `"range":{"from":"2024-01-01T00:00:00Z","until":"2026-03-10T12:00:00Z"}`
	long := strings.Repeat("x", maxNameLen+1)
	for _, tc := range []struct{ name, method, params string }{
		{"unknown method", "dropEverything", `{}`},
		{"empty method", "", `{}`},
		{"malformed json", usage.MethodOverview, `{"range":`},
		{"array instead of object", usage.MethodOverview, `[]`},
		{"string instead of object", usage.MethodStatus, `"status"`},
		{"number instead of object", usage.MethodRescan, `7`},
		{"array for a method without params", usage.MethodClearData, `[1]`},
		{"trailing value", usage.MethodStatus, `{} {}`},
		{"range of the wrong type", usage.MethodOverview, `{"range":"last week"}`},
		{"time of the wrong format", usage.MethodOverview, `{"range":{"from":"yesterday","until":"today"}}`},
		{"missing range", usage.MethodOverview, `{}`},
		{"empty body for a ranged method", usage.MethodTools, ``},
		{"inverted range", usage.MethodTools, `{"range":{"from":"2026-03-10T12:00:00Z","until":"2026-03-03T12:00:00Z"}}`},
		{"empty range", usage.MethodProjects, `{"range":{"from":"2026-03-10T12:00:00Z","until":"2026-03-10T12:00:00Z"}}`},
		{"range too wide", usage.MethodInsights, `{` + wide + `}`},
		{"negative bin", usage.MethodOverview, `{` + testRange + `,"binHours":-1}`},
		{"bin of the wrong type", usage.MethodOverview, `{` + testRange + `,"binHours":"day"}`},
		{"project too long", usage.MethodOverview, `{` + testRange + `,"project":"` + long + `"}`},
		{"sessions without range", usage.MethodSessions, `{"limit":10}`},
		{"sessions negative limit", usage.MethodSessions, `{` + testRange + `,"limit":-1}`},
		{"sessions unknown sort", usage.MethodSessions, `{` + testRange + `,"sort":"random"}`},
		{"sessions limit of the wrong type", usage.MethodSessions, `{` + testRange + `,"limit":"ten"}`},
		{"session without id", usage.MethodSession, `{}`},
		{"session id of the wrong type", usage.MethodSession, `{"sessionId":5}`},
		{"session id too long", usage.MethodSession, `{"sessionId":"` + long + `"}`},
		{"timeline without session", usage.MethodTimeline, `{"limit":10}`},
		{"timeline negative limit", usage.MethodTimeline, `{"sessionId":"s","limit":-5}`},
		{"timeline limit of the wrong type", usage.MethodTimeline, `{"sessionId":"s","limit":"all"}`},
		{"timeline cursor too long", usage.MethodTimeline, `{"sessionId":"s","cursor":"` + strings.Repeat("c", maxCursorLen+1) + `"}`},
		{"ext events source of the wrong type", usage.MethodExtEvents, `{"source":7}`},
		{"extensions without range", usage.MethodExtensions, `{}`},
		{"ext events inverted range", usage.MethodExtEvents, `{"range":{"from":"2026-03-10T12:00:00Z","until":"2026-03-03T12:00:00Z"}}`},
		{"ext events unknown level", usage.MethodExtEvents, `{"level":"loud"}`},
		{"ext events negative limit", usage.MethodExtEvents, `{"limit":-1}`},
		{"exclude without project", usage.MethodSetExcluded, `{"excluded":true}`},
		{"exclude without flag", usage.MethodSetExcluded, `{"project":"alpha"}`},
		{"exclude flag of the wrong type", usage.MethodSetExcluded, `{"project":"alpha","excluded":"yes"}`},
		{"window of zero days", usage.MethodSetWindow, `{"days":0}`},
		{"window without days", usage.MethodSetWindow, `{}`},
		{"window too wide", usage.MethodSetWindow, fmt.Sprintf(`{"days":%d}`, usage.MaxWindowDays+1)},
		{"window of the wrong type", usage.MethodSetWindow, `{"days":"30"}`},
		{"extension without flag", usage.MethodSetExtension, `{}`},
		{"extension flag of the wrong type", usage.MethodSetExtension, `{"enabled":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := h.svc.Handle(context.Background(), tc.method, json.RawMessage(tc.params))
			if !errors.Is(err, usage.ErrBadRequest) || raw != nil {
				t.Fatalf("Handle = %s, %v; want usage.ErrBadRequest", raw, err)
			}
		})
	}
	h.store.with(func(f *fakeStore) {
		if len(f.log) != 0 {
			t.Fatalf("a rejected request changed the store: %v", f.log)
		}
	})
}

func TestHandleAppliesDefaultsAndLimits(t *testing.T) {
	h := settled(t)
	h.store.with(func(f *fakeStore) {
		f.events = []usage.ExtEvent{{ID: "e1"}}
	})
	for _, tc := range []struct {
		params    string
		wantLimit int
		wantSort  usage.SessionSort
	}{
		{`{` + testRange + `}`, usage.DefaultPageSize, usage.SortLast},
		{`{` + testRange + `,"limit":5000,"sort":"cost"}`, usage.MaxPageSize, usage.SortCost},
		{`{` + testRange + `,"limit":7,"sort":"errors","project":"alpha","search":"build","cursor":"c1"}`, 7, usage.SortErrors},
	} {
		call[usage.SessionPage](t, h.svc, usage.MethodSessions, tc.params)
		h.store.with(func(f *fakeStore) {
			q := f.sessionQueries[len(f.sessionQueries)-1]
			if q.Limit != tc.wantLimit || q.Sort != tc.wantSort {
				t.Errorf("sessions %s reached the store as limit %d, sort %q; want %d, %q", tc.params, q.Limit, q.Sort, tc.wantLimit, tc.wantSort)
			}
			if !q.Range.From.Equal(rangeFrom) || !q.Range.Until.Equal(rangeUntil) {
				t.Errorf("sessions range = %+v", q.Range)
			}
		})
	}
	h.store.with(func(f *fakeStore) {
		last := f.sessionQueries[len(f.sessionQueries)-1]
		if last.Project != "alpha" || last.Search != "build" || last.Cursor != "c1" {
			t.Errorf("session filters were not passed on: %+v", last)
		}
	})

	// Extension events may be asked for without any range.
	for params, wantLimit := range map[string]int{
		``:                              usage.DefaultPageSize,
		`null`:                          usage.DefaultPageSize,
		`{"limit":900,"level":"error"}`: usage.MaxPageSize,
		`{"range":{"from":"2026-03-03T12:00:00Z"},"limit":3,"source":"deploy-script"}`: 3,
	} {
		page := call[usage.ExtPage](t, h.svc, usage.MethodExtEvents, params)
		if page.Total != 1 {
			t.Errorf("extEvents %q total = %d; want 1", params, page.Total)
		}
		h.store.with(func(f *fakeStore) {
			if q := f.extQueries[len(f.extQueries)-1]; q.Limit != wantLimit {
				t.Errorf("extEvents %q reached the store with limit %d; want %d", params, q.Limit, wantLimit)
			}
		})
	}

	// The widest allowed range is the widest window plus one day.
	until := testNow
	from := until.Add(-maxRangeSpan)
	params := fmt.Sprintf(`{"range":{"from":%q,"until":%q},"binHours":24,"project":"alpha"}`, from.Format(time.RFC3339), until.Format(time.RFC3339))
	call[usage.Overview](t, h.svc, usage.MethodOverview, params)
	h.store.with(func(f *fakeStore) {
		q := f.overviewQueries[len(f.overviewQueries)-1]
		if q.BinHours != 24 || q.Project != "alpha" || !q.Range.From.Equal(from) {
			t.Errorf("overview query = %+v", q)
		}
		if f.overviewLoc != time.UTC {
			t.Errorf("overview location = %v; want the configured default UTC", f.overviewLoc)
		}
	})
}

func TestHandleOverlaysCoverage(t *testing.T) {
	partial := usage.Coverage{
		State:           usage.CoveragePartial,
		FilesTracked:    3,
		FilesUnreadable: 1,
		LinesSkipped:    4,
		Reasons:         []string{usage.ReasonUnreadableFiles, usage.ReasonSkippedLines},
	}
	// coverageOf reads the coverage of every range aggregate.
	coverageOf := map[string]func(t *testing.T, svc *Service) usage.Coverage{
		usage.MethodOverview: func(t *testing.T, svc *Service) usage.Coverage {
			return call[usage.Overview](t, svc, usage.MethodOverview, `{`+testRange+`}`).Coverage
		},
		usage.MethodTools: func(t *testing.T, svc *Service) usage.Coverage {
			return call[usage.Tools](t, svc, usage.MethodTools, `{`+testRange+`}`).Coverage
		},
		usage.MethodProjects: func(t *testing.T, svc *Service) usage.Coverage {
			return call[usage.Projects](t, svc, usage.MethodProjects, `{`+testRange+`}`).Coverage
		},
		usage.MethodSessions: func(t *testing.T, svc *Service) usage.Coverage {
			return call[usage.SessionPage](t, svc, usage.MethodSessions, `{`+testRange+`,"project":"alpha"}`).Coverage
		},
		usage.MethodInsights: func(t *testing.T, svc *Service) usage.Coverage {
			return call[usage.InsightReport](t, svc, usage.MethodInsights, `{`+testRange+`}`).Coverage
		},
		usage.MethodStatus: func(t *testing.T, svc *Service) usage.Coverage {
			return call[usage.Status](t, svc, usage.MethodStatus, `{}`).Coverage
		},
	}
	for _, tc := range []struct {
		name    string
		consent bool
		scanned bool
		stored  usage.Coverage
		want    usage.CoverageState
	}{
		{"disabled without consent", false, true, partial, usage.CoverageDisabled},
		{"disabled wins over a pending pass", false, false, partial, usage.CoverageDisabled},
		{"scanning while a full pass is pending", true, false, partial, usage.CoverageScanning},
		{"partial from the store", true, true, partial, usage.CoveragePartial},
		{"complete from the store", true, true, usage.Coverage{State: usage.CoverageComplete}, usage.CoverageComplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.fixtures()
			if tc.scanned {
				h.scanOnce()
			}
			h.store.with(func(f *fakeStore) {
				f.settings.Consent = tc.consent
				f.coverage = tc.stored
			})
			for method, read := range coverageOf {
				got := read(t, h.svc)
				if got.State != tc.want {
					t.Errorf("%s coverage state = %q; want %q", method, got.State, tc.want)
				}
				// The overlay changes the state only; the numbers stay.
				if got.FilesTracked != tc.stored.FilesTracked || got.FilesUnreadable != tc.stored.FilesUnreadable ||
					got.LinesSkipped != tc.stored.LinesSkipped || !slices.Equal(got.Reasons, append([]string{}, tc.stored.Reasons...)) {
					t.Errorf("%s coverage = %+v; want the stored numbers of %+v", method, got, tc.stored)
				}
			}
		})
	}
}

func TestHandleAsksCoverageForTheRequest(t *testing.T) {
	h := settled(t)
	h.store.with(func(f *fakeStore) { f.coverageQueries = nil })
	call[usage.SessionPage](t, h.svc, usage.MethodSessions, `{`+testRange+`,"project":"alpha","search":"x"}`)
	call[usage.Status](t, h.svc, usage.MethodStatus, ``)
	h.store.with(func(f *fakeStore) {
		if len(f.coverageQueries) != 2 {
			t.Fatalf("coverage queries = %+v; want one per request", f.coverageQueries)
		}
		page, status := f.coverageQueries[0], f.coverageQueries[1]
		if page.Project != "alpha" || !page.Range.From.Equal(rangeFrom) || !page.Range.Until.Equal(rangeUntil) {
			t.Errorf("session page coverage query = %+v; want the range and project of the request", page)
		}
		// The status describes the whole window, starting safely inside it.
		windowFrom := testNow.AddDate(0, 0, -usage.DefaultWindowDays)
		if status.Project != "" || !status.Range.Until.Equal(testNow) ||
			status.Range.From.Before(windowFrom) || status.Range.From.After(windowFrom.Add(statusRangeSlack)) {
			t.Errorf("status coverage query = %+v; want the window ending at %v", status, testNow)
		}
	})
}

func TestHandleStatus(t *testing.T) {
	extension := &fakeExtension{endpoint: usage.ExtEndpoint{Enabled: true, URL: "http://127.0.0.1:4100/v1/events"}}
	h := settled(t, func(cfg *Config) { cfg.Extension = extension })
	h.store.with(func(f *fakeStore) {
		f.settings.WindowDays = 14
		f.settings.Excluded = []string{"zeta", "beta"}
		f.version = 42
	})
	want := usage.Status{
		Consent:     true,
		Root:        testLabel,
		WindowDays:  14,
		FilesTotal:  2,
		FilesDone:   2,
		Coverage:    usage.Coverage{State: usage.CoverageComplete, Reasons: []string{}},
		Excluded:    []string{"beta", "zeta"},
		Extension:   extension.endpoint,
		DataVersion: 42,
	}
	for _, params := range []string{``, `{}`, ` null `, `{"ignored":true}`} {
		if got := call[usage.Status](t, h.svc, usage.MethodStatus, params); !reflect.DeepEqual(got, want) {
			t.Fatalf("status(%q) = %+v\nwant %+v", params, got, want)
		}
	}

	// Without an extension control the endpoint is reported as disabled.
	bare := settled(t)
	if got := bare.status().Extension; got != (usage.ExtEndpoint{}) {
		t.Fatalf("endpoint without a control = %+v; want the zero value", got)
	}
	for _, method := range []string{"Settings", "Coverage", "DataVersion"} {
		bare.store.with(func(f *fakeStore) { f.fail = map[string]error{method: errInjected} })
		if _, err := bare.svc.Handle(context.Background(), usage.MethodStatus, nil); !errors.Is(err, errInjected) {
			t.Fatalf("status with a failing %s = %v; want the store failure", method, err)
		}
	}
}

func TestHandleAggregates(t *testing.T) {
	h := settled(t)
	h.store.with(func(f *fakeStore) {
		f.overview = usage.Overview{Sessions: 3, ModelCalls: 9}
		f.tools = usage.Tools{Tools: []usage.ToolUsage{{Name: "Bash", Calls: 4}}}
		f.projects = usage.Projects{Items: []usage.ProjectUsage{{Key: "alpha"}}}
		f.sessionPage = usage.SessionPage{Items: []usage.SessionRow{{ID: testSession}}, Total: 1}
		f.extSources = []usage.ExtSourceRow{{Source: "deploy-script", Events: 2}}
		f.extRejected = 5
	})
	params := `{` + testRange + `,"project":"alpha"}`
	if got := call[usage.Overview](t, h.svc, usage.MethodOverview, params); got.Sessions != 3 || got.ModelCalls != 9 {
		t.Errorf("overview = %+v", got)
	}
	if got := call[usage.Tools](t, h.svc, usage.MethodTools, params); len(got.Tools) != 1 || got.Tools[0].Name != "Bash" {
		t.Errorf("tools = %+v", got)
	}
	if got := call[usage.Projects](t, h.svc, usage.MethodProjects, params); len(got.Items) != 1 || got.Items[0].Key != "alpha" {
		t.Errorf("projects = %+v", got)
	}
	if got := call[usage.SessionPage](t, h.svc, usage.MethodSessions, params); got.Total != 1 || got.Items[0].ID != testSession {
		t.Errorf("sessions = %+v", got)
	}
	extensions := call[usage.ExtOverview](t, h.svc, usage.MethodExtensions, params)
	if extensions.Rejected != 5 || len(extensions.Sources) != 1 || extensions.Endpoint != (usage.ExtEndpoint{}) {
		t.Errorf("extensions = %+v", extensions)
	}
	h.store.with(func(f *fakeStore) {
		for name, queries := range map[string][]usage.Query{
			"overview": f.overviewQueries, "tools": f.toolQueries, "projects": f.projectQueries, "extensions": f.extSourceQuery,
		} {
			if len(queries) != 1 || queries[0].Project != "alpha" || !queries[0].Range.From.Equal(rangeFrom) {
				t.Errorf("%s query = %+v; want the request passed on once", name, queries)
			}
		}
	})

	for _, tc := range []struct{ method, failing string }{
		{usage.MethodOverview, "Overview"},
		{usage.MethodOverview, "Coverage"},
		{usage.MethodOverview, "Settings"},
		{usage.MethodTools, "Tools"},
		{usage.MethodTools, "Coverage"},
		{usage.MethodProjects, "Projects"},
		{usage.MethodProjects, "Coverage"},
		{usage.MethodSessions, "Sessions"},
		{usage.MethodSessions, "Coverage"},
		{usage.MethodExtensions, "ExtSources"},
		{usage.MethodExtEvents, "ExtEvents"},
		{usage.MethodInsights, "Overview"},
		{usage.MethodInsights, "Tools"},
		{usage.MethodInsights, "Sessions"},
		{usage.MethodInsights, "ExtSources"},
	} {
		h.store.with(func(f *fakeStore) { f.fail = map[string]error{tc.failing: errInjected} })
		if _, err := h.svc.Handle(context.Background(), tc.method, json.RawMessage(params)); !errors.Is(err, errInjected) {
			t.Errorf("%s with a failing %s = %v; want the store failure", tc.method, tc.failing, err)
		}
	}
}

func TestHandleInsights(t *testing.T) {
	h := settled(t)
	rows := []usage.SessionRow{{ID: "heavy"}, {ID: "light"}}
	sources := []usage.ExtSourceRow{{Source: "deploy-script", Events: 30, Errors: 9}}
	h.store.with(func(f *fakeStore) {
		f.coverage = usage.Coverage{State: usage.CoveragePartial, LinesSkipped: 2, Reasons: []string{usage.ReasonSkippedLines}}
		f.overview = usage.Overview{Sessions: 2, ModelCalls: 60}
		f.tools = usage.Tools{Tools: []usage.ToolUsage{{Name: "Bash", Calls: 25, Errors: 5}}}
		f.sessionPage = usage.SessionPage{Items: rows, Total: 2}
		f.extSources = sources
		f.extRejected = 3
		f.sessionQueries = nil
	})
	report := call[usage.InsightReport](t, h.svc, usage.MethodInsights, `{`+testRange+`,"project":"alpha","locale":"ko"}`)
	if report.Coverage.State != usage.CoveragePartial || report.Coverage.LinesSkipped != 2 {
		t.Errorf("report coverage = %+v; want the partial coverage of the range", report.Coverage)
	}
	if !report.GeneratedAt.Equal(testNow) || report.RulesPassed != 1 {
		t.Errorf("report = %+v; want the evaluator's report stamped with the clock", report)
	}
	if len(h.insights.inputs) != 1 || !h.insights.at[0].Equal(testNow) {
		t.Fatalf("evaluate calls = %d", len(h.insights.inputs))
	}
	in := h.insights.inputs[0]
	if !in.Range.From.Equal(rangeFrom) || !in.Range.Until.Equal(rangeUntil) {
		t.Errorf("input range = %+v", in.Range)
	}
	if in.Locale != usage.LocaleKorean {
		t.Errorf("input locale = %q; want the query's %q", in.Locale, usage.LocaleKorean)
	}
	if in.Overview.ModelCalls != 60 || in.Overview.Coverage.State != usage.CoveragePartial {
		t.Errorf("input overview = %+v; want the numbers with coverage filled in", in.Overview)
	}
	if len(in.Tools.Tools) != 1 || in.Tools.Coverage.State != usage.CoveragePartial {
		t.Errorf("input tools = %+v", in.Tools)
	}
	if !reflect.DeepEqual(in.Sessions, rows) || !reflect.DeepEqual(in.Ext, sources) || in.ExtRejected != 3 {
		t.Errorf("input sessions %+v, ext %+v, rejected %d", in.Sessions, in.Ext, in.ExtRejected)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.sessionQueries) != 1 {
			t.Fatalf("session queries = %+v", f.sessionQueries)
		}
		q := f.sessionQueries[0]
		if q.Sort != usage.SortTokens || q.Limit != usage.MaxInsightSessions || q.Project != "alpha" || !q.Range.From.Equal(rangeFrom) {
			t.Errorf("heaviest-session query = %+v; want tokens order, limit %d", q, usage.MaxInsightSessions)
		}
	})
}

func TestHandleInsightsPassesLocale(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params string
		want   usage.Locale
	}{
		{"English", `{` + testRange + `,"locale":"en"}`, usage.LocaleEnglish},
		{"Korean", `{` + testRange + `,"locale":"ko"}`, usage.LocaleKorean},
		{"absent is left to the evaluator", `{` + testRange + `}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := settled(t)
			call[usage.InsightReport](t, h.svc, usage.MethodInsights, tc.params)
			if len(h.insights.inputs) != 1 {
				t.Fatalf("evaluate calls = %d, want 1", len(h.insights.inputs))
			}
			if got := h.insights.inputs[0].Locale; got != tc.want {
				t.Errorf("input locale = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleChangesReturnFreshStatus(t *testing.T) {
	h := settled(t)
	status := call[usage.Status](t, h.svc, usage.MethodSetExcluded, `{"project":"beta","excluded":true}`)
	if !slices.Equal(status.Excluded, []string{"beta"}) || status.Scanning {
		t.Errorf("status after excluding = %+v", status)
	}
	status = call[usage.Status](t, h.svc, usage.MethodSetWindow, `{"days":90}`)
	if status.WindowDays != 90 || !status.Scanning {
		t.Errorf("status after widening = %+v; want 90 days and a pending pass", status)
	}
	h.scanOnce()
	status = call[usage.Status](t, h.svc, usage.MethodRescan, `{}`)
	if !status.Scanning {
		t.Errorf("status after rescan = %+v; want a pending pass", status)
	}
	h.store.with(func(f *fakeStore) {
		want := []string{"SetExcluded:beta:true", "SetWindowDays:90", "ResetCursors"}
		if !slices.Equal(f.log, want) {
			t.Fatalf("store calls = %v\nwant %v", f.log, want)
		}
		for name, cursor := range f.cursors {
			if cursor.Offset != 0 {
				t.Errorf("cursor of %s = %d after rescan; want 0", name, cursor.Offset)
			}
		}
	})

	for _, tc := range []struct{ method, params, failing string }{
		{usage.MethodSetExcluded, `{"project":"beta","excluded":false}`, "SetExcluded"},
		{usage.MethodSetWindow, `{"days":120}`, "SetWindowDays"},
		{usage.MethodSetWindow, `{"days":120}`, "Settings"},
		{usage.MethodRescan, ``, "ResetCursors"},
		{usage.MethodClearData, ``, "SetConsent"},
	} {
		h.store.with(func(f *fakeStore) { f.fail = map[string]error{tc.failing: errInjected} })
		if _, err := h.svc.Handle(context.Background(), tc.method, json.RawMessage(tc.params)); !errors.Is(err, errInjected) {
			t.Errorf("%s with a failing %s = %v; want the store failure", tc.method, tc.failing, err)
		}
	}
}

func TestHandleClearDataWithdrawsConsentFirst(t *testing.T) {
	h := settled(t)
	h.store.with(func(f *fakeStore) { f.fail["Clear"] = errInjected })
	if _, err := h.svc.Handle(context.Background(), usage.MethodClearData, nil); !errors.Is(err, errInjected) {
		t.Fatalf("clearData with a failing Clear = %v", err)
	}
	h.store.with(func(f *fakeStore) {
		if f.settings.Consent {
			t.Fatal("consent must stay withdrawn when clearing fails")
		}
		f.fail = map[string]error{}
		f.settings.Consent = true
		f.log = nil
	})

	status := call[usage.Status](t, h.svc, usage.MethodClearData, `{}`)
	if status.Consent || status.Scanning || status.Coverage.State != usage.CoverageDisabled {
		t.Fatalf("status after clearData = %+v; want collection off", status)
	}
	h.store.with(func(f *fakeStore) {
		if want := []string{"SetConsent:false", "Clear"}; !slices.Equal(f.log, want) {
			t.Fatalf("store calls = %v\nwant %v", f.log, want)
		}
	})

	// Nothing is read until consent is granted anew; then everything is.
	h.scans.reset()
	h.scanOnce()
	if calls := h.scans.calls(); len(calls) != 0 {
		t.Fatalf("scanned after clearData: %+v", calls)
	}
	if err := h.svc.SetConsent(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	h.scanOnce()
	if got, want := h.scanned(), []string{testSession + "/@0", testSession + "/" + testAgent + "@0"}; !slices.Equal(got, want) {
		t.Fatalf("scans after granting again = %v\nwant %v", got, want)
	}
}

func TestHandleSetExtensionEnabled(t *testing.T) {
	const port = 4100
	extension := &fakeExtension{port: port, endpoint: usage.ExtEndpoint{URL: "http://127.0.0.1:4100/v1/events"}}
	h := settled(t, func(cfg *Config) { cfg.Extension = extension })

	status := call[usage.Status](t, h.svc, usage.MethodSetExtension, `{"enabled":true}`)
	if !status.Extension.Enabled || status.Extension.URL != extension.endpoint.URL {
		t.Fatalf("status after enabling = %+v; want the live endpoint", status.Extension)
	}
	status = call[usage.Status](t, h.svc, usage.MethodSetExtension, `{"enabled":false}`)
	if status.Extension.Enabled {
		t.Fatalf("status after disabling = %+v", status.Extension)
	}
	h.store.with(func(f *fakeStore) {
		want := []extensionSetting{{true, port}, {false, port}}
		if !slices.Equal(f.extensionSets, want) || !slices.Equal(extension.calls, []bool{true, false}) {
			t.Fatalf("stored %+v after control calls %v; want %+v", f.extensionSets, extension.calls, want)
		}
	})

	// A control that fails leaves the stored setting alone.
	extension.err = errInjected
	if _, err := h.svc.Handle(context.Background(), usage.MethodSetExtension, json.RawMessage(`{"enabled":true}`)); !errors.Is(err, errInjected) {
		t.Fatalf("setExtensionEnabled with a failing control = %v", err)
	}
	extension.err = nil
	h.store.with(func(f *fakeStore) {
		if len(f.extensionSets) != 2 {
			t.Fatalf("a failed switch was stored: %+v", f.extensionSets)
		}
		f.fail["SetExtension"] = errInjected
	})
	if _, err := h.svc.Handle(context.Background(), usage.MethodSetExtension, json.RawMessage(`{"enabled":true}`)); !errors.Is(err, errInjected) {
		t.Fatalf("setExtensionEnabled with a failing store = %v", err)
	}

	bare := settled(t)
	if _, err := bare.svc.Handle(context.Background(), usage.MethodSetExtension, json.RawMessage(`{"enabled":true}`)); !errors.Is(err, ErrExtensionUnavailable) {
		t.Fatalf("setExtensionEnabled without a control = %v; want ErrExtensionUnavailable", err)
	}
}

// responseTypes lists every method with a request that is valid on an empty
// store and the type its response decodes into.
func responseTypes() map[string]struct {
	params   string
	response any
} {
	ranged := `{` + testRange + `}`
	session := `{"sessionId":"` + testSession + `"}`
	return map[string]struct {
		params   string
		response any
	}{
		usage.MethodStatus:       {``, &usage.Status{}},
		usage.MethodOverview:     {ranged, &usage.Overview{}},
		usage.MethodSessions:     {ranged, &usage.SessionPage{}},
		usage.MethodSession:      {session, &usage.SessionDetail{}},
		usage.MethodTimeline:     {session, &usage.TimelinePage{}},
		usage.MethodTools:        {ranged, &usage.Tools{}},
		usage.MethodProjects:     {ranged, &usage.Projects{}},
		usage.MethodInsights:     {ranged, &usage.InsightReport{}},
		usage.MethodExtensions:   {ranged, &usage.ExtOverview{}},
		usage.MethodExtEvents:    {``, &usage.ExtPage{}},
		usage.MethodSetExcluded:  {`{"project":"beta","excluded":true}`, &usage.Status{}},
		usage.MethodSetWindow:    {`{"days":30}`, &usage.Status{}},
		usage.MethodRescan:       {``, &usage.Status{}},
		usage.MethodSetExtension: {`{"enabled":true}`, &usage.Status{}},
		usage.MethodClearData:    {``, &usage.Status{}},
	}
}

// The fake store answers with nil lists throughout; no response may encode
// one as null.
func TestHandleNeverEncodesNullLists(t *testing.T) {
	methods := responseTypes()
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	// clearData runs last: it withdraws consent, which session and timeline need.
	slices.SortFunc(names, func(a, b string) int {
		if (a == usage.MethodClearData) != (b == usage.MethodClearData) {
			if a == usage.MethodClearData {
				return 1
			}
			return -1
		}
		return strings.Compare(a, b)
	})
	h := settled(t, func(cfg *Config) { cfg.Extension = &fakeExtension{} })
	if len(names) != len(h.svc.methods) {
		t.Fatalf("the test covers %d methods; the service serves %d", len(names), len(h.svc.methods))
	}
	for _, name := range names {
		method := methods[name]
		raw, err := h.svc.Handle(context.Background(), name, json.RawMessage(method.params))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err = json.Unmarshal(raw, method.response); err != nil {
			t.Fatalf("%s response %s: %v", name, raw, err)
		}
		// A list that was encoded as null decodes back into a nil slice.
		assertNoNilSlice(t, name, reflect.ValueOf(method.response).Elem())
	}
}

func assertNoNilSlice(t *testing.T, where string, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			assertNoNilSlice(t, where, v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			assertNoNilSlice(t, where+"."+v.Type().Field(i).Name, v.Field(i))
		}
	case reflect.Slice:
		if v.IsNil() {
			t.Errorf("%s was encoded as null", where)
		}
		for i := range v.Len() {
			assertNoNilSlice(t, where, v.Index(i))
		}
	}
}
