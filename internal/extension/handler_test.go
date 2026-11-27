package extension

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func TestNewHandlerArguments(t *testing.T) {
	sink := &fakeSink{}
	now := newFakeClock().Now
	for _, tc := range []struct {
		name   string
		sink   usage.ExtSink
		host   string
		bearer string
		now    func() time.Time
		want   error
	}{
		{"valid", sink, testHost, testBearer(), now, nil},
		{"shortest bearer", sink, testHost, strings.Repeat("k", minBearerLen), now, nil},
		{"longest bearer", sink, testHost, strings.Repeat("k", maxBearerLen), now, nil},
		{"ipv6 loopback host", sink, "[::1]:4318", testBearer(), now, nil},
		{"nil sink", nil, testHost, testBearer(), now, errNilSink},
		{"nil clock", sink, testHost, testBearer(), nil, errNilClock},
		{"bearer too short", sink, testHost, strings.Repeat("k", minBearerLen-1), now, errBadBearer},
		{"bearer too long", sink, testHost, strings.Repeat("k", maxBearerLen+1), now, errBadBearer},
		{"bearer with space", sink, testHost, strings.Repeat("k", 20) + " " + strings.Repeat("k", 20), now, errBadBearer},
		{"bearer with newline", sink, testHost, strings.Repeat("k", 40) + "\n", now, errBadBearer},
		{"bearer with control character", sink, testHost, strings.Repeat("k", 40) + "\x00", now, errBadBearer},
		{"empty host", sink, "", testBearer(), now, errBadHost},
		{"host without port", sink, "127.0.0.1", testBearer(), now, errBadHost},
		{"host name", sink, "localhost:4318", testBearer(), now, errBadHost},
		{"public address", sink, "192.0.2.1:4318", testBearer(), now, errBadHost},
		{"zero port", sink, "127.0.0.1:0", testBearer(), now, errBadHost},
		{"port out of range", sink, "127.0.0.1:65536", testBearer(), now, errBadHost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewHandler(tc.sink, tc.host, tc.bearer, tc.now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v; want %v", err, tc.want)
			}
			if (h == nil) != (tc.want != nil) {
				t.Fatalf("handler = %v with error %v", h, err)
			}
		})
	}
}

// Requests refused before authentication must leave no trace in the store.
func TestHandlerRefusesUnaddressedAndUnauthenticated(t *testing.T) {
	body := encode(t, minimalEvent())
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		want   int
	}{
		{"get", func(r *http.Request) { r.Method = http.MethodGet }, http.StatusForbidden},
		{"put", func(r *http.Request) { r.Method = http.MethodPut }, http.StatusForbidden},
		{"other path", func(r *http.Request) { r.URL.Path = "/v1/hooks" }, http.StatusForbidden},
		{"path with trailing slash", func(r *http.Request) { r.URL.Path = EventsPath + "/" }, http.StatusForbidden},
		{"query", func(r *http.Request) { r.URL.RawQuery = "a=1" }, http.StatusForbidden},
		{"host name", func(r *http.Request) { r.Host = "localhost:4318" }, http.StatusForbidden},
		{"other port", func(r *http.Request) { r.Host = "127.0.0.1:4319" }, http.StatusForbidden},
		{"foreign host", func(r *http.Request) { r.Host = "example.com" }, http.StatusForbidden},
		{"origin", func(r *http.Request) { r.Header.Set(headerOrigin, "http://"+testHost) }, http.StatusForbidden},
		{"empty origin", func(r *http.Request) { r.Header[headerOrigin] = []string{""} }, http.StatusForbidden},
		{"forbidden wins over unauthorized", func(r *http.Request) {
			r.Host = "example.com"
			r.Header.Del(headerAuthorization)
		}, http.StatusForbidden},
		{"no credential", func(r *http.Request) { r.Header.Del(headerAuthorization) }, http.StatusUnauthorized},
		{"wrong credential", func(r *http.Request) {
			r.Header.Set(headerAuthorization, bearerPrefix+strings.Repeat("x", testBearerLen))
		}, http.StatusUnauthorized},
		{"credential prefix only", func(r *http.Request) {
			r.Header.Set(headerAuthorization, bearerPrefix+testBearer()[:testBearerLen-1])
		}, http.StatusUnauthorized},
		{"credential with suffix", func(r *http.Request) {
			r.Header.Set(headerAuthorization, bearerPrefix+testBearer()+"k")
		}, http.StatusUnauthorized},
		{"no scheme", func(r *http.Request) { r.Header.Set(headerAuthorization, testBearer()) }, http.StatusUnauthorized},
		{"lowercase scheme", func(r *http.Request) {
			r.Header.Set(headerAuthorization, "bearer "+testBearer())
		}, http.StatusUnauthorized},
		{"two credentials", func(r *http.Request) {
			r.Header.Add(headerAuthorization, bearerPrefix+testBearer())
		}, http.StatusUnauthorized},
		{"unauthorized wins over content type", func(r *http.Request) {
			r.Header.Del(headerAuthorization)
			r.Header.Set(headerContentType, "text/plain")
		}, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			req := newRequest(body)
			tc.change(req)
			if rec := f.do(req); rec.Code != tc.want {
				t.Fatalf("status = %d; want %d", rec.Code, tc.want)
			}
			if !f.sink.untouched() {
				t.Fatalf("sink was called: %d appends, rejects %v", f.sink.appendCalls(), f.sink.rejectCalls())
			}
		})
	}
}

