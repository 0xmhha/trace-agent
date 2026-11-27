// Package timeline builds the display timeline of one transcript stream from
// the original file. Text is masked with usage.Mask and capped before it
// leaves this package; nothing is persisted.
package timeline

import (
	"errors"
	"io"

	"github.com/0xmhha/trace-agent/internal/usage"
)

var errNotImplemented = errors.New("usage/timeline: not implemented")

// Read returns one page of timeline items from the transcript r. The cursor
// in req is the opaque value of a previous page's NextCursor, or empty for
// the first page. It encodes the byte offset of the next line together with
// the tool calls still open at that point (ID, name, category, time), so a
// result on a later page can name its call and report a duration; a cursor that does not point at the start of a line returns
// usage.ErrBadRequest. Page.Source is usage.SourceAvailable on success.
// Malformed or oversized lines are skipped and counted in Page.Skipped. Every
// text field is cut to usage.MaxMaskInput, masked, then cut to
// usage.MaxTimelineText. Tool input is masked value by value on the decoded
// JSON and then re-encoded, so that escaped quotes cannot hide a secret.
func Read(r io.ReadSeeker, req usage.TimelineRequest) (usage.TimelinePage, error) {
	return usage.TimelinePage{}, errNotImplemented
}
