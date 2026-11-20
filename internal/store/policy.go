package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
)

// SourceConfig contains the current source-specific collection policy.
type SourceConfig struct {
	Enabled, Consent bool
	RetentionDays    int
	FromMS, UntilMS  *int64
}

func config(ctx context.Context, q querier, source string) (SourceConfig, error) {
	cfg := SourceConfig{Enabled: true, RetentionDays: 7}
	err := q.QueryRowContext(ctx, `SELECT enabled,retention_days,history_from_ms,history_until_ms FROM source_config WHERE source_id=? ORDER BY rev DESC LIMIT 1`, source).Scan(&cfg.Enabled, &cfg.RetentionDays, &cfg.FromMS, &cfg.UntilMS)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return cfg, err
	}
	err = q.QueryRowContext(ctx, "SELECT enabled FROM consent WHERE source_id=? ORDER BY rev DESC LIMIT 1", source).Scan(&cfg.Consent)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return cfg, err
}

// SourceConfig returns defaults for a newly attached source.
func (s *Store) SourceConfig(ctx context.Context, source string) (SourceConfig, error) {
	return config(ctx, s.db, source)
}
func (s *Store) setting(ctx context.Context, source string, edit func(*SourceConfig), privacy bool) (Point, error) {
	if !token(source) {
		return Point{}, errInvalid
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Point{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, err := config(ctx, tx, source)
	if err != nil {
		return Point{}, err
	}
	previous := cfg
	edit(&cfg)
	if !validConfig(cfg) {
		return Point{}, errInvalid
	}
	rev, err := s.revision(ctx, tx, source)
	if err != nil {
		return Point{}, err
	}
	if privacy {
		_, err = tx.ExecContext(ctx, "INSERT INTO consent VALUES(?,?,?,?); UPDATE store_state SET epoch=epoch+1; INSERT INTO change_scope VALUES(?,'store','privacy')", source, rev, cfg.Consent, s.now().UnixMilli(), rev)
	} else {
		err = writeConfig(ctx, tx, source, rev, previous, cfg)
	}
	if err != nil {
		return Point{}, err
	}

	p, err := point(ctx, tx)
	if err != nil {
		return Point{}, err
	}
	return p, tx.Commit()
}

// SetConsent changes current consent and invalidates frames from earlier epochs.
func (s *Store) SetConsent(ctx context.Context, source string, enabled bool) (Point, error) {
	return s.setting(ctx, source, func(c *SourceConfig) { c.Consent = enabled }, true)
}

// SetEnabled updates the collection switch for a source.
func (s *Store) SetEnabled(ctx context.Context, source string, enabled bool) (Point, error) {
	return s.setting(ctx, source, func(c *SourceConfig) { c.Enabled = enabled }, false)
}

// SetRetention sets a retention period in days.
func (s *Store) SetRetention(ctx context.Context, source string, days int) (Point, error) {
	return s.setting(ctx, source, func(c *SourceConfig) { c.RetentionDays = days }, false)
}

// SetHistory configures the selected historical event interval.
func (s *Store) SetHistory(ctx context.Context, source string, fromMS, untilMS *int64) (Point, error) {
	return s.setting(ctx, source, func(c *SourceConfig) { c.FromMS = fromMS; c.UntilMS = untilMS }, false)
}

func validConfig(cfg SourceConfig) bool {
	return cfg.RetentionDays >= 1 && cfg.RetentionDays <= 36500 && (cfg.FromMS == nil || cfg.UntilMS == nil || *cfg.FromMS < *cfg.UntilMS)
}

func writeConfig(ctx context.Context, tx *sql.Tx, source string, rev int64, previous, cfg SourceConfig) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO source_config(source_id,rev,enabled,picker_ref,history_from_ms,history_until_ms,retention_days) VALUES(?,?,?,?,?,?,?)`, source, rev, cfg.Enabled, source, cfg.FromMS, cfg.UntilMS, cfg.RetentionDays)
	if err != nil {
		return err
	}
	if datesChanged(previous, cfg) {
		_, err = tx.ExecContext(ctx, "DELETE FROM checkpoint WHERE source_id=?", source)
	}
	return err
}

// Register creates a visible source before the first evidence arrives.
func (s *Store) Register(ctx context.Context, a *collect.Adapter) (Point, error) {
	return s.Attach(ctx, a, 0, nil, nil)
}

// Registered reports whether the source has a persisted catalog entry.
func (s *Store) Registered(ctx context.Context, id string) (bool, error) {
	if !token(id) {
		return false, errInvalid
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM source WHERE source_id=?)", id).Scan(&exists)
	return exists, err
}

// Attach commits source registration, retention and the history interval at one
// revision. Changing that interval clears the checkpoint so the reader can
// revisit history. Zero days and a pair of nil bounds preserve existing settings.
func (s *Store) Attach(ctx context.Context, a *collect.Adapter, retentionDays int, fromMS, untilMS *int64) (Point, error) {
	if a == nil {
		return Point{}, errInvalid
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Point{}, err
	}
	defer func() { _ = tx.Rollback() }()
	id := a.Source()
	prev, err := config(ctx, tx, id)
	if err != nil {
		return Point{}, err
	}
	cfg := prev
	if retentionDays != 0 {
		cfg.RetentionDays = retentionDays
	}
	if fromMS != nil || untilMS != nil {
		cfg.FromMS, cfg.UntilMS = fromMS, untilMS
	}
	if !validConfig(cfg) {
		return Point{}, errInvalid
	}
	r, err := tx.ExecContext(ctx, "INSERT INTO source VALUES(?,?,?,?,?) ON CONFLICT(source_id) DO NOTHING", id, client(a.Kind()), a.Kind(), a.Profile(), id)
	if err != nil {
		return Point{}, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return Point{}, err
	}
	changed := cfg.RetentionDays != prev.RetentionDays || datesChanged(prev, cfg)
	if n != 0 || changed {
		rev, err := s.revision(ctx, tx, id)
		if err != nil {
			return Point{}, err
		}
		if changed {
			if err = writeConfig(ctx, tx, id, rev, prev, cfg); err != nil {
				return Point{}, err
			}
		}
	}
	p, err := point(ctx, tx)
	if err != nil {
		return Point{}, err
	}
	if err = tx.Commit(); err != nil {
		return Point{}, err
	}
	return p, nil
}

// ReplayFloor returns the oldest retained change-log base.
func (s *Store) ReplayFloor(ctx context.Context) (int64, error) {
	var rev int64
	err := s.db.QueryRowContext(ctx, "SELECT replay_floor FROM store_state").Scan(&rev)
	return rev, err
}

// ChangedSources returns the distinct sources touched in (after,through].
func (s *Store) ChangedSources(ctx context.Context, after, through int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT scope_key FROM change_scope WHERE scope_kind='source' AND rev>? AND rev<=? ORDER BY scope_key", after, through)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ErrDisabled stops an in-flight adapter when its source has been switched off.
var ErrDisabled = errors.New("source collection disabled")

func datesChanged(a, b SourceConfig) bool {
	equal := func(x, y *int64) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	return !equal(a.FromMS, b.FromMS) || !equal(a.UntilMS, b.UntilMS)
}

// SourceInfo describes an attached source at a committed point.
type SourceInfo struct {
	LastObservedMS, LastEventMS, CheckedMS *int64
	Health                                 string
	ID, Client, Kind                       string
	ClientVersion, AdapterVersion          string
	Enabled, Consent, HasMetadata          bool
	RetentionDays                          int
	FromMS, UntilMS                        *int64
}

// Sources includes attached sources that have not yet delivered their first record.
func (s *Store) Sources(ctx context.Context, p Point) ([]SourceInfo, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := point(ctx, tx)
	if err != nil {
		return nil, err
	}
	if p.Epoch != current.Epoch || p.Revision > current.Revision || p.Revision < 0 {
		return nil, ErrStale
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.source_id,s.client,s.kind,coalesce(c.enabled,1),
 coalesce((SELECT enabled FROM consent t WHERE t.source_id=s.source_id AND t.rev<=?1 ORDER BY rev DESC LIMIT 1),0),
 coalesce(c.retention_days,7),c.history_from_ms,c.history_until_ms,
 (SELECT max(observed_ms) FROM evidence e WHERE e.source_id=s.source_id AND e.rev<=?1),
 (SELECT max(occurred_ms) FROM evidence e WHERE e.source_id=s.source_id AND e.rev<=?1),
 coalesce((SELECT health FROM source_status st WHERE st.source_id=s.source_id AND st.rev<=?1 ORDER BY rev DESC LIMIT 1),''),
 (SELECT at_ms FROM source_status st WHERE st.source_id=s.source_id AND st.rev<=?1 ORDER BY rev DESC LIMIT 1),
 coalesce((SELECT a.client_version FROM adapter_record a JOIN evidence e USING(evidence_id) WHERE a.client_version<>'' AND e.source_id=s.source_id AND e.rev<=?1 ORDER BY e.evidence_id DESC LIMIT 1),''),
 coalesce((SELECT e.adapter_version FROM evidence e WHERE e.source_id=s.source_id AND e.rev<=?1 AND e.adapter_version IS NOT NULL ORDER BY e.rev DESC,e.evidence_id DESC LIMIT 1),
 (SELECT st.adapter_version FROM source_status st WHERE st.source_id=s.source_id AND st.rev<=?1 ORDER BY st.rev DESC LIMIT 1),''),
 EXISTS(SELECT 1 FROM evidence e WHERE e.source_id=s.source_id AND e.rev<=?1 AND e.record_kind NOT IN ('unsupported','source_gap'))
 FROM source s LEFT JOIN source_config c ON c.source_id=s.source_id AND c.rev=(
 SELECT max(t.rev) FROM source_config t WHERE t.source_id=s.source_id AND t.rev<=?1)
 WHERE EXISTS(SELECT 1 FROM change_scope WHERE scope_kind='source' AND scope_key=s.source_id AND rev<=?1)
 ORDER BY s.source_id`, p.Revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SourceInfo
	for rows.Next() {
		var v SourceInfo
		if err := rows.Scan(&v.ID, &v.Client, &v.Kind, &v.Enabled, &v.Consent, &v.RetentionDays, &v.FromMS, &v.UntilMS, &v.LastObservedMS, &v.LastEventMS, &v.Health, &v.CheckedMS, &v.ClientVersion, &v.AdapterVersion, &v.HasMetadata); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Expire removes expired evidence and leaves replay barriers. Untimed inputs use
// their original observed time, with opaque tombstones for matching later copies.
func (s *Store) Expire(ctx context.Context, now time.Time) error {
	p, err := s.Point(ctx)
	if err != nil {
		return err
	}
	sources, err := s.Sources(ctx, p)
	if err != nil {
		return err
	}
	for _, src := range sources {
		cut := now.AddDate(0, 0, -src.RetentionDays).UnixMilli()
		f := Filter{Source: src.ID, Kind: "all", FromMS: math.MinInt64, UntilMS: cut, retention: true}
		preview, err := s.Preview(ctx, f)
		if err != nil {
			return err
		}
		if preview.Evidence > 0 {
			if _, err = s.Delete(ctx, f, preview); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReportHealth records observed health transitions without generating a revision on each idle poll.
func (s *Store) ReportHealth(ctx context.Context, a *collect.Adapter, health string) error {
	if a == nil {
		return errInvalid
	}
	switch health {
	case "connected", "lost", "delayed", "unsupported":
	default:
		return errInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var old string
	err = tx.QueryRowContext(ctx, "SELECT health FROM source_status WHERE source_id=? ORDER BY rev DESC LIMIT 1", a.Source()).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if health == old {
		return nil
	}
	rev, err := s.revision(ctx, tx, a.Source())
	if err != nil {
		return err
	}
	capability := "partial"
	switch health {
	case "connected":
		capability = "available"
	case "unsupported":
		capability = "unavailable"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO source_status(source_id,rev,at_ms,health,adapter_version,permission_code,capability_code,capability_state)
 VALUES(?,?,?,?,?,'selected','connection',?)`, a.Source(), rev, s.now().UnixMilli(), health, collect.Version, capability)
	if err != nil {
		return err
	}
	return tx.Commit()
}
