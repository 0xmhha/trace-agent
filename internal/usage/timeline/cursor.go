package timeline

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

const (
	// cursorVersion is bumped whenever the cursor layout changes, so that a
	// cursor of an older build is refused instead of being misread.
	cursorVersion = 1
	// maxOpenCalls caps the tool calls a cursor carries. Calls that never get
	// a result would otherwise make every later cursor of the stream grow.
	maxOpenCalls = 256
	// maxIDLen is the longest tool-use, message or line ID the reader keeps.
	// Real IDs are a few dozen bytes; the bound keeps a cursor small no
	// matter what the transcript holds.
	maxIDLen = 128
	// maxCursorLen is the longest cursor accepted. It is above the largest
	// cursor encodeCursor can produce: maxOpenCalls entries whose ID and name
	// are bounded by maxIDLen and usage.MaxLabelLen stay below 700 KiB even
	// when every character needs a six-byte JSON escape.
	maxCursorLen = 1 << 20
)

// openCall is a tool call whose result has not been read yet. AtMs is the
// call time in Unix milliseconds, or zero when the line carried none.
type openCall struct {
	ID       string             `json:"i"`
	Name     string             `json:"n"`
	Category usage.ToolCategory `json:"k"`
	AtMs     int64              `json:"t"`
}

// callProgress remembers what the reader already reported about the model
// call it is in the middle of. One model call spans several transcript lines
// and a page may end between them.
type callProgress struct {
	ID       string `json:"i,omitempty"`
	Usage    bool   `json:"u,omitempty"`
	Thinking bool   `json:"h,omitempty"`
}

// state is everything the reader carries from one line to the next. A cursor
// holds all of it, so a page boundary never changes the items that follow.
type state struct {
	open []openCall
	call callProgress
}

func (s state) clone() state {
	return state{open: slices.Clone(s.open), call: s.call}
}

// openTool records a tool call until its result is read. IDs the cursor
// could not carry are not tracked at all, so that a paged read and a full
// read always agree.
func (s *state) openTool(call openCall) {
	if call.ID == "" || len(call.ID) > maxIDLen {
		return
	}
	s.open = slices.DeleteFunc(s.open, func(c openCall) bool { return c.ID == call.ID })
	if len(s.open) >= maxOpenCalls {
		s.open = slices.Delete(s.open, 0, len(s.open)-maxOpenCalls+1)
	}
	s.open = append(s.open, call)
}

// closeTool removes and returns the open call with the given ID.
func (s *state) closeTool(id string) (openCall, bool) {
	i := slices.IndexFunc(s.open, func(c openCall) bool { return c.ID == id })
	if i < 0 {
		return openCall{}, false
	}
	call := s.open[i]
	s.open = slices.Delete(s.open, i, i+1)
	return call, true
}

// cursorWire is the JSON layout inside the opaque cursor.
type cursorWire struct {
	Version int          `json:"v"`
	Offset  int64        `json:"o"`
	Open    []openCall   `json:"c,omitempty"`
	Call    callProgress `json:"m"`
}

func encodeCursor(offset int64, st state) (string, error) {
	raw, err := json.Marshal(cursorWire{Version: cursorVersion, Offset: offset, Open: st.open, Call: st.call})
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeCursor returns the offset and reader state of a cursor. The empty
// cursor starts at the beginning of the transcript. Anything this package
// could not have produced is refused with usage.ErrBadRequest.
func decodeCursor(cursor string) (int64, state, error) {
	if cursor == "" {
		return 0, state{}, nil
	}
	if len(cursor) > maxCursorLen {
		return 0, state{}, fmt.Errorf("%w: cursor too long", usage.ErrBadRequest)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, state{}, fmt.Errorf("%w: cursor encoding", usage.ErrBadRequest)
	}
	var wire cursorWire
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return 0, state{}, fmt.Errorf("%w: cursor content", usage.ErrBadRequest)
	}
	if _, err := dec.Token(); err != io.EOF {
		return 0, state{}, fmt.Errorf("%w: cursor has trailing data", usage.ErrBadRequest)
	}
	if err := wire.validate(); err != nil {
		return 0, state{}, err
	}
	return wire.Offset, state{open: wire.Open, call: wire.Call}, nil
}

func (w cursorWire) validate() error {
	switch {
	case w.Version != cursorVersion:
		return fmt.Errorf("%w: cursor version", usage.ErrBadRequest)
	case w.Offset < 0:
		return fmt.Errorf("%w: negative cursor offset", usage.ErrBadRequest)
	case len(w.Open) > maxOpenCalls:
		return fmt.Errorf("%w: cursor holds too many open calls", usage.ErrBadRequest)
	case len(w.Call.ID) > maxIDLen:
		return fmt.Errorf("%w: cursor call id", usage.ErrBadRequest)
	}
	for _, call := range w.Open {
		switch {
		case call.ID == "" || len(call.ID) > maxIDLen:
			return fmt.Errorf("%w: cursor tool id", usage.ErrBadRequest)
		case utf8.RuneCountInString(call.Name) > usage.MaxLabelLen:
			return fmt.Errorf("%w: cursor tool name", usage.ErrBadRequest)
		case !knownCategory(call.Category):
			return fmt.Errorf("%w: cursor tool category", usage.ErrBadRequest)
		case call.AtMs < 0:
			return fmt.Errorf("%w: cursor tool time", usage.ErrBadRequest)
		}
	}
	return nil
}
