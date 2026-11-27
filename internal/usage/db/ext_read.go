package db

import (
	"context"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// ExtSources aggregates extension events of the query range by sender, one
// row per sender with at least one event in the range. rejected is the
// cumulative number of refused inputs across all senders, including those
// that could not be attributed.
func (s *Store) ExtSources(ctx context.Context, q usage.Query) (sources []usage.ExtSourceRow, rejected int64, err error) {
	return nil, 0, errNotImplemented
}

// ExtEvents returns one page of extension events, newest first. A zero bound
// of q.Range is unbounded on that side.
func (s *Store) ExtEvents(ctx context.Context, q usage.ExtQuery) (usage.ExtPage, error) {
	return usage.ExtPage{}, errNotImplemented
}
