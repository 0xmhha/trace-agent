package db

import (
	"context"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Settings returns the persisted configuration. A fresh database reports no
// consent, usage.DefaultWindowDays and a disabled extension endpoint.
func (s *Store) Settings(ctx context.Context) (usage.Settings, error) {
	return usage.Settings{}, errNotImplemented
}

// SetConsent records whether transcript collection is allowed.
func (s *Store) SetConsent(ctx context.Context, granted bool) error { return errNotImplemented }

// SetWindowDays sets the history window, 1 to usage.MaxWindowDays.
func (s *Store) SetWindowDays(ctx context.Context, days int) error { return errNotImplemented }

// SetExtension records whether the event endpoint is enabled and the port it
// last bound.
func (s *Store) SetExtension(ctx context.Context, enabled bool, port int) error {
	return errNotImplemented
}

// SetExcluded includes or excludes a project. Excluding deletes, in the same
// transaction, the project's stored rows, its file cursors and the extension
// events linked to its sessions.
func (s *Store) SetExcluded(ctx context.Context, project string, excluded bool) error {
	return errNotImplemented
}

// DataVersion returns a counter that increases whenever stored data changes.
func (s *Store) DataVersion(ctx context.Context) (int64, error) { return 0, errNotImplemented }
