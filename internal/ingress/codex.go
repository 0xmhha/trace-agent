package ingress

import (
	"encoding/json"
	"math"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
)

// Codex 0.169.1 emits metadata in attributes plus an SDK observation time
// in nanoseconds. Millisecond event.timestamp alone collides within a batch.
func codexEntry(a *collect.Adapter, l *logs.LogRecord, resource []*common.KeyValue, scope string, consent bool) (collect.Evidence, bool) {
	attrs := l.GetAttributes()
	name := attr(attrs, "event.name")
	session := attr(attrs, "conversation.id")
	stamp, err := time.Parse(time.RFC3339Nano, attr(attrs, "event.timestamp"))
	if scope != "codex_otel.log_only" || session == "" || name == "" || err != nil || l.ObservedTimeUnixNano == 0 || l.ObservedTimeUnixNano > math.MaxInt64 {
		return collect.Evidence{}, false
	}
	fields := map[string]string{"service.version": attr(resource, "service.version"), "event.name": name, "conversation.id": session, "event.timestamp": stamp.UTC().Format(time.RFC3339Nano)}
	for _, k := range []string{"model", "attempt", "http.response.status_code", "duration_ms", "call_id", "tool_name", "success"} {
		if v := attr(attrs, k); v != "" {
			fields[k] = v
		}
	}
	if consent && name == "codex.user_prompt" {
		fields["prompt"] = attr(attrs, "prompt")
	}
	stream, _ := json.Marshal([]string{scope, session})
	gen, _ := json.Marshal([]string{fields["event.timestamp"], name, fields["call_id"], fields["attempt"], attr(attrs, "event.kind"), attr(attrs, "tool_result_seq")})
	raw, _ := json.Marshal(fields)
	e, err := a.Project(collect.Position{Stream: string(stream), Generation: string(gen), Offset: int64(l.ObservedTimeUnixNano)}, raw)
	return e, err == nil
}
