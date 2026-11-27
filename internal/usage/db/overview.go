package db

import (
	"context"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Overview aggregates the dashboard numbers of the query range. loc decides
// day boundaries, active days and the weekday-hour heatmap.
// The Coverage field is left zero for the service to fill.
func (s *Store) Overview(ctx context.Context, q usage.Query, loc *time.Location) (usage.Overview, error) {
	return usage.Overview{}, errNotImplemented
}