// An authenticated request that is refused as a whole counts as one
// unattributed rejection and stores nothing.
func TestHandlerRefusesWholeRequest(t *testing.T) {
	valid := encode(t, minimalEvent())
	oversized := bytes.Repeat([]byte(" "), MaxBodyBytes+1)
	tooMany := make([]any, MaxEvents+1)
	for i := range tooMany {
		tooMany[i] = minimalEvent()
	}
	for _, tc := range []struct {
		name   string
		body   []byte
		change func(*http.Request)
		want   int
	}{
		{"text content", valid, func(r *http.Request) { r.Header.Set(headerContentType, "text/plain") }, http.StatusUnsupportedMediaType},
		{"no content type", valid, func(r *http.Request) { r.Header.Del(headerContentType) }, http.StatusUnsupportedMediaType},
		{"malformed content type", valid, func(r *http.Request) { r.Header.Set(headerContentType, "application/json; =") }, http.StatusUnsupportedMediaType},
		{"json suffix type", valid, func(r *http.Request) { r.Header.Set(headerContentType, "application/problem+json") }, http.StatusUnsupportedMediaType},
		{"declared length over the cap", oversized, nil, http.StatusRequestEntityTooLarge},
		{"undeclared length over the cap", oversized, func(r *http.Request) { r.ContentLength = -1 }, http.StatusRequestEntityTooLarge},
		{"empty body", nil, nil, http.StatusBadRequest},
		{"blank body", []byte(" \n"), nil, http.StatusBadRequest},
		{"truncated json", []byte(`{"version":1`), nil, http.StatusBadRequest},
		{"trailing data", append(bytes.Clone(valid), " x"...), nil, http.StatusBadRequest},
		{"top-level array", encode(t, []any{minimalEvent()}), nil, http.StatusBadRequest},
		{"top-level null", []byte("null"), nil, http.StatusBadRequest},
		{"top-level string", []byte(`"event"`), nil, http.StatusBadRequest},
		{"events is an object", []byte(`{"events":{}}`), nil, http.StatusBadRequest},
		{"events is null", []byte(`{"events":null}`), nil, http.StatusBadRequest},
		{"events is a number", []byte(`{"events":1}`), nil, http.StatusBadRequest},
		{"too many events", encode(t, batch(tooMany...)), nil, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			req := newRequest(tc.body)
			if tc.change != nil {
				tc.change(req)
			}
			if rec := f.do(req); rec.Code != tc.want {
				t.Fatalf("status = %d, body %q; want %d", rec.Code, rec.Body.String(), tc.want)
			}
			if n := f.sink.appendCalls(); n != 0 {
				t.Fatalf("AppendExt called %d times", n)
			}
			if got, want := f.sink.rejectCalls(), []rejectCall{{unattributed, 1}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("rejections = %v; want %v", got, want)
			}
		})
	}
}

func TestHandlerAcceptsContentTypeWithParameters(t *testing.T) {
	f := newFixture(t)
	req := newRequest(encode(t, minimalEvent()))
	req.Header.Set(headerContentType, "Application/JSON; charset=utf-8")
	if got := decodeResponse(t, f.do(req)); got.Accepted != 1 {
		t.Fatalf("accepted = %d; want 1", got.Accepted)
	}
}

