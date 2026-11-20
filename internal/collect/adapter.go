package collect

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// Version identifies this metadata projection.
	Version   = "2.1.0"
	maxRecord = 4 << 20
)

// Kind selects the source-specific decoder.
type Kind string

// Supported source kinds.
const (
	Rollout        Kind = "codex-rollout"
	AppServer      Kind = "codex-app-server"
	Transcript     Kind = "claude-transcript"
	Telemetry      Kind = "claude-otel"
	CodexTelemetry Kind = "codex-otel"
)

var (
	errInput = errors.New("invalid source record")
	errLimit = errors.New("source record exceeds limit")
)

// Adapter belongs to exactly one configured source. It is safe for concurrent use.
type Adapter struct {
	parentSource, observerServer   string
	bodyEnabled                    bool
	kind                           Kind
	source, profile, clientVersion string
	secret                         []byte
	now                            func() time.Time
	tools                          map[string]bool
}

// New binds an adapter to a profile and instance. The caller supplies a persistent
// installation secret and trusted tool names from local configuration.
func New(kind Kind, profile, instance, clientVersion string, secret []byte, allowedTools []string, now func() time.Time) (*Adapter, error) {
	if !slices.Contains([]Kind{Rollout, AppServer, Transcript, Telemetry, CodexTelemetry}, kind) ||
		profile == "" || instance == "" || len(secret) < 32 || now == nil {
		return nil, errors.New("invalid adapter configuration")
	}
	a := &Adapter{kind: kind, secret: slices.Clone(secret), now: now, clientVersion: numericVersion(clientVersion), tools: make(map[string]bool)}
	client := "claude"
	if kind == Rollout || kind == AppServer || kind == CodexTelemetry {
		client = "codex"
	}
	a.profile = a.token("profile", client, profile)
	a.source = a.token("source", string(kind), profile, instance)
	for _, name := range allowedTools {
		a.tools[name] = true
	}
	return a, nil
}

func (a *Adapter) token(parts ...string) string {
	h := hmac.New(sha256.New, a.secret)
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p)) + ":"))
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Position is the stable logical position inside a source, independent of delivery order.
type Position struct {
	Stream, Generation string
	Offset             int64
}

// Evidence is safe to pass to the single writer. Metadata contains only projected fields.
type Evidence struct {
	legacyTag                                                               string
	slots                                                                   []BodySlot
	PreviousGeneration                                                      string `json:",omitempty"`
	projectedHash                                                           [sha256.Size]byte
	Source, Profile, Stream, Generation, RecordKey, VersionKey, SemanticTag string
	Kind                                                                    Kind
	Observer, AdapterVersion, ClientVersion, Redaction                      string
	Position                                                                int64
	ObservedMS                                                              int64
	OccurredMS                                                              *int64 `json:",omitempty"`
	Metadata                                                                map[string]any
}

func (a *Adapter) evidence(pos Position, meta object, occurred *int64) Evidence {
	encoded, _ := json.Marshal(meta) // The projector builds JSON exclusively from JSON scalar/container types.
	stream := a.token("stream", a.source, pos.Stream)
	generation := a.token("generation", stream, pos.Generation)
	return Evidence{projectedHash: sha256.Sum256(encoded), Source: a.source, Profile: a.profile, Stream: stream, Generation: generation, Position: pos.Offset,
		RecordKey: a.token("position", generation, strconv.FormatInt(pos.Offset, 10)), VersionKey: "1",
		SemanticTag: a.token("metadata", a.source, string(encoded)), Kind: a.kind, Observer: "client",
		AdapterVersion: Version, ClientVersion: a.clientVersion, Redaction: "metadata_only",
		ObservedMS: a.now().UnixMilli(), OccurredMS: occurred, Metadata: meta}
}

