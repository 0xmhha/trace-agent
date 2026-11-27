package extension

import (
	"bytes"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"
	"testing/iotest"
	"time"
)

const testTimestamp = "2026-01-01T01:00:00Z"

// storeOne posts one event to a fresh handler and returns the ID it was
// stored under.
func storeOne(t *testing.T, event obj) string {
	t.Helper()
	f := newFixture(t)
	f.accept(t, event)
	stored := f.sink.stored()
	if len(stored) != 1 {
		t.Fatalf("stored %d events; want 1", len(stored))
	}
	assertEventID(t, stored[0].ID)
	return stored[0].ID
}

// richEvent sets every member that takes part in a content hash.
func richEvent() obj {
	return obj{
		"version":        WireVersion,
		"source":         testSource,
		"kind":           "span",
		"name":           "upload",
		"timestamp":      testTimestamp,
		"duration_ms":    12.5,
		"status":         "ok",
		"level":          "info",
		"session_id":     "session-1",
		"tool_use_id":    "tool-1",
		"trace_id":       "trace-1",
		"span_id":        "span-2",
		"parent_span_id": "span-1",
		"message":        "done",
		"attributes":     obj{"step": "3", "region": "east"},
		"value":          2,
		"unit":           "count",
	}
}

// Rule 1: a sender key identifies the event together with its source, and
// nothing else does.
func TestEventIDFromSenderKey(t *testing.T) {
	keyed := with(richEvent(), obj{"id": "attempt-1"})
	base := storeOne(t, keyed)
	for _, tc := range []struct {
		name  string
		event obj
		same  bool
	}{
		{"sent again", keyed, true},
		{"other content", with(keyed, obj{"name": "download", "message": "changed", "status": "error"}), true},
		{"other timestamp", with(keyed, obj{"timestamp": "2027-05-05T05:05:05Z"}), true},
		{"no timestamp", without(keyed, "timestamp"), true},
		{"other key", with(keyed, obj{"id": "attempt-2"}), false},
		{"other source", with(keyed, obj{"source": "other-script"}), false},
		{"key moved into the source", with(keyed, obj{"source": testSource + "a", "id": "ttempt-1"}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := storeOne(t, tc.event); (got == base) != tc.same {
				t.Fatalf("id = %s, base %s; want same = %v", got, base, tc.same)
			}
		})
	}
}

// Rule 2: without a key, the same content at the same instant is one event.
func TestEventIDFromContent(t *testing.T) {
	base := storeOne(t, richEvent())
	for _, tc := range []struct {
		name  string
		event obj
		same  bool
	}{
		{"sent again", richEvent(), true},
		{"same instant in another zone", with(richEvent(), obj{"timestamp": "2026-01-01T10:00:00+09:00"}), true},
		{"other source", with(richEvent(), obj{"source": "other-script"}), false},
		{"other kind", with(richEvent(), obj{"kind": "log"}), false},
		{"other name", with(richEvent(), obj{"name": "download"}), false},
		{"other instant", with(richEvent(), obj{"timestamp": "2026-01-01T01:00:00.001Z"}), false},
		{"other duration", with(richEvent(), obj{"duration_ms": 12.6}), false},
		{"no duration", without(richEvent(), "duration_ms"), false},
		{"zero duration", with(richEvent(), obj{"duration_ms": 0}), false},
		{"other status", with(richEvent(), obj{"status": "error"}), false},
		{"other level", with(richEvent(), obj{"level": "warn"}), false},
		{"other session", with(richEvent(), obj{"session_id": "session-2"}), false},
		{"other tool use", with(richEvent(), obj{"tool_use_id": "tool-2"}), false},
		{"other trace", with(richEvent(), obj{"trace_id": "trace-2"}), false},
		{"other span", with(richEvent(), obj{"span_id": "span-3"}), false},
		{"other parent span", with(richEvent(), obj{"parent_span_id": "span-0"}), false},
		{"other message", with(richEvent(), obj{"message": "failed"}), false},
		{"other attribute value", with(richEvent(), obj{"attributes": obj{"step": "4", "region": "east"}}), false},
		{"other attribute key", with(richEvent(), obj{"attributes": obj{"stage": "3", "region": "east"}}), false},
		{"fewer attributes", with(richEvent(), obj{"attributes": obj{"step": "3"}}), false},
		{"attribute text moved between key and value", with(richEvent(), obj{"attributes": obj{"step3": "", "region": "east"}}), false},
		{"other value", with(richEvent(), obj{"value": 3}), false},
		{"other unit", with(richEvent(), obj{"unit": "bytes"}), false},
		{"text moved between fields", with(richEvent(), obj{"name": "uploaddone", "message": ""}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := storeOne(t, tc.event); (got == base) != tc.same {
				t.Fatalf("id = %s, base %s; want same = %v", got, base, tc.same)
			}
		})
	}
}