func TestHandlerStoresFullEvent(t *testing.T) {
	f := newFixture(t)
	rec := f.postJSON(t, obj{
		"version":        WireVersion,
		"id":             "run-7:step-3",
		"source":         testSource,
		"kind":           "span",
		"name":           "upload",
		"timestamp":      "2026-01-01T10:00:00.250+09:00",
		"duration_ms":    812.5,
		"status":         "error",
		"level":          "warn",
		"session_id":     "session-1",
		"tool_use_id":    "toolu_01",
		"trace_id":       "trace.1",
		"span_id":        "span:2",
		"parent_span_id": "span:1",
		"message":        "exit status 1",
		"attributes":     obj{"step": "3", "retries": 2, "ratio": 0.25, "dry_run": true},
		"value":          1.0,
		"unit":           "count",
		"future_field":   "ignored",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), `{"accepted":1,"rejected":[]}`; got != want {
		t.Fatalf("body = %s; want %s", got, want)
	}
	if got := rec.Header().Get(headerContentType); got != jsonMediaType {
		t.Fatalf("content type = %q", got)
	}
	if n := f.sink.appendCalls(); n != 1 {
		t.Fatalf("AppendExt called %d times; want 1", n)
	}
	if calls := f.sink.rejectCalls(); len(calls) != 0 {
		t.Fatalf("unexpected rejections %v", calls)
	}
	if f.sink.noDeadline {
		t.Fatal("sink was called without a deadline")
	}
	got := f.sink.stored()[0]
	duration, value := 812.5, 1.0
	want := usage.ExtEvent{
		ID:           got.ID,
		Source:       testSource,
		Kind:         usage.ExtSpan,
		Name:         "upload",
		At:           time.Date(2026, 1, 1, 1, 0, 0, 250_000_000, time.UTC),
		ReceivedAt:   testEpoch(),
		DurationMs:   &duration,
		Status:       usage.ExtError,
		Level:        usage.ExtWarn,
		SessionID:    "session-1",
		ToolUseID:    "toolu_01",
		TraceID:      "trace.1",
		SpanID:       "span:2",
		ParentSpanID: "span:1",
		Message:      "exit status 1",
		Attributes:   map[string]string{"step": "3", "retries": "2", "ratio": "0.25", "dry_run": "true"},
		Value:        &value,
		Unit:         "count",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored event\n got %+v\nwant %+v", got, want)
	}
	assertEventID(t, got.ID)
}

func TestHandlerAppliesDefaults(t *testing.T) {
	f := newFixture(t)
	f.accept(t, minimalEvent())
	got := f.sink.stored()[0]
	if got.Status != usage.ExtUnknown || got.Level != usage.ExtInfo {
		t.Fatalf("status %q level %q; want %q %q", got.Status, got.Level, usage.ExtUnknown, usage.ExtInfo)
	}
	if !got.At.Equal(testEpoch()) || !got.ReceivedAt.Equal(testEpoch()) {
		t.Fatalf("at %v received %v; want both %v", got.At, got.ReceivedAt, testEpoch())
	}
	if got.DurationMs != nil || got.Value != nil || got.Attributes != nil || got.Message != "" || got.Unit != "" {
		t.Fatalf("optional fields were invented: %+v", got)
	}
}

func TestHandlerAcceptsBoundaryValues(t *testing.T) {
	manyAttributes := obj{}
	for i := range MaxAttributes {
		manyAttributes[strings.Repeat("k", i+1)] = i
	}
	for _, tc := range []struct {
		name    string
		members obj
	}{
		{"longest source", obj{"source": strings.Repeat("s", 64)}},
		{"longest name in multi-byte characters", obj{"name": strings.Repeat("가", MaxNameLen)}},
		{"longest id", obj{"id": strings.Repeat("i", 128)}},
		{"longest link ids", obj{"session_id": strings.Repeat("a", 128), "tool_use_id": strings.Repeat("b", 128)}},
		{"longest unit", obj{"unit": strings.Repeat("u", MaxUnitLen)}},
		{"most attributes", obj{"attributes": manyAttributes}},
		{"longest attribute key", obj{"attributes": obj{strings.Repeat("k", 64): "v"}}},
		{"zero numbers", obj{"duration_ms": 0, "value": 0}},
		{"every kind: log", obj{"kind": "log"}},
		{"every kind: metric", obj{"kind": "metric"}},
		{"every status: ok", obj{"status": "ok"}},
		{"every status: unknown", obj{"status": "unknown"}},
		{"every level: debug", obj{"level": "debug"}},
		{"every level: error", obj{"level": "error"}},
		{"timestamp with fraction", obj{"timestamp": "2026-01-01T01:00:00.123456789Z"}},
		{"empty optional members", obj{"id": "", "timestamp": "", "status": "", "level": "", "session_id": "", "message": "", "unit": "", "attributes": obj{}}},
		{"null optional members", obj{"duration_ms": nil, "value": nil, "attributes": nil, "message": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			got := f.accept(t, with(minimalEvent(), tc.members))
			if got.Accepted != 1 || len(got.Rejected) != 0 {
				t.Fatalf("response = %+v; want one accepted", got)
			}
		})
	}
}

func TestHandlerAcceptsBodyAtTheCap(t *testing.T) {
	f := newFixture(t)
	event := with(minimalEvent(), obj{"message": ""})
	padding := MaxBodyBytes - len(encode(t, event))
	event["message"] = strings.Repeat("m", padding)
	body := encode(t, event)
	if len(body) != MaxBodyBytes {
		t.Fatalf("body is %d bytes; want %d", len(body), MaxBodyBytes)
	}
	if got := decodeResponse(t, f.post(body)); got.Accepted != 1 {
		t.Fatalf("accepted = %d; want 1", got.Accepted)
	}
}

func TestHandlerAcceptsMostEvents(t *testing.T) {
	f := newFixture(t)
	events := make([]any, MaxEvents)
	for i := range events {
		events[i] = minimalEvent()
	}
	got := f.accept(t, batch(events...))
	if got.Accepted != MaxEvents || len(f.sink.stored()) != MaxEvents {
		t.Fatalf("accepted %d, stored %d; want %d", got.Accepted, len(f.sink.stored()), MaxEvents)
	}
}

func TestHandlerAcceptsEmptyBatch(t *testing.T) {
	f := newFixture(t)
	rec := f.post([]byte(`{"events":[]}`))
	if rec.Code != http.StatusAccepted || rec.Body.String() != `{"accepted":0,"rejected":[]}` {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !f.sink.untouched() {
		t.Fatal("an empty batch reached the sink")
	}
}

// Every invalid event is refused with a reason and counted under its source
// when the source itself is usable.
func TestHandlerRejectsInvalidEvent(t *testing.T) {
	tooManyAttributes := obj{}
	for i := range MaxAttributes + 1 {
		tooManyAttributes[strings.Repeat("k", i+1)] = "v"
	}
	raw := func(text string) json.RawMessage { return json.RawMessage(text) }
	for _, tc := range []struct {
		name       string
		event      obj
		reason     rejectReason
		wantSource string
	}{
		{"no version", without(minimalEvent(), "version"), reasonInvalidVersion, testSource},
		{"newer version", with(minimalEvent(), obj{"version": WireVersion + 1}), reasonInvalidVersion, testSource},
		{"version as text", with(minimalEvent(), obj{"version": "1"}), reasonInvalidVersion, testSource},
		{"fractional version", with(minimalEvent(), obj{"version": 1.5}), reasonInvalidVersion, testSource},
		{"id with space", with(minimalEvent(), obj{"id": "a b"}), reasonInvalidID, testSource},
		{"id too long", with(minimalEvent(), obj{"id": strings.Repeat("i", 129)}), reasonInvalidID, testSource},
		{"id as number", with(minimalEvent(), obj{"id": 7}), reasonInvalidID, testSource},
		{"no source", without(minimalEvent(), "source"), reasonMissingSource, unattributed},
		{"empty source", with(minimalEvent(), obj{"source": ""}), reasonMissingSource, unattributed},
		{"source with slash", with(minimalEvent(), obj{"source": "a/b"}), reasonInvalidSource, unattributed},
		{"source with newline", with(minimalEvent(), obj{"source": "deploy\n"}), reasonInvalidSource, unattributed},
		{"source too long", with(minimalEvent(), obj{"source": strings.Repeat("s", 65)}), reasonInvalidSource, unattributed},
		{"source as number", with(minimalEvent(), obj{"source": 5}), reasonInvalidSource, unattributed},
		{"no kind", without(minimalEvent(), "kind"), reasonMissingKind, testSource},
		{"unknown kind", with(minimalEvent(), obj{"kind": "trace"}), reasonInvalidKind, testSource},
		{"kind in capitals", with(minimalEvent(), obj{"kind": "SPAN"}), reasonInvalidKind, testSource},
		{"no name", without(minimalEvent(), "name"), reasonMissingName, testSource},
		{"empty name", with(minimalEvent(), obj{"name": ""}), reasonMissingName, testSource},
		{"name too long", with(minimalEvent(), obj{"name": strings.Repeat("가", MaxNameLen+1)}), reasonInvalidName, testSource},
		{"name as number", with(minimalEvent(), obj{"name": 1}), reasonInvalidName, testSource},
		{"timestamp in words", with(minimalEvent(), obj{"timestamp": "yesterday"}), reasonInvalidTimestamp, testSource},
		{"timestamp without zone", with(minimalEvent(), obj{"timestamp": "2026-01-01T01:00:00"}), reasonInvalidTimestamp, testSource},
		{"timestamp as number", with(minimalEvent(), obj{"timestamp": 1767229200}), reasonInvalidTimestamp, testSource},
		{"negative duration", with(minimalEvent(), obj{"duration_ms": -1}), reasonInvalidDuration, testSource},
		{"duration as text", with(minimalEvent(), obj{"duration_ms": "fast"}), reasonInvalidDuration, testSource},
		{"duration beyond float range", with(minimalEvent(), obj{"duration_ms": raw("1e999")}), reasonInvalidDuration, testSource},
		{"negative value", with(minimalEvent(), obj{"value": -0.5}), reasonInvalidValue, testSource},
		{"value beyond float range", with(minimalEvent(), obj{"value": raw("1e999")}), reasonInvalidValue, testSource},
		{"unknown status", with(minimalEvent(), obj{"status": "failed"}), reasonInvalidStatus, testSource},
		{"unknown level", with(minimalEvent(), obj{"level": "fatal"}), reasonInvalidLevel, testSource},
		{"session id with space", with(minimalEvent(), obj{"session_id": "a b"}), reasonInvalidSessionID, testSource},
		{"tool use id too long", with(minimalEvent(), obj{"tool_use_id": strings.Repeat("t", 129)}), reasonInvalidToolUseID, testSource},
		{"trace id with slash", with(minimalEvent(), obj{"trace_id": "a/b"}), reasonInvalidTraceID, testSource},
		{"span id with quote", with(minimalEvent(), obj{"span_id": `a"b`}), reasonInvalidSpanID, testSource},
		{"parent span id with space", with(minimalEvent(), obj{"parent_span_id": " "}), reasonInvalidParentSpanID, testSource},
		{"message as object", with(minimalEvent(), obj{"message": obj{"text": "x"}}), reasonInvalidMessage, testSource},
		{"unit too long", with(minimalEvent(), obj{"unit": strings.Repeat("u", MaxUnitLen+1)}), reasonInvalidUnit, testSource},
		{"attributes as array", with(minimalEvent(), obj{"attributes": []any{"a"}}), reasonInvalidAttributes, testSource},
		{"too many attributes", with(minimalEvent(), obj{"attributes": tooManyAttributes}), reasonTooManyAttributes, testSource},
		{"attribute key with space", with(minimalEvent(), obj{"attributes": obj{"a b": "v"}}), reasonInvalidAttributeKey, testSource},
		{"empty attribute key", with(minimalEvent(), obj{"attributes": obj{"": "v"}}), reasonInvalidAttributeKey, testSource},
		{"attribute key too long", with(minimalEvent(), obj{"attributes": obj{strings.Repeat("k", 65): "v"}}), reasonInvalidAttributeKey, testSource},
		{"null attribute", with(minimalEvent(), obj{"attributes": obj{"a": nil}}), reasonInvalidAttributeValue, testSource},
		{"object attribute", with(minimalEvent(), obj{"attributes": obj{"a": obj{}}}), reasonInvalidAttributeValue, testSource},
		{"array attribute", with(minimalEvent(), obj{"attributes": obj{"a": []any{1}}}), reasonInvalidAttributeValue, testSource},
		{"bad key reported before bad value", with(minimalEvent(), obj{"attributes": obj{"a b": "v", "z": nil}}), reasonInvalidAttributeKey, testSource},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			got := f.accept(t, tc.event)
			want := WireResponse{Accepted: 0, Rejected: []WireRejection{{Index: 0, Reason: string(tc.reason)}}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("response = %+v; want %+v", got, want)
			}
			if n := f.sink.appendCalls(); n != 0 {
				t.Fatalf("AppendExt called %d times", n)
			}
			if got, want := f.sink.rejectCalls(), []rejectCall{{tc.wantSource, 1}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("rejections = %v; want %v", got, want)
			}
		})
	}
}

