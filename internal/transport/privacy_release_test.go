package transport

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

// TestReleaseRedaction inspects artifacts across process boundaries so logging
// to either standard stream is observable even on an otherwise successful run.
func TestReleaseRedaction(t *testing.T) {
	const markerVar = "TRACE_TEST_PRIVACY_CANARY"
	marker := os.Getenv(markerVar)
	if marker == "" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		marker = "private-marker-" + rand.Text()
		cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestReleaseRedaction$") // #nosec G204 -- same test binary and constant arguments.
		cmd.Env = append(os.Environ(), markerVar+"="+marker)
		output, err := cmd.CombinedOutput()
		if bytes.Contains(output, []byte(marker)) {
			t.Fatal("canary found in process output")
		}
		if err != nil {
			t.Fatalf("privacy subprocess failed: %s", output)
		}
		return
	}

	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "private.sqlite")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	a, err := collect.New(collect.Transcript, "profile", "instance", "2.1.329", []byte(rand.Text()+rand.Text()), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Register(ctx, a); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ingest := func(adapter *collect.Adapter, offset int64) {
		msg := object{"timestamp": iso(now.Add(time.Second * time.Duration(offset+1))), "type": "user", "uuid": strconv.FormatInt(offset, 10), "promptId": strconv.FormatInt(offset, 10), "userType": "external", "isMeta": false, "sessionId": "root", "message": object{"role": "user", "content": "authorization: " + marker}}
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		e, err := adapter.Project(collect.Position{Stream: "one", Generation: "one", Offset: offset}, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	ingest(a, 0)
	if _, err = db.SetConsent(ctx, a.Source(), true); err != nil {
		t.Fatal(err)
	}
	ingest(a.WithBodyCollection(), 1)
	pt, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	records, err := db.Records(ctx, pt)
	if err != nil || len(records) != 2 {
		t.Fatal("expected two committed inputs", err)
	}
	check := func(label string, raw []byte) {
		t.Helper()
		if bytes.Contains(raw, []byte(marker)) {
			t.Fatalf("canary found in %s", label)
		}
	}
	checkFile := func(name string) {
		t.Helper()
		raw, err := root.ReadFile(filepath.Base(name))
		if err != nil || len(raw) == 0 {
			t.Fatal("missing file during privacy scan", filepath.Base(name), err)
		}
		check(filepath.Base(name), raw)
	}
	checkFile(path)
	checkFile(path + "-wal")
	s, url := serveTest(t, db)
	ws := auth(t, s, url, false)
	f := filter{From: now.Add(-time.Minute), Until: now.Add(time.Hour), Clients: []string{}, Tasks: []string{}, Sources: []string{a.Source()}}
	v := view{p: pt, f: f}
	scope := object{"point": pointMap(pt), "cohort": v.token(s, v.sample), "filter": object{"interval": object{"from": iso(f.From), "until": iso(f.Until)}, "sources": []any{a.Source()}}}
	for i, r := range records {
		id := strconv.FormatInt(r.ID, 10)
		subscribe(t, ws, "privacy-"+id, "evidence", object{"scope": scope, "evidenceId": id})
		frame := recv(t, ws)
		check("evidence frame", frame.Payload)
		body := obj(frame.value(t, "evidence"), "data")
		fields := anyList(body, "fields")
		if len(fields) == 0 {
			t.Fatal("missing privacy status")
		}
		slot := fields[0].(object)
		if slot["text"] != nil || (i == 1 && slot["state"] != "FAILED") {
			t.Fatal("unexpected body exposure or status")
		}
		if end := recv(t, ws); end.Type != "complete" {
			t.Fatal("missing query completion")
		}
	}
	if err = ws.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	checkFile(path)
}
