package extension

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

// wireMembers returns the JSON member names of WireEvent.
func wireMembers(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeFor[WireEvent]()
	names := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("field %s has no json name", typ.Field(i).Name)
		}
		names = append(names, name)
	}
	return names
}

// Every member of the wire format must have its own reason, so that a member
// added to WireEvent later cannot fall through to the generic one unnoticed.
func TestTypeErrorReasonCoversEveryWireMember(t *testing.T) {
	seen := map[rejectReason]string{}
	for _, name := range wireMembers(t) {
		reason := typeErrorReason(name)
		if reason == reasonInvalidEvent {
			t.Errorf("member %q has no reason of its own", name)
		}
		if other, taken := seen[reason]; taken {
			t.Errorf("members %q and %q share reason %q", other, name, reason)
		}
		seen[reason] = name
	}
	if got := typeErrorReason("no_such_member"); got != reasonInvalidEvent {
		t.Errorf("unknown member maps to %q; want %q", got, reasonInvalidEvent)
	}
}

// A member of the wrong JSON type is reported by name, and the source that
// was readable next to it still attributes the refusal.
func TestDecodeEventReportsWrongTypes(t *testing.T) {
	for _, name := range wireMembers(t) {
		t.Run(name, func(t *testing.T) {
			// An array fits no member of the wire format.
			event := with(minimalEvent(), obj{name: []any{}})
			wire, reason := decodeEvent(encode(t, event))
			if want := typeErrorReason(name); reason != want {
				t.Fatalf("reason = %q; want %q", reason, want)
			}
			wantSource := testSource
			if name == "source" {
				wantSource = unattributed
			}
			if got := attributableSource(&wire); got != wantSource {
				t.Fatalf("attributed to %q; want %q", got, wantSource)
			}
		})
	}
}

func TestDecodeEventRefusesNonObjects(t *testing.T) {
	for _, raw := range []string{"", " ", "null", "1", `"x"`, "[]", "true", `{"version":`} {
		if _, reason := decodeEvent(json.RawMessage(raw)); reason != reasonInvalidEvent {
			t.Errorf("decodeEvent(%q) reason = %q; want %q", raw, reason, reasonInvalidEvent)
		}
	}
}

func TestAttributeText(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want string
		ok   bool
	}{
		{"text", "east", "east", true},
		{"empty text", "", "", true},
		{"number as sent", json.Number("1e3"), "1e3", true},
		{"decoded whole number", float64(2), "2", true},
		{"decoded fraction", 0.25, "0.25", true},
		{"true", true, "true", true},
		{"false", false, "false", true},
		{"null", nil, "", false},
		{"object", map[string]any{}, "", false},
		{"array", []any{}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := attributeText(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("attributeText(%v) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// A body that cannot be read to its end is refused and counted like any
// other unusable input.
func TestHandlerRefusesUnreadableBody(t *testing.T) {
	f := newFixture(t)
	req := newRequest(nil)
	req.Body = io.NopCloser(iotest.ErrReader(errors.New("connection reset")))
	req.ContentLength = -1
	if rec := f.do(req); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusBadRequest)
	}
	if got, want := f.sink.rejectCalls(), []rejectCall{{unattributed, 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rejections = %v; want %v", got, want)
	}
}
