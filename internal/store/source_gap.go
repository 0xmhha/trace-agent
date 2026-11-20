package store

import (
	"context"
	"database/sql"
)

// SourceGap records a file discontinuity or an interval of unavailable collection.
type SourceGap struct {
	FromMS  int64
	UntilMS *int64
	Reason  string
}

// SourceGaps returns the most recent 200 reported gaps intersecting the user's
// interval. Discontinuities with unknown duration are selected by observation time.
func (s *Store) SourceGaps(ctx context.Context, p Point, source string, from, until int64) ([]SourceGap, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := point(ctx, tx)
	if err != nil {
		return nil, false, err
	}
	if p.Epoch != current.Epoch || p.Revision > current.Revision || p.Revision < 0 || from >= until {
		return nil, false, ErrStale
	}
	rows, err := tx.QueryContext(ctx, `WITH gaps(since,until,reason) AS (
 SELECT e.observed_ms,NULL,coalesce(json_extract(a.metadata,'$.reason'),'file_changed') FROM evidence e
 LEFT JOIN adapter_record a USING(evidence_id) WHERE e.source_id=?1 AND e.rev<=?2 AND e.record_kind='source_gap' AND e.observed_ms>=?3
 UNION ALL
 SELECT coalesce(st.gap_from_ms,st.at_ms),coalesce(st.gap_until_ms,(SELECT min(recovered.at_ms) FROM source_status recovered
 WHERE recovered.source_id=st.source_id AND recovered.rev>st.rev AND recovered.rev<=?2 AND recovered.health='connected')),
 coalesce(st.reason_code,st.health) FROM source_status st WHERE st.source_id=?1 AND st.rev<=?2
 AND (st.gap_from_ms IS NOT NULL OR st.health IN ('lost','delayed'))
 UNION ALL SELECT max(sup.from_ms,?3),min(sup.until_ms,?4),'erased_range' FROM suppression sup
 WHERE sup.source_id=?1 AND sup.scope='all' AND sup.rev<=?2 AND sup.from_ms<?4 AND sup.until_ms>?3
 ) SELECT since,until,reason FROM gaps WHERE since<?4 AND (until IS NULL OR until>?3) ORDER BY since DESC LIMIT 201`, source, p.Revision, from, until)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var gaps []SourceGap
	for rows.Next() {
		var g SourceGap
		if err = rows.Scan(&g.FromMS, &g.UntilMS, &g.Reason); err != nil {
			return nil, false, err
		}
		if len(gaps) == 200 {
			return gaps, true, nil
		}
		gaps = append(gaps, g)
	}
	return gaps, false, rows.Err()
}
