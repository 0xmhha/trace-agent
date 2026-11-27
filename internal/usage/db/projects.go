package db

import (
	"context"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Projects aggregates activity by project for the query range. Excluded
// projects are listed with zero counts so the UI can re-include them.
// The Coverage field is left zero for the service to fill.
func (s *Store) Projects(ctx context.Context, q usage.Query) (usage.Projects, error) {
	return usage.Projects{}, errNotImplemented
}
