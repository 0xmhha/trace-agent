package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Statements of the extension write methods.
const (
	// Only a repeated ID is passed over. INSERT OR IGNORE would also swallow
	// a row that breaks a CHECK constraint, and a refused event must never
	// disappear silently.
	extWriteInsertSQL = `INSERT INTO ext_event(id,source,kind,name,at_ms,received_ms,duration_ms,status,level,session_id,tool_use_id,trace_id,span_id,parent_span_id,message,attributes,value,unit)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`
	extWriteRejectSQL = `INSERT INTO ext_reject(source,n) VALUES(?,?) ON CONFLICT(source) DO UPDATE SET n = n + excluded.n`
	// extWriteExcludedSQL tells whether a session belongs to a stream of an
	// excluded project.
	extWriteExcludedSQL = `SELECT EXISTS(SELECT 1 FROM stream JOIN project USING(project_key)
		WHERE stream.session_id = ? AND project.excluded = 1)`
	// extWriteNoAttributes is the stored form of an event without attributes.
	extWriteNoAttributes = "{}"
)

// Operation names used as the prefix of returned errors.
const (
	extWriteOpAppend = "store extension events"
	extWriteOpReject = "count rejected extension input"
)

// extWriteNullable maps an unreported number to NULL, so that readers can
// tell "not reported" from zero.
func extWriteNullable(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// extWriteAttributes encodes attributes as a JSON object of strings. Keys
// are written in ascending order, so equal maps are stored as equal text.
func extWriteAttributes(attributes map[string]string) (string, error) {
	if len(attributes) == 0 {
		return extWriteNoAttributes, nil
	}
	encoded, err := json.Marshal(attributes)
	if err != nil {
		return "", fmt.Errorf("encode attributes: %w", err)
	}
	return string(encoded), nil
}

// extWriteArgs returns the statement arguments of one event.
func extWriteArgs(e usage.ExtEvent) ([]any, error) {
	if e.ID == "" {
		return nil, fmt.Errorf("event id is required: %w", usage.ErrBadRequest)
	}
	attributes, err := extWriteAttributes(e.Attributes)
	if err != nil {
		return nil, err
	}
	return []any{e.ID, e.Source, string(e.Kind), e.Name, toMS(e.At), toMS(e.ReceivedAt), extWriteNullable(e.DurationMs),
		string(e.Status), string(e.Level), e.SessionID, e.ToolUseID, e.TraceID, e.SpanID, e.ParentSpanID, e.Message,
		attributes, extWriteNullable(e.Value), e.Unit}, nil
}

// AppendExt stores extension events. An event whose ID is already stored is
// ignored. An event whose session ID belongs to a stream of an excluded
// project is silently not stored and not counted anywhere, so that excluding
// a project keeps its sessions out of the store. Events without a session ID
// or with one that no stored stream knows are stored as usual. When the call
// stored new events, the oldest events beyond usage.MaxExtEvents are dropped
// in the same transaction, so the cap holds between prunes as well.
func (s *Store) AppendExt(ctx context.Context, events []usage.ExtEvent) error {
	return s.appendExt(ctx, events, usage.MaxExtEvents)
}

// appendExt is AppendExt with the event cap as a parameter, so that a test
// can exercise the cap without storing usage.MaxExtEvents rows.
func (s *Store) appendExt(ctx context.Context, events []usage.ExtEvent, extCap int64) error {
	if len(events) == 0 {
		return nil
	}
	rows := make([]extWriteItem, 0, len(events))
	for _, e := range events {
		args, err := extWriteArgs(e)
		if err != nil {
			return fmt.Errorf("%s: %w", extWriteOpAppend, err)
		}
		rows = append(rows, extWriteItem{session: e.SessionID, args: args})
	}
	return s.ingestInTx(ctx, extWriteOpAppend, func(tx *sql.Tx) error {
		inserted, err := extWriteInsert(ctx, tx, rows)
		if err != nil || inserted == 0 {
			return err
		}
		if _, err := ingestCapExt(ctx, tx, extCap); err != nil {
			return err
		}
		return bump(ctx, tx)
	})
}

// extWriteItem is one event ready to insert, with the session it is linked to.
type extWriteItem struct {
	session string
	args    []any
}

// extWriteInsert inserts the rows whose session is not excluded and returns
// how many were new.
func extWriteInsert(ctx context.Context, tx *sql.Tx, rows []extWriteItem) (int64, error) {
	stmt, err := tx.PrepareContext(ctx, extWriteInsertSQL)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()
	excluded := extWriteExcluded(ctx, tx)
	var inserted int64
	for _, row := range rows {
		skip, err := excluded(row.session)
		if err != nil {
			return 0, err
		}
		if skip {
			continue
		}
		res, err := stmt.ExecContext(ctx, row.args...)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		inserted += n
	}
	return inserted, nil
}

// extWriteExcluded returns a lookup that reports whether a session belongs to
// an excluded project. A body usually carries one session, so each answer is
// remembered for the rest of the transaction.
func extWriteExcluded(ctx context.Context, tx *sql.Tx) func(session string) (bool, error) {
	known := map[string]bool{}
	return func(session string) (bool, error) {
		if session == "" {
			return false, nil
		}
		if excluded, ok := known[session]; ok {
			return excluded, nil
		}
		var excluded bool
		if err := tx.QueryRowContext(ctx, extWriteExcludedSQL, session).Scan(&excluded); err != nil {
			return false, fmt.Errorf("check excluded session: %w", err)
		}
		known[session] = excluded
		return excluded, nil
	}
}

// CountRejected adds n refused inputs to the sender's rejection counter.
func (s *Store) CountRejected(ctx context.Context, source string, n int) error {
	if n <= 0 {
		return nil
	}
	return s.ingestInTx(ctx, extWriteOpReject, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, extWriteRejectSQL, source, n); err != nil {
			return err
		}
		return bump(ctx, tx)
	})
}
