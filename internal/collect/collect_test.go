package collect

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func adapter(t *testing.T, k Kind) *Adapter {
	t.Helper()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	a, err := New(k, "profile", "instance", "1.0", secret, []string{"Read", "Agent", "wait_agent", "mcp__north__lookup", "mcp__south__lookup"}, func() time.Time { return time.Unix(1234, 0) })
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type fixture struct {
	Stream string          `json:"stream"`
	Seq    int64           `json:"seq"`
	Record json.RawMessage `json:"record"`
}

const project = "../../projects/trace-agent"

func fixtures(t *testing.T, name string) []fixture {
	t.Helper()
	root, err := os.OpenRoot(filepath.Join(project, "fixtures/stage-1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(name + ".jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var rows []fixture
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row fixture
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

// The oracle knows fixture labels, not the installation secret. Restore only
// synthetic tokens in the test process after checking the serialized evidence.
func stringsIn(v any, visit func(string)) {
	switch x := v.(type) {
	case string:
		visit(x)
	case object:
		for _, v := range x {
			stringsIn(v, visit)
		}
	case []any:
		for _, v := range x {
			stringsIn(v, visit)
		}
	}
}
func TestStageOneOracle(t *testing.T) {
	outputs := map[string][]fixture{}
	for name, kind := range map[string]Kind{"codex": Rollout, "claude": Transcript, "claude-otel": Telemetry} {
		rows := fixtures(t, name)
		a := adapter(t, kind)
		app := adapter(t, AppServer)
		seen := make(map[string]bool)
		for _, row := range rows {
			src := a
			if row.Stream == "app-plan" {
				src = app
			}
			e, err := src.Project(Position{row.Stream, "g", row.Seq}, row.Record)
			if err != nil {
				t.Fatal(err)
			}
			if seen[e.RecordKey] {
				continue
			}
			seen[e.RecordKey] = true
			var input object
			if err := json.Unmarshal(row.Record, &input); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(e.Metadata)
			if err != nil {
				t.Fatal(err)
			}
			stringsIn(input, func(s string) {
				q, _ := json.Marshal(s)
				raw = bytes.ReplaceAll(raw, []byte(`"`+src.id(s)+`"`), q)
			})
			row.Record = raw
			outputs[name] = append(outputs[name], row)
		}
	}
	b, err := json.Marshal(outputs)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "tests/verify-adapter.mjs")
	cmd.Dir = project
	cmd.Stdin = bytes.NewReader(b)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stage-one oracle: %v\n%s", err, out)
	}
}

func TestMetadataOnly(t *testing.T) {
	const marker = "private-synthetic-marker"
	for _, tc := range []struct {
		k   Kind
		raw string
	}{
		{Transcript, `{"type":"assistant","uuid":"X","sessionId":"X","agentId":"X","message":{"role":"assistant","content":[{"type":"tool_use","id":"X","name":"X","input":{"mission":"X"}},{"type":"text","text":"X"}]},"error":"X"}`},
		{Transcript, `{"type":"user","message":{"content":"X"},"toolUseResult":{"agentId":"X","error":"X"}}`},
		{Rollout, `{"type":"response_item","payload":{"type":"function_call_output","call_id":"X","output":"X","error":"X"}} `},
		{Telemetry, `{"event.name":"api_response_body","request_body_id":"X","body":"X","body_ref":"X"}`},
		{AppServer, `{"method":"turn/plan/updated","params":{"threadId":"X","turnId":"X","explanation":"X","plan":[{"step":"X","status":"X"}]}}`},
		{CodexTelemetry, `{"event.name":"codex.tool_result","conversation.id":"X","model":"X","call_id":"X","tool_name":"X","arguments":"X","output":"X","prompt":"X","success":"X","duration_ms":"X","endpoint":"X"}`},
		{CodexTelemetry, `{"event.name":"codex.user_prompt","conversation.id":"X","prompt":"X","service.version":"X"}`},
	} {
		a := adapter(t, tc.k)
		raw := bytes.ReplaceAll([]byte(tc.raw), []byte("X"), []byte(marker))
		obs, err := a.Project(Position{marker, marker, 1}, raw)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(obs)
		if err != nil || bytes.Contains(data, []byte(marker)) || obs.ObservedMS != 1234000 || obs.Redaction != "metadata_only" {
			t.Fatal("unsafe projection or missing provenance", tc.k)
		}
		if _, err := a.Project(Position{marker, marker, 1}, []byte(marker)); !errors.Is(err, errInput) {
			t.Fatal("invalid input was not contained")
		}
	}
}

func TestPartialResume(t *testing.T) {
	ctx := t.Context()
	a := adapter(t, Transcript)
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	const name = "selected.jsonl"
	rows := fixtures(t, "claude")
	first := append(bytes.Clone(rows[0].Record), '\n')
	next := append(bytes.Clone(rows[1].Record), '\n')
	write := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(append(bytes.Clone(first), next[:len(next)-1]...)) // valid JSON, but no delimiter
	var accepted []Evidence
	accept := func(_ context.Context, e Evidence, c FileCursor) error {
		if e.Source != c.Source || e.Stream != c.Stream || e.Generation != c.Generation {
			t.Fatal("checkpoint identity")
		}
		accepted = append(accepted, e)
		return nil
	}
	cursor, err := a.ReadFile(ctx, root, name, FileCursor{}, accept)
	if err != nil || cursor.Offset != int64(len(first)) || len(accepted) != 1 {
		t.Fatalf("partial boundary %d, %v", cursor.Offset, err)
	}
	// Recreate the adapter and deserialize the writer checkpoint as on process restart.
	persisted, err := json.Marshal(cursor)
	if err != nil {
		t.Fatal(err)
	}
	cursor = FileCursor{}
	if err := json.Unmarshal(persisted, &cursor); err != nil {
		t.Fatal(err)
	}
	a, err = New(Transcript, "profile", "instance", "1.0", a.secret, []string{"Read"}, a.now)
	if err != nil {
		t.Fatal(err)
	}
	write(append(bytes.Clone(first), next...))
	var attempted Evidence
	old, err := a.ReadFile(ctx, root, name, cursor, func(_ context.Context, e Evidence, _ FileCursor) error {
		attempted = e
		return errors.New("write failed")
	})
	if err == nil || old != cursor {
		t.Fatal("advanced past failure")
	}
	resumed, err := a.ReadFile(ctx, root, name, old, accept)
	if err != nil || len(accepted) != 2 || attempted.RecordKey != accepted[1].RecordKey || resumed.Offset != int64(len(first)+len(next)) {
		t.Fatal("failed resume", err)
	}
	again, err := a.ReadFile(ctx, root, name, resumed, accept)
	if err != nil || again != resumed || len(accepted) != 2 {
		t.Fatal("duplicate on resume")
	}
	// Source replaced with a shorter file, then with a different inode of the same size.
	write(first)
	short, err := a.ReadFile(ctx, root, name, again, accept)
	if err != nil || short.Generation == again.Generation || short.Offset != int64(len(first)) {
		t.Fatal("truncation", err)
	}
	if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, "old")); err != nil {
		t.Fatal(err)
	}
	write(first)
	rotated, err := a.ReadFile(ctx, root, name, short, accept)
	if err != nil || rotated.Generation == short.Generation {
		t.Fatal("rotation", err)
	}
	write(append(bytes.Clone(first), []byte("invalid-private-line\n")...))
	bad, err := a.ReadFile(ctx, root, name, rotated, accept)
	if !errors.Is(err, errInput) || bad != rotated {
		t.Fatal("invalid complete line", err)
	}
}
func TestRootAndSizeLimit(t *testing.T) {
	a := adapter(t, Transcript)
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root.Name(), "link")); err != nil {
		t.Fatal(err)
	}
	accept := func(context.Context, Evidence, FileCursor) error { t.Fatal("unexpected commit"); return nil }
	if _, err = a.ReadFile(t.Context(), root, "link", FileCursor{}, accept); err == nil {
		t.Fatal("escaped root")
	}
	if err := os.WriteFile(filepath.Join(root.Name(), "big"), bytes.Repeat([]byte{'x'}, maxRecord+2), 0600); err != nil {
		t.Fatal(err)
	}
	if c, err := a.ReadFile(t.Context(), root, "big", FileCursor{}, accept); !errors.Is(err, errLimit) || c != (FileCursor{}) {
		t.Fatal("oversize")
	}
}
func FuzzProjection(f *testing.F) {
	f.Add([]byte(`{"type":"assistant","message":{"content":[]}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		a := adapter(t, Transcript)
		_, _ = a.Project(Position{"stream", "g", 0}, raw)
	})
}

func TestIdentityBoundaries(t *testing.T) {
	a := adapter(t, Transcript)
	other := func(kind Kind, profile string) *Adapter {
		b, err := New(kind, profile, "other-instance", "", a.secret, nil, a.now)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	// A writer can verify a documented message UUID bridge inside the same
	// client profile. Record identity stays local to the adapter instance.
	otel := other(Telemetry, "profile")
	if a.id("message") != otel.id("message") || a.source == otel.source || a.id("message") == other(Transcript, "different").id("message") ||
		a.id("message") == other(Rollout, "profile").id("message") {
		t.Fatal("profile/source identity boundaries")
	}
	e, err := a.Project(Position{"s", "g", 0}, []byte(`{"type":"assistant","version":"2.1.329","message":{"content":"private"}}`))
	if err != nil || e.ClientVersion != "2.1.329" {
		t.Fatal("producer version")
	}
}
func TestWaitReportRedactsTerminalText(t *testing.T) {
	a := adapter(t, Rollout)
	var safe object
	output := a.waitReport(`{"timed_out":false,"status":{"private-child":{"completed":"private-result"}}}`)
	if bytes.Contains([]byte(output), []byte("private")) || json.Unmarshal([]byte(output), &safe) != nil ||
		obj(obj(safe["status"])[a.id("private-child")])["completed"] != "" {
		t.Fatal("lost terminal evidence or exposed text")
	}
}
