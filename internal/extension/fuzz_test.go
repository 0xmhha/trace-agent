package extension

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// FuzzHandlerBody feeds arbitrary bodies to an authenticated request. The
// handler must not panic, must answer with one of the documented statuses,
// and whatever it stores must respect every limit of the wire format.
func FuzzHandlerBody(f *testing.F) {
	marshal := func(v any) []byte {
		raw, err := json.Marshal(v)
		if err != nil {
			f.Fatalf("seed: %v", err)
		}
		return raw
	}
	tooMany := make([]any, MaxEvents+1)
	for i := range tooMany {
		tooMany[i] = minimalEvent()
	}
	for _, seed := range [][]byte{
		nil,
		[]byte("{}"),
		[]byte("null"),
		[]byte("[]"),
		[]byte(`{"events":[]}`),
		[]byte(`{"events":[1,null,"x",[],{}]}`),
		[]byte(`{"events":{"a":1}}`),
		[]byte(`{"version":1,"source":"s","kind":"log","name":"n","duration_ms":1e999}`),
		[]byte(`{"version":1,"source":"s","kind":"log","name":"n","attributes":{"a":[1],"b":{"c":null}}}`),
		[]byte(`{"version":1,"source":"s","kind":"log","name":"\ud800","message":"\u0000"}`),
		[]byte("{\"version\":1,\"source\":\"s\",\"kind\":\"log\",\"name\":\"\xff\xfe\"}"),
		marshal(minimalEvent()),
		marshal(richEvent()),
		marshal(with(richEvent(), obj{"id": "attempt-1", "message": strings.Repeat("글", MaxMessageLen+10)})),
		marshal(batch(minimalEvent(), richEvent(), without(minimalEvent(), "name"), 3)),
		marshal(batch(tooMany...)),
	} {
		f.Add(seed)
	}
	statuses := map[int]bool{
		http.StatusAccepted:              true,
		http.StatusBadRequest:            true,
		http.StatusRequestEntityTooLarge: true,
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		fx := newFixture(t)
		rec := fx.post(body)
		if !statuses[rec.Code] {
			t.Fatalf("status = %d for body %q", rec.Code, body)
		}
		stored := fx.sink.stored()
		if rec.Code != http.StatusAccepted {
			if len(stored) != 0 {
				t.Fatalf("status %d but %d events stored", rec.Code, len(stored))
			}
			if got := fx.sink.rejectedBySource(); len(got) != 1 || got[unattributed] != 1 {
				t.Fatalf("status %d counted %v; want one unattributed rejection", rec.Code, got)
			}
			return
		}
		response := decodeResponse(t, rec)
		if response.Rejected == nil {
			t.Fatal("rejected is null")
		}
		total := response.Accepted + len(response.Rejected)
		if response.Accepted < 0 || total > MaxEvents {
			t.Fatalf("accepted %d + rejected %d exceeds %d", response.Accepted, len(response.Rejected), MaxEvents)
		}
		if len(stored) > response.Accepted {
			t.Fatalf("stored %d events but accepted %d", len(stored), response.Accepted)
		}
		if (len(stored) == 0) != (response.Accepted == 0) {
			t.Fatalf("stored %d events for %d accepted", len(stored), response.Accepted)
		}
		if n := fx.sink.appendCalls(); n > 1 {
			t.Fatalf("AppendExt called %d times", n)
		}
		counted := 0
		for source, n := range fx.sink.rejectedBySource() {
			if source != unattributed && !SourcePattern.MatchString(source) {
				t.Fatalf("rejection counted under malformed source %q", source)
			}
			counted += n
		}
		if counted != len(response.Rejected) {
			t.Fatalf("counted %d rejections, reported %d", counted, len(response.Rejected))
		}
		last := -1
		for _, rejection := range response.Rejected {
			if rejection.Index <= last || rejection.Index >= total || rejection.Reason == "" {
				t.Fatalf("bad rejection %+v after index %d of %d", rejection, last, total)
			}
			last = rejection.Index
		}
		ids := map[string]struct{}{}
		for _, event := range stored {
			checkStoredEvent(t, &event)
			if _, repeated := ids[event.ID]; repeated {
				t.Fatalf("id %s handed to the sink twice", event.ID)
			}
			ids[event.ID] = struct{}{}
		}
	})
}

// checkStoredEvent asserts the invariants of an event the sink received.
func checkStoredEvent(t *testing.T, e *usage.ExtEvent) {
	t.Helper()
	assertEventID(t, e.ID)
	if !SourcePattern.MatchString(e.Source) {
		t.Fatalf("source %q", e.Source)
	}
	if _, ok := parseKind(string(e.Kind)); !ok {
		t.Fatalf("kind %q", e.Kind)
	}
	if status, ok := parseStatus(string(e.Status)); !ok || status != e.Status {
		t.Fatalf("status %q", e.Status)
	}
	if level, ok := parseLevel(string(e.Level)); !ok || level != e.Level {
		t.Fatalf("level %q", e.Level)
	}
	if n := utf8.RuneCountInString(e.Name); n < 1 || n > MaxNameLen {
		t.Fatalf("name has %d characters", n)
	}
	if n := utf8.RuneCountInString(e.Message); n > MaxMessageLen {
		t.Fatalf("message has %d characters", n)
	}
	if n := utf8.RuneCountInString(e.Unit); n > MaxUnitLen {
		t.Fatalf("unit has %d characters", n)
	}
	for _, text := range []string{e.Name, e.Message, e.Unit} {
		if !utf8.ValidString(text) {
			t.Fatalf("stored text is not valid UTF-8: %q", text)
		}
	}
	for _, id := range []string{e.SessionID, e.ToolUseID, e.TraceID, e.SpanID, e.ParentSpanID} {
		if id != "" && !IDPattern.MatchString(id) {
			t.Fatalf("link id %q", id)
		}
	}
	for _, v := range []*float64{e.DurationMs, e.Value} {
		if _, ok := measure(v); !ok {
			t.Fatalf("number %v", *v)
		}
	}
	if len(e.Attributes) > MaxAttributes {
		t.Fatalf("%d attributes", len(e.Attributes))
	}
	for key, value := range e.Attributes {
		if !AttributeKeyPattern.MatchString(key) {
			t.Fatalf("attribute key %q", key)
		}
		if n := utf8.RuneCountInString(value); n > MaxAttributeValue || !utf8.ValidString(value) {
			t.Fatalf("attribute %q has %d characters", key, n)
		}
	}
	if e.At.IsZero() || e.ReceivedAt.IsZero() {
		t.Fatalf("times %v, %v", e.At, e.ReceivedAt)
	}
}
