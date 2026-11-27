package db

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Limits of the stored settings.
const (
	settingMinWindowDays = 1
	settingMinPort       = 0
	settingMaxPort       = 65535
)

// Statements of the settings methods.
const (
	settingUpsertSQL   = `INSERT INTO setting(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`
	settingExcludedSQL = `SELECT project_key FROM project WHERE excluded = 1 ORDER BY project_key`
	settingVersionSQL  = `SELECT n FROM data_version WHERE singleton = 1`
	// The row is created when the project has never been scanned, so that a
	// project can be excluded by name and listed for re-inclusion.
	settingExcludeSQL = `INSERT INTO project(project_key,excluded) VALUES(?,1)
		ON CONFLICT(project_key) DO UPDATE SET excluded=1 WHERE project.excluded=0`
	settingIncludeSQL = `UPDATE project SET excluded=0 WHERE project_key = ? AND excluded = 1`
	// Extension events carry no project, so they are reached through the
	// sessions of the project. This must run before the streams are deleted.
	// settingRememberExcludedSQL records the sessions of a project that is
	// being excluded; settingForgetExcludedSQL drops them on re-inclusion.
	settingRememberExcludedSQL = `INSERT OR IGNORE INTO excluded_session(session_id, project_key)
		SELECT DISTINCT session_id, project_key FROM stream WHERE project_key = ?`
	settingForgetExcludedSQL = `DELETE FROM excluded_session WHERE project_key = ?`
	settingProjectExtSQL     = `DELETE FROM ext_event WHERE session_id <> '' AND session_id IN (
		SELECT session_id FROM stream WHERE project_key = ?)`
)

// Operation names used as the prefix of returned errors.
const (
	settingOpRead      = "read usage settings"
	settingOpConsent   = "set usage consent"
	settingOpWindow    = "set usage window"
	settingOpExtension = "set extension endpoint"
	settingOpExclude   = "set project exclusion"
	settingOpVersion   = "read data version"
)

// settingChange is one value to store. fallback is the value a missing key
// stands for, so that writing the default over nothing counts as no change.
type settingChange struct {
	key      string
	value    string
	fallback string
}

// settingFlag encodes a boolean setting.
func settingFlag(v bool) string {
	if v {
		return settingOn
	}
	return settingOff
}

// settingInt reads an integer setting. A missing or out-of-range value
// yields fallback, the same way windowFrom treats it.
func settingInt(ctx context.Context, q queryer, key string, lo, hi, fallback int) (int, error) {
	value, ok, err := readSetting(ctx, q, key)
	if err != nil || !ok {
		return fallback, err
	}
	n, convErr := strconv.Atoi(value)
	if convErr != nil || n < lo || n > hi {
		return fallback, nil
	}
	return n, nil
}

// settingBool reads a boolean setting; a missing key is false.
func settingBool(ctx context.Context, q queryer, key string) (bool, error) {
	value, _, err := readSetting(ctx, q, key)
	return value == settingOn, err
}

// settingStore writes changes in one transaction and advances data_version
// once when at least one stored value differs from the new one.
func (s *Store) settingStore(ctx context.Context, op string, changes ...settingChange) error {
	return s.ingestInTx(ctx, op, func(tx *sql.Tx) error {
		changed := false
		for _, c := range changes {
			current, ok, err := readSetting(ctx, tx, c.key)
			if err != nil {
				return err
			}
			if !ok {
				current = c.fallback
			}
			if current == c.value {
				continue
			}
			if _, err = tx.ExecContext(ctx, settingUpsertSQL, c.key, c.value); err != nil {
				return fmt.Errorf("write setting %s: %w", c.key, err)
			}
			changed = true
		}
		if !changed {
			return nil
		}
		return bump(ctx, tx)
	})
}

// Settings returns the persisted configuration. A fresh database reports no
// consent, usage.DefaultWindowDays and a disabled extension endpoint.
func (s *Store) Settings(ctx context.Context) (usage.Settings, error) {
	var out usage.Settings
	var err error
	if out.Consent, err = settingBool(ctx, s.db, settingConsent); err != nil {
		return usage.Settings{}, fmt.Errorf("%s: %w", settingOpRead, err)
	}
	if out.WindowDays, err = settingInt(ctx, s.db, settingWindowDays, settingMinWindowDays, usage.MaxWindowDays, usage.DefaultWindowDays); err != nil {
		return usage.Settings{}, fmt.Errorf("%s: %w", settingOpRead, err)
	}
	if out.ExtEnabled, err = settingBool(ctx, s.db, settingExtEnabled); err != nil {
		return usage.Settings{}, fmt.Errorf("%s: %w", settingOpRead, err)
	}
	if out.ExtPort, err = settingInt(ctx, s.db, settingExtPort, settingMinPort, settingMaxPort, settingMinPort); err != nil {
		return usage.Settings{}, fmt.Errorf("%s: %w", settingOpRead, err)
	}
	if out.Excluded, err = s.settingExcluded(ctx); err != nil {
		return usage.Settings{}, fmt.Errorf("%s: %w", settingOpRead, err)
	}
	return out, nil
}

