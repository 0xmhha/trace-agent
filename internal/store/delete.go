package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/0xmhha/trace-agent/internal/collect"
)

// Filter selects an interval [FromMS, UntilMS) within a source. Unknown scope
// or time is treated conservatively. Kind is "all" or "body".
type Filter struct {
	retention                   bool
	Source, Session, Task, Kind string
	FromMS, UntilMS             int64
}

// Preview binds deletion to the selected evidence and policy epoch.
type Preview struct {
	Point            Point
	Evidence, Bodies int64
	Stamp            string
}

// DeleteResult reports the committed deletion.
type DeleteResult struct {
	Point                   Point
	Evidence, Bodies, Facts int64
}

func (f Filter) valid() bool {
	return token(f.Source) && (f.Session == "" || token(f.Session)) && (f.Task == "" || token(f.Task)) && f.FromMS < f.UntilMS && (f.Kind == "all" || f.Kind == "body")
}
func (f Filter) selector() string {
	if f.retention {
		return "@retention"
	}
	if f.Task != "" {
		return "task/" + f.Task
	}
	if f.Session != "" {
		return "session/" + f.Session
	}
	return "*"
}
func isBlocked(ctx context.Context, tx *sql.Tx, e collect.Evidence, session, task string) (bool, error) {
	var blocked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM erased_record WHERE source_id=? AND record_key=? AND semantic_tag IN (?,?)) OR EXISTS(
 SELECT 1 FROM expired_undated WHERE source_id=? AND semantic_tag IN (?,?) AND ? IS NULL) OR EXISTS(SELECT 1 FROM suppression WHERE source_id=? AND scope='all'
 AND (? IS NULL OR (? >= from_ms AND ? < until_ms)) AND (selector_key='*' OR (selector_key='@retention' AND ? IS NOT NULL)
 OR (selector_key LIKE 'session/%' AND (?='' OR selector_key='session/'||?))
 OR (selector_key LIKE 'task/%' AND (?='' OR selector_key='task/'||?))))`,
		e.Source, e.RecordKey, e.SemanticTag, e.PreviousSemanticTag(), e.Source, e.SemanticTag, e.PreviousSemanticTag(), e.OccurredMS, e.Source, e.OccurredMS, e.OccurredMS, e.OccurredMS, e.OccurredMS, session, session, task, task).Scan(&blocked)
	return blocked, err
}

// Use a left join so migrated evidence without an adapter projection is also erased.
const selected = ` FROM evidence e LEFT JOIN adapter_record a USING(evidence_id)
 WHERE e.source_id=? AND ((e.occurred_ms IS NULL AND NOT ?) OR (coalesce(e.occurred_ms,e.observed_ms)>=? AND coalesce(e.occurred_ms,e.observed_ms)<?))
 AND (?='' OR coalesce(a.session_key,'')='' OR a.session_key=?)
 AND (?='' OR coalesce(a.task_key,'')='' OR a.task_key=?)`

func (f Filter) args() []any {
	return []any{f.Source, f.retention, f.FromMS, f.UntilMS, f.Session, f.Session, f.Task, f.Task}
}

func preview(ctx context.Context, tx *sql.Tx, f Filter) (Preview, error) {
	if !f.valid() {
		return Preview{}, errInvalid
	}
	var p Preview
	var err error
	p.Point, err = point(ctx, tx)
	if err != nil {
		return p, err
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(f)
	_ = enc.Encode(p.Point.Epoch)
	rows, err := tx.QueryContext(ctx, `SELECT e.evidence_id,e.rev,(SELECT count(*) FROM body b WHERE b.evidence_id=e.evidence_id AND b.status!='deleted')`+selected+" ORDER BY e.evidence_id", f.args()...)
	if err != nil {
		return p, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, rev, n int64
		if err = rows.Scan(&id, &rev, &n); err != nil {
			return p, err
		}
		_ = enc.Encode([3]int64{id, rev, n})
		p.Evidence++
		p.Bodies += n
	}
	p.Stamp = hex.EncodeToString(h.Sum(nil))
	return p, rows.Err()
}

// Preview reads the exact deletion scope in one snapshot.
func (s *Store) Preview(ctx context.Context, f Filter) (Preview, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Preview{}, err
	}
	defer func() { _ = tx.Rollback() }()
	return preview(ctx, tx, f)
}

// Delete writes replay suppression before removing support, recursively affected
// fact versions and evidence. Policy epoch and the changed scopes commit together.
func (s *Store) Delete(ctx context.Context, f Filter, expected Preview) (DeleteResult, error) {
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeleteResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := preview(ctx, tx, f)
	if err != nil {
		return DeleteResult{}, err
	}
	if p.Stamp != expected.Stamp {
		return DeleteResult{}, ErrStale
	}
	rev, err := s.revision(ctx, tx, f.Source)
	if err != nil {
		return DeleteResult{}, err
	}
	result := DeleteResult{Point: Point{rev, p.Point.Epoch + 1}, Bodies: p.Bodies}
	if _, err = tx.ExecContext(ctx, `INSERT INTO suppression VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, f.Source, f.Kind, f.FromMS, f.UntilMS, f.selector(), rev); err != nil {
		return DeleteResult{}, err
	}
	if _, err = tx.ExecContext(ctx, "CREATE TEMP TABLE erase_ids(id INTEGER PRIMARY KEY); INSERT INTO erase_ids SELECT e.evidence_id"+selected, f.args()...); err != nil {
		return DeleteResult{}, err
	}
	if f.Kind == "body" {
		_, err = tx.ExecContext(ctx, `UPDATE body SET clean_text=NULL,status='deleted' WHERE evidence_id IN erase_ids`)
	} else {
		result.Evidence = p.Evidence
		if f.retention {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO expired_undated SELECT source_id,semantic_tag FROM evidence WHERE evidence_id IN erase_ids AND occurred_ms IS NULL"); err != nil {
				return DeleteResult{}, err
			}
		}
		result.Facts, err = eraseAll(ctx, tx, rev)
	}
	if err != nil {
		return DeleteResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `DROP TABLE erase_ids; UPDATE store_state SET epoch=epoch+1; INSERT INTO change_scope VALUES(?,'store','privacy')`, rev); err != nil {
		return DeleteResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return DeleteResult{}, err
	}
	// The writer uses a single connection. A busy external reader may retain its
	// snapshot and WAL pages; never report physical cleanup as a logical rollback.
	_, err = s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return result, err
}