func TestHandlerAcceptsPartOfABatch(t *testing.T) {
	f := newFixture(t)
	alpha := with(minimalEvent(), obj{"source": "alpha"})
	beta := with(minimalEvent(), obj{"source": "beta", "name": "second"})
	got := f.accept(t, batch(
		alpha,
		without(alpha, "name"),
		7,
		beta,
		with(minimalEvent(), obj{"source": "bad source"}),
		with(beta, obj{"kind": "trace"}),
		with(alpha, obj{"status": "failed"}),
		nil,
		"text",
		[]any{alpha},
	))
	want := WireResponse{Accepted: 2, Rejected: []WireRejection{
		{1, string(reasonMissingName)},
		{2, string(reasonInvalidEvent)},
		{4, string(reasonInvalidSource)},
		{5, string(reasonInvalidKind)},
		{6, string(reasonInvalidStatus)},
		{7, string(reasonInvalidEvent)},
		{8, string(reasonInvalidEvent)},
		{9, string(reasonInvalidEvent)},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %+v; want %+v", got, want)
	}
	if n := f.sink.appendCalls(); n != 1 {
		t.Fatalf("AppendExt called %d times; want 1", n)
	}
	stored := f.sink.stored()
	if len(stored) != 2 || stored[0].Source != "alpha" || stored[1].Source != "beta" {
		t.Fatalf("stored = %+v; want alpha then beta", stored)
	}
	// One call per source, in source order.
	wantCalls := []rejectCall{{unattributed, 5}, {"alpha", 2}, {"beta", 1}}
	if calls := f.sink.rejectCalls(); !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("rejections = %v; want %v", calls, wantCalls)
	}
}

