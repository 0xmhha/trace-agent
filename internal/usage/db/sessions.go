package db

import (
	"context"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Sessions returns one page of root sessions with activity in the query
// range. Counts and tokens cover only that range and include sub-agents. The
// Coverage field is left zero for the service to fill.
func (s *Store) Sessions(ctx context.Context, q usage.SessionQuery) (usage.SessionPage, error) {
	return usage.SessionPage{}, errNotImplemented
}

// Session returns the whole-session detail regardless of any range. Source is
// left empty for the service to fill. It returns usage.ErrNotFound for an
// unknown session.
func (s *Store) Session(ctx context.Context, sessionID string) (usage.SessionDetail, error) {
	return usage.SessionDetail{}, errNotImplemented
}

// StreamFile returns the root-relative transcript path of one stream. ok is
// false when the stream is not stored.
func (s *Store) StreamFile(ctx context.Context, sessionID, agentID string) (path string, ok bool, err error) {
	return "", false, errNotImplemented
}
