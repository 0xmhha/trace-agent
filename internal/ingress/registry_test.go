package ingress

import (
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
)

// kindAdapter builds an adapter of any registered kind. An observer kind is
// bound to the first registered kind that accepts it.
func kindAdapter(t *testing.T, k collect.Kind) *collect.Adapter {
	t.Helper()
	info, _ := collect.KindInfo(k)
	if !info.Observer {
		a, err := collect.New(k, "test", "otel", "", make([]byte, 32), nil, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	server := ""
	if info.Server {
		server = "north"
	}
	for _, parent := range collect.Kinds() {
		if collect.AcceptsParent(k, parent) {
			a, err := collect.ObserverAdapter(kindAdapter(t, parent), k, server)
			if err != nil {
				t.Fatal(err)
			}
			return a
		}
	}
	t.Fatalf("no registered kind accepts observer %s", k)
	return nil
}

// Every OTLP kind needs its own log mapping, and no other kind may be bound
// to the receiver. A kind registered without a sample here fails the test.
func TestEveryOTLPKindHasEntry(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	samples := map[collect.Kind]struct {
		scope string
		log   *logs.LogRecord
	}{
		collect.Telemetry: {"", sample("api_request", 1, "", at)},
		collect.CodexTelemetry: {"codex_otel.log_only", &logs.LogRecord{ObservedTimeUnixNano: uint64(at.UnixNano()), Attributes: []*common.KeyValue{
			kv("event.name", "codex.api_request"), kv("event.timestamp", at.Format(time.RFC3339Nano)), kv("conversation.id", "fixture")}}},
	}
	token := strings.Repeat("t", 32)
	for _, k := range collect.Kinds() {
		a := kindAdapter(t, k)
		s, ok := samples[k]
		if ok != collect.IsOTLP(k) {
			t.Errorf("%s: OTLP sample present %t, registered as OTLP %t", k, ok, collect.IsOTLP(k))
			continue
		}
		if !ok {
			// Validation fails before a port is bound.
			if _, err := Listen(&replaySink{}, a, 0, token); err == nil {
				t.Errorf("%s: a receiver was bound to a kind that is not OTLP", k)
			}
			if _, accepted := entry(a, samples[collect.Telemetry].log, nil, "", false); accepted {
				t.Errorf("%s: a kind without a log mapping accepted a record", k)
			}
			continue
		}
		e, accepted := entry(a, s.log, nil, s.scope, false)
		if !accepted || e.Kind != k || e.Metadata["event.name"] == nil || e.OccurredMS == nil || *e.OccurredMS != at.UnixMilli() {
			t.Errorf("%s: sample log was not mapped: %+v", k, e)
		}
	}
}
