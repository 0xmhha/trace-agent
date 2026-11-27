package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"
)

// jsonSpace is the whitespace JSON allows between tokens. A CRLF line ending
// leaves its carriage return in this set.
const jsonSpace = " \t\r\n"

// maxExactFloat bounds the JSON numbers accepted as integers: beyond it a
// float64 no longer holds every integer, so the value would be a guess.
const maxExactFloat = 1 << 53

// walkObject calls visit for every member of the JSON object in data, with
// the raw key (quotes removed, escapes untouched) and the raw value. It
// reports whether data is one complete object. The walk reads only the outer
// level: nested values are stepped over by matching brackets and quotes, so
// a line of a kind the scanner does not use costs one pass and no allocation.
// Members that precede a syntax error or the end of a cut-off input are still
// visited, which is what lets a bounded prefix of an oversized line name its
// type.
func walkObject(data []byte, visit func(key, value []byte)) bool {
	i := skipSpace(data, 0)
	if i >= len(data) || data[i] != '{' {
		return false
	}
	i = skipSpace(data, i+1)
	if i < len(data) && data[i] == '}' {
		return skipSpace(data, i+1) == len(data)
	}
	for {
		if i >= len(data) || data[i] != '"' {
			return false
		}
		end, ok := stringEnd(data, i)
		if !ok {
			return false
		}
		key := data[i+1 : end-1]
		i = skipSpace(data, end)
		if i >= len(data) || data[i] != ':' {
			return false
		}
		i = skipSpace(data, i+1)
		if end, ok = valueEnd(data, i); !ok {
			return false
		}
		visit(key, data[i:end])
		i = skipSpace(data, end)
		if i >= len(data) {
			return false
		}
		switch data[i] {
		case ',':
			i = skipSpace(data, i+1)
		case '}':
			return skipSpace(data, i+1) == len(data)
		default:
			return false
		}
	}
}

// member returns the raw value of the named member of the JSON object in
// data, or nil. The last occurrence wins, as it does in encoding/json.
func member(data []byte, name string) []byte {
	var found []byte
	walkObject(data, func(key, value []byte) {
		if string(key) == name {
			found = value
		}
	})
	return found
}

// arrayLen counts the elements of the JSON array in data. It returns zero for
// anything that is not a complete array.
func arrayLen(data []byte) int {
	i := skipSpace(data, 0)
	if i >= len(data) || data[i] != '[' {
		return 0
	}
	i = skipSpace(data, i+1)
	if i < len(data) && data[i] == ']' {
		return 0
	}
	n := 0
	for {
		end, ok := valueEnd(data, i)
		if !ok {
			return 0
		}
		n++
		i = skipSpace(data, end)
		if i >= len(data) {
			return 0
		}
		switch data[i] {
		case ',':
			i = skipSpace(data, i+1)
		case ']':
			return n
		default:
			return 0
		}
	}
}

func skipSpace(data []byte, i int) int {
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\r' || data[i] == '\n') {
		i++
	}
	return i
}

// valueEnd returns the index after the JSON value that starts at data[i].
func valueEnd(data []byte, i int) (int, bool) {
	if i >= len(data) {
		return 0, false
	}
	switch data[i] {
	case '"':
		return stringEnd(data, i)
	case '{', '[':
		return compositeEnd(data, i)
	}
	end := i
	for end < len(data) && !endsScalar(data[end]) {
		end++
	}
	return end, end > i
}

// endsScalar reports whether c cannot be part of a number, true, false or
// null, and therefore ends one.
func endsScalar(c byte) bool {
	switch c {
	case ',', '}', ']', ' ', '\t', '\r', '\n':
		return true
	}
	return false
}

// stringEnd returns the index after the closing quote of the JSON string
// that starts at data[i]. A quote closes the string when an even number of
// backslashes precedes it.
func stringEnd(data []byte, i int) (int, bool) {
	at := i + 1
	for {
		next := bytes.IndexByte(data[at:], '"')
		if next < 0 {
			return 0, false
		}
		at += next
		escapes := 0
		for p := at - 1; p > i && data[p] == '\\'; p-- {
			escapes++
		}
		at++
		if escapes%2 == 0 {
			return at, true
		}
	}
}

// compositeEnd returns the index after the object or array that starts at
// data[i]. It only balances brackets outside strings and does not check that
// each closing bracket matches its opening kind: the parts the scanner uses
// are decoded by encoding/json afterwards, and the rest is never read.
func compositeEnd(data []byte, i int) (int, bool) {
	depth := 0
	for i < len(data) {
		switch data[i] {
		case '"':
			end, ok := stringEnd(data, i)
			if !ok {
				return 0, false
			}
			i = end
			continue
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
		i++
	}
	return 0, false
}

// isString reports whether raw is a complete JSON string token.
func isString(raw []byte) bool {
	return len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"'
}

// stringBody returns the bytes between the quotes of a JSON string token, or
// nil when raw is not a string. Escapes are left as written, which is enough
// to compare against the plain identifiers the scanner looks for.
func stringBody(raw []byte) []byte {
	if !isString(raw) {
		return nil
	}
	return raw[1 : len(raw)-1]
}

// jsonString decodes a JSON string token. It returns the empty string for any
// other value, so a null or a number in place of a name reads as "absent".
func jsonString(raw []byte) string {
	body := stringBody(raw)
	if len(body) == 0 {
		return ""
	}
	if bytes.IndexByte(body, '\\') < 0 && utf8.Valid(body) {
		return string(body)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// latest overwrites dst with the string in raw when raw holds a non-empty
// string. The comparison first skips the decoding for the common case of a
// value that repeats on every line.
func latest(dst *string, raw []byte) {
	body := stringBody(raw)
	if len(body) == 0 || string(body) == *dst {
		return
	}
	if v := jsonString(raw); v != "" {
		*dst = v
	}
}

// integer reads a JSON number as an int64. A fraction is truncated. It
// reports false for anything that is not a number it can represent exactly.
func integer(raw []byte) (int64, bool) {
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return 0, false
	}
	if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
		return n, true
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || f < -maxExactFloat || f > maxExactFloat {
		return 0, false
	}
	return int64(f), true
}

// count reads a JSON number as a non-negative quantity such as a token count
// or a duration. Anything else, and any negative number, reads as zero.
func count(raw []byte) int64 {
	n, _ := integer(raw)
	return max(n, 0)
}

// float reads a finite JSON number. A quoted number is not a number.
func float(raw []byte) (float64, bool) {
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		// An out-of-range literal parses to an infinity with an error.
		return 0, false
	}
	return f, true
}

// timestamp parses an RFC 3339 JSON string into UTC. It returns the zero time
// for anything else, which callers treat as "this line carries no time".
func timestamp(raw []byte) time.Time {
	body := stringBody(raw)
	if len(body) == 0 {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, string(body))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// number is a JSON number inside a decoded structure. Decoding never fails on
// it: a null, a string or an out-of-range value leaves zero, so that one odd
// field does not cost the whole line.
type number int64

// UnmarshalJSON implements json.Unmarshaler.
func (n *number) UnmarshalJSON(raw []byte) error {
	v, _ := integer(raw)
	*n = number(v)
	return nil
}

// decode fills v from raw and reports whether raw was syntactically valid
// JSON. A value of an unexpected type is not a failure: encoding/json leaves
// that field at zero and decodes the rest, and the scanner keeps what it got.
func decode(raw []byte, v any) bool {
	err := json.Unmarshal(raw, v)
	if err == nil {
		return true
	}
	var mismatch *json.UnmarshalTypeError
	return errors.As(err, &mismatch)
}
