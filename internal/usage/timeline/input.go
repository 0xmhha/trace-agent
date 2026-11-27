package timeline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/0xmhha/trace-agent/internal/usage"
)

const (
	// inputIndent is one nesting level of the re-encoded tool input.
	inputIndent = "  "
	jsonNull    = "null"
)

// errInputFull stops the re-encoding once the output is longer than the UI
// will be given. What follows would be cut off anyway.
var errInputFull = errors.New("tool input exceeds the display limit")

// maskInput re-encodes the arguments of a tool call for display: indented
// with two spaces, keys in their original order, every string value masked
// on its decoded form. Masking the decoded value instead of the raw JSON is
// what catches a secret inside escaped quotes, such as a shell command that
// exports one. The value of a key that names a secret is replaced as a whole.
// An absent or null input gives an empty string.
func maskInput(raw json.RawMessage) (text string, truncated bool, masked int) {
	trimmed := bytes.Trim(raw, jsonSpace)
	if len(trimmed) == 0 || string(trimmed) == jsonNull {
		return "", false, 0
	}
	w := newInputWriter(trimmed)
	// The input was validated together with its line, so an error here means
	// the output is full. Either way what was written so far is shown as cut.
	stopped := w.value("", 0) != nil
	text, clipped := clip(w.out.String(), usage.MaxTimelineText)
	return text, stopped || clipped || w.truncated, w.masked
}

// inputWriter streams one JSON value from dec to out. Streaming keeps the
// key order of the transcript and lets the writer stop early on a huge input
// instead of building all of it first.
type inputWriter struct {
	dec       *json.Decoder
	out       bytes.Buffer
	quoter    *json.Encoder
	masked    int
	truncated bool
}

func newInputWriter(raw []byte) *inputWriter {
	w := &inputWriter{dec: json.NewDecoder(bytes.NewReader(raw))}
	// Numbers keep their transcript spelling; a float64 round trip would
	// turn a large integer into an exponent.
	w.dec.UseNumber()
	w.quoter = json.NewEncoder(&w.out)
	// The UI renders the text as is, so "&&" must not become "&&".
	w.quoter.SetEscapeHTML(false)
	return w
}

// value writes the next value of the stream. key is the object key it
// belongs to, or empty at the top level and inside an array.
func (w *inputWriter) value(key string, depth int) error {
	if w.out.Len() > usage.MaxTimelineText {
		return errInputFull
	}
	if usage.SecretKey(key) {
		return w.secretValue()
	}
	tok, err := w.dec.Token()
	if err != nil {
		return fmt.Errorf("read tool input: %w", err)
	}
	switch v := tok.(type) {
	case json.Delim:
		if v == '{' {
			return w.object(depth)
		}
		return w.array(depth)
	case string:
		return w.text(v)
	case json.Number:
		w.out.WriteString(v.String())
	case bool:
		w.out.WriteString(strconv.FormatBool(v))
	default:
		w.out.WriteString(jsonNull)
	}
	return nil
}

// secretValue replaces the value of a secret-looking key. Strings and
// containers are hidden as a whole. Numbers, booleans, null and the empty
// string stay: they are counts and flags far more often than secrets, which
// is the same call usage.Mask makes for literals.
func (w *inputWriter) secretValue() error {
	var raw json.RawMessage
	if err := w.dec.Decode(&raw); err != nil {
		return fmt.Errorf("read tool input: %w", err)
	}
	const emptyString = `""`
	if len(raw) == 0 {
		return nil
	}
	switch raw[0] {
	case '{', '[':
	case '"':
		if string(raw) == emptyString {
			w.out.Write(raw)
			return nil
		}
	default:
		w.out.Write(raw)
		return nil
	}
	w.masked++
	return w.quote(usage.MaskToken)
}

func (w *inputWriter) object(depth int) error {
	if !w.dec.More() {
		w.out.WriteString("{}")
		return w.closeDelim()
	}
	w.out.WriteByte('{')
	for first := true; w.dec.More(); first = false {
		if !first {
			w.out.WriteByte(',')
		}
		w.newline(depth + 1)
		tok, err := w.dec.Token()
		if err != nil {
			return fmt.Errorf("read tool input: %w", err)
		}
		key, _ := tok.(string)
		if err := w.quote(key); err != nil {
			return err
		}
		w.out.WriteString(": ")
		if err := w.value(key, depth+1); err != nil {
			return err
		}
	}
	w.newline(depth)
	w.out.WriteByte('}')
	return w.closeDelim()
}

func (w *inputWriter) array(depth int) error {
	if !w.dec.More() {
		w.out.WriteString("[]")
		return w.closeDelim()
	}
	w.out.WriteByte('[')
	for first := true; w.dec.More(); first = false {
		if !first {
			w.out.WriteByte(',')
		}
		w.newline(depth + 1)
		if err := w.value("", depth+1); err != nil {
			return err
		}
	}
	w.newline(depth)
	w.out.WriteByte(']')
	return w.closeDelim()
}

// closeDelim consumes the closing bracket of the container just written.
func (w *inputWriter) closeDelim() error {
	if _, err := w.dec.Token(); err != nil {
		return fmt.Errorf("read tool input: %w", err)
	}
	return nil
}

// text writes one string value, cut and masked like any other display text.
func (w *inputWriter) text(s string) error {
	s, cut := clip(s, usage.MaxMaskInput)
	if cut {
		w.truncated = true
	}
	s, masked := usage.Mask(s)
	w.masked += masked
	return w.quote(s)
}

// quote writes s as a JSON string.
func (w *inputWriter) quote(s string) error {
	if err := w.quoter.Encode(s); err != nil {
		return fmt.Errorf("encode tool input: %w", err)
	}
	// Encode ends every value with a newline.
	w.out.Truncate(w.out.Len() - 1)
	return nil
}

func (w *inputWriter) newline(depth int) {
	w.out.WriteByte('\n')
	w.out.WriteString(strings.Repeat(inputIndent, depth))
}
