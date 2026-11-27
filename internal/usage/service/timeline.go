package service

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

const (
	// maxTimelineExtEvents bounds the extension events considered for one
	// timeline page. The newest are kept; the rest stay visible through
	// extEvents.
	maxTimelineExtEvents = 1000
	// extItemPrefix keeps the ID of an inserted item apart from the IDs the
	// timeline reader derives from transcript lines.
	extItemPrefix = "ext:"
)

func (s *Service) handleTimeline(ctx context.Context, params json.RawMessage) (any, error) {
	request, err := decode[usage.TimelineRequest](params)
	if err != nil {
		return nil, err
	}
	if request.Limit, err = pageLimit(request.Limit); err != nil {
		return nil, err
	}
	if err = errors.Join(
		checkName("sessionId", request.SessionID, true),
		checkName("agentId", request.AgentID, false),
		checkCursor(request.Cursor),
	); err != nil {
		return nil, err
	}
	if err = s.requireConsent(ctx); err != nil {
		return nil, err
	}
	rel, stored, err := s.cfg.Store.StreamFile(ctx, request.SessionID, request.AgentID)
	if err != nil {
		return nil, fmt.Errorf("locate transcript: %w", err)
	}
	if !stored {
		return nil, fmt.Errorf("timeline stream: %w", usage.ErrNotFound)
	}
	page, err := s.readTimeline(ctx, rel, request)
	if err != nil {
		return nil, err
	}
	if len(page.Items) > 0 {
		events, eventsErr := s.sessionExtEvents(ctx, request.SessionID)
		if eventsErr != nil {
			return nil, eventsErr
		}
		page.Items = mergeExt(page.Items, events)
	}
	return &page, nil
}

// readTimeline reads one page from the original transcript. A transcript
// that is gone or cannot be read is a state of the page, not an error; only
// a request the reader rejects is.
func (s *Service) readTimeline(ctx context.Context, rel string, request usage.TimelineRequest) (usage.TimelinePage, error) {
	select {
	case s.timelineGate <- struct{}{}:
	case <-ctx.Done():
		return usage.TimelinePage{}, ctx.Err()
	}
	defer func() { <-s.timelineGate }()

	f, source := s.openTranscript(rel)
	if f == nil {
		return usage.TimelinePage{Source: source}, nil
	}
	defer func() { _ = f.Close() }()
	page, err := s.cfg.Timeline(newCtxReader(ctx, f), request)
	if errors.Is(err, usage.ErrBadRequest) {
		return usage.TimelinePage{}, fmt.Errorf("read timeline: %w", err)
	}
	if ctxErr := ctx.Err(); err != nil && ctxErr != nil {
		// The request ran out of time or was abandoned; the transcript is
		// not at fault.
		return usage.TimelinePage{}, fmt.Errorf("read timeline: %w", ctxErr)
	}
	if err != nil {
		// The file opened but reading it failed part-way.
		return usage.TimelinePage{Source: usage.SourceUnreadable}, nil
	}
	page.Source = usage.SourceAvailable
	return page, nil
}

// probeTranscript reports whether the original transcript can still be
// opened, without reading it.
func (s *Service) probeTranscript(rel string) usage.SourceState {
	f, source := s.openTranscript(rel)
	if f != nil {
		_ = f.Close()
	}
	return source
}

// openTranscript opens a stored transcript path through an os.Root of the
// transcript root, so the path cannot leave it. Only a regular .jsonl file
// is opened. The file is nil unless the state is usage.SourceAvailable.
func (s *Service) openTranscript(rel string) (*os.File, usage.SourceState) {
	name := filepath.FromSlash(rel)
	if !strings.HasSuffix(rel, transcriptExt) || !filepath.IsLocal(name) {
		return nil, usage.SourceUnreadable
	}
	root, err := os.OpenRoot(s.cfg.Root)
	if err != nil {
		return nil, sourceState(err)
	}
	defer func() { _ = root.Close() }()
	// Lstat first: a symbolic link or a FIFO is refused without opening it.
	info, err := root.Lstat(name)
	if err != nil {
		return nil, sourceState(err)
	}
	if !info.Mode().IsRegular() {
		return nil, usage.SourceUnreadable
	}
	f, _, err := openRegular(root, rel)
	if err != nil {
		return nil, sourceState(err)
	}
	return f, usage.SourceAvailable
}

