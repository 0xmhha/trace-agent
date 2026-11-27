package db

import (
	"context"
	"errors"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func TestProjectsWorld(t *testing.T) {
	alpha := usage.ProjectUsage{
		Key: "alpha", Path: "/work/alpha", Sessions: 1, Prompts: 2,
		Tokens: usage.TokenTotals{Input: 150, Output: 80, CacheRead: 1300, CacheCreate: 260}, LastAt: aggAt(2, 10, 20, 0),
	}
	beta := usage.ProjectUsage{
		Key: "beta", Path: "/work/beta", Sessions: 2, Prompts: 2,
		Tokens: usage.TokenTotals{Input: 1, Output: 2, CacheRead: 3, CacheCreate: 4}, LastAt: aggAt(5, 23, 30, 0),
	}
	// delta is idle in the range and gamma is excluded: both are listed with
	// zero counts, after the active projects and in key order.
	delta := usage.ProjectUsage{Key: "delta", Path: "/work/delta"}
	gamma := usage.ProjectUsage{Key: "gamma", Path: "/work/gamma", Excluded: true}
	tests := []struct {
		name string
		q    usage.Query
		want []usage.ProjectUsage
	}{
		{"all projects", aggWeek, []usage.ProjectUsage{alpha, beta, delta, gamma}},
		{"one project", usage.Query{Range: aggWeek.Range, Project: "beta"}, []usage.ProjectUsage{beta}},
		{"excluded project", usage.Query{Range: aggWeek.Range, Project: "gamma"}, []usage.ProjectUsage{gamma}},
		{"unknown project", usage.Query{Range: aggWeek.Range, Project: "missing"}, []usage.ProjectUsage{}},
		{
			"range after the week",
			usage.Query{Range: usage.Range{From: aggAt(9, 0, 0, 0), Until: aggAt(10, 0, 0, 0)}},
			[]usage.ProjectUsage{
				{Key: "delta", Path: "/work/delta", Sessions: 1, Prompts: 1, Tokens: usage.TokenTotals{Input: 6000}, LastAt: aggAt(9, 8, 1, 0)},
				{Key: "alpha", Path: "/work/alpha"},
				{Key: "beta", Path: "/work/beta"},
				gamma,
			},
		},
	}
	s := openTest(t)
	aggSeedWorld(t, s)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Projects(context.Background(), tc.q)
			if err != nil {
				t.Fatal(err)
			}
			aggEqualJSON(t, "projects", got, usage.Projects{Items: tc.want})
		})
	}
}

func TestProjectsEmptyStore(t *testing.T) {
	got, err := openTest(t).Projects(context.Background(), aggWeek)
	if err != nil {
		t.Fatal(err)
	}
	aggEqualJSON(t, "empty projects", got, usage.Projects{Items: []usage.ProjectUsage{}})
}

func TestProjectsErrors(t *testing.T) {
	s := openTest(t)
	tests := []struct {
		name string
		r    usage.Range
	}{
		{"empty range", usage.Range{From: aggFrom, Until: aggFrom}},
		{"reversed range", usage.Range{From: aggUntil, Until: aggFrom}},
		{"zero range", usage.Range{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Projects(context.Background(), usage.Query{Range: tc.r}); !errors.Is(err, usage.ErrBadRequest) {
				t.Fatalf("error = %v; want usage.ErrBadRequest", err)
			}
		})
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Projects(context.Background(), aggWeek); err == nil || errors.Is(err, usage.ErrBadRequest) {
		t.Fatalf("error = %v; want a store failure", err)
	}
}