func TestHandlerMasksSecrets(t *testing.T) {
	f := newFixture(t)
	secret := hostingToken()
	secretKey := "api" + "_key"
	f.accept(t, with(minimalEvent(), obj{
		"name":    "push " + secret,
		"message": "remote rejected " + secret + " as expired",
		"attributes": obj{
			"note":         "used " + secret,
			secretKey:      "looks-harmless",
			"auth_token":   12345,
			"db_password":  true,
			"input_tokens": 15234,
			"step":         "3",
		},
	}))
	got := f.sink.stored()[0]
	if want := "push " + usage.MaskToken; got.Name != want {
		t.Errorf("name = %q; want %q", got.Name, want)
	}
	if want := "remote rejected " + usage.MaskToken + " as expired"; got.Message != want {
		t.Errorf("message = %q; want %q", got.Message, want)
	}
	wantAttributes := map[string]string{
		"note":         "used " + usage.MaskToken,
		secretKey:      usage.MaskToken,
		"auth_token":   usage.MaskToken,
		"db_password":  usage.MaskToken,
		"input_tokens": "15234",
		"step":         "3",
	}
	if !reflect.DeepEqual(got.Attributes, wantAttributes) {
		t.Errorf("attributes = %v; want %v", got.Attributes, wantAttributes)
	}
	if strings.Contains(string(encode(t, got)), secret) {
		t.Errorf("the secret survived in the stored event: %+v", got)
	}
}