// sourceState tells "no such file" from "there, but not readable".
func sourceState(err error) usage.SourceState {
	if errors.Is(err, fs.ErrNotExist) {
		return usage.SourceMissing
	}
	return usage.SourceUnreadable
}

// sessionExtEvents collects the extension events linked to a session, newest
// first, up to maxTimelineExtEvents.
func (s *Service) sessionExtEvents(ctx context.Context, sessionID string) ([]usage.ExtEvent, error) {
	var events []usage.ExtEvent
	q := usage.ExtQuery{SessionID: sessionID, Limit: usage.MaxPageSize}
	for len(events) < maxTimelineExtEvents {
		page, err := s.cfg.Store.ExtEvents(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("read extension events: %w", err)
		}
		events = append(events, page.Items...)
		// A cursor that does not move would loop for ever.
		if page.NextCursor == "" || page.NextCursor == q.Cursor || len(page.Items) == 0 {
			break
		}
		q.Cursor = page.NextCursor
	}
	return events[:min(len(events), maxTimelineExtEvents)], nil
}

// mergeExt inserts extension events into a timeline page as ext items. An
// event with a tool-use ID goes directly after the tool_call item with that
// ID and is left out when the page has no such item. An event without one
// goes before the first item that is later in time, and only when it lies
// inside the time span of the page. Events placed at the same spot are in
// time order.
func mergeExt(items []usage.TimelineItem, events []usage.ExtEvent) []usage.TimelineItem {
	if len(items) == 0 || len(events) == 0 {
		return items
	}
	ordered := slices.Clone(events)
	slices.SortStableFunc(ordered, func(a, b usage.ExtEvent) int {
		return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.ID, b.ID))
	})
	calls := toolCallIndex(items)
	first, last, timed := timeSpan(items)
	// after[i] follows item i; before[i] precedes it, and before[len(items)]
	// ends the page.
	after := make(map[int][]usage.ExtEvent)
	before := make(map[int][]usage.ExtEvent)
	placed := 0
	for _, event := range ordered {
		if event.ToolUseID != "" {
			if i, ok := calls[event.ToolUseID]; ok {
				after[i] = append(after[i], event)
				placed++
			}
			continue
		}
		if !timed || event.At.IsZero() || event.At.Before(first) || event.At.After(last) {
			continue
		}
		i := firstLater(items, event.At)
		before[i] = append(before[i], event)
		placed++
	}
	if placed == 0 {
		return items
	}
	merged := make([]usage.TimelineItem, 0, len(items)+placed)
	for i, item := range items {
		merged = appendExt(merged, before[i])
		merged = append(merged, item)
		merged = appendExt(merged, after[i])
	}
	return appendExt(merged, before[len(items)])
}

func appendExt(items []usage.TimelineItem, events []usage.ExtEvent) []usage.TimelineItem {
	for _, event := range events {
		items = append(items, usage.TimelineItem{
			ID:   extItemPrefix + event.ID,
			At:   event.At,
			Kind: usage.ItemExt,
			Ext:  &event,
		})
	}
	return items
}

// toolCallIndex maps a tool-use ID to the first tool_call item that carries it.
func toolCallIndex(items []usage.TimelineItem) map[string]int {
	calls := make(map[string]int)
	for i, item := range items {
		if item.Kind != usage.ItemToolCall || item.Tool == nil || item.Tool.UseID == "" {
			continue
		}
		if _, dup := calls[item.Tool.UseID]; !dup {
			calls[item.Tool.UseID] = i
		}
	}
	return calls
}

// timeSpan is the earliest and latest time of the items that carry one. ok
// is false when none does.
func timeSpan(items []usage.TimelineItem) (first, last time.Time, ok bool) {
	for _, item := range items {
		if item.At.IsZero() {
			continue
		}
		if !ok || item.At.Before(first) {
			first = item.At
		}
		if !ok || item.At.After(last) {
			last = item.At
		}
		ok = true
	}
	return first, last, ok
}

// firstLater is the index of the first item later than at, or len(items).
func firstLater(items []usage.TimelineItem, at time.Time) int {
	for i, item := range items {
		if !item.At.IsZero() && item.At.After(at) {
			return i
		}
	}
	return len(items)
}
