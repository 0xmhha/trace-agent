package ingress

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestCodexOTLPObservationReplay(t *testing.T) {
	r, db, a, token, _ := receiverFor(t, collect.CodexTelemetry)
	if _, err := db.Register(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	const canary = "sensitive-fixture-value"
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	makeRecord := func(observed time.Time, status string) *logs.LogRecord {
		return &logs.LogRecord{ObservedTimeUnixNano: uint64(observed.UnixNano()), Attributes: []*common.KeyValue{kv("event.name", "codex.api_request"), kv("event.timestamp", stamp.Format(time.RFC3339Nano)), kv("conversation.id", canary), kv("endpoint", canary), kv("output", canary), kv("duration_ms", "11"), kv("attempt", "0"), kv("http.response.status_code", status)}}
	}
	first := makeRecord(stamp.Add(time.Microsecond), "429")
	second := makeRecord(stamp.Add(2*time.Microsecond), canary)
	scope := &logs.ScopeLogs{Scope: &common.InstrumentationScope{Name: "codex_otel.log_only"}, LogRecords: []*logs.LogRecord{first, second}}
	d := &logs.LogsData{ResourceLogs: []*logs.ResourceLogs{{ScopeLogs: []*logs.ScopeLogs{scope}}}}
	b, err := proto.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	db.afterCommit = true
	if status, _ := send(t, r, protoType, b, token, nil); status != 503 {
		t.Fatal("failed to simulate lost acknowledgement", status)
	}
	p, err := db.Point(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.Records(t.Context(), p)
	if err != nil || len(before) != 2 {
		t.Fatal("missing durable observations before retry", err)
	}
	scope.LogRecords = []*logs.LogRecord{second, first}
	b, err = protojson.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if status, reply := send(t, r, jsonType, b, token, nil); status != 200 || string(reply) != "{}" {
		t.Fatal(status, string(reply))
	}
	point, err := db.Point(t.Context())
	if err != nil || point.Epoch != p.Epoch {
		t.Fatal("retry changed point", err)
	}
	records, err := db.Records(t.Context(), point)
	if err != nil || len(records) != 2 {
		t.Fatal("same-ms observations merged", err, len(records))
	}
	statusCount := 0
	for _, r := range records {
		e := r.Evidence
		safe, err := json.Marshal(e.Metadata)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(safe, []byte(canary)) || e.Metadata["attempt"] != float64(0) || e.Metadata["duration_ms"] != float64(11) {
			t.Fatal("unsafe or incomplete metadata")
		}
		if v, ok := e.Metadata["http.response.status_code"]; ok {
			if v != float64(429) {
				t.Fatal("invalid status")
			}
			statusCount++
		}
	}
	if statusCount != 1 {
		t.Fatal("HTTP status projection failed")
	}
	first.ObservedTimeUnixNano = 0
	scope.LogRecords = []*logs.LogRecord{first}
	b, err = protojson.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if status, reply := send(t, r, jsonType, b, token, nil); status != 200 || !bytes.Contains(reply, []byte(`"rejectedLogRecords":"1"`)) {
		t.Fatal("unidentified observation accepted", status, string(reply))
	}
}