func TestHandlerCapsAfterMasking(t *testing.T) {
	f := newFixture(t)
	secret := hostingToken()
	// Masking lengthens this name past the cap: a one-character password
	// becomes the longer mask token.
	growingName := strings.Repeat("n", MaxNameLen-8) + " -" + "u a:" + "b"
	// The secret straddles the cap. Cutting first would leave a prefix too
	// short for any rule to recognise.
	straddling := strings.Repeat("m", MaxMessageLen-8) + " " + secret + " " + strings.Repeat("t", 100)
	f.accept(t, batch(
		with(minimalEvent(), obj{
			"name":    growingName,
			"message": straddling,
			"attributes": obj{
				"ascii": strings.Repeat("v", MaxAttributeValue+50),
				"wide":  strings.Repeat("값", MaxAttributeValue+50),
				"edge":  strings.Repeat("v", MaxAttributeValue-8) + " " + secret + " " + strings.Repeat("t", 100),
			},
		}),
		with(minimalEvent(), obj{"message": strings.Repeat("글", MaxMessageLen+500)}),
		with(minimalEvent(), obj{"message": strings.Repeat("m", MaxMessageLen)}),
	))
	stored := f.sink.stored()
	first := stored[0]
	if n := len([]rune(first.Name)); n != MaxNameLen {
		t.Errorf("name has %d characters; want %d", n, MaxNameLen)
	}
	if !strings.Contains(first.Name, "a:[") || strings.HasSuffix(first.Name, "a:b") {
		t.Errorf("name was not masked before the cut: %q", first.Name[len(first.Name)-16:])
	}
	if n := len([]rune(first.Message)); n != MaxMessageLen {
		t.Errorf("message has %d characters; want %d", n, MaxMessageLen)
	}
	if strings.Contains(first.Message, secret[:6]) {
		t.Errorf("message keeps a piece of the secret: %q", first.Message[len(first.Message)-16:])
	}
	if got := first.Attributes["ascii"]; got != strings.Repeat("v", MaxAttributeValue) {
		t.Errorf("ascii attribute has %d bytes; want %d", len(got), MaxAttributeValue)
	}
	if got := first.Attributes["wide"]; got != strings.Repeat("값", MaxAttributeValue) {
		t.Errorf("wide attribute has %d characters; want %d", len([]rune(got)), MaxAttributeValue)
	}
	if got := first.Attributes["edge"]; strings.Contains(got, secret[:6]) || len([]rune(got)) > MaxAttributeValue {
		t.Errorf("edge attribute = %q", got)
	}
	if got := stored[1].Message; got != strings.Repeat("글", MaxMessageLen) {
		t.Errorf("wide message has %d characters; want %d", len([]rune(got)), MaxMessageLen)
	}
	if got := stored[2].Message; len(got) != MaxMessageLen {
		t.Errorf("message at the cap was cut to %d", len(got))
	}
}

func TestHandlerFoldsNegativeZero(t *testing.T) {
	f := newFixture(t)
	f.accept(t, with(minimalEvent(), obj{"duration_ms": json.RawMessage("-0"), "value": json.RawMessage("-0.0")}))
	got := f.sink.stored()[0]
	for name, v := range map[string]*float64{"duration": got.DurationMs, "value": got.Value} {
		if v == nil || *v != 0 || 1 / *v < 0 {
			t.Errorf("%s = %v; want positive zero", name, v)
		}
	}
}

