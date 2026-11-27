// Package db persists usage records in a dedicated SQLite file and answers
// the aggregate queries of the usage pages. The file holds numbers and
// metadata only; see schema.sql.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"

	_ "github.com/mattn/go-sqlite3" // SQLite driver.
)

//go:embed schema.sql
var schema string

const (
	schemaVersion = 1
	maxOpenConns  = 4
	fileMode      = 0o600
	driverName    = "sqlite3"
	dirMode       = 0o700
	privateMask   = 0o077
	// Every explicit transaction takes the write lock at once (_txlock), so
	// read queries must run directly on the pool and never inside a
	// transaction.
	dsnOptions = "_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on&_secure_delete=on&_txlock=immediate"
)

// Setting keys. Booleans are stored as "1" or "0" and integers in decimal. A
// missing key means the default: no consent, usage.DefaultWindowDays, the
// endpoint disabled, port 0.
const (
	settingConsent    = "consent"
	settingWindowDays = "window_days"
	settingExtEnabled = "ext_enabled"
	settingExtPort    = "ext_port"
	settingOn         = "1"
	settingOff        = "0"
)

// toolDurationSQL is the duration in milliseconds of a closed tool call, for
// a query that joins tool_call as c to tool_result as r on (session_id, key).
const toolDurationSQL = "CASE WHEN r.duration_ms > 0 THEN r.duration_ms ELSE MAX(r.at_ms - c.at_ms, 0) END"

// skillQualifier separates the plugin from the skill in the full name of a
// plugin-provided skill: <plugin>:<skill>.
const skillQualifier = ":"

// Every read groups skills by their full name, so that one skill has one name
// on every page. A Skill tool call belongs to the group named by its stored
// skill. The transcript attributes a model call to the same full name and
// repeats the plugin beside it; skillGroupName and skillGroupSQL are the one
// rule that turns such an attribution into a group name, in Go and in SQL,
// and must stay identical.

// skillGroupName is the skill group of a model call attributed to skill and
// plugin, or "" when the call is attributed to no skill. A bare skill name
// beside a plugin is qualified, so that it joins the Skill calls that name
// the skill <plugin>:<skill> instead of forming a second group.
func skillGroupName(skill, plugin string) string {
	if skill == "" || plugin == "" || strings.Contains(skill, skillQualifier) {
		return skill
	}
	return plugin + skillQualifier + skill
}

// skillGroupSQL is skillGroupName as an SQL expression over the columns that
// hold the attributed skill and plugin of a model call.
func skillGroupSQL(skillColumn, pluginColumn string) string {
	return "CASE WHEN " + skillColumn + " = '' OR " + pluginColumn + " = ''" +
		" OR instr(" + skillColumn + ", '" + skillQualifier + "') > 0" +
		" THEN " + skillColumn +
		" ELSE " + pluginColumn + " || '" + skillQualifier + "' || " + skillColumn + " END"
}

// A root session is "in range" when any model_call or prompt row with its
// session_id, from any agent, has at_ms in [from, until). Sub-agent rows
// count toward their root session. Every read uses this definition.

// errNotImplemented marks a method whose owner has not filled it in yet.
var errNotImplemented = errors.New("usage/db: not implemented")

// Store is the usage database. It is safe for concurrent use.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open creates or opens the usage database at path with owner-only
// permissions and applies the schema to an empty file. now supplies the
// clock for scan and window bookkeeping.
func Open(ctx context.Context, path string, now func() time.Time) (*Store, error) {
	if path == "" || now == nil {
		return nil, errors.New("usage/db: path and clock are required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("open usage database: %w", err)
	}
	if err = os.MkdirAll(filepath.Dir(abs), dirMode); err != nil {
		return nil, fmt.Errorf("open usage database: %w", err)
	}
	if err = requirePrivateFile(abs); err != nil {
		return nil, fmt.Errorf("open usage database: %w", err)
	}
	uri := url.URL{Scheme: "file", Path: abs, RawQuery: dsnOptions}
	conn, err := sql.Open(driverName, uri.String())
	if err != nil {
		return nil, fmt.Errorf("open usage database: %w", err)
	}
	conn.SetMaxOpenConns(maxOpenConns)
	s := &Store{db: conn, now: now}
	if err = s.init(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

// requirePrivateFile creates the database file when missing and refuses a
// symbolic link, a non-regular file, or one readable by other users.
func requirePrivateFile(abs string) error {
	root, err := os.OpenRoot(filepath.Dir(abs))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(filepath.Base(abs), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, fileMode)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if closeErr := f.Close(); err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&privateMask != 0 {
		return errors.New("private database file required")
	}
	return nil
}

// init applies the schema to an empty file. The tables and the version are
// written in one transaction, so an interrupted first start leaves an empty
// file that the next start initializes again.
func (s *Store) init(ctx context.Context) (err error) {
	var version int
	if err = s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read usage schema version: %w", err)
	}
	if version == schemaVersion {
		return nil
	}
	if version != 0 {
		return fmt.Errorf("unsupported usage schema version %d", version)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("create usage schema: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create usage schema: %w", err)
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("create usage schema: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("create usage schema: %w", err)
	}
	return nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// toMS converts a time to the stored Unix-millisecond form; the zero time is 0.
func toMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// fromMS is the inverse of toMS. Times are returned in UTC.
func fromMS(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// queryer is the read side shared by *sql.DB and *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// readSetting returns the stored value of key; ok is false when it is unset.
func readSetting(ctx context.Context, q queryer, key string) (value string, ok bool, err error) {
	err = q.QueryRowContext(ctx, "SELECT value FROM setting WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %s: %w", key, err)
	}
	return value, true, nil
}

// windowFrom is the start of the collection window: now minus the configured
// window days.
func (s *Store) windowFrom(ctx context.Context) (time.Time, error) {
	days := usage.DefaultWindowDays
	if value, ok, err := readSetting(ctx, s.db, settingWindowDays); err != nil {
		return time.Time{}, err
	} else if ok {
		if n, convErr := strconv.Atoi(value); convErr == nil && n >= 1 && n <= usage.MaxWindowDays {
			days = n
		}
	}
	return s.now().AddDate(0, 0, -days), nil
}

// percentile returns the nearest-rank percentile p, in (0, 1], of values
// sorted in ascending order, or nil when there are none.
func percentile(sorted []float64, p float64) *float64 {
	if len(sorted) == 0 {
		return nil
	}
	rank := int(math.Ceil(p * float64(len(sorted))))
	rank = min(max(rank, 1), len(sorted))
	v := sorted[rank-1]
	return &v
}

// bump advances data_version inside tx so that readers refetch.
func bump(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "UPDATE data_version SET n = n + 1 WHERE singleton = 1"); err != nil {
		return fmt.Errorf("advance data version: %w", err)
	}
	return nil
}
