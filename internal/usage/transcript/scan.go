// Package transcript turns Claude Code transcript lines into usage records.
// It extracts numbers and names only and never returns message text.
package transcript

import (
	"errors"
	"io"

	"github.com/0xmhha/trace-agent/internal/usage"
)

var errNotImplemented = errors.New("usage/transcript: not implemented")

// Scan reads complete JSONL lines from r, which is positioned at absolute
// file offset in.Base, until EOF or until the first line boundary at or after
// in.MaxBytes. A trailing line without a newline is left for the next call.
// Malformed lines never stop the scan: they are skipped and counted as
// described on usage.Batch. A line longer than usage.MaxLineBytes is skipped
// without being held in memory. Lines of another session are counted in
// Batch.Foreign when in.SessionID is set. Batch.BaseOffset is in.Base and
// Batch.NextOffset the absolute offset after the last complete line. An
// error is returned only when reading r fails.
func Scan(r io.Reader, in usage.ScanInput) (usage.Batch, error) {
	return usage.Batch{BaseOffset: in.Base, NextOffset: in.Base}, errNotImplemented
}