// Omitted status and level hash like their defaults, because the hash covers
// the stored event.
func TestEventIDCoversStoredDefaults(t *testing.T) {
	explicit := with(minimalEvent(), obj{"timestamp": testTimestamp, "status": "unknown", "level": "info"})
	implicit := with(minimalEvent(), obj{"timestamp": testTimestamp})
	if a, b := storeOne(t, explicit), storeOne(t, implicit); a != b {
		t.Fatalf("ids differ: %s and %s", a, b)
	}
}

// Attribute order in the body must not reach the hash.
func TestEventIDIgnoresAttributeOrder(t *testing.T) {
	const head = `{"version":1,"source":"deploy-script","kind":"log","name":"n","timestamp":"` + testTimestamp + `","attributes":`
	ids := map[string]struct{}{}
	for _, attributes := range []string{
		`{"a":"1","b":"2","c":"3"}`,
		`{"c":"3","a":"1","b":"2"}`,
		`{"b":"2","c":"3","a":"1"}`,
	} {
		f := newFixture(t)
		decodeResponse(t, f.post([]byte(head+attributes+"}")))
		ids[f.sink.stored()[0].ID] = struct{}{}
	}
	if len(ids) != 1 {
		t.Fatalf("attribute order produced %d different ids", len(ids))
	}
}

// The receive time is not part of a content hash: a retry a minute later is
// still the same event.
func TestEventIDIgnoresReceiveTime(t *testing.T) {
	f := newFixture(t)
	f.accept(t, richEvent())
	f.clock.Advance(time.Minute)
	f.accept(t, richEvent())
	stored := f.sink.stored()
	if stored[0].ID != stored[1].ID {
		t.Fatalf("ids differ across receive times: %s and %s", stored[0].ID, stored[1].ID)
	}
	if stored[0].ReceivedAt.Equal(stored[1].ReceivedAt) {
		t.Fatal("the clock did not move between the two deliveries")
	}
}

// Rule 3: with neither key nor timestamp every delivery is a new event.
func TestEventIDRandomWithoutKeyAndTimestamp(t *testing.T) {
	f := newFixture(t)
	f.accept(t, minimalEvent())
	f.accept(t, minimalEvent())
	got := f.accept(t, batch(minimalEvent(), minimalEvent()))
	if got.Accepted != 2 {
		t.Fatalf("accepted = %d; want 2", got.Accepted)
	}
	stored := f.sink.stored()
	if len(stored) != 4 {
		t.Fatalf("stored %d events; want 4", len(stored))
	}
	ids := map[string]struct{}{}
	for _, event := range stored {
		assertEventID(t, event.ID)
		ids[event.ID] = struct{}{}
	}
	if len(ids) != len(stored) {
		t.Fatalf("%d events share %d ids", len(stored), len(ids))
	}
}

func TestEventIDUsesInjectedRandomness(t *testing.T) {
	sink := &fakeSink{}
	random := bytes.Repeat([]byte{0xab}, eventIDBytes)
	h := newHandler(sink, testHost, testBearer(), newFakeClock().Now, bytes.NewReader(random))
	f := &fixture{handler: h, sink: sink}
	f.accept(t, minimalEvent())
	if got, want := sink.stored()[0].ID, hex.EncodeToString(random); got != want {
		t.Fatalf("id = %s; want %s", got, want)
	}
	// The reader is now empty: an ID cannot be made, and nothing is stored or
	// counted for a request the endpoint could not serve.
	calls := sink.appendCalls()
	rec := f.postJSON(t, batch(with(minimalEvent(), obj{"id": "kept"}), minimalEvent()))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if sink.appendCalls() != calls || len(sink.rejectCalls()) != 0 {
		t.Fatal("the sink was called for a request that failed")
	}
}

func TestEventIDReportsRandomFailure(t *testing.T) {
	failure := errors.New("entropy source closed")
	valid, reason := validate(&WireEvent{Version: WireVersion, Source: testSource, Kind: "log", Name: "n"}, testEpoch())
	if reason != "" {
		t.Fatalf("validate: %s", reason)
	}
	if _, err := eventID("", false, &valid.event, iotest.ErrReader(failure)); !errors.Is(err, failure) {
		t.Fatalf("error = %v; want %v", err, failure)
	}
}

// A repeat inside one body is accepted like a re-delivery but handed to the
// sink once, so that a sink never sees the same ID twice in a call.
func TestHandlerStoresRepeatWithinBatchOnce(t *testing.T) {
	f := newFixture(t)
	keyed := with(minimalEvent(), obj{"id": "attempt-1"})
	got := f.accept(t, batch(keyed, richEvent(), with(keyed, obj{"name": "later"}), richEvent()))
	if got.Accepted != 4 || len(got.Rejected) != 0 {
		t.Fatalf("response = %+v; want four accepted", got)
	}
	stored := f.sink.stored()
	if len(stored) != 2 || stored[0].Name != "upload" || stored[0].ID == stored[1].ID {
		t.Fatalf("stored = %+v; want the first of each pair", stored)
	}
}
