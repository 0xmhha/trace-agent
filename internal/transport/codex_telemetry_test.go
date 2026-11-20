package transport

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

func TestCodexOTLPEvidenceDetailOverSocket(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "trace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a, err := collect.New(collect.CodexTelemetry, "profile", "otel", "0.169.1", make([]byte, 32), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	e, err := a.Project(collect.Position{Stream: "scope", Generation: "sample"}, []byte(`{"event.name":"codex.api_request","event.timestamp":"1970-01-01T00:00:02Z","attempt":"0","http.response.status_code":"429","duration_ms":"23","output":"sensitive-fixture-value"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	pt, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := db.Records(ctx, pt)
	if err != nil || len(rs) != 1 {
		t.Fatal(err)
	}
	id := strconv.FormatInt(rs[0].ID, 10)
	s, url := serveTest(t, db)
	ws := auth(t, s, url, false)
	input := watchInput()
	f := obj(obj(input, "scope"), "filter")
	f["sources"] = []any{a.Source()}
	input["selection"] = object{"kind": "UNLINKED_DETAIL", "evidenceId": id}
	subscribe(t, ws, "stream", "watch", object{"input": input})
	snapshot := recv(t, ws).value(t, "watch")
	check := func(detail object) {
		t.Helper()
		m := obj(detail, "safeMetadata")
		if m["httpStatus"] != float64(429) || m["attempt"] != float64(0) || m["durationMs"] != float64(23) || m["toolSuccess"] != nil || str(obj(detail, "evidence"), "link") != "UNLINKED" {
			t.Fatal("incorrect source observation", detail)
		}
	}
	check(obj(snapshot, "screen"))
	stamp := obj(snapshot, "stamp")
	scope := object{"point": stamp["point"], "cohort": stamp["cohort"], "filter": f}
	subscribe(t, ws, "once", "evidence", object{"scope": scope, "evidenceId": id})
	check(obj(recv(t, ws).value(t, "evidence"), "data"))
}
