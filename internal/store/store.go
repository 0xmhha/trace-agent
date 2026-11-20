// Package store commits collected trace evidence to a private SQLite WAL database.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	contract "github.com/0xmhha/trace-agent/projects/trace-agent"
	// Register the database/sql driver used by this package.
	_ "github.com/mattn/go-sqlite3"
)

// Point fixes both the visible revision and the privacy policy generation.
type Point struct{ Revision, Epoch int64 }

// Record retains the evidence identity used by derived facts.
type Record struct {
	ID, Rev  int64
	Evidence collect.Evidence
}

// Store serializes writes. Senders use WithEpoch when handing a frame to a client.
type Store struct {
	db       *sql.DB
	gate     sync.RWMutex
	now      func() time.Time
	boundary func(string)
}

// Open initializes a private file or opens a supported existing database.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(abs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(filepath.Base(abs), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private database file required")
	}
	uri := url.URL{Scheme: "file", Path: abs, RawQuery: "_busy_timeout=5000&_foreign_keys=on&_secure_delete=on&_synchronous=FULL&_txlock=immediate"}
	db, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
	if err = s.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases all database handles.
func (s *Store) Close() error { return s.db.Close() }

const extensions = `
CREATE TABLE IF NOT EXISTS adapter_record(
 evidence_id INTEGER PRIMARY KEY REFERENCES evidence ON DELETE CASCADE,
 position INTEGER NOT NULL,client_version TEXT NOT NULL, metadata TEXT NOT NULL CHECK(json_valid(metadata)),
 session_key TEXT NOT NULL,task_key TEXT NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS adapter_item(
 record_key TEXT PRIMARY KEY, source_id TEXT NOT NULL REFERENCES source, state TEXT NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS expired_undated(source_id TEXT NOT NULL REFERENCES source,semantic_tag TEXT NOT NULL,PRIMARY KEY(source_id,semantic_tag)) STRICT;
CREATE TABLE IF NOT EXISTS erased_record(
 source_id TEXT NOT NULL REFERENCES source, record_key TEXT NOT NULL,
 semantic_tag TEXT NOT NULL, PRIMARY KEY(source_id,record_key,semantic_tag)
) STRICT;`

func (s *Store) initialize(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	// The original v1.1 DDL did not set user_version. Only the exact published
	// schema fingerprint is eligible for that legacy path.
	if version == 0 {
		var shape string
		if err := s.db.QueryRowContext(ctx, "SELECT coalesce(group_concat(sql,char(10)),'') FROM (SELECT sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' AND sql IS NOT NULL ORDER BY type,name)").Scan(&shape); err != nil {
			return err
		}
		if shape != "" {
			sum := sha256.Sum256([]byte(shape))
			if hex.EncodeToString(sum[:]) != "bd97d4bc37502882b4d21aca16bc030dbb3f97df7af2fd31915549de72ac7b84" {
				return errors.New("unknown unversioned schema")
			}
			version = 11
		}
	}
	if version != 0 && version != 11 && version != 12 && version != 13 {
		return fmt.Errorf("unknown database version %d", version)
	}
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return err
	}
	if mode != "wal" {
		return errors.New("WAL mode unavailable")
	}
	if version == 11 {
		if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	switch version {
	case 0:
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return errors.New("unversioned nonempty database")
		}
		_, err = tx.ExecContext(ctx, contract.Schema)
	case 11, 12:
		err = migrate(ctx, tx, version)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, extensions); err != nil {
		return err
	}
	var bad int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM pragma_foreign_key_check").Scan(&bad); err != nil {
		return err
	}
	if bad != 0 {
		return errors.New("database contains invalid foreign keys")
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if version == 11 {
		_, err = s.db.ExecContext(ctx, "PRAGMA foreign_keys=ON")
	}
	return err
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func point(ctx context.Context, q querier) (Point, error) {
	var p Point
	err := q.QueryRowContext(ctx, "SELECT (SELECT max(rev) FROM revision),epoch FROM store_state WHERE singleton=1").Scan(&p.Revision, &p.Epoch)
	return p, err
}

// Point returns the latest fully committed revision and epoch.
func (s *Store) Point(ctx context.Context) (Point, error) { return point(ctx, s.db) }

// CommittedAt returns the clock of the specified revision.
func (s *Store) CommittedAt(ctx context.Context, rev int64) (time.Time, error) {
	var ms int64
	err := s.db.QueryRowContext(ctx, "SELECT committed_ms FROM revision WHERE rev=?", rev).Scan(&ms)
	return time.UnixMilli(ms), err
}

// WithEpoch serializes the last privacy check and a bounded frame write with policy changes.
func (s *Store) WithEpoch(ctx context.Context, epoch int64, send func() error) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	p, err := s.Point(ctx)
	if err != nil {
		return err
	}
	if epoch != p.Epoch {
		return ErrStale
	}
	return send()
}

// ErrStale indicates a stale checkpoint, preview, snapshot, or writer version.
var ErrStale = errors.New("stale trace state")

func (s *Store) revision(ctx context.Context, tx *sql.Tx, source string) (int64, error) {
	result, err := tx.ExecContext(ctx, "INSERT INTO revision(committed_ms) VALUES(?)", s.now().UnixMilli())
	if err != nil {
		return 0, err
	}
	rev, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO change_scope VALUES(?,'source',?)", rev, source)
	return rev, err
}
