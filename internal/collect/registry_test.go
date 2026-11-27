package collect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	sampleStamp   = "2026-01-02T03:04:05Z"
	sampleVersion = "9.8.7"
	sampleText    = "plain words"
	sampleServer  = "north"
)

// kindSample is valid synthetic input for one registered kind. A registered
// kind without a sample fails the tests, so a new kind cannot skip them.
type kindSample struct {
	// record is accepted by Project, or by ProjectObservation for observer kinds.
	// For kinds that support bodies it carries one clean body field.
	record object
	// versioned reports sampleVersion. It is nil when the kind's records carry
	// no client version.
	versioned object
}

func kindSamples() map[Kind]kindSample {
	transcript := object{"timestamp": sampleStamp, "type": "user", "version": sampleVersion, "sessionId": "s1", "uuid": "u1", "message": object{"role": "user", "content": sampleText}}
	body, _ := json.Marshal(object{"messages": []object{{"role": "user", "content": sampleText}}})
	telemetry := object{"event.timestamp": sampleStamp, "service.version": sampleVersion, "event.name": "api_request_body", "request_body_id": "b1", "body": string(body)}
	codex := object{"event.timestamp": sampleStamp, "service.version": sampleVersion, "event.name": "codex.user_prompt", "conversation.id": "c1", "prompt": sampleText}
	return map[Kind]kindSample{
		Rollout: {
			record:    object{"timestamp": sampleStamp, "type": "response_item", "payload": object{"type": "message", "role": "user", "id": "m1", "content": []object{{"type": "input_text", "text": sampleText}}}},
			versioned: object{"timestamp": sampleStamp, "type": "session_meta", "payload": object{"id": "s1", "cli_version": sampleVersion}},
		},
		// The app-server reports its version in the handshake, not per record.
		AppServer:      {record: object{"timestamp": sampleStamp, "method": "turn/plan/updated", "params": object{"threadId": "t1", "turnId": "u1", "plan": []object{{"step": sampleText, "status": "pending"}}}}},
		Transcript:     {record: transcript, versioned: transcript},
		Telemetry:      {record: telemetry, versioned: telemetry},
		CodexTelemetry: {record: codex, versioned: codex},
		ClaudeHook:     {record: object{"timestamp": sampleStamp, "hook_event_name": "PreToolUse", "session_id": "s1", "tool_use_id": "t1"}},
		MCPObserver:    {record: object{"timestamp": sampleStamp, "version": 1, "action": "receive", "connection_id": "c1", "rpc_id": "r1", "server": sampleServer, "clock_id": "k1"}},
	}
}

func encode(t testing.TB, v object) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// sampleParent returns the first registered kind an observer kind can be bound to.
func sampleParent(t testing.TB, observer Kind) Kind {
	t.Helper()
	for _, parent := range Kinds() {
		if AcceptsParent(observer, parent) {
			return parent
		}
	}
	t.Fatalf("no registered kind accepts observer %s", observer)
	return ""
}

// sampleAdapter builds an adapter of any registered kind.
func sampleAdapter(t testing.TB, k Kind) *Adapter {
	t.Helper()
	info, ok := KindInfo(k)
	if !ok {
		t.Fatalf("%s is not registered", k)
	}
	if !info.Observer {
		a, err := New(k, "profile", "instance", "", make([]byte, 32), nil, func() time.Time { return time.Unix(1234, 0) })
		if err != nil {
			t.Fatalf("New(%s): %v", k, err)
		}
		return a
	}
	server := ""
	if info.Server {
		server = sampleServer
	}
	a, err := ObserverAdapter(sampleAdapter(t, sampleParent(t, k)), k, server)
	if err != nil {
		t.Fatalf("ObserverAdapter(%s): %v", k, err)
	}
	return a
}

// SampleEvidence projects the sample record of a registered kind. The
// cross-package dispatch test feeds it to the writer and the read model.
func SampleEvidence(t testing.TB, k Kind) Evidence {
	t.Helper()
	sample, ok := kindSamples()[k]
	if !ok {
		t.Fatalf("%s has no sample record", k)
	}
	a := sampleAdapter(t, k)
	var e Evidence
	var err error
	if IsObserver(k) {
		e, err = a.ProjectObservation(encode(t, sample.record))
	} else {
		e, err = a.Project(Position{Stream: "stream", Generation: "g"}, encode(t, sample.record))
	}
	if err != nil {
		t.Fatalf("%s sample: %v", k, err)
	}
	return e
}

