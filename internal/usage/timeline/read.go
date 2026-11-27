// Package timeline builds the display timeline of one transcript stream from
// the original file. Text is masked with usage.Mask and capped before it
// leaves this package; nothing is persisted.
package timeline

import (
	"errors"
	"fmt"
	"io"

	"github.com/0xmhha/trace-agent/internal/usage"
)

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
	return read(r, req, usage.MaxLineBytes)
}

// read is Read with the line size limit as a parameter, so that tests can
// exercise the oversized-line path without building a 16 MiB line each time.
func read(r io.ReadSeeker, req usage.TimelineRequest, maxLine int) (usage.TimelinePage, error) {
	if r == nil {
		return usage.TimelinePage{}, fmt.Errorf("%w: no transcript reader", usage.ErrBadRequest)
	}
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return usage.TimelinePage{}, err
	}
	offset, st, err := decodeCursor(req.Cursor)
	if err != nil {
		return usage.TimelinePage{}, err
	}
	if err := seekLineStart(r, offset); err != nil {
		return usage.TimelinePage{}, err
	}

	page := usage.TimelinePage{Source: usage.SourceAvailable, Items: []usage.TimelineItem{}}
	conv := converter{sessionID: req.SessionID, agentID: req.AgentID}
	lines := newLineReader(r, offset, maxLine)
	for {
		ln, err := lines.next()
		if errors.Is(err, io.EOF) {
			return page, nil
		}
		if err != nil {
			return usage.TimelinePage{}, fmt.Errorf("read transcript: %w", err)
		}
		if ln.oversized {
			page.Skipped++
			continue
		}
		// Once the page is full the reader keeps going over lines that add
		// nothing, so that NextCursor is empty exactly when no item is left.
		// The first line that would add items is undone and starts the next
		// page, which keeps all items of one line on one page.
		full := len(page.Items) >= limit
		var before state
		if full {
			before = st.clone()
		}
		items, ok := conv.line(ln.data, ln.start, &st)
		if !ok {
			// An unterminated last line is most likely still being written.
			if ln.terminated {
				page.Skipped++
			}
			continue
		}
		if full && len(items) > 0 {
			page.NextCursor, err = encodeCursor(ln.start, before)
			if err != nil {
				return usage.TimelinePage{}, err
			}
			return page, nil
		}
		page.Items = append(page.Items, items...)
	}
}

// pageLimit applies the default and the cap of the page size.
func pageLimit(requested int) (int, error) {
	switch {
	case requested < 0:
		return 0, fmt.Errorf("%w: negative limit", usage.ErrBadRequest)
	case requested == 0:
		return usage.DefaultPageSize, nil
	case requested > usage.MaxPageSize:
		return usage.MaxPageSize, nil
	}
	return requested, nil
}

// seekLineStart positions r at offset and verifies that a line starts there:
// the offset is zero or the byte before it is a newline. An offset inside a
// line would hand a fragment of that line to the parser.
func seekLineStart(r io.ReadSeeker, offset int64) error {
	if offset == 0 {
		if _, err := r.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("seek transcript: %w", err)
		}
		return nil
	}
	if _, err := r.Seek(offset-1, io.SeekStart); err != nil {
		return fmt.Errorf("seek transcript: %w", err)
	}
	var prev [1]byte
	if _, err := io.ReadFull(r, prev[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: cursor beyond the end of the transcript", usage.ErrBadRequest)
		}
		return fmt.Errorf("read transcript: %w", err)
	}
	if prev[0] != '\n' {
		return fmt.Errorf("%w: cursor is not at the start of a line", usage.ErrBadRequest)
	}
	return nil
}