func TestHandlerKeepsNumberSpellingInAttributes(t *testing.T) {
	f := newFixture(t)
	f.accept(t, with(minimalEvent(), obj{"attributes": obj{
		"big":   json.RawMessage("12345678901234567890"),
		"exp":   json.RawMessage("1e3"),
		"small": json.RawMessage("0.10"),
	}}))
	want := map[string]string{"big": "12345678901234567890", "exp": "1e3", "small": "0.10"}
	if got := f.sink.stored()[0].Attributes; !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes = %v; want %v", got, want)
	}
}

func TestHandlerLimitsRate(t *testing.T) {
	f := newFixture(t)
	body := encode(t, minimalEvent())
	// Requests that fail to authenticate must not use up the allowance.
	for range ratePerWindow {
		unauthenticated := newRequest(body)
		unauthenticated.Header.Del(headerAuthorization)
		f.do(unauthenticated)
	}
	expect := func(want int) {
		t.Helper()
		rec := f.post(body)
		if rec.Code != want {
			t.Fatalf("status = %d; want %d", rec.Code, want)
		}
		if want == http.StatusTooManyRequests && rec.Header().Get(headerRetryAfter) != retryAfterSeconds {
			t.Fatalf("429 without %s", headerRetryAfter)
		}
	}
	for range ratePerWindow {
		expect(http.StatusAccepted)
	}
	appended, rejected := f.sink.appendCalls(), len(f.sink.rejectCalls())
	expect(http.StatusTooManyRequests)
	f.clock.Advance(rateWindow - time.Millisecond)
	expect(http.StatusTooManyRequests)
	if f.sink.appendCalls() != appended || len(f.sink.rejectCalls()) != rejected {
		t.Fatal("a rate-limited request reached the sink")
	}
	f.clock.Advance(time.Millisecond)
	for range ratePerWindow {
		expect(http.StatusAccepted)
	}
	expect(http.StatusTooManyRequests)
}

func TestHandlerLimitsRateOfRefusedRequests(t *testing.T) {
	body := encode(t, minimalEvent())
	tooMany := make([]any, MaxEvents+1)
	for i := range tooMany {
		tooMany[i] = minimalEvent()
	}
	for _, tc := range []struct {
		name   string
		body   []byte
		mutate func(*http.Request)
		want   int
	}{
		{name: "wrong content type", body: body, mutate: func(r *http.Request) { r.Header.Set(headerContentType, "text/plain") },
			want: http.StatusUnsupportedMediaType},
		{name: "body too large", body: body, mutate: func(r *http.Request) { r.ContentLength = MaxBodyBytes + 1 },
			want: http.StatusRequestEntityTooLarge},
		{name: "too many events", body: encode(t, batch(tooMany...)), want: http.StatusRequestEntityTooLarge},
		{name: "not an event", body: []byte("[]"), want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			send := func() *httptest.ResponseRecorder {
				req := newRequest(tc.body)
				if tc.mutate != nil {
					tc.mutate(req)
				}
				return f.do(req)
			}
			for i := range ratePerWindow {
				if rec := send(); rec.Code != tc.want {
					t.Fatalf("request %d: status = %d; want %d", i, rec.Code, tc.want)
				}
			}
			for i := range ratePerWindow {
				rec := send()
				if rec.Code != http.StatusTooManyRequests || rec.Header().Get(headerRetryAfter) != retryAfterSeconds {
					t.Fatalf("request %d over the limit: status = %d, %s %q; want 429 with a wait",
						ratePerWindow+i, rec.Code, headerRetryAfter, rec.Header().Get(headerRetryAfter))
				}
			}
			if got := len(f.sink.rejectCalls()); got != ratePerWindow {
				t.Fatalf("CountRejected calls = %d; want %d, one per admitted request", got, ratePerWindow)
			}
			if f.sink.appendCalls() != 0 {
				t.Fatal("a refused request appended events")
			}
		})
	}
}

func TestHandlerLimitsConcurrencyBeforeCounting(t *testing.T) {
	sink := &fakeSink{blockAppend: true, entered: make(chan struct{}), release: make(chan struct{})}
	h, err := NewHandler(sink, testHost, testBearer(), newFakeClock().Now)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{handler: h, sink: sink}
	body := encode(t, minimalEvent())
	codes := make(chan int, maxInFlight)
	var wg sync.WaitGroup
	for range maxInFlight {
		wg.Go(func() { codes <- f.post(body).Code })
	}
	for range maxInFlight {
		<-sink.entered
	}
	// With every slot taken, a wrong-content request must not reach the
	// store's rejection counter.
	wrongContent := newRequest(body)
	wrongContent.Header.Set(headerContentType, "text/plain")
	rec := f.do(wrongContent)
	close(sink.release)
	wg.Wait()
	close(codes)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusTooManyRequests)
	}
	if got := len(sink.rejectCalls()); got != 0 {
		t.Fatalf("CountRejected calls = %d while every slot was taken; want 0", got)
	}
	for code := range codes {
		if code != http.StatusAccepted {
			t.Fatalf("in-flight request ended with %d", code)
		}
	}
}

