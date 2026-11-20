package ingress

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type replaySink struct {
	*store.Store
	mu          sync.Mutex
	afterCommit bool
}

func (s *replaySink) AppendBatch(ctx context.Context, es []collect.Evidence) error {
	if err := s.Store.AppendBatch(ctx, es); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.afterCommit {
		s.afterCommit = false
		return errors.New("simulated lost acknowledgment")
	}
	return nil
}
func newReceiver(t *testing.T) (*Receiver, *replaySink, *collect.Adapter, string, string) {
	t.Helper()
	return receiverFor(t, collect.Telemetry)
}

func receiverFor(t *testing.T, kind collect.Kind) (*Receiver, *replaySink, *collect.Adapter, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ingress.sqlite")
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a, err := collect.New(kind, "test", "otel", "", make([]byte, 32), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := Token()
	if err != nil {
		t.Fatal(err)
	}
	s := &replaySink{Store: db}
	r, err := Listen(s, a, 0, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctx, c := context.WithTimeout(context.Background(), time.Second); defer c(); _ = r.Close(ctx) })
	return r, s, a, token, path
}
func send(t *testing.T, r *Receiver, media string, raw []byte, token string, patch func(*http.Request)) (int, []byte) {
	t.Helper()
	q, err := http.NewRequestWithContext(t.Context(), http.MethodPost, r.URL(), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	q.Header.Set("Authorization", "Bearer "+token)
	q.Header.Set("Content-Type", media)
	if patch != nil {
		patch(q)
	}
	resp, err := http.DefaultClient.Do(q)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}
func kv(key, value string) *common.KeyValue {
	return &common.KeyValue{Key: key, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: value}}}
}
func sample(name string, seq int, body string, at time.Time) *logs.LogRecord {
	return &logs.LogRecord{EventName: "claude_code." + name, Attributes: []*common.KeyValue{kv("event.name", name), kv("event.timestamp", at.Format(time.RFC3339Nano)), kv("event.sequence", strconv.Itoa(seq)), kv("session.id", "fixture"), kv("request_body_id", "C1"), kv("body", body)}}
}
func wire(t *testing.T, media string, rs ...*logs.LogRecord) []byte {
	t.Helper()
	d := &logs.LogsData{ResourceLogs: []*logs.ResourceLogs{{ScopeLogs: []*logs.ScopeLogs{{LogRecords: rs}}}}}
	var b []byte
	var err error
	if media == jsonType {
		b, err = protojson.Marshal(d)
	} else {
		b, err = proto.Marshal(d)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestOTLPCommitReplayConsent(t *testing.T) {
	r, db, a, token, path := newReceiver(t)
	const canary = "private-ingress-synthetic-credential"
	at := time.Now().UTC()
	rawBody := `{"messages":[{"role":"user","content":"` + canary + `"}]}`
	req := sample("api_request_body", 0, rawBody, at)
	res := sample("api_response_body", 1, `{"content":[{"type":"text","text":"Sample response"}]}`, at.Add(time.Second))
	b := wire(t, protoType, res, req)
	if status, _ := send(t, r, protoType, b, token, nil); status != 403 {
		t.Fatal("unregistered source accepted", status)
	}
	if _, err := db.Register(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	db.afterCommit = true
	if status, _ := send(t, r, protoType, b, token, nil); status != 503 {
		t.Fatal("missing retryable failure", status)
	}
	before, _ := db.Point(t.Context())
	beforeRows, err := db.Records(t.Context(), before)
	if err != nil || len(beforeRows) != 2 {
		t.Fatal("missing durable records before ACK", err)
	}
	// Resend the same occurrence in a different batch order and encoding.
	b = wire(t, jsonType, req, res)
	if status, out := send(t, r, jsonType, b, token, nil); status != 200 || string(out) != "{}" {
		t.Fatal(status, string(out))
	}
	again, _ := db.Point(t.Context())
	if again.Epoch != before.Epoch {
		t.Fatal("replay changed epoch")
	}
	rs, err := db.Records(t.Context(), again)
	if err != nil || len(rs) != 2 {
		t.Fatal(len(rs), err)
	}
	for _, record := range rs {
		slots, err := db.BodyFields(t.Context(), again, record.ID)
		if err != nil || len(slots) != 0 {
			t.Fatal("unconsented body", err)
		}
	}
	for _, file := range []string{path, path + "-wal", path + "-shm"} {
		dir, err := os.OpenRoot(filepath.Dir(file))
		if err != nil {
			t.Fatal(err)
		}
		data, err := dir.ReadFile(filepath.Base(file))
		_ = dir.Close()
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(canary)) || bytes.Contains(data, []byte(token)) {
			t.Fatal("private bytes reached database")
		}
	}
	if _, err = db.SetConsent(t.Context(), a.Source(), true); err != nil {
		t.Fatal(err)
	}
	res = sample("api_response_body", 2, `{"content":[{"type":"text","text":"Sample response"}]}`, time.Now().UTC().Add(time.Second))
	var zipped bytes.Buffer
	gz := gzip.NewWriter(&zipped)
	_, _ = gz.Write(wire(t, protoType, res))
	_ = gz.Close()
	if status, _ := send(t, r, protoType, zipped.Bytes(), token, func(q *http.Request) { q.Header.Set("Content-Encoding", "gzip") }); status != 200 {
		t.Fatal(status)
	}
	p, _ := db.Point(t.Context())
	rs, err = db.Records(t.Context(), p)
	if err != nil || len(rs) != 3 {
		t.Fatal(err, len(rs))
	}
	slots, err := db.BodyFields(t.Context(), p, rs[2].ID)
	if err != nil || len(slots) != 1 || slots[0].Text != "Sample response" {
		t.Fatal(slots, err)
	}
}
func TestOTLPAdmissionAndBounds(t *testing.T) {
	r, db, a, token, _ := newReceiver(t)
	_, err := db.Register(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	msg := wire(t, jsonType, sample("api_request_body", 1, "s", time.Now()))
	for _, edit := range []func(*http.Request){func(q *http.Request) { q.Host = "foreign.invalid" }, func(q *http.Request) { q.Header.Set("Origin", "null") }, func(q *http.Request) { q.Header.Set("Origin", "https://untrusted.invalid") }} {
		if status, _ := send(t, r, jsonType, msg, token, edit); status != 403 {
			t.Fatal(status)
		}
	}
	if status, _ := send(t, r, jsonType, msg, "incorrect", nil); status != 401 {
		t.Fatal(status)
	}
	if status, _ := send(t, r, jsonType, []byte("!"), token, nil); status != 400 {
		t.Fatal(status)
	}
	bad := sample("user_prompt", -1, "private", time.Now())
	status, out := send(t, r, jsonType, wire(t, jsonType, bad), token, nil)
	if status != 200 || !bytes.Contains(out, []byte(`"rejectedLogRecords":"1"`)) {
		t.Fatal(status, string(out))
	}
	rs := make([]*logs.LogRecord, maxRecords+1)
	for i := range rs {
		rs[i] = bad
	}
	if status, _ = send(t, r, protoType, wire(t, protoType, rs...), token, nil); status != 413 {
		t.Fatal(status)
	}
	if status, _ = send(t, r, jsonType, make([]byte, maxPayload+1), token, nil); status != 413 {
		t.Fatal(status)
	}
	if _, err = db.SetEnabled(t.Context(), a.Source(), false); err != nil {
		t.Fatal(err)
	}
	if status, _ = send(t, r, jsonType, msg, token, nil); status != 403 {
		t.Fatal(status)
	}
}

func TestInstalledClaudeOTLP(t *testing.T) {
	if os.Getenv("TRACE_CLAUDE_OTLP_COMPAT") != "1" {
		t.Skip("set TRACE_CLAUDE_OTLP_COMPAT=1 to run the installed Claude Code exporter")
	}
	for _, format := range []string{"http/protobuf", "http/json"} {
		t.Run(strings.TrimPrefix(format, "http/"), func(t *testing.T) {
			r, db, a, token, _ := newReceiver(t)
			if _, err := db.Register(t.Context(), a); err != nil {
				t.Fatal(err)
			}
			if _, err := db.SetConsent(t.Context(), a.Source(), true); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "node", "../../projects/trace-agent/tests/compat/claude.mjs", t.TempDir()) // #nosec G204 -- fixed test script and fresh private directory.
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TRACE_TEST_OTLP_ENDPOINT=" + r.URL(), "TRACE_TEST_OTLP_TOKEN=" + token, "TRACE_TEST_OTLP_PROTOCOL=" + format, "TRACE_TEST_OTLP_CONSENT=1"}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated Claude exporter: %v %s", err, output)
			}
			p, err := db.Point(ctx)
			if err != nil {
				t.Fatal(err)
			}
			records, err := db.Records(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			names := map[string]int{}
			bodyCount := 0
			for _, rec := range records {
				n, _ := rec.Evidence.Metadata["event.name"].(string)
				names[n]++
				fields, err := db.BodyFields(ctx, p, rec.ID)
				if err != nil {
					t.Fatal(err)
				}
				bodyCount += len(fields)
			}
			encoded, _ := json.Marshal(names)
			t.Logf("%s records=%d names=%s body slots=%d", format, len(records), encoded, bodyCount)
			if names["api_request_body"] == 0 || names["api_response_body"] == 0 || names["tool_result"] == 0 {
				t.Fatal("installed exporter did not deliver expected events")
			}
		})
	}
}
