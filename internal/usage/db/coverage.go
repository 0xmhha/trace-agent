package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Named parameters shared by the aggregate queries.
const (
	aggArgFrom    = "from"
	aggArgUntil   = "until"
	aggArgProject = "project"
	aggArgLimit   = "limit"
)

// aggMaxRows caps every grouped list of an aggregate response, so that a
// store with thousands of distinct labels cannot produce an unbounded page.
const aggMaxRows = usage.MaxPageSize

// aggScope is a validated aggregate query: the half-open range in stored
// milliseconds and the optional project filter.
type aggScope struct {
	fromMS  int64
	untilMS int64
	project string
}

// aggNewScope validates q. A range that does not start before it ends is
// usage.ErrBadRequest.
func aggNewScope(q usage.Query) (aggScope, error) {
	if !q.Range.From.Before(q.Range.Until) {
		return aggScope{}, fmt.Errorf("range must start before it ends: %w", usage.ErrBadRequest)
	}
	return aggScope{fromMS: toMS(q.Range.From), untilMS: toMS(q.Range.Until), project: q.Project}, nil
}

// args returns the scope parameters followed by extra. Every query receives
// the same three scope arguments, so a condition can never be bound in the
// wrong order.
func (sc aggScope) args(extra ...sql.NamedArg) []any {
	args := make([]any, 0, 3+len(extra))
	args = append(args, sql.Named(aggArgFrom, sc.fromMS), sql.Named(aggArgUntil, sc.untilMS), sql.Named(aggArgProject, sc.project))
	for _, arg := range extra {
		args = append(args, arg)
	}
	return args
}

// aggInProject is the SQL condition that keeps the rows of the table aliased
// as alias that belong to the scope's project, or every row without a filter.
func aggInProject(alias string) string {
	return "(:" + aggArgProject + " = '' OR " + alias + ".project_key = :" + aggArgProject + ")"
}

// aggInRange is the SQL condition that keeps the rows of the table aliased as
// alias that fall in the scope: at_ms in [from, until) and the project.
func aggInRange(alias string) string {
	return alias + ".at_ms >= :" + aggArgFrom + " AND " + alias + ".at_ms < :" + aggArgUntil + " AND " + aggInProject(alias)
}

// aggActiveSessions selects the IDs of the sessions in range as defined in
// db.go: a model call or a prompt of any agent falls in the scope.
func aggActiveSessions() string {
	return "SELECT m.session_id FROM model_call m WHERE " + aggInRange("m") +
		" UNION SELECT p.session_id FROM prompt p WHERE " + aggInRange("p")
}

// aggQuery runs a read query directly on the pool, never inside a
// transaction (see dsnOptions), and hands every row to scan.
func (s *Store) aggQuery(ctx context.Context, query string, args []any, scan func(*sql.Rows) error) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err = scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// coverageWindowSlack is how far before the collection window a range may
// start without counting as outside it. A page builds "the last N days" from
// its own clock some time before the store reads its clock, so with a window
// of the same N days the range would otherwise always start a moment early.
const coverageWindowSlack = 24 * time.Hour

// Coverage reports how completely the stored data covers the query. State is
// usage.CoveragePartial when a tracked file of the queried projects is
// unreadable, lines were skipped, or the range starts before the collection
// window (now minus the configured window days); otherwise it is
// usage.CoverageComplete. The service overlays the scanning and disabled
// states.
//
// A zero q.Range asks about the collection window as a whole and never
// reports usage.ReasonOutsideWindow; any other range must start before it
// ends, else the error is usage.ErrBadRequest. Reasons is never nil.
func (s *Store) Coverage(ctx context.Context, q usage.Query) (usage.Coverage, error) {
	wholeWindow := q.Range.From.IsZero() && q.Range.Until.IsZero()
	if !wholeWindow {
		if _, err := aggNewScope(q); err != nil {
			return usage.Coverage{}, err
		}
	}
	window, err := s.windowFrom(ctx)
	if err != nil {
		return usage.Coverage{}, fmt.Errorf("query usage coverage: %w", err)
	}
	out := usage.Coverage{State: usage.CoverageComplete, WindowFrom: window.UTC(), Reasons: []string{}}
	var lastScanMS int64
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(f.unreadable), 0), COALESCE(SUM(f.skipped), 0), COALESCE(MAX(f.scanned_ms), 0)
		 FROM file f WHERE `+aggInProject("f"),
		sql.Named(aggArgProject, q.Project)).Scan(&out.FilesTracked, &out.FilesUnreadable, &out.LinesSkipped, &lastScanMS)
	if err != nil {
		return usage.Coverage{}, fmt.Errorf("query usage coverage: %w", err)
	}
	out.LastScanAt = fromMS(lastScanMS)
	// The order of the reasons is part of the response: files, lines, window.
	if out.FilesUnreadable > 0 {
		out.Reasons = append(out.Reasons, usage.ReasonUnreadableFiles)
	}
	if out.LinesSkipped > 0 {
		out.Reasons = append(out.Reasons, usage.ReasonSkippedLines)
	}
	if !wholeWindow && q.Range.From.Before(window.Add(-coverageWindowSlack)) {
		out.Reasons = append(out.Reasons, usage.ReasonOutsideWindow)
	}
	if len(out.Reasons) > 0 {
		out.State = usage.CoveragePartial
	}
	return out, nil
}
