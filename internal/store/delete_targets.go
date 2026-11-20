package store

import (
	"context"
	"database/sql"
)

// Confirmed targets must come from evidence with a known owner in this range.
// The wider conservative selection in delete.go also erases unattributed rows.
const targetRange = ` FROM evidence e JOIN adapter_record a USING(evidence_id)
 WHERE e.source_id=? AND (e.occurred_ms IS NULL OR (e.occurred_ms>=? AND e.occurred_ms<?))`

func validateDeleteTarget(ctx context.Context, tx *sql.Tx, f Filter) error {
	if f.Session == "" && f.Task == "" {
		return nil
	}
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1`+targetRange+` AND (?='' OR a.session_key=?) AND (?='' OR a.task_key=?))`, f.Source, f.FromMS, f.UntilMS, f.Session, f.Session, f.Task, f.Task).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return errInvalid
	}
	return nil
}

// DeleteTargets lists confirmed session or task IDs in opaque-key order.
// The next page starts strictly after its last returned ID.
func (s *Store) DeleteTargets(ctx context.Context, f Filter, kind, after string) ([]string, bool, Point, error) {
	const pageSize = 50
	if !f.valid() || (kind != "SESSION" && kind != "TASK") || (after != "" && !token(after)) {
		return nil, false, Point{}, errInvalid
	}
	col := "a.session_key"
	if kind == "TASK" {
		col = "a.task_key"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, Point{}, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := point(ctx, tx)
	if err != nil {
		return nil, false, Point{}, err
	}
	rs, err := tx.QueryContext(ctx, `SELECT DISTINCT `+col+targetRange+` AND `+col+`>? ORDER BY `+col+` LIMIT ?`, f.Source, f.FromMS, f.UntilMS, after, pageSize+1)
	if err != nil {
		return nil, false, Point{}, err
	}
	defer func() { _ = rs.Close() }()
	ids := []string{}
	for rs.Next() {
		var id string
		if err = rs.Scan(&id); err != nil {
			return nil, false, Point{}, err
		}
		ids = append(ids, id)
	}
	if err = rs.Err(); err != nil {
		return nil, false, Point{}, err
	}
	return ids[:min(len(ids), pageSize)], len(ids) > pageSize, p, nil
}