// settingExcluded lists the excluded project keys in ascending order. The
// result is never nil.
func (s *Store) settingExcluded(ctx context.Context) (keys []string, err error) {
	rows, err := s.db.QueryContext(ctx, settingExcludedSQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	keys = []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

// SetConsent records whether transcript collection is allowed.
func (s *Store) SetConsent(ctx context.Context, granted bool) error {
	return s.settingStore(ctx, settingOpConsent,
		settingChange{key: settingConsent, value: settingFlag(granted), fallback: settingOff})
}

// SetWindowDays sets the history window, 1 to usage.MaxWindowDays.
func (s *Store) SetWindowDays(ctx context.Context, days int) error {
	if days < settingMinWindowDays || days > usage.MaxWindowDays {
		return fmt.Errorf("%s: %d days: %w", settingOpWindow, days, usage.ErrBadRequest)
	}
	return s.settingStore(ctx, settingOpWindow,
		settingChange{key: settingWindowDays, value: strconv.Itoa(days), fallback: strconv.Itoa(usage.DefaultWindowDays)})
}

// SetExtension records whether the event endpoint is enabled and the port it
// last bound.
func (s *Store) SetExtension(ctx context.Context, enabled bool, port int) error {
	if port < settingMinPort || port > settingMaxPort {
		return fmt.Errorf("%s: port %d: %w", settingOpExtension, port, usage.ErrBadRequest)
	}
	return s.settingStore(ctx, settingOpExtension,
		settingChange{key: settingExtEnabled, value: settingFlag(enabled), fallback: settingOff},
		settingChange{key: settingExtPort, value: strconv.Itoa(port), fallback: strconv.Itoa(settingMinPort)})
}

// SetExcluded includes or excludes a project. Excluding deletes, in the same
// transaction, the project's stored rows, its file cursors and the extension
// events linked to its sessions.
func (s *Store) SetExcluded(ctx context.Context, project string, excluded bool) error {
	if project == "" {
		return fmt.Errorf("%s: project key is required: %w", settingOpExclude, usage.ErrBadRequest)
	}
	return s.ingestInTx(ctx, settingOpExclude, func(tx *sql.Tx) error {
		if !excluded {
			changed, err := ingestExec(ctx, tx, settingIncludeSQL, project)
			if err != nil || changed == 0 {
				return err
			}
			if _, err = ingestExec(ctx, tx, settingForgetExcludedSQL, project); err != nil {
				return fmt.Errorf("forget excluded sessions: %w", err)
			}
			return bump(ctx, tx)
		}
		changed, err := ingestExec(ctx, tx, settingExcludeSQL, project)
		if err != nil {
			return err
		}
		// The rows are deleted even when the project was excluded already:
		// it costs nothing when there are none and repairs a store that
		// somehow still holds some.
		deleted, err := settingDeleteProject(ctx, tx, project)
		if err != nil {
			return err
		}
		if changed+deleted == 0 {
			return nil
		}
		return bump(ctx, tx)
	})
}

// settingDeleteProject deletes everything stored for project except its
// project row, and returns the number of rows deleted.
func settingDeleteProject(ctx context.Context, tx *sql.Tx, project string) (int64, error) {
	// The sessions are remembered before their streams go, so that later
	// extension events for them are still recognised as excluded.
	if _, err := ingestExec(ctx, tx, settingRememberExcludedSQL, project); err != nil {
		return 0, fmt.Errorf("remember excluded sessions: %w", err)
	}
	deleted, err := ingestExec(ctx, tx, settingProjectExtSQL, project)
	if err != nil {
		return 0, fmt.Errorf("delete linked extension events: %w", err)
	}
	for _, table := range ingestTables() {
		n, err := ingestExec(ctx, tx, "DELETE FROM "+table.name+" WHERE project_key = ?", project)
		if err != nil {
			return 0, fmt.Errorf("delete %s rows: %w", table.name, err)
		}
		deleted += n
	}
	return deleted, nil
}

// DataVersion returns a counter that increases whenever stored data changes.
func (s *Store) DataVersion(ctx context.Context) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx, settingVersionSQL).Scan(&n); err != nil {
		return 0, fmt.Errorf("%s: %w", settingOpVersion, err)
	}
	return n, nil
}
