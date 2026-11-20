package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
)

// FactProjection is a typed, metadata-only interpretation of visible evidence.
// Entity identifiers must be opaque tokens. Free-text body fields are excluded.
type FactProjection struct {
	ID, Task, Session, Source, Client, Root, Kind            string
	Actor, Target, Tool, Module, ModuleKind, Server, Attempt string
	Phase, Result, State, Action, Side, Grade, Model         string
	Created                                                  bool
	Occurred, ValidUntil                                     *int64
	Observed                                                 int64
	Reported                                                 *float64
	Evidence                                                 []string
	Stream, Clock                                            string
	Position                                                 int64
	Ordered                                                  bool
	Steps                                                    []string
	Parents                                                  []FactRelation
}

// FactRelation names an independently supported relation to a previous event.
type FactRelation struct{ Kind, From string }

const factFieldsDDL = `CREATE TABLE IF NOT EXISTS projected_fact_fields (
 task_id TEXT NOT NULL, fact_id TEXT NOT NULL, rev INTEGER NOT NULL, fields TEXT NOT NULL CHECK(json_valid(fields)),
 PRIMARY KEY(task_id,fact_id,rev), FOREIGN KEY(task_id,fact_id,rev) REFERENCES fact_version ON DELETE CASCADE
) STRICT;`

func (f FactProjection) validate() error {
	if len(f.Evidence) == 0 || !token(f.ID) || !token(f.Task) || !token(f.Session) || !token(f.Source) {
		return errInvalid
	}
	for _, s := range []string{f.Root, f.Actor, f.Target, f.Module, f.Server, f.Attempt, f.Model, f.Stream, f.Clock} {
		if s != "" && !token(s) {
			return errInvalid
		}
	}
	if f.Tool != "" && !token(f.Tool) {
		return errInvalid
	}
	for _, p := range f.Parents {
		if !token(p.From) || !slices.Contains([]string{"JOIN_INPUT", "CALL_RESULT", "STREAM_ORDER", "RETRY", "CAUSAL", "OVERLAP"}, p.Kind) {
			return errInvalid
		}
	}
	for _, s := range f.Steps {
		if !slices.Contains([]string{"ACTIVE", "PENDING", "DONE", "FAILED", "CANCELLED", "UNKNOWN"}, s) {
			return errInvalid
		}
	}
	for _, v := range []struct{ s, choices string }{
		{f.Client, "CODEX CLAUDE MCP GATEWAY"}, {f.Kind, "task call result assignment state wait return coordination plan message gap"},
		{f.Action, "SPAWN ASSIGN SEND CALL RETURN WAIT TIMEOUT RESUME JOIN STATE GAP"},
		{f.Side, "SENDER RECEIVER BOTH LOCAL UNKNOWN"}, {f.Grade, "DIRECT INFERRED UNKNOWN"},
		{f.Phase, "PROPOSED START_OBSERVED RUNNING ENDED UNKNOWN"}, {f.Result, "SUCCESS TOOL_ERROR RPC_ERROR MODEL_ERROR CANCELLED UNKNOWN"}, {f.State, "RUNNING ENDED UNKNOWN"}, {f.ModuleKind, "AGENT MODEL TOOL MCP_SERVER MCP_TOOL"},
	} {
		if v.s != "" && !slices.Contains(strings.Fields(v.choices), v.s) {
			return errInvalid
		}
	}
	if f.Kind == "" || f.Grade == "" {
		return errInvalid
	}
	return nil
}

