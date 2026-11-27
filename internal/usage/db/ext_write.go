package db

import (
	"context"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// AppendExt stores extension events. An event whose ID is already stored is
// ignored.
func (s *Store) AppendExt(ctx context.Context, events []usage.ExtEvent) error {
	return errNotImplemented
}

// CountRejected adds n refused inputs to the sender's rejection counter.
func (s *Store) CountRejected(ctx context.Context, source string, n int) error {
	return errNotImplemented
}
