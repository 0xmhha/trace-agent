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
// ignored.
func (s *Store) AppendExt(ctx context.Context, events []usage.ExtEvent) error {
	if len(events) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(events))
	for _, e := range events {
		args, err := extWriteArgs(e)
		if err != nil {
			return fmt.Errorf("%s: %w", extWriteOpAppend, err)
		}
		rows = append(rows, args)
	}
	return s.ingestInTx(ctx, extWriteOpAppend, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, extWriteInsertSQL)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		var inserted int64
		for _, args := range rows {
			res, execErr := stmt.ExecContext(ctx, args...)
			if execErr != nil {
				return execErr
			}
			n, countErr := res.RowsAffected()
			if countErr != nil {
				return countErr
			}
			inserted += n
		}
		if inserted == 0 {
			return nil
		}
		return bump(ctx, tx)
	})
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