// Project decodes a single complete native record. Unknown record kinds yield a
// metadata-only unsupported marker, so a complete line can still be checkpointed.
func (a *Adapter) Project(pos Position, raw []byte) (Evidence, error) {
	if len(raw) > maxRecord {
		return Evidence{}, errLimit
	}
	if pos.Stream == "" || pos.Generation == "" || pos.Offset < 0 {
		return Evidence{}, errInput
	}
	if !utf8.Valid(raw) {
		return Evidence{}, errInput
	}
	var r object
	if err := json.Unmarshal(raw, &r); err != nil || r == nil {
		return Evidence{}, errInput
	}
	var m object
	switch a.kind {
	case Rollout:
		m = a.rollout(r)
	case Transcript:
		m = a.transcript(r)
	case CodexTelemetry:
		m = a.codexTelemetry(r)
	case Telemetry:
		m = a.telemetry(r)
	case AppServer:
		m = a.plan(r)
	}
	if m == nil {
		m = object{"type": "unsupported"}
	}
	var occurred *int64
	stamp := str(r, "timestamp")
	if a.kind == Telemetry || a.kind == CodexTelemetry {
		stamp = str(r, "event.timestamp")
	}
	if t, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
		ms := t.UnixMilli()
		occurred = &ms
	}
	e := a.evidence(pos, m, occurred)
	a.annotateModules(&e, r)
	a.annotateLineage(&e, r)
	if a.bodyEnabled && occurred != nil {
		e.slots = bodyFields(a.kind, r)
	}
	var version string
	switch a.kind {
	case Transcript:
		version = str(r, "version")
	case Telemetry, CodexTelemetry:
		version = str(r, "service.version")
	case Rollout:
		if str(r, "type") == "session_meta" {
			version = str(obj(r["payload"]), "cli_version")
		}
	}
	if version != "" {
		e.ClientVersion = numericVersion(version)
	}
	return e, nil
}

type object = map[string]any

func obj(v any) object              { m, _ := v.(map[string]any); return m }
func str(m object, k string) string { s, _ := m[k].(string); return s }
func choice(m object, k string, choices ...string) string {
	s := str(m, k)
	if slices.Contains(choices, s) {
		return s
	}
	return ""
}
func (a *Adapter) ids(dst, src object, keys ...string) {
	for _, k := range keys {
		if v := str(src, k); v != "" {
			dst[k] = a.id(v)
		}
	}
}
func copyBool(dst, src object, k string) {
	if b, ok := src[k].(bool); ok {
		dst[k] = b
	}
}
func (a *Adapter) tool(name string) string {
	if name == "" || a.tools[name] {
		return name
	}
	return a.token("unlisted-tool", a.source, name)
}

// Field names supply the namespace; equal tokens alone never create a bridge.
func (a *Adapter) id(raw string) string { return a.token("id", a.profile, raw) }

func numericVersion(v string) string {
	if len(v) > 32 {
		return ""
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return ""
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return ""
		}
	}
	return v
}

// Source returns the configured source identity.
func (a *Adapter) Source() string { return a.source }

// StreamKey returns the key used for file checkpoint lookup.
func (a *Adapter) StreamKey(name string) string { return a.token("stream", a.source, name) }

// StorageMetadata verifies that the projection has not been replaced after redaction.
func (e Evidence) StorageMetadata() ([]byte, error) {
	data, err := json.Marshal(e.Metadata)
	if err != nil || len(data) > maxRecord || sha256.Sum256(data) != e.projectedHash {
		return nil, errors.New("unverified evidence projection")
	}
	return data, nil
}

// Kind identifies the source protocol of this adapter.
func (a *Adapter) Kind() Kind { return a.kind }

// Profile returns the opaque client-profile identity shared by this adapter's records.
func (a *Adapter) Profile() string { return a.profile }

// PreviousSemanticTag identifies the same record projected by an earlier adapter
// that did not retain the response's child reference.
func (e Evidence) PreviousSemanticTag() string { return e.legacyTag }
