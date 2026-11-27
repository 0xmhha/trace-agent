package collect

import "strings"

// Public source client codes. The lower-case form is the value stored in
// source.client and the namespace of a client profile.
const (
	ClientCodex  = "CODEX"
	ClientClaude = "CLAUDE"
	ClientMCP    = "MCP"
)

// Reporter codes name who witnessed a record (evidence.observer_code).
const (
	ReporterClient    = "client"
	ReporterTool      = "tool"
	ReporterCollector = "collector"
)

// maxServerName bounds the MCP server name an observer can be bound to.
const maxServerName = 512

// Properties describes one registered source kind. Kind-dependent decisions
// outside the per-kind decoders read these fields instead of listing kinds.
type Properties struct {
	Kind Kind
	// Client is the public client code returned by ClientCode.
	Client string
	// Reporter is the observer code stamped on the records this kind delivers
	// through its own contract: Project, or ProjectObservation for observers.
	Reporter string
	// OTLP marks kinds fed by an OTLP/HTTP log exporter. Their records carry
	// event.timestamp and service.version instead of the native file fields.
	OTLP bool
	// Observer marks kinds bound to a parent client source by ObserverAdapter.
	// Every other kind is created by New.
	Observer bool
	// Body reports whether opted-in body fields can be prepared for this kind.
	Body bool
	// Parent reports whether observer sources may be bound to this kind.
	Parent bool
	// Ordered reports whether positions inside one stream follow the client's
	// own write order, so stream order is evidence of event order.
	Ordered bool
	// ParentClient restricts an observer kind to parents of one client. An
	// observer kind with an empty value accepts every Parent kind.
	ParentClient string
	// Server reports whether an observer kind is bound to a named MCP server.
	Server bool
}

type kindSpec struct {
	Properties
	// project decodes one native record. It is nil for observer kinds, which
	// accept event envelopes through ProjectObservation instead.
	project func(*Adapter, object) object
}

// registry is the single owner of the source kinds. Adding a kind starts here.
// The registry tests of this package, ingress and observer iterate over every
// entry and fail until each dispatch site they reach handles the new kind.
var registry = []kindSpec{
	{Properties{Kind: Rollout, Client: ClientCodex, Reporter: ReporterClient, Body: true, Parent: true, Ordered: true}, (*Adapter).rollout},
	{Properties{Kind: AppServer, Client: ClientCodex, Reporter: ReporterClient, Body: true, Parent: true}, (*Adapter).plan},
	{Properties{Kind: Transcript, Client: ClientClaude, Reporter: ReporterClient, Body: true, Parent: true, Ordered: true}, (*Adapter).transcript},
	{Properties{Kind: Telemetry, Client: ClientClaude, Reporter: ReporterClient, OTLP: true, Body: true}, (*Adapter).telemetry},
	{Properties{Kind: CodexTelemetry, Client: ClientCodex, Reporter: ReporterClient, OTLP: true, Body: true}, (*Adapter).codexTelemetry},
	{Properties{Kind: ClaudeHook, Client: ClientClaude, Reporter: ReporterClient, Observer: true, ParentClient: ClientClaude}, nil},
	{Properties{Kind: MCPObserver, Client: ClientMCP, Reporter: ReporterTool, Observer: true, Server: true}, nil},
}

func lookup(k Kind) (kindSpec, bool) {
	for _, s := range registry {
		if s.Kind == k {
			return s, true
		}
	}
	return kindSpec{}, false
}

// Kinds returns every registered source kind in registration order.
func Kinds() []Kind {
	out := make([]Kind, 0, len(registry))
	for _, s := range registry {
		out = append(out, s.Kind)
	}
	return out
}

// KindInfo returns the properties of a registered kind. The zero value and
// false report an unregistered kind.
func KindInfo(k Kind) (Properties, bool) {
	s, ok := lookup(k)
	return s.Properties, ok
}

// ClientCode returns the public source client, or "" for an unregistered kind.
func ClientCode(kind Kind) string {
	s, _ := lookup(kind)
	return s.Client
}

// StoredClient returns the lower-case client stored in source.client. It is
// also the namespace of a client profile, so sources of one client can share
// identifiers. An unregistered kind yields "".
func StoredClient(kind Kind) string { return strings.ToLower(ClientCode(kind)) }

// IsOTLP reports whether the kind is delivered by an OTLP/HTTP log exporter.
func IsOTLP(k Kind) bool { s, _ := lookup(k); return s.OTLP }

// IsObserver reports whether the kind is bound to a parent client source.
func IsObserver(k Kind) bool { s, _ := lookup(k); return s.Observer }

// SupportsBody reports whether opted-in body fields can be prepared for the kind.
func SupportsBody(k Kind) bool { s, _ := lookup(k); return s.Body }

// CanParent reports whether observer sources may be bound to the kind.
func CanParent(k Kind) bool { s, _ := lookup(k); return s.Parent }

// StreamOrdered reports whether stream positions of the kind follow the
// client's own write order.
func StreamOrdered(k Kind) bool { s, _ := lookup(k); return s.Ordered }

// AcceptsParent reports whether an observer kind may be bound to a parent kind.
func AcceptsParent(observer, parent Kind) bool {
	o, _ := lookup(observer)
	p, _ := lookup(parent)
	return o.Observer && p.Parent && (o.ParentClient == "" || o.ParentClient == p.Client)
}
