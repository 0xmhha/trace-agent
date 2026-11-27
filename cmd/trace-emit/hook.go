package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/extension"
)

// hookInputLimit bounds the hook JSON read from stdin. Hook input carries
// tool arguments and can be large; anything beyond the bound is not parsed.
const hookInputLimit = 256 << 10

// hookReadTimeout bounds the wait for hook input, where stdin may be a
// terminal or a pipe that never reaches end of file.
const hookReadTimeout = 500 * time.Millisecond

// hookReadChunk is the size of one read of hook input.
const hookReadChunk = 32 << 10

var errHookTooLarge = errors.New("hook input is larger than the read limit")

// hookInput is the part of a Claude Code hook payload the command uses.
// Every other field, tool arguments included, is dropped unread.
type hookInput struct {
	SessionID string `json:"session_id"`
	ToolUseID string `json:"tool_use_id"`
}

// readHookInput reads stdin up to one byte past hookInputLimit, so that the
// caller can tell an oversized input from one that fits exactly. It returns
// what was read even when the read failed.
func readHookInput(stdin io.Reader) ([]byte, error) {
	return readHookInputWithin(context.Background(), stdin)
}

// readHookInputWithin is readHookInput bounded by ctx. When ctx ends first it
// returns what had arrived by then together with the context's error.
func readHookInputWithin(ctx context.Context, stdin io.Reader) ([]byte, error) {
	read := startHookRead(stdin)
	defer read.stop()
	raw, _, err := read.wait(ctx)
	return raw, err
}

// hookRead is a read of hook input that runs in its own goroutine, because a
// read blocked on a terminal or an open pipe cannot be cancelled. Bytes that
// arrive after the caller stopped waiting are not lost: the rest returned by
// wait delivers them.
type hookRead struct {
	stdin  io.Reader
	chunks chan []byte
	quit   chan struct{}
	// The goroutine writes these before it closes chunks, and they are read
	// only after chunks was seen closed, which orders the accesses.
	err        error
	reachedEOF bool
}

// startHookRead starts reading stdin up to one byte past hookInputLimit. The
// caller must call stop once nothing reads the input any more.
func startHookRead(stdin io.Reader) *hookRead {
	r := &hookRead{stdin: stdin, chunks: make(chan []byte), quit: make(chan struct{})}
	if stdin == nil {
		r.reachedEOF = true
		close(r.chunks)
		return r
	}
	// The goroutine ends when the read reaches the limit, end of file or an
	// error, or, after stop, when it next has a chunk to hand on. A read
	// that blocks forever ends with the process.
	go r.pump()
	return r
}

// pump reads stdin chunk by chunk and hands every chunk on at once, so that
// a wait that gives up has everything read so far.
func (r *hookRead) pump() {
	defer close(r.chunks)
	limited := io.LimitReader(r.stdin, hookInputLimit+1)
	total := 0
	for {
		buf := make([]byte, hookReadChunk)
		n, err := limited.Read(buf)
		if n > 0 {
			total += n
			select {
			case r.chunks <- buf[:n]:
			case <-r.quit:
				return
			}
		}
		switch {
		case err == nil:
			continue
		case !errors.Is(err, io.EOF):
			r.err = fmt.Errorf("read hook input: %w", err)
		case total <= hookInputLimit:
			// The limit reader also reports end of file at the limit; only a
			// shorter input has really ended.
			r.reachedEOF = true
		}
		return
	}
}

// wait collects the hook input until the read ends or ctx is done. raw is
// what was collected, and rest yields what stdin holds after raw, so that
// raw followed by rest is the whole input. err is the read's failure or,
// when the wait gave up, the context's error.
func (r *hookRead) wait(ctx context.Context) (raw []byte, rest io.Reader, err error) {
	for {
		select {
		case chunk, ok := <-r.chunks:
			if !ok {
				return raw, r.remainder(), r.err
			}
			raw = append(raw, chunk...)
		case <-ctx.Done():
			return raw, &hookRest{read: r}, fmt.Errorf("read hook input: %w", ctx.Err())
		}
	}
}

// remainder is what stdin holds after a read that has ended: nothing after
// end of file, otherwise stdin itself.
func (r *hookRead) remainder() io.Reader {
	if r.reachedEOF {
		return eofReader{}
	}
	return r.stdin
}

// stop releases the reading goroutine; reads of rest end with end of file
// afterwards. It may be called more than once from one goroutine.
func (r *hookRead) stop() {
	select {
	case <-r.quit:
	default:
		close(r.quit)
	}
}

// hookRest yields the chunks a hookRead delivers after its wait gave up and
// then the remainder of stdin. Only one goroutine may read it.
type hookRest struct {
	read    *hookRead
	pending []byte
	tail    io.Reader
}

// Read implements io.Reader.
func (h *hookRest) Read(p []byte) (int, error) {
	for len(h.pending) == 0 {
		if h.tail != nil {
			return h.tail.Read(p)
		}
		select {
		case chunk, ok := <-h.read.chunks:
			if !ok {
				h.tail = h.read.remainder()
				continue
			}
			h.pending = chunk
		case <-h.read.quit:
			return 0, io.EOF
		}
	}
	n := copy(p, h.pending)
	h.pending = h.pending[n:]
	return n, nil
}

// eofReader is a reader that has already ended.
type eofReader struct{}

// Read implements io.Reader.
func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

// linkFromHook copies the session and tool call identifiers of the hook
// payload raw into ev. A value given by flag wins, and an identifier that
// the wire format would refuse is left out so the event is still sent.
func linkFromHook(ev *extension.WireEvent, raw []byte) error {
	if len(raw) > hookInputLimit {
		return errHookTooLarge
	}
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		// The decoder's message can quote the input, which is not ours to print.
		return errors.New("hook input is not the expected JSON")
	}
	var refused []string
	for _, id := range []struct {
		name  string
		value string
		field *string
	}{
		{"session_id", in.SessionID, &ev.SessionID},
		{"tool_use_id", in.ToolUseID, &ev.ToolUseID},
	} {
		switch {
		case *id.field != "" || id.value == "":
			// Set by flag, or absent from the payload: nothing to copy.
		case !extension.IDPattern.MatchString(id.value):
			refused = append(refused, id.name)
		default:
			*id.field = id.value
		}
	}
	if len(refused) > 0 {
		return fmt.Errorf("%s does not match %s", strings.Join(refused, " and "), extension.IDPattern)
	}
	return nil
}
