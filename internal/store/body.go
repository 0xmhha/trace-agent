package store

import (
	"context"
	"database/sql"

	"github.com/0xmhha/trace-agent/internal/collect"
)

func writeBodies(ctx context.Context, tx *sql.Tx, e collect.Evidence, id int64, session, task string) error {
	slots := e.BodySlots()
	if len(slots) == 0 || e.OccurredMS == nil {
		return nil
	}
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT
 coalesce((SELECT enabled FROM consent WHERE source_id=? ORDER BY rev DESC LIMIT 1),0)
 AND coalesce((SELECT enabled FROM consent WHERE source_id=? AND effective_ms<=? ORDER BY effective_ms DESC,rev DESC LIMIT 1),0)
 AND NOT EXISTS(SELECT 1 FROM suppression WHERE source_id=? AND ? >=from_ms AND ? <until_ms
 AND (selector_key='*' OR (selector_key LIKE 'task/%' AND (?='' OR selector_key='task/'||?))
 OR (selector_key LIKE 'session/%' AND (?='' OR selector_key='session/'||?))))`, e.Source, e.Source, *e.OccurredMS, e.Source, *e.OccurredMS, *e.OccurredMS, task, task, session, session).Scan(&allowed)
	if err != nil || !allowed {
		return err
	}
	for _, b := range slots {
		var text any
		if b.Status == "sanitized" || b.Status == "truncated" {
			text = b.Text
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO body(evidence_id,field_code,status,clean_text,observer_code,conversation_role) VALUES(?,?,?,?,?,NULLIF(?,'')) ON CONFLICT DO NOTHING`, id, b.Field, b.Status, text, e.Observer, b.Role); err != nil {
			return err
		}
	}
	return nil
}

// BodyFields checks the snapshot epoch and the current consent before returning
// display fields. The transport separately guards the final send with WithEpoch.
func (s *Store) BodyFields(ctx context.Context, p Point, id int64) ([]collect.BodySlot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	latest, err := point(ctx, tx)
	if err != nil {
		return nil, err
	}
	if p.Epoch != latest.Epoch || p.Revision > latest.Revision {
		return nil, ErrStale
	}
	rows, err := tx.QueryContext(ctx, `SELECT field_code,status,coalesce(clean_text,''),coalesce(conversation_role,'') FROM body b JOIN evidence e USING(evidence_id)
 WHERE evidence_id=? AND rev<=? AND coalesce((SELECT enabled FROM consent c WHERE c.source_id=e.source_id ORDER BY c.rev DESC LIMIT 1),0)=1 ORDER BY field_code`, id, p.Revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []collect.BodySlot
	for rows.Next() {
		var b collect.BodySlot
		if err := rows.Scan(&b.Field, &b.Status, &b.Text, &b.Role); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BodyAvailability explains an empty body result without disclosing its content.
func (s *Store) BodyAvailability(ctx context.Context, p Point, id int64) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := point(ctx, tx)
	if err != nil {
		return "", err
	}
	if p.Epoch != current.Epoch || p.Revision > current.Revision {
		return "", ErrStale
	}
	var code string
	err = tx.QueryRowContext(ctx, `SELECT CASE (SELECT enabled FROM consent WHERE source_id=e.source_id ORDER BY rev DESC LIMIT 1) WHEN 0 THEN 'REVOKED' WHEN 1 THEN 'MISSING' ELSE 'DISABLED' END FROM evidence e WHERE evidence_id=? AND rev<=?`, id, p.Revision).Scan(&code)
	return code, err
}
