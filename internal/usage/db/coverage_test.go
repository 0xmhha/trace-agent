package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// coverageSeedFile tracks one transcript file with the given number of
// skipped lines.
func coverageSeedFile(t *testing.T, s *Store, path, project string, skipped int) {
	t.Helper()
	seedBatch(t, s, usage.FileRef{Path: path, ProjectKey: project, Size: 10, ModTime: testNow},
		usage.Batch{Skipped: skipped, NextOffset: 10})
}

// coverageMarkUnreadable sets the flag the ingest side stores when a scan
// cannot read a file.
func coverageMarkUnreadable(t *testing.T, s *Store, path string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), "UPDATE file SET unreadable = 1 WHERE path = ?", path); err != nil {
		t.Fatalf("mark unreadable: %v", err)
	}
}

func TestCoverageReasons(t *testing.T) {
	defaultWindow := testNow.AddDate(0, 0, -usage.DefaultWindowDays)
	inWindow := usage.Range{From: testNow.AddDate(0, 0, -7), Until: testNow}
	tests := []struct {
		name  string
		seed  func(t *testing.T, s *Store)
		query usage.Query
		want  usage.Coverage
	}{
		{
			name:  "empty store is complete",
			seed:  func(*testing.T, *Store) {},
			query: usage.Query{Range: inWindow},
			want:  usage.Coverage{State: usage.CoverageComplete, WindowFrom: defaultWindow, Reasons: []string{}},
		},
		{
			name:  "clean files are complete",
			seed:  func(t *testing.T, s *Store) { coverageSeedFile(t, s, "alpha/a.jsonl", "alpha", 0) },
			query: usage.Query{Range: inWindow},
			want:  usage.Coverage{State: usage.CoverageComplete, FilesTracked: 1, WindowFrom: defaultWindow, LastScanAt: testNow, Reasons: []string{}},
		},
		{
			name: "unreadable file",
			seed: func(t *testing.T, s *Store) {
				coverageSeedFile(t, s, "alpha/a.jsonl", "alpha", 0)
				coverageSeedFile(t, s, "alpha/b.jsonl", "alpha", 0)
				coverageMarkUnreadable(t, s, "alpha/b.jsonl")
			},
			query: usage.Query{Range: inWindow},
			want: usage.Coverage{
				State: usage.CoveragePartial, FilesTracked: 2, FilesUnreadable: 1, WindowFrom: defaultWindow, LastScanAt: testNow,
				Reasons: []string{usage.ReasonUnreadableFiles},
			},
		},
		{
			name: "skipped lines",
			seed: func(t *testing.T, s *Store) {
				coverageSeedFile(t, s, "alpha/a.jsonl", "alpha", 3)
				coverageSeedFile(t, s, "beta/b.jsonl", "beta", 4)
			},
			query: usage.Query{Range: inWindow},
			want: usage.Coverage{
				State: usage.CoveragePartial, FilesTracked: 2, LinesSkipped: 7, WindowFrom: defaultWindow, LastScanAt: testNow,
				Reasons: []string{usage.ReasonSkippedLines},
			},
		},
		{
			name:  "range starts before the window",
			seed:  func(t *testing.T, s *Store) { coverageSeedFile(t, s, "alpha/a.jsonl", "alpha", 0) },
			query: usage.Query{Range: usage.Range{From: defaultWindow.Add(-time.Millisecond), Until: testNow}},
			want: usage.Coverage{
				State: usage.CoveragePartial, FilesTracked: 1, WindowFrom: defaultWindow, LastScanAt: testNow,
				Reasons: []string{usage.ReasonOutsideWindow},
			},
		},
		{
			name:  "range starting on the window edge is inside",
			seed:  func(*testing.T, *Store) {},
			query: usage.Query{Range: usage.Range{From: defaultWindow, Until: testNow}},
			want:  usage.Coverage{State: usage.CoverageComplete, WindowFrom: defaultWindow, Reasons: []string{}},
		},
		{
			name: "all reasons in their fixed order",
			seed: func(t *testing.T, s *Store) {
				coverageSeedFile(t, s, "alpha/a.jsonl", "alpha", 2)
				coverageMarkUnreadable(t, s, "alpha/a.jsonl")
			},
			query: usage.Query{Range: usage.Range{From: testNow.AddDate(-1, 0, 0), Until: testNow}},
			want: usage.Coverage{
				State: usage.CoveragePartial, FilesTracked: 1, FilesUnreadable: 1, LinesSkipped: 2, WindowFrom: defaultWindow, LastScanAt: testNow,
				Reasons: []string{usage.ReasonUnreadableFiles, usage.ReasonSkippedLines, usage.ReasonOutsideWindow},
			},
		},
		{
			name:  "configured window moves the edge",
			seed:  func(t *testing.T, s *Store) { seedSetting(t, s, settingWindowDays, "3") },
			query: usage.Query{Range: inWindow},
			want: usage.Coverage{
				State: usage.CoveragePartial, WindowFrom: testNow.AddDate(0, 0, -3),
				Reasons: []string{usage.ReasonOutsideWindow},
			},
		},
		{
			name: "project filter keeps other projects out",
			seed: func(t *testing.T, s *Store) {
				coverageSeedFile(t, s, "alpha/a.jsonl", "alpha", 0)
				coverageMarkUnreadable(t, s, "alpha/a.jsonl")
				coverageSeedFile(t, s, "beta/b.jsonl", "beta", 5)
				coverageSeedFile(t, s, "beta/c.jsonl", "beta", 0)
			},
			query: usage.Query{Range: inWindow, Project: "beta"},
			want: usage.Coverage{
				State: usage.CoveragePartial, FilesTracked: 2, LinesSkipped: 5, WindowFrom: defaultWindow, LastScanAt: testNow,
				Reasons: []string{usage.ReasonSkippedLines},
			},
		},
		{
			name: "project without problems is complete",
			seed: func(t *testing.T, s *Store) {
				coverageSeedFile(t, s, "alpha/a.jsonl", "alpha", 9)
				coverageSeedFile(t, s, "beta/b.jsonl", "beta", 0)
			},
			query: usage.Query{Range: inWindow, Project: "beta"},
			want:  usage.Coverage{State: usage.CoverageComplete, FilesTracked: 1, WindowFrom: defaultWindow, LastScanAt: testNow, Reasons: []string{}},
		},
		{
			name:  "zero range asks about the whole window",
			seed:  func(t *testing.T, s *Store) { coverageSeedFile(t, s, "alpha/a.jsonl", "alpha", 0) },
			query: usage.Query{},
			want:  usage.Coverage{State: usage.CoverageComplete, FilesTracked: 1, WindowFrom: defaultWindow, LastScanAt: testNow, Reasons: []string{}},
		},
		{
			name:  "zero range still reports unread data",
			seed:  func(t *testing.T, s *Store) { coverageSeedFile(t, s, "alpha/a.jsonl", "alpha", 1) },
			query: usage.Query{},
			want: usage.Coverage{
				State: usage.CoveragePartial, FilesTracked: 1, LinesSkipped: 1, WindowFrom: defaultWindow, LastScanAt: testNow,
				Reasons: []string{usage.ReasonSkippedLines},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			tc.seed(t, s)
			got, err := s.Coverage(context.Background(), tc.query)
			if err != nil {
				t.Fatal(err)
			}
			aggEqualJSON(t, "coverage", got, tc.want)
		})
	}
}

func TestCoverageErrors(t *testing.T) {
	s := openTest(t)
	tests := []struct {
		name string
		r    usage.Range
	}{
		{"empty range", usage.Range{From: testNow, Until: testNow}},
		{"reversed range", usage.Range{From: testNow, Until: testNow.Add(-time.Hour)}},
		{"only the end is set", usage.Range{Until: testNow.AddDate(-3000, 0, 0)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Coverage(context.Background(), usage.Query{Range: tc.r}); !errors.Is(err, usage.ErrBadRequest) {
				t.Fatalf("error = %v; want usage.ErrBadRequest", err)
			}
		})
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := s.Coverage(context.Background(), usage.Query{Range: usage.Range{From: testNow.Add(-time.Hour), Until: testNow}})
	if err == nil || errors.Is(err, usage.ErrBadRequest) {
		t.Fatalf("error = %v; want a store failure", err)
	}
}
