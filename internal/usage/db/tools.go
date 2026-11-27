package db

import (
	"context"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Tools aggregates tool, MCP server, skill, plugin, sub-agent, Bash program
// and hook usage of the query range.
// The Coverage field is left zero for the service to fill.
func (s *Store) Tools(ctx context.Context, q usage.Query) (usage.Tools, error) {
	return usage.Tools{}, errNotImplemented
}
