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

// hookReadTimeout bounds the wait for hook input in emit mode, where stdin
// may be a terminal that never reaches end of file.
const hookReadTimeout = 500 * time.Millisecond

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
	if stdin == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, hookInputLimit+1))
	if err != nil {
		return raw, fmt.Errorf("read hook input: %w", err)
	}
	return raw, nil
}

// readHookInputWithin is readHookInput bounded by ctx.
func readHookInputWithin(ctx context.Context, stdin io.Reader) ([]byte, error) {
	type result struct {
		raw []byte
		err error
	}
	done := make(chan result, 1)
	// A read blocked on a terminal cannot be cancelled. The goroutine ends
	// when the read returns or, at the latest, with the process, which exits
	// right after emit mode gives up. The buffered channel lets it finish
	// even when nobody is waiting any more.
	go func() {
		raw, err := readHookInput(stdin)
		done <- result{raw, err}
	}()
	select {
	case res := <-done:
		return res.raw, res.err
	case <-ctx.Done():
		return nil, fmt.Errorf("read hook input: %w", ctx.Err())
	}
}

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
