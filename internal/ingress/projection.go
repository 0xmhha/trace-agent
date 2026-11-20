package ingress

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var errInput = errors.New("invalid OTLP payload")

// Cap container allocations before handing a packet to the generated decoder.
// A small wire packet can otherwise encode millions of empty repeated messages.
func boundWire(raw []byte, desc protoreflect.MessageDescriptor, left *int, depth int) error {
	if depth > 40 {
		return errSize
	}
	for len(raw) > 0 {
		*left--
		if *left < 0 {
			return errSize
		}
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return errInput
		}
		raw = raw[n:]
		field := desc.Fields().ByNumber(num)
		if typ == protowire.BytesType && field != nil && field.Message() != nil {
			v, n := protowire.ConsumeBytes(raw)
			if n < 0 {
				return errInput
			}
			if err := boundWire(v, field.Message(), left, depth+1); err != nil {
				return err
			}
			raw = raw[n:]
			continue
		}
		// Modern OTLP schemas have no group fields.
		if typ == protowire.StartGroupType || typ == protowire.EndGroupType {
			return errInput
		}
		n = protowire.ConsumeFieldValue(num, typ, raw)
		if n < 0 {
			return errInput
		}
		raw = raw[n:]
	}
	return nil
}
func boundJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	depth := 0
	for count := 0; count < 180000; count++ {
		t, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errInput
		}
		if v, ok := t.(json.Delim); ok {
			switch v {
			case '[', '{':
				depth++
			case ']', '}':
				depth--
			}
			if depth > 40 {
				return errSize
			}
		}
	}
	return errSize
}
func attr(kvs []*common.KeyValue, key string) string {
	for _, kv := range kvs {
		if kv.GetKey() != key {
			continue
		}
		switch v := kv.GetValue().GetValue().(type) {
		case *common.AnyValue_StringValue:
			return v.StringValue
		case *common.AnyValue_IntValue:
			return strconv.FormatInt(v.IntValue, 10)
		}
	}
	return ""
}

func project(raw []byte, media string, consent bool, a *collect.Adapter) ([]collect.Evidence, int, error) {
	// ExportLogsServiceRequest and LogsData both carry repeated ResourceLogs at tag 1.
	var data logs.LogsData
	if media == jsonType {
		if err := boundJSON(raw); err != nil {
			return nil, 0, err
		}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true, RecursionLimit: 40}).Unmarshal(raw, &data); err != nil {
			return nil, 0, errInput
		}
	} else {
		remaining := 65000
		if err := boundWire(raw, data.ProtoReflect().Descriptor(), &remaining, 0); err != nil {
			return nil, 0, err
		}
		if err := (proto.UnmarshalOptions{DiscardUnknown: true, RecursionLimit: 40}).Unmarshal(raw, &data); err != nil {
			return nil, 0, errInput
		}
	}
	if consent {
		a = a.WithBodyCollection()
	}
	var out []collect.Evidence
	rejected := 0
	for _, r := range data.GetResourceLogs() {
		attrs := r.GetResource().GetAttributes()
		for _, s := range r.GetScopeLogs() {
			for _, log := range s.GetLogRecords() {
				if len(out)+rejected >= maxRecords {
					return nil, 0, errSize
				}
				e, ok := entry(a, log, attrs, s.GetScope().GetName(), consent)
				if !ok {
					rejected++
					continue
				}
				out = append(out, e)
			}
		}
	}
	return out, rejected, nil
}
func entry(a *collect.Adapter, l *logs.LogRecord, resource []*common.KeyValue, scope string, consent bool) (collect.Evidence, bool) {
	kvs := l.GetAttributes()
	name := attr(kvs, "event.name")
	if name == "" {
		name = strings.TrimPrefix(l.GetEventName(), "claude_code.")
	}
	fields := map[string]string{"event.name": name, "service.version": attr(resource, "service.version")}
	for _, k := range []string{"session.id", "request_body_id", "message.uuid", "request_id", "message.id", "prompt.id", "tool_use_id", "model"} {
		if v := attr(kvs, k); v != "" {
			fields[k] = v
		}
	}
	if fields["session.id"] == "" {
		fields["session.id"] = attr(resource, "session.id")
	}
	stamp := attr(kvs, "event.timestamp")
	if stamp == "" && l.TimeUnixNano > 0 && l.TimeUnixNano <= math.MaxInt64 {
		stamp = time.Unix(0, int64(l.TimeUnixNano)).UTC().Format(time.RFC3339Nano)
	}
	if t, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
		fields["event.timestamp"] = t.UTC().Format(time.RFC3339Nano)
	}
	seq, err := strconv.ParseInt(attr(kvs, "event.sequence"), 10, 64)
	if err != nil || seq < 0 || fields["event.timestamp"] == "" || name == "" {
		return collect.Evidence{}, false
	}
	// Sequence numbers reset when Claude Code restarts. Use the event's original
	// timestamp with its sequence and stable source scope; transport batch/order
	// and request/response body bytes have no place in the retry identity.
	stream, _ := json.Marshal([]string{"otlp", scope, fields["session.id"], attr(resource, "service.instance.id")})
	gen, _ := json.Marshal([]string{fields["event.timestamp"], name})
	if consent && (name == "api_request_body" || name == "api_response_body") {
		fields["body"] = attr(kvs, "body")
	}
	raw, _ := json.Marshal(fields)
	e, err := a.Project(collect.Position{Stream: string(stream), Generation: string(gen), Offset: seq}, raw)
	return e, err == nil
}
