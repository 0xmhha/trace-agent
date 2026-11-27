package timeline

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// maxDurationMs bounds a duration read from the transcript. It is the
// largest integer a JSON number carries exactly, far beyond any real run.
const maxDurationMs = 1 << 53

// jsonSpace is the whitespace JSON allows around a value.
const jsonSpace = " \t\r\n"

func blank(data []byte) bool {
	return len(bytes.Trim(data, jsonSpace)) == 0
}

// rawStringValue decodes raw when it is a JSON string. isString is false for
// every other value, including null and an absent field.
func rawStringValue(raw json.RawMessage) (text string, isString bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	return text, true
}

// rawString is rawStringValue for callers that treat a missing string and an
// empty one alike.
func rawString(raw json.RawMessage) string {
	text, _ := rawStringValue(raw)
	return text
}

// rawFloat decodes raw when it is a JSON number.
func rawFloat(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || raw[0] == '"' {
		return 0, false
	}
	var number *float64
	if err := json.Unmarshal(raw, &number); err != nil || number == nil {
		return 0, false
	}
	return *number, true
}

// clampFloat limits f to [low, high]. NaN becomes low.
func clampFloat(f, low, high float64) float64 {
	switch {
	case f >= high:
		return high
	case f >= low:
		return f
	}
	return low
}

// parseTime reads a line timestamp. A missing or unreadable one is the zero
// time, which callers treat as unknown.
func parseTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// unixMillis returns t in Unix milliseconds, or zero when t is unknown.
func unixMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return max(t.UnixMilli(), 0)
}

// label cuts a name to usage.MaxLabelLen characters.
func label(s string) string {
	if len(s) <= usage.MaxLabelLen {
		return s
	}
	runes := 0
	for i := range s {
		if runes == usage.MaxLabelLen {
			return s[:i]
		}
		runes++
	}
	return s
}

// clip cuts s to at most limit bytes without splitting a UTF-8 sequence.
func clip(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	cut := limit
	for back := 0; back < utf8.UTFMax && cut > 0 && !utf8.RuneStart(s[cut]); back++ {
		cut--
	}
	return s[:cut], true
}

// displayText prepares transcript text for the screen: cut, mask, cut again.
// The first cut bounds the cost of masking a huge tool result; the second
// bounds what is sent to the UI.
func displayText(s string) (text string, truncated bool, masked int) {
	s, cutBefore := clip(s, usage.MaxMaskInput)
	s, masked = usage.Mask(s)
	s, cutAfter := clip(s, usage.MaxTimelineText)
	return s, cutBefore || cutAfter, masked
}

// cappedText joins text parts without growing past what displayText would
// keep. It stores one byte more than usage.MaxMaskInput at most, so that
// displayText still notices the text was longer.
type cappedText struct {
	b     strings.Builder
	parts int
}

func (c *cappedText) add(part string) {
	if c.parts > 0 {
		c.write(textSeparator)
	}
	c.parts++
	c.write(part)
}

func (c *cappedText) write(s string) {
	room := usage.MaxMaskInput + 1 - c.b.Len()
	if room <= 0 {
		return
	}
	if len(s) > room {
		s = s[:room]
	}
	c.b.WriteString(s)
}

func (c *cappedText) String() string { return c.b.String() }
