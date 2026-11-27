package extension

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// rejectReason is the machine-readable cause a refused event is reported
// with in WireRejection.Reason.
type rejectReason string

// Reasons an event is refused. The names are part of the response contract:
// senders and the documentation match on them.
const (
	reasonInvalidEvent          rejectReason = "invalid_event"
	reasonInvalidVersion        rejectReason = "invalid_version"
	reasonInvalidID             rejectReason = "invalid_id"
	reasonMissingSource         rejectReason = "missing_source"
	reasonInvalidSource         rejectReason = "invalid_source"
	reasonMissingKind           rejectReason = "missing_kind"
	reasonInvalidKind           rejectReason = "invalid_kind"
	reasonMissingName           rejectReason = "missing_name"
	reasonInvalidName           rejectReason = "invalid_name"
	reasonInvalidTimestamp      rejectReason = "invalid_timestamp"
	reasonInvalidDuration       rejectReason = "invalid_duration_ms"
	reasonInvalidStatus         rejectReason = "invalid_status"
	reasonInvalidLevel          rejectReason = "invalid_level"
	reasonInvalidSessionID      rejectReason = "invalid_session_id"
	reasonInvalidToolUseID      rejectReason = "invalid_tool_use_id"
	reasonInvalidTraceID        rejectReason = "invalid_trace_id"
	reasonInvalidSpanID         rejectReason = "invalid_span_id"
	reasonInvalidParentSpanID   rejectReason = "invalid_parent_span_id"
	reasonInvalidMessage        rejectReason = "invalid_message"
	reasonInvalidAttributes     rejectReason = "invalid_attributes"
	reasonTooManyAttributes     rejectReason = "too_many_attributes"
	reasonInvalidAttributeKey   rejectReason = "invalid_attribute_key"
	reasonInvalidAttributeValue rejectReason = "invalid_attribute_value"
	reasonInvalidValue          rejectReason = "invalid_value"
	reasonInvalidUnit           rejectReason = "invalid_unit"
)

// First bytes that tell JSON value kinds apart, and the white space JSON
// allows before a value.
const (
	jsonObjectStart = '{'
	jsonArrayStart  = '['
	jsonSpace       = " \t\r\n"
)

// startsWith reports whether the first byte of a JSON text, after leading
// white space, is start.
func startsWith(raw []byte, start byte) bool {
	trimmed := bytes.TrimLeft(raw, jsonSpace)
	return len(trimmed) > 0 && trimmed[0] == start
}

// typeErrorReason maps the wire field a JSON value had the wrong type for to
// the reason reported for it. It is the one place that ties the json tags of
// WireEvent to reasons.
func typeErrorReason(field string) rejectReason {
	switch field {
	case "version":
		return reasonInvalidVersion
	case "id":
		return reasonInvalidID
	case "source":
		return reasonInvalidSource
	case "kind":
		return reasonInvalidKind
	case "name":
		return reasonInvalidName
	case "timestamp":
		return reasonInvalidTimestamp
	case "duration_ms":
		return reasonInvalidDuration
	case "status":
		return reasonInvalidStatus
	case "level":
		return reasonInvalidLevel
	case "session_id":
		return reasonInvalidSessionID
	case "tool_use_id":
		return reasonInvalidToolUseID
	case "trace_id":
		return reasonInvalidTraceID
	case "span_id":
		return reasonInvalidSpanID
	case "parent_span_id":
		return reasonInvalidParentSpanID
	case "message":
		return reasonInvalidMessage
	case "attributes":
		return reasonInvalidAttributes
	case "value":
		return reasonInvalidValue
	case "unit":
		return reasonInvalidUnit
	}
	return reasonInvalidEvent
}

