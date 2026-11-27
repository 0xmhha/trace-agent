package extension

import "regexp"

// Wire format version accepted by the endpoint.
const WireVersion = 1

// Request and field limits of the wire format.
const (
	MaxBodyBytes      = 256 << 10
	MaxEvents         = 100
	MaxNameLen        = 128
	MaxMessageLen     = 2048
	MaxAttributes     = 32
	MaxAttributeValue = 256
	MaxUnitLen        = 16
)

// Field patterns of the wire format.
var (
	SourcePattern       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	IDPattern           = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	AttributeKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// WireEvent is one event as senders encode it. The endpoint and the
// trace-emit command share this type so the two cannot drift apart.
//
// ID is an optional sender-chosen idempotency key. With an ID, re-sending the
// event stores it once per source. Without an ID, an event that carries a
// timestamp is deduplicated by its content, and an event without a timestamp
// is always stored as new.
type WireEvent struct {
	Version      int            `json:"version"`
	ID           string         `json:"id,omitempty"`
	Source       string         `json:"source"`
	Kind         string         `json:"kind"`
	Name         string         `json:"name"`
	Timestamp    string         `json:"timestamp,omitempty"`
	DurationMs   *float64       `json:"duration_ms,omitempty"`
	Status       string         `json:"status,omitempty"`
	Level        string         `json:"level,omitempty"`
	SessionID    string         `json:"session_id,omitempty"`
	ToolUseID    string         `json:"tool_use_id,omitempty"`
	TraceID      string         `json:"trace_id,omitempty"`
	SpanID       string         `json:"span_id,omitempty"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	Message      string         `json:"message,omitempty"`
	Attributes   map[string]any `json:"attributes,omitempty"`
	Value        *float64       `json:"value,omitempty"`
	Unit         string         `json:"unit,omitempty"`
}

// WireBatch is the multi-event request body.
type WireBatch struct {
	Events []WireEvent `json:"events"`
}

// WireRejection explains one refused event of a request.
type WireRejection struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

// WireResponse is the body of a 202 response.
type WireResponse struct {
	Accepted int             `json:"accepted"`
	Rejected []WireRejection `json:"rejected"`
}
