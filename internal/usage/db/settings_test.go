package db

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// settingRead returns the stored settings and fails the test on error.
func settingRead(t *testing.T, s *Store) usage.Settings {
	t.Helper()
	got, err := s.Settings(context.Background())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	return got
}

// settingEqual compares two settings values field by field.
func settingEqual(a, b usage.Settings) bool {
	return a.Consent == b.Consent && a.WindowDays == b.WindowDays && a.ExtEnabled == b.ExtEnabled &&
		a.ExtPort == b.ExtPort && slices.Equal(a.Excluded, b.Excluded)
}

func TestSettingsDefaults(t *testing.T) {
	s := openTest(t)
	got := settingRead(t, s)
	want := usage.Settings{WindowDays: usage.DefaultWindowDays, Excluded: []string{}}
	if !settingEqual(got, want) {
		t.Fatalf("Settings of a fresh store = %+v; want %+v", got, want)
	}
	if got.Excluded == nil {
		t.Fatal("Excluded of a fresh store is nil; want an empty list")
	}
	if n, err := s.DataVersion(context.Background()); err != nil || n != 0 {
		t.Fatalf("DataVersion of a fresh store = %d, %v; want 0", n, err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	for _, step := range []struct {
		name  string
		set   func() error
		want  usage.Settings
		bumps bool
	}{
		{name: "consent already off", set: func() error { return s.SetConsent(ctx, false) },
			want: usage.Settings{WindowDays: usage.DefaultWindowDays}},
		{name: "grant consent", set: func() error { return s.SetConsent(ctx, true) },
			want: usage.Settings{Consent: true, WindowDays: usage.DefaultWindowDays}, bumps: true},
		{name: "grant consent again", set: func() error { return s.SetConsent(ctx, true) },
			want: usage.Settings{Consent: true, WindowDays: usage.DefaultWindowDays}},
		{name: "window already default", set: func() error { return s.SetWindowDays(ctx, usage.DefaultWindowDays) },
			want: usage.Settings{Consent: true, WindowDays: usage.DefaultWindowDays}},
		{name: "narrowest window", set: func() error { return s.SetWindowDays(ctx, 1) },
			want: usage.Settings{Consent: true, WindowDays: 1}, bumps: true},
		{name: "widest window", set: func() error { return s.SetWindowDays(ctx, usage.MaxWindowDays) },
			want: usage.Settings{Consent: true, WindowDays: usage.MaxWindowDays}, bumps: true},
		{name: "same window again", set: func() error { return s.SetWindowDays(ctx, usage.MaxWindowDays) },
			want: usage.Settings{Consent: true, WindowDays: usage.MaxWindowDays}},
		{name: "endpoint already off", set: func() error { return s.SetExtension(ctx, false, 0) },
			want: usage.Settings{Consent: true, WindowDays: usage.MaxWindowDays}},
		{name: "endpoint on", set: func() error { return s.SetExtension(ctx, true, 4100) },
			want: usage.Settings{Consent: true, WindowDays: usage.MaxWindowDays, ExtEnabled: true, ExtPort: 4100}, bumps: true},
		{name: "endpoint unchanged", set: func() error { return s.SetExtension(ctx, true, 4100) },
			want: usage.Settings{Consent: true, WindowDays: usage.MaxWindowDays, ExtEnabled: true, ExtPort: 4100}},
		{name: "endpoint off keeps the last port", set: func() error { return s.SetExtension(ctx, false, 4100) },
			want: usage.Settings{Consent: true, WindowDays: usage.MaxWindowDays, ExtPort: 4100}, bumps: true},
		{name: "highest port", set: func() error { return s.SetExtension(ctx, true, settingMaxPort) },
			want: usage.Settings{Consent: true, WindowDays: usage.MaxWindowDays, ExtEnabled: true, ExtPort: settingMaxPort}, bumps: true},
		{name: "withdraw consent", set: func() error { return s.SetConsent(ctx, false) },
			want: usage.Settings{WindowDays: usage.MaxWindowDays, ExtEnabled: true, ExtPort: settingMaxPort}, bumps: true},
	} {
		version := ingestVersion(t, s)
		if err := step.set(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		step.want.Excluded = []string{}
		if got := settingRead(t, s); !settingEqual(got, step.want) {
			t.Fatalf("%s: Settings = %+v; want %+v", step.name, got, step.want)
		}
		want := version
		if step.bumps {
			want++
		}
		if got := ingestVersion(t, s); got != want {
			t.Fatalf("%s: data_version = %d; want %d", step.name, got, want)
		}
	}
}

func TestSettingsRejectBadValues(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		set  func(s *Store) error
	}{
		{name: "zero days", set: func(s *Store) error { return s.SetWindowDays(ctx, 0) }},
		{name: "negative days", set: func(s *Store) error { return s.SetWindowDays(ctx, -3) }},
		{name: "days above the limit", set: func(s *Store) error { return s.SetWindowDays(ctx, usage.MaxWindowDays+1) }},
		{name: "negative port", set: func(s *Store) error { return s.SetExtension(ctx, true, -1) }},
		{name: "port above the range", set: func(s *Store) error { return s.SetExtension(ctx, true, settingMaxPort+1) }},
		{name: "empty project", set: func(s *Store) error { return s.SetExcluded(ctx, "", true) }},
		{name: "empty project re-included", set: func(s *Store) error { return s.SetExcluded(ctx, "", false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			if err := tc.set(s); !errors.Is(err, usage.ErrBadRequest) {
				t.Fatalf("setter = %v; want usage.ErrBadRequest", err)
			}
			want := usage.Settings{WindowDays: usage.DefaultWindowDays, Excluded: []string{}}
			if got := settingRead(t, s); !settingEqual(got, want) {
				t.Fatalf("Settings after a refused value = %+v; want the defaults", got)
			}
			if got := ingestVersion(t, s); got != 0 {
				t.Fatalf("data_version = %d after a refused value; want 0", got)
			}
		})
	}
}

func TestSettingsIgnoreUnreadableValues(t *testing.T) {
	for _, tc := range []struct {
		name, key, value string
		want             usage.Settings
	}{
		{name: "window not a number", key: settingWindowDays, value: "many", want: usage.Settings{WindowDays: usage.DefaultWindowDays}},
		{name: "window below the range", key: settingWindowDays, value: "0", want: usage.Settings{WindowDays: usage.DefaultWindowDays}},
		{name: "window above the range", key: settingWindowDays, value: strconv.Itoa(usage.MaxWindowDays + 1), want: usage.Settings{WindowDays: usage.DefaultWindowDays}},
		{name: "port not a number", key: settingExtPort, value: "http", want: usage.Settings{WindowDays: usage.DefaultWindowDays}},
		{name: "port above the range", key: settingExtPort, value: strconv.Itoa(settingMaxPort + 1), want: usage.Settings{WindowDays: usage.DefaultWindowDays}},
		{name: "consent not a flag", key: settingConsent, value: "yes", want: usage.Settings{WindowDays: usage.DefaultWindowDays}},
		{name: "stored window", key: settingWindowDays, value: "7", want: usage.Settings{WindowDays: 7}},
		{name: "stored flag", key: settingExtEnabled, value: settingOn, want: usage.Settings{WindowDays: usage.DefaultWindowDays, ExtEnabled: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			seedSetting(t, s, tc.key, tc.value)
			tc.want.Excluded = []string{}
			if got := settingRead(t, s); !settingEqual(got, tc.want) {
				t.Fatalf("Settings = %+v; want %+v", got, tc.want)
			}
		})
	}
}

func TestSettingsRepairUnreadableValue(t *testing.T) {
	s := openTest(t)
	seedSetting(t, s, settingWindowDays, "many")
	if err := s.SetWindowDays(context.Background(), usage.DefaultWindowDays); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRowContext(context.Background(), "SELECT value FROM setting WHERE key = ?", settingWindowDays).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != strconv.Itoa(usage.DefaultWindowDays) {
		t.Fatalf("stored window = %q; want the unreadable value replaced", stored)
	}
}

func TestSettingsHonourContext(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Settings(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Settings = %v; want context.Canceled", err)
	}
	if _, err := s.DataVersion(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("DataVersion = %v; want context.Canceled", err)
	}
	if err := s.SetConsent(ctx, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("SetConsent = %v; want context.Canceled", err)
	}
	if got := settingRead(t, s); got.Consent {
		t.Fatal("a cancelled SetConsent stored consent")
	}
}

func TestSetExcludedListsSorted(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	for _, step := range []struct {
		name     string
		project  string
		excluded bool
		want     []string
		bumps    bool
	}{
		{name: "unknown project re-included", project: "proj-z", excluded: false, want: []string{}},
		{name: "exclude creates the row", project: "proj-z", excluded: true, want: []string{"proj-z"}, bumps: true},
		{name: "exclude again", project: "proj-z", excluded: true, want: []string{"proj-z"}},
		{name: "second project", project: "proj-a", excluded: true, want: []string{"proj-a", "proj-z"}, bumps: true},
		{name: "third project", project: "proj-m", excluded: true, want: []string{"proj-a", "proj-m", "proj-z"}, bumps: true},
		{name: "re-include", project: "proj-a", excluded: false, want: []string{"proj-m", "proj-z"}, bumps: true},
		{name: "re-include again", project: "proj-a", excluded: false, want: []string{"proj-m", "proj-z"}},
	} {
		version := ingestVersion(t, s)
		if err := s.SetExcluded(ctx, step.project, step.excluded); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := settingRead(t, s).Excluded; !slices.Equal(got, step.want) {
			t.Fatalf("%s: Excluded = %v; want %v", step.name, got, step.want)
		}
		want := version
		if step.bumps {
			want++
		}
		if got := ingestVersion(t, s); got != want {
			t.Fatalf("%s: data_version = %d; want %d", step.name, got, want)
		}
	}
	// Re-including a project that was never stored must not invent a row.
	if got := ingestCount(t, s, "project", "project_key = 'proj-unknown'"); got != 0 {
		t.Fatalf("project rows for a never-seen project = %d; want 0", got)
	}
}

func TestSetExcludedDeletesProjectRows(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	ingestFill(t, s)
	// A second project and an event linked to it must survive.
	other := ingestRef(ingestOtherFile, ingestProjectB, 200)
	ingestApply(t, s, other, usage.Batch{BaseOffset: 80, NextOffset: 200,
		Session:    usage.SessionPatch{SessionID: "sess-9", ProjectPath: "/work/proj-b", FirstAt: ingestStart, LastAt: ingestStart},
		ModelCalls: []usage.ModelCall{{Key: "msg-b", SessionID: "sess-9", At: ingestStart, Model: "model-a"}},
		Cost:       &usage.CostState{SessionID: "sess-9", At: ingestStart, TotalCostUSD: 1},
	})
	if err := s.AppendExt(ctx, []usage.ExtEvent{ingestExtEvent("ext-b", "sess-9", 3)}); err != nil {
		t.Fatal(err)
	}
	keep := map[string][]string{}
	for _, table := range ingestTableNames(t, s) {
		if table == "ext_event" || table == "ext_reject" || table == "project" || table == "excluded_session" {
			continue
		}
		if ingestCount(t, s, table, "project_key = ?", ingestProjectA) == 0 {
			t.Fatalf("table %s holds no rows of the project before exclusion", table)
		}
		keep[table] = settingProjectRows(t, s, table, ingestProjectB)
	}
	version := ingestVersion(t, s)

	if err := s.SetExcluded(ctx, ingestProjectA, true); err != nil {
		t.Fatal(err)
	}
	for table, want := range keep {
		if got := ingestCount(t, s, table, "project_key = ?", ingestProjectA); got != 0 {
			t.Errorf("%s rows of the excluded project = %d; want 0", table, got)
		}
		if got := settingProjectRows(t, s, table, ingestProjectB); !slices.Equal(got, want) {
			t.Errorf("%s rows of the other project changed\n got: %q\nwant: %q", table, got, want)
		}
	}
	// ext-1 was linked to a session of the excluded project. ext-2 has no
	// session and ext-b belongs to the other project.
	if got := ingestTableRows(t, s, "ext_event"); len(got) != 2 || ingestCount(t, s, "ext_event", "id IN ('ext-2','ext-b')") != 2 {
		t.Fatalf("extension events after exclusion = %q; want ext-2 and ext-b", got)
	}
	if got := ingestCount(t, s, "ext_reject", ""); got != 1 {
		t.Fatalf("rejection counters = %d; want 1 kept", got)
	}
	if got := ingestCount(t, s, "project", "project_key = ? AND excluded = 1", ingestProjectA); got != 1 {
		t.Fatalf("excluded project rows = %d; want 1", got)
	}
	if got := ingestVersion(t, s); got != version+1 {
		t.Fatalf("data_version = %d after exclusion; want %d", got, version+1)
	}
	if _, ok, err := s.Cursor(ctx, ingestRootFile); err != nil || ok {
		t.Fatalf("Cursor after exclusion = %v, %v; want no cursor", ok, err)
	}

	// A scan that was already running must not bring the rows back.
	for _, base := range []int64{0, 950} {
		batch := ingestRichBatch()
		batch.BaseOffset, batch.NextOffset = base, base+100
		if err := s.Apply(ctx, ingestRootRef(), batch); !errors.Is(err, usage.ErrStale) {
			t.Fatalf("Apply at %d for an excluded project = %v; want usage.ErrStale", base, err)
		}
	}
	if got := ingestCount(t, s, "model_call", "project_key = ?", ingestProjectA); got != 0 {
		t.Fatalf("model_call rows of the excluded project = %d after a stale Apply; want 0", got)
	}

	if err := s.SetExcluded(ctx, ingestProjectA, false); err != nil {
		t.Fatal(err)
	}
	// The cursors are gone, so only a read from the start is accepted.
	if err := s.Apply(ctx, ingestRootRef(), usage.Batch{BaseOffset: 950, NextOffset: 960}); !errors.Is(err, usage.ErrStale) {
		t.Fatalf("Apply at the old offset after re-inclusion = %v; want usage.ErrStale", err)
	}
	ingestApply(t, s, ingestRootRef(), ingestRichBatch())
	if got := ingestCount(t, s, "model_call", "project_key = ?", ingestProjectA); got != 2 {
		t.Fatalf("model_call rows after re-inclusion = %d; want 2", got)
	}
}

func TestSetExcludedRepairsLeftoverRows(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.SetExcluded(ctx, ingestProjectA, true); err != nil {
		t.Fatal(err)
	}
	// Rows written behind the API, as an interrupted older version might
	// have left them.
	seedBatch(t, s, ingestRootRef(), ingestRichBatch())
	version := ingestVersion(t, s)
	if err := s.SetExcluded(ctx, ingestProjectA, true); err != nil {
		t.Fatal(err)
	}
	if got := ingestCount(t, s, "model_call", ""); got != 0 {
		t.Fatalf("model_call rows = %d; want the leftover rows deleted", got)
	}
	if got := ingestVersion(t, s); got != version+1 {
		t.Fatalf("data_version = %d; want %d", got, version+1)
	}
	if got := settingRead(t, s).Excluded; !slices.Equal(got, []string{ingestProjectA}) {
		t.Fatalf("Excluded = %v; want the project still excluded", got)
	}
}

// settingProjectRows renders the rows of table that belong to project.
func settingProjectRows(t *testing.T, s *Store, table, project string) []string {
	t.Helper()
	out := []string{}
	for _, row := range ingestTableRows(t, s, table) {
		if strings.Contains(row, "project_key="+project+"|") {
			out = append(out, row)
		}
	}
	return out
}

// Excluding a project deletes its streams, so the store has to remember its
// sessions: an extension event that arrives afterwards for one of them must
// not be stored, and re-including the project lifts that.
func TestExcludedSessionStaysExcludedForLaterEvents(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	ingestFill(t, s)
	const session = "sess-1"
	if ingestCount(t, s, "stream", "session_id = ? AND project_key = ?", session, ingestProjectA) == 0 {
		t.Fatalf("fixture has no stream of %s in project %s", session, ingestProjectA)
	}
	if err := s.SetExcluded(ctx, ingestProjectA, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendExt(ctx, []usage.ExtEvent{ingestExtEvent("late-1", session, 1)}); err != nil {
		t.Fatal(err)
	}
	if got := ingestCount(t, s, "ext_event", "id = ?", "late-1"); got != 0 {
		t.Fatal("an event of an excluded session was stored after the exclusion")
	}
	if err := s.SetExcluded(ctx, ingestProjectA, false); err != nil {
		t.Fatal(err)
	}
	if got := ingestCount(t, s, "excluded_session", "project_key = ?", ingestProjectA); got != 0 {
		t.Fatalf("re-including left %d remembered sessions", got)
	}
	if err := s.AppendExt(ctx, []usage.ExtEvent{ingestExtEvent("late-2", session, 2)}); err != nil {
		t.Fatal(err)
	}
	if got := ingestCount(t, s, "ext_event", "id = ?", "late-2"); got != 1 {
		t.Fatal("an event of a re-included session was not stored")
	}
}
