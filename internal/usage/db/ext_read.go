package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// extReadCursorVersion is bumped when the cursor layout changes.
const extReadCursorVersion = 1

// extReadSourcesSQL reports one row per sender with events in [?1, ?2). The
// rejection counter is cumulative and does not follow the range.
const extReadSourcesSQL = `
SELECT e.source, COUNT(*),
       SUM(CASE WHEN e.status = ?3 THEN 1 ELSE 0 END),
       SUM(CASE WHEN e.session_id <> '' THEN 1 ELSE 0 END),
       MAX(e.at_ms),
       COALESCE((SELECT j.n FROM ext_reject j WHERE j.source = e.source), 0)
FROM ext_event e WHERE e.at_ms >= ?1 AND e.at_ms < ?2
GROUP BY e.source ORDER BY COUNT(*) DESC, e.source`

const extReadRejectedSQL = `SELECT COALESCE(SUM(n), 0) FROM ext_reject`

const extReadColumns = `id, source, kind, name, at_ms, received_ms, duration_ms, status, level,
       session_id, tool_use_id, trace_id, span_id, parent_span_id, message, attributes, value, unit`

// The list is newest first; events of the same millisecond are ordered by ID
// so that paging is stable.
const (
	extReadOrder     = " ORDER BY at_ms DESC, id LIMIT ?"
	extReadAfterCond = "(at_ms < ? OR (at_ms = ? AND id > ?))"
)

// extReadCursor is the JSON form of the last event of a page.
type extReadCursor struct {
	Version int    `json:"v"`
	AtMS    int64  `json:"t"`
	ID      string `json:"i"`
}

// extReadFilter collects the conditions of an event query and their
// arguments. Only fixed fragments reach the SQL text.
type extReadFilter struct {
	conds []string
	args  []any
}

func (f *extReadFilter) add(cond string, args ...any) {
	f.conds = append(f.conds, cond)
	f.args = append(f.args, args...)
}

func (f *extReadFilter) where() string {
	if len(f.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(f.conds, " AND ")
}

// extReadFilterOf translates the query. A zero range bound and an empty
// field do not restrict the result.
func extReadFilterOf(q usage.ExtQuery) extReadFilter {
	var f extReadFilter
	if !q.Range.From.IsZero() {
		f.add("at_ms >= ?", toMS(q.Range.From))
	}
	if !q.Range.Until.IsZero() {
		f.add("at_ms < ?", toMS(q.Range.Until))
	}
	if q.Source != "" {
		f.add("source = ?", q.Source)
	}
	if q.SessionID != "" {
		f.add("session_id = ?", q.SessionID)
	}
	if q.ToolUseID != "" {
		f.add("tool_use_id = ?", q.ToolUseID)
	}
	if q.Level != "" {
		f.add("level = ?", string(q.Level))
	}
	return f
}

func extReadScanSource(rows *sql.Rows) (usage.ExtSourceRow, error) {
	var (
		r      usage.ExtSourceRow
		lastMS int64
	)
	if err := rows.Scan(&r.Source, &r.Events, &r.Errors, &r.Linked, &lastMS, &r.Rejected); err != nil {
		return usage.ExtSourceRow{}, err
	}
	r.Unlinked = r.Events - r.Linked
	r.LastAt = fromMS(lastMS)
	return r, nil
}

func extReadScanEvent(rows *sql.Rows) (usage.ExtEvent, error) {
	var (
		e                   usage.ExtEvent
		atMS, receivedMS    int64
		kind, status, level string
		attributes          string
		duration, value     sql.NullFloat64
	)
	err := rows.Scan(&e.ID, &e.Source, &kind, &e.Name, &atMS, &receivedMS, &duration, &status, &level,
		&e.SessionID, &e.ToolUseID, &e.TraceID, &e.SpanID, &e.ParentSpanID, &e.Message, &attributes, &value, &e.Unit)
	if err != nil {
		return usage.ExtEvent{}, err
	}
	e.Kind, e.Status, e.Level = usage.ExtKind(kind), usage.ExtStatus(status), usage.ExtLevel(level)
	e.At, e.ReceivedAt = fromMS(atMS), fromMS(receivedMS)
	e.DurationMs, e.Value = sessNullable(duration), sessNullable(value)
	if attributes != "" {
		// A row whose attributes cannot be decoded is reported, not shown
		// without them: a silently emptied field would look like "none".
		if err = json.Unmarshal([]byte(attributes), &e.Attributes); err != nil {
			return usage.ExtEvent{}, fmt.Errorf("decode attributes of event %q: %w", e.ID, err)
		}
	}
	if len(e.Attributes) == 0 {
		e.Attributes = nil
	}
	return e, nil
}

// ExtSources aggregates extension events of the query range by sender, one
// row per sender with at least one event in the range. rejected is the
// cumulative number of refused inputs across all senders, including those
// that could not be attributed.
func (s *Store) ExtSources(ctx context.Context, q usage.Query) (sources []usage.ExtSourceRow, rejected int64, err error) {
	// Events are not owned by a project: an event may carry no session at
	// all, so q.Project does not narrow the result.
	sources, err = sessCollect(ctx, s.db, "read extension sources", extReadScanSource, extReadSourcesSQL,
		toMS(q.Range.From), toMS(q.Range.Until), string(usage.ExtError))
	if err != nil {
		return nil, 0, err
	}
	if err = s.db.QueryRowContext(ctx, extReadRejectedSQL).Scan(&rejected); err != nil {
		return nil, 0, fmt.Errorf("read extension rejections: %w", err)
	}
	return sources, rejected, nil
}

// ExtEvents returns one page of extension events, newest first. A zero bound
// of q.Range is unbounded on that side.
func (s *Store) ExtEvents(ctx context.Context, q usage.ExtQuery) (usage.ExtPage, error) {
	limit, err := sessPageLimit(q.Limit)
	if err != nil {
		return usage.ExtPage{}, err
	}
	if r := q.Range; !r.From.IsZero() && !r.Until.IsZero() && !r.Until.After(r.From) {
		return usage.ExtPage{}, fmt.Errorf("extension event range is empty: %w", usage.ErrBadRequest)
	}
	filter := extReadFilterOf(q)
	var total int
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ext_event"+filter.where(), filter.args...).Scan(&total); err != nil {
		return usage.ExtPage{}, fmt.Errorf("count extension events: %w", err)
	}
	if q.Cursor != "" {
		var c extReadCursor
		if err = sessCursorDecode(q.Cursor, &c); err != nil {
			return usage.ExtPage{}, err
		}
		if c.Version != extReadCursorVersion || c.ID == "" {
			return usage.ExtPage{}, fmt.Errorf("cursor does not match the request: %w", usage.ErrBadRequest)
		}
		filter.add(extReadAfterCond, c.AtMS, c.AtMS, c.ID)
	}
	// One row beyond the page tells whether another page follows.
	filter.args = append(filter.args, limit+1)
	items, err := sessCollect(ctx, s.db, "read extension events", extReadScanEvent,
		"SELECT "+extReadColumns+" FROM ext_event"+filter.where()+extReadOrder, filter.args...)
	if err != nil {
		return usage.ExtPage{}, err
	}
	page := usage.ExtPage{Items: items, Total: total}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor, err = sessCursorEncode(extReadCursor{Version: extReadCursorVersion, AtMS: toMS(last.At), ID: last.ID})
		if err != nil {
			return usage.ExtPage{}, err
		}
	}
	return page, nil
}
