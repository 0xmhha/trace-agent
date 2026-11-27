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
//
// Once a page is full, Read looks at most maxTailLines further lines for the
// next item. When none of them adds one, the page ends there with a
// NextCursor at the following line, so NextCursor is empty only at the end
// of the transcript.
func Read(r io.ReadSeeker, req usage.TimelineRequest) (usage.TimelinePage, error) {
	return read(r, req, usage.MaxLineBytes)
}

// maxTailLines bounds the itemless lines read after a page is full. Without
// it a page over a transcript whose rest adds no item would read to the end
// of the file, however large, before it could answer.
const maxTailLines = 10_000

// read is Read with the line size limit as a parameter, so that tests can
// exercise the oversized-line path without building a 16 MiB line each time.
func read(r io.ReadSeeker, req usage.TimelineRequest, maxLine int) (usage.TimelinePage, error) {
	return readBounded(r, req, maxLine, maxTailLines)
}

// readBounded is read with the itemless tail bound as a parameter, so that
// tests can reach it without writing ten thousand lines.
func readBounded(r io.ReadSeeker, req usage.TimelineRequest, maxLine, maxTail int) (usage.TimelinePage, error) {
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
	// tail counts the itemless lines read since the page became full.
	tail := 0
	for {
		ln, err := lines.next()
		if errors.Is(err, io.EOF) {
			return page, nil
		}
		if err != nil {
			return usage.TimelinePage{}, fmt.Errorf("read transcript: %w", err)
		}
		// Once the page is full the reader keeps going over lines that add
		// nothing, so that NextCursor is empty exactly when no item is left.
		// The first line that would add items is undone and starts the next
		// page, which keeps all items of one line on one page.
		full := len(page.Items) >= limit
		items, ok, before := convertLine(&conv, ln, &st, full, &page)
		if full && len(items) > 0 {
			page.NextCursor, err = encodeCursor(ln.start, before)
			if err != nil {
				return usage.TimelinePage{}, err
			}
			return page, nil
		}
		if !full {
			if ok {
				page.Items = append(page.Items, items...)
			}
			continue
		}
		tail++
		// Only a terminated line ends where the next one starts; a cursor
		// anywhere else would be refused.
		if tail >= maxTail && ln.terminated {
			page.NextCursor, err = encodeCursor(lines.off, st)
			if err != nil {
				return usage.TimelinePage{}, err
			}
			return page, nil
		}
	}
}

// convertLine turns one line into items and counts it as skipped when it
// cannot be read. before is the state ahead of the line, kept only when the
// page is full, so that the line can be undone and start the next page.
func convertLine(conv *converter, ln line, st *state, full bool, page *usage.TimelinePage) (items []usage.TimelineItem, ok bool, before state) {
	if ln.oversized {
		page.Skipped++
		return nil, false, before
	}
	if full {
		before = st.clone()
	}
	items, ok = conv.line(ln.data, ln.start, st)
	// An unterminated last line is most likely still being written.
	if !ok {
		if ln.terminated {
			page.Skipped++
		}
		return nil, false, before
	}
	return items, true, before
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
