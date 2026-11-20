package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/0xmhha/trace-agent/internal/collect"
)

var errInvalid = errors.New("invalid trace storage input")

func token(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func get(m map[string]any, k string) string            { v, _ := m[k].(string); return v }
func nested(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }
func scopes(e collect.Evidence) (string, string) {
	m := e.Metadata
	if get(m, "method") == "turn/plan/updated" {
		m = nested(m, "params")
	}
	session, task := get(m, "threadId"), get(m, "turnId")
	if session == "" {
		session = get(m, "sessionId")
	}
	if e.Kind == collect.CodexTelemetry {
		session = get(m, "conversation.id")
	}
	if get(m, "type") == "thread" {
		session = get(m, "id")
	}
	if collect.HumanPrompt(e) {
		task = get(m, "promptId")
	}
	if e.Kind == collect.MCPObserver || e.Kind == collect.ClaudeHook {
		task = ""
	}
	return collect.SessionKey(e.Profile, session), collect.TaskKey(e.Profile, session, task)
}

func loadJSON[T any](ctx context.Context, q querier, query string, args ...any) (T, error) {
	var raw string
	var v T
	err := q.QueryRowContext(ctx, query, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(raw), &v)
	return v, err
}

const cursorQuery = "SELECT position FROM checkpoint WHERE source_id=? AND stream_key=?"
const itemQuery = "SELECT state FROM adapter_item WHERE record_key=?"

// Cursor returns the last complete file boundary. A new stream returns the zero cursor.
func (s *Store) Cursor(ctx context.Context, source, stream string) (collect.FileCursor, error) {
	return loadJSON[collect.FileCursor](ctx, s.db, cursorQuery, source, stream)
}

// Latest implements collect.AppSink.
func (s *Store) Latest(ctx context.Context, key string) (collect.ItemVersion, error) {
	return loadJSON[collect.ItemVersion](ctx, s.db, itemQuery, key)
}

// Commit implements collect.AppSink.
func (s *Store) Commit(ctx context.Context, e collect.Evidence, v collect.ItemVersion) error {
	return s.write(ctx, e, nil, &v)
}

// FileCommit is the durable callback for collect.ReadFile.
func (s *Store) FileCommit(ctx context.Context, e collect.Evidence, c collect.FileCursor) error {
	return s.write(ctx, e, &c, nil)
}

// Append commits an immutable evidence record identified by its native position.
func (s *Store) Append(ctx context.Context, e collect.Evidence) error {
	return s.write(ctx, e, nil, nil)
}

func client(kind collect.Kind) string { return strings.ToLower(collect.ClientCode(kind)) }

// AppendBatch atomically ingests up to 100 immutable records at one public revision.
func (s *Store) AppendBatch(ctx context.Context, es []collect.Evidence) error {
	if len(es) > 100 {
		return errInvalid
	}
	return s.transact(ctx, func(tx *sql.Tx, rev *int64) error {
		for _, e := range es {
			if err := s.insert(ctx, tx, rev, e, nil, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) write(ctx context.Context, e collect.Evidence, c *collect.FileCursor, v *collect.ItemVersion) error {
	return s.transact(ctx, func(tx *sql.Tx, rev *int64) error { return s.insert(ctx, tx, rev, e, c, v) })
}
func (s *Store) transact(ctx context.Context, fn func(*sql.Tx, *int64) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var rev int64
	if err = fn(tx, &rev); err != nil {
		return err
	}
	if rev == 0 {
		return nil
	}
	if s.boundary != nil {
		s.boundary("before")
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if s.boundary != nil {
		s.boundary("after")
	}
	return nil
}

func (s *Store) insert(ctx context.Context, tx *sql.Tx, batchRev *int64, e collect.Evidence, c *collect.FileCursor, v *collect.ItemVersion) error {
	meta, err := e.StorageMetadata()
	if err != nil {
		return err
	}
	if !token(e.Source) || !token(e.Stream) || !token(e.Generation) || !token(e.RecordKey) || !token(e.Profile) || !token(e.SemanticTag) || client(e.Kind) == "" || e.Redaction != "metadata_only" {
		return errInvalid
	}
	if c != nil && (c.Source != e.Source || c.Stream != e.Stream || c.Generation != e.Generation || !token(c.FileID) || !token(c.Anchor) || c.Offset < e.Position) {
		return errInvalid
	}
	if err := checkVersion(e, v); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO source VALUES(?,?,?,?,?) ON CONFLICT(source_id) DO NOTHING", e.Source, client(e.Kind), string(e.Kind), e.Profile, e.Source)
	if err != nil {
		return err
	}
	cfg, err := config(ctx, tx, e.Source)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return ErrDisabled
	}
	var id int64
	var tag string
	err = tx.QueryRowContext(ctx, "SELECT evidence_id,semantic_tag FROM evidence WHERE source_id=? AND stream_key=? AND generation=? AND record_key=? AND version_key=?", e.Source, e.Stream, e.Generation, e.RecordKey, e.VersionKey).Scan(&id, &tag)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if id != 0 && tag != e.SemanticTag && tag != e.PreviousSemanticTag() {
		return errors.New("conflicting evidence at immutable position")
	}
	session, task := scopes(e)
	blocked, err := isBlocked(ctx, tx, e, session, task)
	if err != nil {
		return err
	}
	if cfg.FromMS != nil || cfg.UntilMS != nil {
		if e.OccurredMS == nil || cfg.FromMS != nil && *e.OccurredMS < *cfg.FromMS || cfg.UntilMS != nil && *e.OccurredMS >= *cfg.UntilMS {
			blocked = true
		}
	}
	var advance bool
	if c != nil {
		prev, err := loadJSON[collect.FileCursor](ctx, tx, cursorQuery, e.Source, e.Stream)
		if err != nil {
			return err
		}
		switch {
		case prev == *c:
		case prev.Generation == c.Generation && prev.Offset > c.Offset && (id != 0 || blocked):
		case prev.Generation == c.Generation && prev.Offset == e.Position:
			advance = true
		case prev == (collect.FileCursor{}) && e.Position == 0:
			advance = true
		case prev.Generation != c.Generation && e.PreviousGeneration != "" && prev.Generation == e.PreviousGeneration && c.Offset == 0:
			advance = true
		case id != 0 || blocked: // An old generation can be replayed without moving the live cursor.
		default:
			return ErrStale
		}
	}
	if (id != 0 || blocked) && !advance {
		return nil
	}
	if v != nil && !blocked {
		prev, err := loadJSON[collect.ItemVersion](ctx, tx, itemQuery, e.RecordKey)
		if err != nil {
			return err
		}
		if v.Sequence != prev.Sequence+1 {
			return ErrStale
		}
	}
	rev := *batchRev
	if rev == 0 {
		rev, err = s.revision(ctx, tx, e.Source)
		*batchRev = rev
	} else {
		_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO change_scope VALUES(?,'source',?)", rev, e.Source)
	}
	if err != nil {
		return err
	}
	if !blocked && id == 0 {
		kind := get(e.Metadata, "type")
		if kind == "" {
			kind = get(e.Metadata, "event.name")
		}
		if kind == "" {
			kind = get(e.Metadata, "method")
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO evidence(source_id,stream_key,generation,record_key,version_key,semantic_tag,occurred_ms,observed_ms,rev,record_kind,observer_code,adapter_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, e.Source, e.Stream, e.Generation, e.RecordKey, e.VersionKey, e.SemanticTag, e.OccurredMS, e.ObservedMS, rev, kind, e.Observer, e.AdapterVersion)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO adapter_record VALUES(?,?,?,?,?,?)", id, e.Position, e.ClientVersion, string(meta), session, task); err != nil {
			return err
		}
		if cfg.Consent {
			if err = writeBodies(ctx, tx, e, id, session, task); err != nil {
				return err
			}
		}
		if err = observe(ctx, tx, id, rev, e, session, task, kind); err != nil {
			return err
		}
	}
	if advance {
		state, _ := json.Marshal(c)
		_, err = tx.ExecContext(ctx, `INSERT INTO checkpoint VALUES(?,?,?,?,?) ON CONFLICT(source_id,stream_key) DO UPDATE SET generation=excluded.generation,position=excluded.position,rev=excluded.rev`, e.Source, e.Stream, e.Generation, string(state), rev)
	} else if v != nil && !blocked {
		state, _ := json.Marshal(v)
		_, err = tx.ExecContext(ctx, `INSERT INTO adapter_item VALUES(?,?,?) ON CONFLICT(record_key) DO UPDATE SET state=excluded.state`, e.RecordKey, e.Source, string(state))
	}
	if err != nil {
		return err
	}
	return nil
}

func checkVersion(e collect.Evidence, v *collect.ItemVersion) error {
	if len(e.ClientVersion) > 32 || strings.Trim(e.ClientVersion, ".0123456789") != "" || e.AdapterVersion != collect.Version {
		return errInvalid
	}
	if v == nil {
		if e.VersionKey != "1" {
			return errInvalid
		}
		return nil
	}
	valid := func(s string) bool {
		phase, val, ok := strings.Cut(s, "/")
		return s == "" || token(s) || ok && (phase == "started" || phase == "completed" || phase == "observed") && token(val)
	}
	if !valid(v.Current) || !valid(v.Started) || !valid(v.Completed) || v.Sequence == 0 {
		return errInvalid
	}
	if e.VersionKey == "1" && v.Sequence == 1 {
		return nil
	}
	phase, n, ok := strings.Cut(e.VersionKey, "/")
	if !ok || (phase != "started" && phase != "completed" && phase != "observed") || n != strconv.FormatUint(v.Sequence, 10) {
		return errInvalid
	}
	return nil
}

func observe(ctx context.Context, tx *sql.Tx, id, rev int64, e collect.Evidence, session, task, kind string) error {
	for _, key := range []string{session, task} {
		if key != "" {
			if _, err := tx.ExecContext(ctx, "INSERT INTO evidence_scope VALUES(?,?)", id, key); err != nil {
				return err
			}
		}
	}
	if e.Kind == collect.Telemetry && (kind == "api_request_body" || kind == "api_response_body") && get(e.Metadata, "request_body_id") != "" {
		role, phase, result := "request", "start_observed", "unknown"
		if kind == "api_response_body" {
			role, phase, result = "response", "ended", "success"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_model_observation(evidence_id,evidence_rev,rev,scope_key,attempt_key,model_key,role,phase,result) VALUES(?,?,?,?,?,NULLIF(?,''),?,?,?)`, id, rev, rev, e.Stream, get(e.Metadata, "request_body_id"), get(e.Metadata, "model"), role, phase, result); err != nil {
			return err
		}
	}
	if task == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO session VALUES(?,?,?,?) ON CONFLICT(session_id) DO NOTHING;
 INSERT INTO task VALUES(?,?,?,?) ON CONFLICT(task_id) DO NOTHING;
 INSERT OR IGNORE INTO change_scope VALUES(?,'task',?)`, session, e.Source, session, rev, task, session, task, rev, rev, task); err != nil {
		return err
	}
	fact := e.RecordKey
	if _, err := tx.ExecContext(ctx, `INSERT INTO fact VALUES(?,?,?) ON CONFLICT(task_id,fact_id) DO NOTHING;
 INSERT INTO fact_version(task_id,fact_id,rev,occurred_ms,kind,grade,value_code) VALUES(?,?,?,?,'source_observation','direct',?);
 INSERT INTO support VALUES(?,?,?,?,?)`, task, fact, fact, task, fact, rev, e.OccurredMS, kind, task, fact, rev, id, rev); err != nil {
		return err
	}
	return nil
}