// PublishFacts publishes an attributed batch only if its evidence snapshot is
// still current. Relations refer to existing or earlier rows of the same batch.
// Support/dependency foreign keys make deletion traverse all materialized fields.
func (s *Store) PublishFacts(ctx context.Context, base Point, fs []FactProjection) (Point, error) {
	if len(fs) == 0 {
		return base, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Point{}, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := point(ctx, tx)
	if err != nil {
		return Point{}, err
	}
	if p != base {
		return Point{}, ErrStale
	}
	rev, err := s.revision(ctx, tx, fs[0].Source)
	if err != nil {
		return Point{}, err
	}
	for _, f := range fs {
		if err = f.validate(); err != nil {
			return Point{}, err
		}
		if err = writeFact(ctx, tx, f, rev); err != nil {
			return Point{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Point{}, err
	}
	return Point{Revision: rev, Epoch: p.Epoch}, nil
}

func writeFact(ctx context.Context, tx *sql.Tx, f FactProjection, rev int64) error {
	fields, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO session VALUES(?,?,?,?);
 INSERT OR IGNORE INTO task VALUES(?,?,?,?); INSERT OR IGNORE INTO fact VALUES(?,?,?);
 INSERT INTO fact_version(task_id,fact_id,rev,occurred_ms,kind,grade,edge_code,value_code,reason_code)
 VALUES(?,?,?,?,'activity',?,?,?,'projected_evidence');
 INSERT INTO projected_fact_fields VALUES(?,?,?,?);
 INSERT OR IGNORE INTO change_scope VALUES(?,'task',?); INSERT OR IGNORE INTO change_scope VALUES(?,'source',?)`,
		f.Session, f.Source, f.Session, rev, f.Task, f.Session, f.Task, rev, f.Task, f.ID, f.ID,
		f.Task, f.ID, rev, f.Occurred, strings.ToLower(f.Grade), f.Action, f.Kind, f.Task, f.ID, rev, string(fields), rev, f.Task, rev, f.Source); err != nil {
		return err
	}
	for _, sid := range f.Evidence {
		id, err := strconv.ParseInt(sid, 10, 64)
		if err != nil || id < 1 {
			return errInvalid
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO support SELECT ?,?,?,evidence_id,rev FROM evidence WHERE evidence_id=? AND source_id=?`, f.Task, f.ID, rev, id, f.Source)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n != 1 {
			return errors.New("fact evidence unavailable")
		}
	}
	for _, rel := range f.Parents {
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO fact_dependency SELECT ?,?,?,?,MAX(rev) FROM fact_version WHERE task_id=? AND fact_id=? HAVING COUNT(*)>0`, f.Task, f.ID, rev, rel.From, f.Task, rel.From)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.New("fact dependency unavailable")
		}
	}
	return nil
}

// attachFacts adds the latest visible fact version to one support record. All
// supports remain in Evidence; the query never silently imports a future version.
func attachFacts(ctx context.Context, tx *sql.Tx, revision int64, rows []Record) error {
	var indices map[int64]int
	rs, err := tx.QueryContext(ctx, `SELECT f.fields,MIN(s.evidence_id) FROM projected_fact_fields f JOIN support s
 ON (s.task_id,s.fact_id,s.fact_rev)=(f.task_id,f.fact_id,f.rev)
 JOIN fact_version v ON (v.task_id,v.fact_id,v.rev)=(f.task_id,f.fact_id,f.rev)
 WHERE f.rev<=? AND v.withdrawn=0 AND NOT EXISTS(SELECT 1 FROM fact_version newer WHERE newer.task_id=f.task_id AND newer.fact_id=f.fact_id AND newer.rev BETWEEN f.rev+1 AND ?)
 GROUP BY f.task_id,f.fact_id,f.rev ORDER BY f.rev,f.fact_id`, revision, revision)
	if err != nil {
		return err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var data []byte
		var id int64
		var f FactProjection
		if err := rs.Scan(&data, &id); err != nil {
			return err
		}
		if indices == nil {
			indices = make(map[int64]int, len(rows))
			for i, r := range rows {
				indices[r.ID] = i
			}
		}
		i, ok := indices[id]
		if !ok {
			continue
		}
		if json.Unmarshal(data, &f) != nil {
			return errInvalid
		}
		rows[i].Facts = append(rows[i].Facts, f)
	}
	return rs.Err()
}