func eraseAll(ctx context.Context, tx *sql.Tx, rev int64) (int64, error) {
	_, err := tx.ExecContext(ctx, `CREATE TEMP TABLE erased_facts AS
 WITH RECURSIVE tree(task_id,fact_id,rev) AS (
 SELECT task_id,fact_id,fact_rev FROM support WHERE evidence_id IN erase_ids
 UNION SELECT v.task_id,v.fact_id,v.rev FROM fact_version v JOIN tree t ON v.task_id=t.task_id AND v.fact_id=t.fact_id AND v.rev<t.rev
 UNION SELECT d.task_id,d.fact_id,d.fact_rev FROM fact_dependency d JOIN tree t
 ON d.task_id=t.task_id AND d.input_id=t.fact_id AND d.input_rev=t.rev)
 SELECT * FROM tree;
 INSERT OR IGNORE INTO change_scope SELECT ?,'task',task_id FROM erased_facts;
 INSERT OR IGNORE INTO erased_record SELECT source_id,record_key,semantic_tag FROM evidence WHERE evidence_id IN erase_ids;
 CREATE TEMP TABLE erased_sessions AS SELECT DISTINCT session_id FROM task WHERE task_id IN (SELECT task_id FROM erased_facts);
 DELETE FROM fact_version WHERE (task_id,fact_id,rev) IN (SELECT task_id,fact_id,rev FROM erased_facts);
 DELETE FROM evidence WHERE evidence_id IN erase_ids;
 DELETE FROM fact WHERE (task_id,fact_id) IN (SELECT task_id,fact_id FROM erased_facts) AND NOT EXISTS(SELECT 1 FROM fact_version fv WHERE fv.task_id=fact.task_id AND fv.fact_id=fact.fact_id);
 DELETE FROM task WHERE task_id IN (SELECT task_id FROM erased_facts) AND NOT EXISTS(SELECT 1 FROM fact WHERE fact.task_id=task.task_id);
 DELETE FROM session WHERE session_id IN erased_sessions AND NOT EXISTS(SELECT 1 FROM task WHERE task.session_id=session.session_id);
 DROP TABLE erased_sessions`, rev)
	if err != nil {
		return 0, err
	}
	var n int64
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM erased_facts").Scan(&n); err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, "DROP TABLE erased_facts")
	return n, err
}

// Records returns adapter projections visible at the fixed point, in commit order.
func (s *Store) Records(ctx context.Context, p Point) ([]Record, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	latest, err := point(ctx, tx)
	if err != nil {
		return nil, err
	}
	if p.Epoch != latest.Epoch || p.Revision < 0 || p.Revision > latest.Revision {
		return nil, ErrStale
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.evidence_id,e.rev,e.source_id,s.profile_key,e.stream_key,e.generation,e.record_key,e.version_key,e.semantic_tag,s.kind,
 coalesce(e.observer_code,'unknown'),coalesce(e.adapter_version,''),coalesce(a.client_version,''),coalesce(a.position,0),e.observed_ms,e.occurred_ms,coalesce(a.metadata,json_object('type',e.record_kind))
 FROM evidence e JOIN source s USING(source_id) LEFT JOIN adapter_record a USING(evidence_id) WHERE e.rev<=? ORDER BY e.rev,e.evidence_id`, p.Revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Record
	for rows.Next() {
		var r Record
		var raw []byte
		e := &r.Evidence
		if err = rows.Scan(&r.ID, &r.Rev, &e.Source, &e.Profile, &e.Stream, &e.Generation, &e.RecordKey, &e.VersionKey, &e.SemanticTag, &e.Kind, &e.Observer, &e.AdapterVersion, &e.ClientVersion, &e.Position, &e.ObservedMS, &e.OccurredMS, &raw); err != nil {
			return nil, err
		}
		e.Redaction = "metadata_only"
		if json.Unmarshal(raw, &e.Metadata) != nil {
			return nil, errors.New("invalid stored metadata")
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := attachFacts(ctx, tx, p.Revision, out); err != nil {
		return nil, err
	}
	return out, nil
}