func TestHandlerLimitsConcurrency(t *testing.T) {
	sink := &fakeSink{blockAppend: true, entered: make(chan struct{}), release: make(chan struct{})}
	h, err := NewHandler(sink, testHost, testBearer(), newFakeClock().Now)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{handler: h, sink: sink}
	body := encode(t, minimalEvent())
	codes := make(chan int, maxInFlight)
	var wg sync.WaitGroup
	for range maxInFlight {
		wg.Go(func() { codes <- f.post(body).Code })
	}
	// Every slot is taken once each request has reached the sink.
	for range maxInFlight {
		<-sink.entered
	}
	rec := f.post(body)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get(headerRetryAfter) != retryAfterSeconds {
		t.Fatalf("status = %d, %s %q; want 429 with a wait", rec.Code, headerRetryAfter, rec.Header().Get(headerRetryAfter))
	}
	close(sink.release)
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusAccepted {
			t.Fatalf("in-flight request ended with %d", code)
		}
	}
	// The slots are free again.
	sink.blockAppend = false
	if rec := f.post(body); rec.Code != http.StatusAccepted {
		t.Fatalf("status after release = %d", rec.Code)
	}
}

func TestHandlerReportsSinkFailure(t *testing.T) {
	failure := errors.New("store is closed")
	mixed := batch(minimalEvent(), without(minimalEvent(), "name"))
	for _, tc := range []struct {
		name        string
		sink        *fakeSink
		body        any
		contentType string
		wantAppends int
		wantRejects int
	}{
		{"append fails", &fakeSink{appendErr: failure}, mixed, jsonMediaType, 1, 0},
		{"count fails after append", &fakeSink{rejectErr: failure}, mixed, jsonMediaType, 1, 1},
		{"count fails for a refused event", &fakeSink{rejectErr: failure}, without(minimalEvent(), "name"), jsonMediaType, 0, 1},
		{"count fails for a refused request", &fakeSink{rejectErr: failure}, minimalEvent(), "text/plain", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewHandler(tc.sink, testHost, testBearer(), newFakeClock().Now)
			if err != nil {
				t.Fatal(err)
			}
			f := &fixture{handler: h, sink: tc.sink}
			req := newRequest(encode(t, tc.body))
			req.Header.Set(headerContentType, tc.contentType)
			if rec := f.do(req); rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d; want %d", rec.Code, http.StatusServiceUnavailable)
			}
			if got := tc.sink.appendCalls(); got != tc.wantAppends {
				t.Errorf("AppendExt called %d times; want %d", got, tc.wantAppends)
			}
			if got := len(tc.sink.rejectCalls()); got != tc.wantRejects {
				t.Errorf("CountRejected called %d times; want %d", got, tc.wantRejects)
			}
		})
	}
}

func TestSplitBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{"single event", `{"version":1}`, []string{`{"version":1}`}},
		{"single event after white space", "\n\t {}", []string{"\n\t {}"}},
		{"batch", `{"events":[{"a":1}, 2,null]}`, []string{`{"a":1}`, `2`, `null`}},
		{"empty batch", `{"events":[]}`, []string{}},
		{"batch member name in capitals", `{"EVENTS":[1]}`, []string{`1`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, err := splitBody([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(items))
			for i, item := range items {
				got[i] = string(item)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("items = %q; want %q", got, tc.want)
			}
		})
	}
	for _, body := range []string{"", "[]", "null", `{"events":"x"}`, `{"events":[1,]}`, `{} {}`} {
		if _, err := splitBody([]byte(body)); !errors.Is(err, errPayload) {
			t.Errorf("splitBody(%q) error = %v; want %v", body, err, errPayload)
		}
	}
}

func TestCapRunes(t *testing.T) {
	for _, tc := range []struct {
		in    string
		limit int
		want  string
	}{
		{"", 3, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"가나다라", 3, "가나다"},
		{"가나다", 3, "가나다"},
		{"a가b나", 2, "a가"},
		{"abc", 0, ""},
	} {
		if got := capRunes(tc.in, tc.limit); got != tc.want {
			t.Errorf("capRunes(%q, %d) = %q; want %q", tc.in, tc.limit, got, tc.want)
		}
	}
}

// assertEventID checks the shape every stored ID has.
func assertEventID(t *testing.T, id string) {
	t.Helper()
	if len(id) != 2*eventIDBytes || strings.Trim(id, "0123456789abcdef") != "" {
		t.Fatalf("event id %q is not %d hex characters", id, 2*eventIDBytes)
	}
}
