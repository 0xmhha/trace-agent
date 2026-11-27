package db

import (
	"context"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Cursor returns the stored read position of a transcript file. ok is false
// when the file has not been scanned yet.
func (s *Store) Cursor(ctx context.Context, path string) (cursor usage.FileCursor, ok bool, err error) {
	return usage.FileCursor{}, false, errNotImplemented
}

// Apply stores one batch and advances the file cursor to batch.NextOffset in
// a single transaction. It returns usage.ErrStale and stores nothing when
// consent is not granted, when the file's project is excluded, or when
// batch.BaseOffset is neither zero nor
// the stored cursor offset (zero when the file has no cursor). A batch with
// BaseOffset zero restarts the file: its Skipped replaces the stored count;
// otherwise Skipped is added. Zero session times never overwrite stored
// ones, batch.Cost is stored only for the root transcript, and data_version
// advances only when the batch holds at least one record or session
// attribute. The rows written match seedBatch in testutil_test.go.
func (s *Store) Apply(ctx context.Context, file usage.FileRef, batch usage.Batch) error {
	return errNotImplemented
}

// MarkUnreadable records that the last scan could not read the file, so that
// coverage reports it instead of silently showing less data. The flag is
// cleared by the next successful Apply. data_version advances only when the
// flag changes.
func (s *Store) MarkUnreadable(ctx context.Context, file usage.FileRef) error {
	return errNotImplemented
}

// Clear deletes every usage row, extension event, rejection counter and file
// cursor. Settings and the excluded-project list are kept. Extension events
// cannot be rebuilt afterwards.
func (s *Store) Clear(ctx context.Context) error { return errNotImplemented }

// Prune deletes usage rows older than usageBefore and extension events older
// than extBefore, then drops the oldest extension events beyond
// usage.MaxExtEvents. A zero time skips that part.
func (s *Store) Prune(ctx context.Context, usageBefore, extBefore time.Time) error {
	return errNotImplemented
}

// ResetCursors sets every file cursor back to offset zero so that the next
// scan re-reads all tracked files. Stored rows stay; re-reading is idempotent.
func (s *Store) ResetCursors(ctx context.Context) error { return errNotImplemented }