// decodeEvent decodes one event. A field of the wrong type does not stop the
// decoding of the others, so the returned event still names its source when
// that one was readable and the refusal can be attributed to the sender.
// Numbers inside attributes keep the sender's spelling.
func decodeEvent(raw json.RawMessage) (WireEvent, rejectReason) {
	var w WireEvent
	if !startsWith(raw, jsonObjectStart) {
		return w, reasonInvalidEvent
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	err := dec.Decode(&w)
	if err == nil {
		return w, ""
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return w, typeErrorReason(typeErr.Field)
	}
	return w, reasonInvalidEvent
}

// attributableSource returns the source to count a refusal under: the one
// the event names when it is well formed, else the unattributed bucket "".
func attributableSource(w *WireEvent) string {
	if SourcePattern.MatchString(w.Source) {
		return w.Source
	}
	return ""
}

// admitted is a validated, masked and capped event that still lacks its ID.
type admitted struct {
	event usage.ExtEvent
	// senderKey is the idempotency key the sender chose, if any.
	senderKey string
	// timestamped tells whether the sender supplied the event time.
	timestamped bool
}

// validate applies the field rules of the wire format and returns the event
// as it will be stored. received is the default event time.
func validate(w *WireEvent, received time.Time) (admitted, rejectReason) {
	var out admitted
	if w.Version != WireVersion {
		return out, reasonInvalidVersion
	}
	if w.ID != "" && !IDPattern.MatchString(w.ID) {
		return out, reasonInvalidID
	}
	if w.Source == "" {
		return out, reasonMissingSource
	}
	if !SourcePattern.MatchString(w.Source) {
		return out, reasonInvalidSource
	}
	if w.Kind == "" {
		return out, reasonMissingKind
	}
	kind, ok := parseKind(w.Kind)
	if !ok {
		return out, reasonInvalidKind
	}
	if w.Name == "" {
		return out, reasonMissingName
	}
	if utf8.RuneCountInString(w.Name) > MaxNameLen {
		return out, reasonInvalidName
	}
	at := received
	if w.Timestamp != "" {
		parsed, err := time.Parse(time.RFC3339, w.Timestamp)
		if err != nil {
			return out, reasonInvalidTimestamp
		}
		at = parsed
	}
	status, ok := parseStatus(w.Status)
	if !ok {
		return out, reasonInvalidStatus
	}
	level, ok := parseLevel(w.Level)
	if !ok {
		return out, reasonInvalidLevel
	}
	for _, link := range []struct {
		value  string
		reason rejectReason
	}{
		{w.SessionID, reasonInvalidSessionID},
		{w.ToolUseID, reasonInvalidToolUseID},
		{w.TraceID, reasonInvalidTraceID},
		{w.SpanID, reasonInvalidSpanID},
		{w.ParentSpanID, reasonInvalidParentSpanID},
	} {
		if link.value != "" && !IDPattern.MatchString(link.value) {
			return out, link.reason
		}
	}
	duration, ok := measure(w.DurationMs)
	if !ok {
		return out, reasonInvalidDuration
	}
	value, ok := measure(w.Value)
	if !ok {
		return out, reasonInvalidValue
	}
	if utf8.RuneCountInString(w.Unit) > MaxUnitLen {
		return out, reasonInvalidUnit
	}
	attributes, reason := storedAttributes(w.Attributes)
	if reason != "" {
		return out, reason
	}
	out = admitted{
		senderKey:   w.ID,
		timestamped: w.Timestamp != "",
		event: usage.ExtEvent{
			Source:       w.Source,
			Kind:         kind,
			Name:         maskAndCap(w.Name, MaxNameLen),
			At:           at.UTC(),
			ReceivedAt:   received.UTC(),
			DurationMs:   duration,
			Status:       status,
			Level:        level,
			SessionID:    w.SessionID,
			ToolUseID:    w.ToolUseID,
			TraceID:      w.TraceID,
			SpanID:       w.SpanID,
			ParentSpanID: w.ParentSpanID,
			Message:      maskAndCap(w.Message, MaxMessageLen),
			Attributes:   attributes,
			Value:        value,
			Unit:         w.Unit,
		},
	}
	return out, ""
}

func parseKind(s string) (usage.ExtKind, bool) {
	switch kind := usage.ExtKind(s); kind {
	case usage.ExtSpan, usage.ExtLog, usage.ExtMetric:
		return kind, true
	}
	return "", false
}

// parseStatus defaults an omitted status to unknown.
func parseStatus(s string) (usage.ExtStatus, bool) {
	switch status := usage.ExtStatus(s); status {
	case "":
		return usage.ExtUnknown, true
	case usage.ExtOK, usage.ExtError, usage.ExtUnknown:
		return status, true
	}
	return "", false
}

// parseLevel defaults an omitted level to info.
func parseLevel(s string) (usage.ExtLevel, bool) {
	switch level := usage.ExtLevel(s); level {
	case "":
		return usage.ExtInfo, true
	case usage.ExtDebug, usage.ExtInfo, usage.ExtWarn, usage.ExtFail:
		return level, true
	}
	return "", false
}

// measure accepts an optional finite number that is not negative. It returns
// a copy, with negative zero folded into zero so that both hash alike.
func measure(v *float64) (*float64, bool) {
	if v == nil {
		return nil, true
	}
	if math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 {
		return nil, false
	}
	n := *v + 0
	return &n, true
}

// storedAttributes validates the attributes and returns them as stored:
// values as text, masked, capped. Keys are visited in order so that the
// reason reported for an event with several bad attributes is stable.
func storedAttributes(in map[string]any) (map[string]string, rejectReason) {
	if len(in) == 0 {
		return nil, ""
	}
	if len(in) > MaxAttributes {
		return nil, reasonTooManyAttributes
	}
	out := make(map[string]string, len(in))
	for _, key := range slices.Sorted(maps.Keys(in)) {
		if !AttributeKeyPattern.MatchString(key) {
			return nil, reasonInvalidAttributeKey
		}
		text, ok := attributeText(in[key])
		if !ok {
			return nil, reasonInvalidAttributeValue
		}
		if usage.SecretKey(key) {
			// The key says the value is a secret, whatever it looks like.
			out[key] = usage.MaskToken
			continue
		}
		out[key] = maskAndCap(text, MaxAttributeValue)
	}
	return out, ""
}

// attributeText renders a scalar attribute value as text. Objects, arrays
// and null are not attribute values.
func attributeText(v any) (string, bool) {
	switch value := v.(type) {
	case string:
		return value, true
	case json.Number:
		return value.String(), true
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(value), true
	}
	return "", false
}

// maskAndCap masks first and cuts second: cutting first could split a secret
// into a prefix that no rule recognises any more.
func maskAndCap(text string, limit int) string {
	masked, _ := usage.Mask(text)
	return capRunes(masked, limit)
}

// capRunes cuts s to at most limit characters without splitting one.
func capRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	count := 0
	for i := range s {
		if count == limit {
			return s[:i]
		}
		count++
	}
	return s
}