// schemaCodes returns the values a CHECK(column IN (...)) constraint allows.
func schemaCodes(t *testing.T, column string) []string {
	t.Helper()
	schema, err := os.ReadFile(filepath.Join(project, "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	list := regexp.MustCompile(`CHECK\(` + column + ` IN \(([^)]*)\)\)`).FindSubmatch(schema)
	if list == nil {
		t.Fatalf("no CHECK constraint for %s", column)
	}
	var out []string
	for _, v := range strings.Split(string(list[1]), ",") {
		out = append(out, strings.Trim(strings.TrimSpace(v), "'"))
	}
	return out
}

// The registry replaced hand-written kind lists. This table pins what those
// lists encoded: kind strings and client codes are stored identifiers.
func TestRegisteredKinds(t *testing.T) {
	want := []Properties{
		{Kind: "codex-rollout", Client: "CODEX", Reporter: "client", Body: true, Parent: true, Ordered: true},
		{Kind: "codex-app-server", Client: "CODEX", Reporter: "client", Body: true, Parent: true},
		{Kind: "claude-transcript", Client: "CLAUDE", Reporter: "client", Body: true, Parent: true, Ordered: true},
		{Kind: "claude-otel", Client: "CLAUDE", Reporter: "client", OTLP: true, Body: true},
		{Kind: "codex-otel", Client: "CODEX", Reporter: "client", OTLP: true, Body: true},
		{Kind: "claude-hook", Client: "CLAUDE", Reporter: "client", Observer: true, ParentClient: "CLAUDE"},
		{Kind: "mcp-observer", Client: "MCP", Reporter: "tool", Observer: true, Server: true},
	}
	kinds := Kinds()
	if len(kinds) != len(want) {
		t.Fatalf("registered kinds %v", kinds)
	}
	clients, reporters, samples := schemaCodes(t, "client"), schemaCodes(t, "observer_code"), kindSamples()
	seen := map[Kind]bool{}
	for i, k := range kinds {
		info, ok := KindInfo(k)
		if !ok || info != want[i] {
			t.Errorf("%s: properties %+v, want %+v", k, info, want[i])
		}
		if seen[k] {
			t.Errorf("%s is registered twice", k)
		}
		seen[k] = true
		spec, _ := lookup(k)
		if (spec.project == nil) != info.Observer {
			t.Errorf("%s: a kind needs exactly one of a record decoder and the observer contract", k)
		}
		if info.Observer && (info.OTLP || info.Body || info.Parent || info.Ordered) {
			t.Errorf("%s: an observer kind cannot carry client-source properties", k)
		}
		if !info.Observer && (info.ParentClient != "" || info.Server) {
			t.Errorf("%s: only an observer kind is bound to a parent or server", k)
		}
		if ClientCode(k) != info.Client || StoredClient(k) != strings.ToLower(info.Client) || !slices.Contains(clients, StoredClient(k)) {
			t.Errorf("%s: client %q is not allowed by the source.client constraint %v", k, StoredClient(k), clients)
		}
		if !slices.Contains(reporters, info.Reporter) {
			t.Errorf("%s: reporter %q is not allowed by the observer_code constraint %v", k, info.Reporter, reporters)
		}
		if IsOTLP(k) != info.OTLP || IsObserver(k) != info.Observer || SupportsBody(k) != info.Body || CanParent(k) != info.Parent || StreamOrdered(k) != info.Ordered {
			t.Errorf("%s: predicate disagrees with its properties", k)
		}
		if _, ok := samples[k]; !ok {
			t.Errorf("%s has no sample record", k)
		}
	}
	if !slices.Contains(reporters, ReporterCollector) {
		t.Errorf("collector reporter is not allowed by the observer_code constraint %v", reporters)
	}
	// An unregistered kind has no properties and cannot become a source.
	const unknown Kind = "codex-unregistered"
	if info, ok := KindInfo(unknown); ok || info != (Properties{}) || ClientCode(unknown) != "" || StoredClient(unknown) != "" ||
		IsOTLP(unknown) || IsObserver(unknown) || SupportsBody(unknown) || CanParent(unknown) || StreamOrdered(unknown) {
		t.Error("an unregistered kind has properties")
	}
	if _, err := New(unknown, "profile", "instance", "", make([]byte, 32), nil, time.Now); err == nil {
		t.Error("an unregistered kind became an adapter")
	}
	if _, err := ObserverAdapter(sampleAdapter(t, Transcript), unknown, ""); err == nil {
		t.Error("an unregistered kind became an observer")
	}
	if _, err := ObserverAdapter(nil, ClaudeHook, ""); err == nil {
		t.Error("an observer without a parent was accepted")
	}
}

// Every registered kind must reach each dispatch site of the projection: the
// record decoder, the timestamp and version fields, body preparation and the
// reporter code. None may fall through to the unsupported marker.
func TestEveryKindProjects(t *testing.T) {
	occurred, err := time.Parse(time.RFC3339, sampleStamp)
	if err != nil {
		t.Fatal(err)
	}
	samples := kindSamples()
	profiles := map[string]string{}
	for _, k := range Kinds() {
		info, _ := KindInfo(k)
		sample := samples[k]
		a := sampleAdapter(t, k)
		e := SampleEvidence(t, k)
		if e.Kind != k || e.Observer != info.Reporter || e.Redaction != "metadata_only" || e.OccurredMS == nil || *e.OccurredMS != occurred.UnixMilli() {
			t.Errorf("%s: provenance %+v", k, e)
		}
		if str(e.Metadata, "type") == "unsupported" || len(e.Metadata) == 0 {
			t.Errorf("%s: sample fell through to %v", k, e.Metadata)
		}
		if _, err := e.StorageMetadata(); err != nil {
			t.Errorf("%s: %v", k, err)
		}
		if info.Observer {
			if _, err := New(k, "profile", "instance", "", make([]byte, 32), nil, time.Now); err == nil {
				t.Errorf("%s: an observer kind was created without a parent", k)
			}
			if a.Profile() != sampleAdapter(t, sampleParent(t, k)).Profile() {
				t.Errorf("%s: an observer left its parent's profile", k)
			}
			continue
		}
		if _, err := a.ProjectObservation(encode(t, sample.record)); err == nil {
			t.Errorf("%s: a client source accepted an observation", k)
		}
		// Sources of one client share a profile namespace; clients never do.
		if p, ok := profiles[info.Client]; ok && p != a.Profile() {
			t.Errorf("%s: profile differs inside client %s", k, info.Client)
		}
		profiles[info.Client] = a.Profile()
		slots := mustProject(t, a.WithBodyCollection(), sample.record).BodySlots()
		if info.Body != (len(slots) == 1 && slots[0].Status == "sanitized" && slots[0].Text == sampleText) {
			t.Errorf("%s: body slots %+v, supported %t", k, slots, info.Body)
		}
		if len(e.BodySlots()) != 0 {
			t.Errorf("%s: body prepared without opt-in", k)
		}
		if sample.versioned != nil && mustProject(t, a, sample.versioned).ClientVersion != sampleVersion {
			t.Errorf("%s: client version not read", k)
		}
	}
	if len(profiles) < 2 || profiles[ClientCodex] == profiles[ClientClaude] {
		t.Error("clients share a profile namespace")
	}
}

func mustProject(t *testing.T, a *Adapter, record object) Evidence {
	t.Helper()
	e, err := a.Project(Position{Stream: "stream", Generation: "g"}, encode(t, record))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestObserverParents(t *testing.T) {
	want := map[Kind][]Kind{
		ClaudeHook:  {Transcript},
		MCPObserver: {Rollout, AppServer, Transcript},
	}
	parents := map[Kind]bool{}
	for _, observer := range Kinds() {
		info, _ := KindInfo(observer)
		if _, listed := want[observer]; listed != info.Observer {
			t.Errorf("%s: parent expectations are missing or stale", observer)
		}
		server := ""
		if info.Server {
			server = sampleServer
		}
		for _, parent := range Kinds() {
			accepted := slices.Contains(want[observer], parent)
			parents[parent] = parents[parent] || accepted
			if AcceptsParent(observer, parent) != accepted {
				t.Errorf("AcceptsParent(%s, %s) = %t", observer, parent, !accepted)
			}
			a, err := ObserverAdapter(sampleAdapter(t, parent), observer, server)
			if (err == nil) != accepted {
				t.Errorf("ObserverAdapter(%s, %s): %v", parent, observer, err)
			}
			if err == nil && (a.Kind() != observer || a.Source() == sampleAdapter(t, parent).Source()) {
				t.Errorf("%s shares the source of its parent %s", observer, parent)
			}
		}
	}
	for _, k := range Kinds() {
		if CanParent(k) != parents[k] {
			t.Errorf("CanParent(%s) = %t", k, !parents[k])
		}
	}
	// Only a server-bound observer takes a server name, and it must be bounded.
	parent := sampleAdapter(t, Transcript)
	for _, tc := range []struct {
		kind   Kind
		server string
		ok     bool
	}{
		{ClaudeHook, "", true},
		{ClaudeHook, sampleServer, false},
		{MCPObserver, "", false},
		{MCPObserver, sampleServer, true},
		{MCPObserver, strings.Repeat("s", maxServerName), true},
		{MCPObserver, strings.Repeat("s", maxServerName+1), false},
	} {
		if _, err := ObserverAdapter(parent, tc.kind, tc.server); (err == nil) != tc.ok {
			t.Errorf("ObserverAdapter(%s, server of %d bytes): %v", tc.kind, len(tc.server), err)
		}
	}
}
